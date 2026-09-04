package backend

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

type Codex struct {
	Binary          string
	Timeout         time.Duration
	ModelsCachePath string
}

func (adapter Codex) Execute(ctx context.Context, request Request, emit func(Event)) (Result, error) {
	binary := adapter.Binary
	if binary == "" {
		binary = "codex"
	}
	timeout := adapter.Timeout
	if timeout == 0 {
		timeout = 2 * time.Hour
	}
	catalogPath := adapter.ensureModelCatalog(ctx, request)
	args := codexArguments(request, catalogPath)
	environment := filterEnvironment(request.Environment)
	var reply, sessionID, providerError string
	result, err := request.Runner.Run(ctx, workspace.Command{
		Executable: binary,
		Args:       args,
		Dir:        request.WorkDir,
		Env:        environment,
		Stdin:      request.Prompt,
		Timeout:    timeout,
	}, func(line string) {
		event, parsedReply, parsedSession := parseCodexLine(line)
		if parsedReply != "" {
			reply = parsedReply
		}
		if parsedSession != "" {
			sessionID = parsedSession
		}
		if event.Type == "agent_error" {
			providerError = strings.TrimSpace(fmt.Sprint(event.Data["message"]))
			if providerError == "<nil>" {
				providerError = ""
			}
		}
		if event.Type != "" && emit != nil {
			emit(event)
		}
	})
	if err != nil {
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, err
	}
	if sessionID == "" {
		sessionID = request.ProviderSessionID
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = providerError
		}
		if message == "" {
			message = fmt.Sprintf("codex exited with code %d", result.ExitCode)
		}
		message = tail(message, 2000)
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%s", message)
	}
	if strings.TrimSpace(reply) == "" {
		return Result{ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("codex returned no agent message")
	}
	return Result{Reply: strings.TrimSpace(reply), ExitCode: result.ExitCode, ProviderSessionID: sessionID}, nil
}

func codexArguments(request Request, catalogPath string) []string {
	args := []string{}
	providerID := request.Environment["AHA_PROVIDER_ID"]
	baseURL := request.Environment["OPENAI_BASE_URL"]
	wireAPI := request.Environment["CODEX_WIRE_API"]
	envKey := request.Environment["CODEX_ENV_KEY"]
	if providerID == "" {
		providerID = "aha2_provider"
	}
	if wireAPI == "" {
		wireAPI = "responses"
	}
	if envKey == "" {
		envKey = "OPENAI_API_KEY"
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(effort))
	}
	if catalogPath != "" {
		args = append(args, "-c", "model_catalog_json="+strconv.Quote(catalogPath))
	} else if request.ContextWindow > 0 {
		args = append(args, "-c", fmt.Sprintf("model_context_window=%d", request.ContextWindow))
	}
	if baseURL != "" {
		args = append(args,
			"-c", "model_provider="+strconv.Quote(providerID),
			"-c", "model_providers."+providerID+".name="+strconv.Quote(providerID),
			"-c", "model_providers."+providerID+".base_url="+strconv.Quote(baseURL),
			"-c", "model_providers."+providerID+".wire_api="+strconv.Quote(wireAPI),
			"-c", "model_providers."+providerID+".requires_openai_auth=false",
			"-c", "model_providers."+providerID+".env_key="+strconv.Quote(envKey),
		)
	}
	if request.Model != "" {
		args = append(args, "-m", request.Model)
	}
	args = append(args, "--disable", "multi_agent")
	args = append(args, "exec", "--skip-git-repo-check", "--sandbox", codexSandbox(request.Filesystem), "-C", request.WorkDir)
	if request.ProviderSessionID != "" {
		args = append(args, "resume")
	}
	args = append(args, "--json")
	if request.ProviderSessionID != "" {
		args = append(args, request.ProviderSessionID, "-")
	} else {
		args = append(args, "-")
	}
	return args
}

func (adapter Codex) ensureModelCatalog(ctx context.Context, request Request) string {
	if request.Model == "" || request.ContextWindow <= 0 {
		return ""
	}
	raw, local := adapter.readModelsCache(ctx, request)
	if len(raw) == 0 {
		return ""
	}
	var cache struct {
		Models []map[string]any `json:"models"`
	}
	if json.Unmarshal(raw, &cache) != nil {
		return ""
	}
	var template map[string]any
	for _, item := range cache.Models {
		if item["slug"] == request.Model && completeCodexModelTemplate(item) {
			template = item
			break
		}
	}
	if template == nil {
		for _, item := range cache.Models {
			if item["slug"] == "gpt-5.5" && completeCodexModelTemplate(item) {
				template = item
				break
			}
		}
	}
	if template == nil {
		return ""
	}
	entry := make(map[string]any, len(template))
	for key, value := range template {
		entry[key] = value
	}
	entry["slug"] = request.Model
	entry["display_name"] = request.Model
	entry["context_window"] = request.ContextWindow
	maxWindow := int64(numberValue(entry["max_context_window"]))
	if maxWindow < request.ContextWindow {
		maxWindow = request.ContextWindow
	}
	entry["max_context_window"] = maxWindow
	delete(entry, "auto_compact_token_limit")
	payload, err := json.MarshalIndent(map[string]any{"models": []map[string]any{entry}}, "", "  ")
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", request.Model, request.ContextWindow)))
	name := fmt.Sprintf("%x.json", sum[:8])
	if local {
		path := filepath.Join(request.WorkDir, ".aha2-context", "runtime", "codex-models", name)
		if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
			return ""
		}
		if existing, err := os.ReadFile(path); err == nil && string(existing) == string(payload) {
			return path
		}
		if os.WriteFile(path, payload, 0o600) != nil {
			return ""
		}
		return path
	}
	dir := pathpkg.Join(strings.ReplaceAll(request.WorkDir, `\`, "/"), ".aha2-context", "runtime", "codex-models")
	path := pathpkg.Join(dir, name)
	result, err := request.Runner.Run(ctx, workspace.Command{
		Executable: "sh",
		Args: []string{
			"-c", `umask 077; mkdir -p "$1"; cat > "$2"`,
			"aha2-catalog", dir, path,
		},
		Dir: request.WorkDir, Stdin: string(payload), Timeout: 20 * time.Second,
	}, nil)
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	return path
}

func (adapter Codex) readModelsCache(ctx context.Context, request Request) ([]byte, bool) {
	if _, ok := request.Runner.(workspace.LocalRunner); ok {
		cachePath := adapter.ModelsCachePath
		if cachePath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, true
			}
			cachePath = filepath.Join(home, ".codex", "models_cache.json")
		}
		raw, _ := os.ReadFile(cachePath)
		return raw, true
	}
	result, err := request.Runner.Run(ctx, workspace.Command{
		Executable: "sh",
		Args:       []string{"-c", `cat "$HOME/.codex/models_cache.json"`},
		Dir:        request.WorkDir,
		Timeout:    20 * time.Second,
	}, nil)
	if err != nil || result.ExitCode != 0 {
		return nil, false
	}
	return []byte(result.Stdout), false
}

func completeCodexModelTemplate(value map[string]any) bool {
	for _, key := range []string{
		"slug", "display_name", "base_instructions", "supported_reasoning_levels",
		"default_reasoning_level", "shell_type", "visibility", "supported_in_api",
		"priority", "context_window",
	} {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		result, _ := typed.Float64()
		return result
	default:
		return 0
	}
}

func parseCodexLine(line string) (Event, string, string) {
	var payload map[string]any
	if json.Unmarshal([]byte(line), &payload) != nil {
		return Event{}, "", ""
	}
	rawType, _ := payload["type"].(string)
	switch rawType {
	case "thread.started":
		sessionID, _ := payload["thread_id"].(string)
		return Event{Type: "agent_session", Data: map[string]any{"provider_session_id": sessionID}}, "", sessionID
	case "turn.completed":
		usage, _ := payload["usage"].(map[string]any)
		sessionID, _ := payload["thread_id"].(string)
		return Event{Type: "agent_usage", Data: map[string]any{"usage": usage}}, "", sessionID
	case "error":
		message, _ := payload["message"].(string)
		return Event{Type: "agent_error", Data: map[string]any{"message": message}}, "", ""
	case "item.started", "item.completed":
		item, _ := payload["item"].(map[string]any)
		itemType, _ := item["type"].(string)
		if itemType == "agent_message" && rawType == "item.completed" {
			text, _ := item["text"].(string)
			return Event{Type: "agent_message", Data: map[string]any{"text": text}}, text, ""
		}
		if itemType == "command_execution" {
			data := map[string]any{
				"command": item["command"], "status": item["status"], "exit_code": item["exit_code"],
			}
			if rawType == "item.completed" {
				output, _ := item["aggregated_output"].(string)
				data["output_tail"] = tail(output, 2000)
				return Event{Type: "agent_command_finished", Data: data}, "", ""
			}
			return Event{Type: "agent_command_started", Data: data}, "", ""
		}
	}
	return Event{}, "", ""
}

func codexSandbox(value string) string {
	switch value {
	case "read-only", "workspace-write", "danger-full-access":
		return value
	default:
		return "workspace-write"
	}
}

func filterEnvironment(values map[string]string) map[string]string {
	allowed := map[string]bool{
		"OPENAI_API_KEY": true, "OPENAI_BASE_URL": true, "OPENAI_MODEL": true,
		"CODEX_WIRE_API": true, "CODEX_ENV_KEY": true, "AHA_PROVIDER_ID": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	}
	result := map[string]string{}
	credentialEnv := values["CODEX_ENV_KEY"]
	if !safeCredentialEnvName(credentialEnv) {
		credentialEnv = "OPENAI_API_KEY"
	}
	for key, value := range values {
		if allowed[strings.ToUpper(key)] || key == credentialEnv {
			result[key] = value
		}
	}
	return result
}

func safeCredentialEnvName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		valid := character == '_' || character >= 'A' && character <= 'Z' || index > 0 && character >= '0' && character <= '9'
		if !valid {
			return false
		}
	}
	return strings.Contains(value, "KEY") || strings.Contains(value, "TOKEN")
}

func tail(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}
