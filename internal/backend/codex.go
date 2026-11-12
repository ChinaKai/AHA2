package backend

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

type Codex struct {
	Binary          string
	Timeout         time.Duration
	IdleWarning     time.Duration
	IdleTimeout     time.Duration
	Heartbeat       time.Duration
	ModelsCachePath string
	// LivePoll overrides how often a running Turn claims live inbox messages.
	LivePoll time.Duration
}

// codexAppServerRecoveryPrompt continues a thread whose app-server process
// exited mid-turn. It must not restate the original task: the thread history
// already holds it, and a resumed turn only needs the interruption context.
const codexAppServerRecoveryPrompt = "The AHA backend process that was running this turn exited unexpectedly. The thread history, workspace changes and any completed tool results are preserved. Continue the original task from where it stopped. First inspect the current workspace state and the result of any interrupted command; do not repeat work that already completed. If a command's effect is uncertain, verify it before running it again. Do not reply with only a status report when there is remaining work."

func (adapter Codex) Execute(ctx context.Context, request Request, emit func(Event)) (turnResult Result, turnErr error) {
	binary := adapter.Binary
	if binary == "" {
		binary = "codex"
	}
	timeout := adapter.Timeout
	if timeout == 0 {
		timeout = defaultBackendTurnTimeout
	}
	// Codex reads MCP servers from $CODEX_HOME/config.toml. Rendering them there
	// keeps server env and headers out of the -c argv overrides, which the OS
	// exposes to every local process. The file is restored when the run ends.
	if content := codexMCPConfigTOML(request.MCPServers); content != "" {
		config, err := prepareCodexMCPConfig(ctx, request, content)
		if err != nil {
			return Result{}, err
		}
		defer func() {
			if cleanupErr := config.cleanup(ctx); cleanupErr != nil {
				// A failed restore can leave the original config replaced by the
				// generated one (or a backup behind), so fail the Turn instead of
				// reporting success.
				turnErr = errors.Join(turnErr, fmt.Errorf("restore Codex MCP config: %w", cleanupErr))
			}
		}()
	}
	// Live inbox delivery is the default whenever the transport can carry it.
	// A non-streaming runner, or a Turn with no inbox, keeps the one-shot exec
	// path unchanged.
	if request.LiveInbox != nil && workspace.StreamsInput(request.Runner) {
		liveResult, liveErr, started := adapter.executeLiveInbox(ctx, request, emit, binary, timeout)
		if started {
			if liveErr == nil || ctx.Err() != nil || errors.Is(liveErr, ErrBackendIdleTimeout) {
				return liveResult, liveErr
			}
			if errors.Is(liveErr, errCodexAppServerExited) && liveResult.ProviderSessionID != "" {
				// The app-server process exited on its own while the turn was still
				// running. This is exactly the long-silent-tool case: the tool may
				// run for minutes without producing a line, and a backend crash or
				// an external kill then looks identical to a finished turn unless the
				// thread is resumed. The thread, workspace changes and completed tool
				// results live in the rollout, so resume that thread once instead of
				// failing a Turn that was doing useful work.
				recovery := request
				recovery.ProviderSessionID = liveResult.ProviderSessionID
				recovery.Prompt = codexAppServerRecoveryPrompt
				if emit != nil {
					emit(Event{Type: "agent_progress", Data: map[string]any{
						"message":  "Codex app-server 在 Turn 中退出，正在用同一会话恢复；已投递的 live inbox 消息保留在会话历史中",
						"recovery": "app_server_resume", "cause": clipError(providerErrorText(liveErr.Error()), 500),
					}})
				}
				recovered, recoveryErr, recoveryStarted := adapter.executeLiveInbox(ctx, recovery, emit, binary, timeout)
				if recoveryStarted || recoveryErr == nil || ctx.Err() != nil || errors.Is(recoveryErr, ErrBackendIdleTimeout) {
					return recovered, recoveryErr
				}
				// The resumed app-server could not start a new turn. Continue the
				// same thread through the one-shot exec path instead.
				request = recovery
				if emit != nil {
					message := "恢复用的 Codex app-server 未能启动，已回退 codex exec 继续同一会话；本 Turn 不会实时接收 live inbox 消息"
					if recoveryErr != nil {
						message += "：" + clipError(providerErrorText(recoveryErr.Error()), 300)
					}
					emit(Event{Type: "agent_progress", Data: map[string]any{
						"message": message, "fallback": "codex_exec",
					}})
				}
			} else {
				return liveResult, liveErr
			}
		} else if liveErr == nil || ctx.Err() != nil || errors.Is(liveErr, ErrBackendIdleTimeout) {
			return liveResult, liveErr
		} else {
			// app-server could not start the turn (an unsupported CLI or a
			// transport that cannot carry a live stdin channel, such as a Windows
			// SSH remote). No message was claimed, so the Turn still runs through
			// the one-shot exec path and the next Turn receives the messages.
			if emit != nil {
				message := "Codex app-server 未能在 Turn 开始前启动，已回退到 codex exec；本 Turn 不会实时接收 live inbox 消息"
				if liveErr != nil {
					message += "：" + clipError(providerErrorText(liveErr.Error()), 300)
				}
				emit(Event{Type: "agent_progress", Data: map[string]any{
					"message": message, "fallback": "codex_exec",
				}})
			}
		}
	}
	idleWarning, idleTimeout, heartbeat := backendWatchdogDurations(adapter.IdleWarning, adapter.IdleTimeout, adapter.Heartbeat)
	catalogPath := adapter.ensureModelCatalog(ctx, request)
	args := codexArguments(request, catalogPath)
	environment := filterEnvironment(request.Environment)
	var reply, sessionID, providerError string
	rollout := &codexRollout{
		home:   strings.TrimSpace(request.Environment["CODEX_HOME"]),
		thread: strings.TrimSpace(request.ProviderSessionID),
		runner: request.Runner,
	}
	var pendingUnphasedMessage *Event
	var emitMu sync.Mutex
	send := func(event Event) {
		if event.Type == "" || emit == nil {
			return
		}
		emitMu.Lock()
		emit(event)
		emitMu.Unlock()
	}
	flushPendingMessage := func(final bool) {
		if pendingUnphasedMessage == nil {
			return
		}
		pendingUnphasedMessage.Data["intermediate"] = !final
		pendingUnphasedMessage.Data["final"] = final
		send(*pendingUnphasedMessage)
		pendingUnphasedMessage = nil
	}
	runContext, cancelRun := context.WithCancelCause(ctx)
	// A resumed session appends to the rollout it already owns. Skip everything
	// that exists before the process starts so only this turn's thinking is
	// projected; a fresh session has no file yet and starts at zero.
	rollout.prime(runContext)
	activity := make(chan string, 1)
	monitorDone := make(chan struct{})
	go monitorBackendActivity(runContext, idleWarning, idleTimeout, heartbeat, activity, send, cancelRun, monitorDone)
	result, err := request.Runner.Run(runContext, workspace.Command{
		Executable:     binary,
		Args:           args,
		Dir:            request.WorkDir,
		Env:            environment,
		Stdin:          request.Prompt,
		Timeout:        timeout,
		KillTree:       true,
		ReverseForward: request.ReverseForward,
	}, func(line string) {
		for _, event := range rollout.drain(runContext, false) {
			send(event)
		}
		event, parsedReply, parsedSession := parseCodexLine(line)
		select {
		case activity <- event.Type:
		default:
		}
		if parsedReply != "" {
			reply = parsedReply
		}
		if parsedSession != "" {
			sessionID = parsedSession
			rollout.thread = parsedSession
		}
		if event.Type == "agent_error" {
			providerError = strings.TrimSpace(fmt.Sprint(event.Data["message"]))
			if providerError == "<nil>" {
				providerError = ""
			}
		}
		if event.Type == "agent_message" {
			if _, hasPhase := event.Data["phase"]; !hasPhase {
				flushPendingMessage(false)
				pendingUnphasedMessage = &event
				return
			}
			flushPendingMessage(false)
		} else if pendingUnphasedMessage != nil && event.Type != "" {
			flushPendingMessage(event.Type == "agent_usage")
		}
		if event.Type == "agent_reasoning" {
			rollout.note(fmt.Sprint(event.Data["text"]))
		}
		send(event)
	})
	for _, event := range rollout.drain(runContext, true) {
		send(event)
	}
	flushPendingMessage(true)
	cause := context.Cause(runContext)
	cancelRun(context.Canceled)
	<-monitorDone
	if errors.Is(cause, ErrBackendIdleTimeout) {
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, ErrBackendIdleTimeout
	}
	if err != nil {
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, err
	}
	if sessionID == "" {
		sessionID = request.ProviderSessionID
	}
	if result.ExitCode != 0 {
		message := providerErrorText(result.Stderr)
		if message == "" {
			message = providerError
		}
		if message == "" {
			message = fmt.Sprintf("codex exited with code %d", result.ExitCode)
		}
		message = clipError(message, 8000)
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%s", message)
	}
	if strings.TrimSpace(reply) == "" {
		return Result{ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("codex returned no agent message")
	}
	return Result{Reply: strings.TrimSpace(reply), ExitCode: result.ExitCode, ProviderSessionID: sessionID}, nil
}

func codexArguments(request Request, catalogPath string) []string {
	args := codexConfigArguments(request, catalogPath)
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

// codexAppServerArguments builds the long-lived app-server invocation. Model
// selection is passed per thread/turn, so the exec-only -m flag is omitted.
func codexAppServerArguments(request Request, catalogPath string) []string {
	args := codexConfigArguments(request, catalogPath)
	return append(args, "--disable", "multi_agent", "app-server")
}

// codexConfigArguments is the part both surfaces share: provider overrides,
// effort, catalog, stream recovery, and MCP registrations.
func codexConfigArguments(request Request, catalogPath string) []string {
	args := []string{}
	providerID := codexProviderID(request)
	baseURL := request.Environment["OPENAI_BASE_URL"]
	wireAPI := request.Environment["CODEX_WIRE_API"]
	envKey := request.Environment["CODEX_ENV_KEY"]
	if wireAPI == "" {
		wireAPI = "responses"
	}
	if envKey == "" {
		envKey = "OPENAI_API_KEY"
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(effort))
	}
	// Codex calls its accelerated tier "Fast": roughly 1.5x the speed at a higher
	// token rate. It is requested by name, and only when the Task asked for it.
	if request.FastMode {
		args = append(args, "-c", "service_tier="+strconv.Quote("priority"))
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
	if baseURL != "" && request.StreamIdleTimeoutMS > 0 {
		args = append(args, "-c", fmt.Sprintf("model_providers.%s.stream_idle_timeout_ms=%d", providerID, request.StreamIdleTimeoutMS))
	}
	if baseURL != "" && request.StreamMaxRetries > 0 {
		args = append(args, "-c", fmt.Sprintf("model_providers.%s.stream_max_retries=%d", providerID, request.StreamMaxRetries))
	}
	return args
}

// codexProviderID resolves the provider id both the exec and app-server
// surfaces must use. New Codex versions reserve built-in ids such as openai,
// so a legacy AHA import carrying that id is renamed deterministically.
func codexProviderID(request Request) string {
	providerID := request.Environment["AHA_PROVIDER_ID"]
	baseURL := request.Environment["OPENAI_BASE_URL"]
	if providerID == "" {
		if baseURL == "" {
			providerID = "openai"
		} else {
			providerID = "aha2_provider"
		}
	}
	if baseURL != "" && providerID == "openai" {
		providerID = "openai-custom"
	}
	return providerID
}

// codexMCPKey accepts only TOML bare-key characters and normalizes hyphens to
// underscores. Codex derives the model-facing tool namespace from this key;
// a hyphenated namespace (aha2-pet) was accepted by mcp list but its tools did
// not register in headless exec, while the underscore namespace (aha2_channel)
// worked. Keeping the key to [A-Za-z0-9_] makes the two paths identical.
func codexMCPKey(name string) string {
	name = strings.ReplaceAll(strings.TrimSpace(name), "-", "_")
	if name == "" {
		return ""
	}
	for _, character := range name {
		valid := character == '_' || character == '-' ||
			character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9'
		if !valid {
			return ""
		}
	}
	return name
}

// tomlString renders a TOML basic string. Go/JSON escaping is a compatible
// subset for the URL, command and credential-key values Codex receives here.
func tomlString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func headersWithoutAuthorization(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		if strings.EqualFold(strings.TrimSpace(key), "Authorization") {
			continue
		}
		result[key] = value
	}
	return result
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
	if request.AutoCompactWindow > 0 {
		entry["auto_compact_token_limit"] = request.AutoCompactWindow
	} else {
		delete(entry, "auto_compact_token_limit")
	}
	payload, err := json.MarshalIndent(map[string]any{"models": []map[string]any{entry}}, "", "  ")
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", request.Model, request.ContextWindow, request.AutoCompactWindow)))
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
	if err := workspace.WriteRemoteTextFile(ctx, request.Runner, path, string(payload)); err != nil {
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
	command := workspace.Command{
		Executable: "sh", Args: []string{"-c", `cat "$HOME/.codex/models_cache.json"`},
		Dir: request.WorkDir, Timeout: 20 * time.Second,
	}
	if workspace.IsWindowsRunner(request.Runner) {
		command = workspace.Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				`[Console]::Out.Write([IO.File]::ReadAllText((Join-Path $HOME '.codex\models_cache.json'))) `},
			Dir: request.WorkDir, Timeout: 20 * time.Second,
		}
	}
	result, err := request.Runner.Run(ctx, command, nil)
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
		// codex exec reports provider failures as a top-level error event. Keep
		// the full message and any nested error object instead of projecting an
		// empty string when the CLI puts the cause outside "message".
		message := ""
		if text, ok := payload["message"].(string); ok {
			message = strings.TrimSpace(text)
		}
		if message == "" {
			message = errorValueText(payload["error"])
		}
		if message == "" {
			message = compactJSONValue(json.RawMessage(line))
		}
		if message == "" {
			message = "codex reported an error"
		}
		return Event{Type: "agent_error", Data: map[string]any{"message": providerErrorText(message)}}, "", ""
	case "item.started", "item.completed":
		item, _ := payload["item"].(map[string]any)
		itemType, _ := item["type"].(string)
		if itemType == "reasoning" && rawType == "item.completed" {
			// Codex reports the model's reasoning summary as its own item. The
			// text lives in item.text on older builds and in summary[] entries
			// (type summary_text) on newer ones; accept either.
			if text := codexReasoningText(item); text != "" {
				return Event{Type: "agent_reasoning", Data: map[string]any{"text": text, "reasoning": true}}, "", ""
			}
		}
		if itemType == "agent_message" && rawType == "item.completed" {
			text, _ := item["text"].(string)
			data := map[string]any{"text": text}
			if phase, _ := item["phase"].(string); phase != "" {
				data["phase"] = phase
				data["intermediate"] = phase == "commentary"
				data["final"] = phase == "final_answer"
			}
			return Event{Type: "agent_message", Data: data}, text, ""
		}
		if itemType == "command_execution" {
			data := map[string]any{
				"tool_call_id": item["id"], "command": item["command"], "status": item["status"], "exit_code": item["exit_code"],
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
		"CODEX_HOME":         true,
		"AHA2_AGENT_API_URL": true, "AHA2_AGENT_API_TOKEN": true,
		"AHA2_SCRATCH_DIR": true,
		"HTTP_PROXY":       true, "HTTPS_PROXY": true, "NO_PROXY": true,
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

// codexReasoningText joins the reasoning text Codex exposes for one item.
//
// The shape depends on the surface: a rollout stores content[] entries as
// {"type":"reasoning_text","text":...} and summary[] entries as summary_text
// objects, while the app-server JSON stream flattens both to plain strings.
// Real rollouts (codex-cli 0.146) carry summary: [] with everything in
// content[], so content must be read or every reasoning step looks empty.
func codexReasoningText(item map[string]any) string {
	parts := make([]string, 0, 4)
	appendText := func(value string) {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, strings.TrimSpace(value))
		}
	}
	appendList := func(value any) {
		list, ok := value.([]any)
		if !ok {
			return
		}
		for _, entry := range list {
			switch typed := entry.(type) {
			case string:
				appendText(typed)
			case map[string]any:
				if text, _ := typed["text"].(string); text != "" {
					appendText(text)
				}
			}
		}
	}
	if text, _ := item["text"].(string); text != "" {
		appendText(text)
	}
	appendList(item["summary"])
	appendList(item["content"])
	return strings.Join(parts, "\n\n")
}

// codexRollout follows the session rollout Codex writes under CODEX_HOME while
// a turn runs. `codex exec --json` only exposes the model's reasoning summary,
// and providers that return raw reasoning instead (reasoning_text content with
// an empty summary) leave the stream with no reasoning item at all. The rollout
// keeps the raw text, so AHA reads it and projects it as a reasoning event.
//
// The rollout lives inside the workspace, not on the AHA host: a WSL or SSH
// workspace stores it under a path only the runner can open. Reads therefore go
// through the runner (tail from the last offset); a Windows-local workspace
// falls back to reading the file directly. Draining is throttled and happens on
// stdout lines, so no extra goroutine races the runner.
type codexRollout struct {
	home      string
	thread    string
	path      string
	offset    int64
	seenIDs   map[string]bool
	seenText  map[string]bool
	runner    workspace.Runner
	lastDrain time.Time
}

const codexRolloutInterval = 700 * time.Millisecond

// prime positions the reader at the end of an existing rollout, before the turn
// starts writing. Reasoning for this turn is produced only after this point, so
// nothing earlier has to be parsed, buffered or filtered: history is simply not
// read. A fresh session has no rollout yet and stays at offset zero.
func (rollout *codexRollout) prime(ctx context.Context) {
	if rollout == nil || rollout.home == "" || rollout.thread == "" {
		return
	}
	remote := rollout.runner != nil && !workspace.IsWindowsRunner(rollout.runner)
	path := ""
	if remote {
		path = rollout.discover(ctx)
	} else {
		matches, err := filepath.Glob(filepath.Join(rollout.home, "sessions", "*", "*", "*", "rollout-*"+rollout.thread+".jsonl"))
		if err == nil && len(matches) > 0 {
			sort.Strings(matches)
			path = matches[len(matches)-1]
		}
	}
	if path == "" {
		return
	}
	size, ok := rollout.fileSize(ctx, path, remote)
	if !ok {
		return
	}
	rollout.path = path
	rollout.offset = size
}

// fileSize returns the current length of path, through the runner when the file
// lives in the workspace.
func (rollout *codexRollout) fileSize(ctx context.Context, path string, remote bool) (int64, bool) {
	if !remote {
		info, err := os.Stat(path)
		if err != nil {
			return 0, false
		}
		return info.Size(), true
	}
	result, err := rollout.runner.Run(ctx, workspace.Command{
		Executable: "sh",
		Args:       []string{"-c", "wc -c < " + quoteCommandWord(path)},
		Timeout:    5 * time.Second,
	}, nil)
	if err != nil || result.ExitCode != 0 {
		return 0, false
	}
	size, err := strconv.ParseInt(strings.TrimSpace(result.Stdout), 10, 64)
	if err != nil || size < 0 {
		return 0, false
	}
	return size, true
}

// note records reasoning text already emitted from stdout so the rollout copy
// is not shown twice.
func (rollout *codexRollout) note(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if rollout.seenText == nil {
		rollout.seenText = map[string]bool{}
	}
	rollout.seenText[text] = true
}

func (rollout *codexRollout) drain(ctx context.Context, force bool) []Event {
	if rollout == nil || rollout.home == "" || rollout.thread == "" {
		return nil
	}
	if !force && time.Since(rollout.lastDrain) < codexRolloutInterval {
		return nil
	}
	rollout.lastDrain = time.Now()
	if rollout.runner == nil || workspace.IsWindowsRunner(rollout.runner) {
		return rollout.drainFile()
	}
	return rollout.drainRunner(ctx)
}

// drainFile reads the rollout directly. Only valid when the file is visible to
// the AHA process itself.
func (rollout *codexRollout) drainFile() []Event {
	if rollout.path == "" {
		matches, err := filepath.Glob(filepath.Join(rollout.home, "sessions", "*", "*", "*", "rollout-*"+rollout.thread+".jsonl"))
		if err != nil || len(matches) == 0 {
			return nil
		}
		sort.Strings(matches)
		rollout.path = matches[len(matches)-1]
	}
	file, err := os.Open(rollout.path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= rollout.offset {
		return nil
	}
	if _, err := file.Seek(rollout.offset, io.SeekStart); err != nil {
		return nil
	}
	data := make([]byte, info.Size()-rollout.offset)
	read, _ := io.ReadFull(file, data)
	return rollout.consume(string(data[:read]))
}

// drainRunner tails the rollout through the workspace runner, which is the only
// way to reach a WSL or SSH workspace path from the AHA host.
func (rollout *codexRollout) drainRunner(ctx context.Context) []Event {
	if rollout.path == "" {
		rollout.path = rollout.discover(ctx)
	}
	if rollout.path == "" {
		return nil
	}
	result, err := rollout.runner.Run(ctx, workspace.Command{
		Executable: "tail",
		Args:       []string{"-c", "+" + strconv.FormatInt(rollout.offset+1, 10), rollout.path},
		Timeout:    5 * time.Second,
	}, nil)
	if err != nil || result.ExitCode != 0 {
		return nil
	}
	return rollout.consume(result.Stdout)
}

func (rollout *codexRollout) discover(ctx context.Context) string {
	script := "ls -1t " + quoteCommandWord(filepath.ToSlash(rollout.home)) + "/sessions/*/*/*/rollout-*" +
		quoteCommandWord(rollout.thread) + ".jsonl 2>/dev/null | head -1"
	result, err := rollout.runner.Run(ctx, workspace.Command{
		Executable: "sh",
		Args:       []string{"-c", script},
		Timeout:    5 * time.Second,
	}, nil)
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

// consume parses complete rollout lines and advances the offset by exactly the
// bytes consumed, so a partially written line is retried on the next drain.
func (rollout *codexRollout) consume(chunk string) []Event {
	cut := strings.LastIndexByte(chunk, '\n')
	if cut < 0 {
		return nil
	}
	complete := chunk[:cut+1]
	rollout.offset += int64(len(complete))
	events := make([]Event, 0, 2)
	for _, line := range strings.Split(strings.TrimSuffix(complete, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &record) != nil || record.Type != "response_item" {
			continue
		}
		if fmt.Sprint(record.Payload["type"]) != "reasoning" {
			continue
		}
		id := strings.TrimSpace(fmt.Sprint(record.Payload["id"]))
		if id != "" && rollout.seenIDs[id] {
			continue
		}
		text := strings.TrimSpace(codexReasoningText(record.Payload))
		if text == "" || rollout.seenText[text] {
			continue
		}
		if id != "" {
			if rollout.seenIDs == nil {
				rollout.seenIDs = map[string]bool{}
			}
			rollout.seenIDs[id] = true
		}
		rollout.note(text)
		events = append(events, Event{Type: "agent_reasoning", Data: map[string]any{"text": text, "reasoning": true}})
	}
	return events
}

func quoteCommandWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
