package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

type Codex struct {
	Binary  string
	Timeout time.Duration
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
	args := codexArguments(request)
	environment := filterEnvironment(request.Environment)
	var reply, sessionID string
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
			message = fmt.Sprintf("codex exited with code %d", result.ExitCode)
		}
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%s", message)
	}
	if strings.TrimSpace(reply) == "" {
		return Result{ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("codex returned no agent message")
	}
	return Result{Reply: strings.TrimSpace(reply), ExitCode: result.ExitCode, ProviderSessionID: sessionID}, nil
}

func codexArguments(request Request) []string {
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
	if request.ContextWindow > 0 {
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
