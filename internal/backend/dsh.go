package backend

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

//go:embed aha_live_inbox.mjs
var dshLiveInboxPluginSource string

// DSH runs DeepSeek Harness in its one-shot headless profile. The profile patch
// is generated per AHA backend session so DSH reuses the Env Provider selected
// by AHA without reading or modifying the operator's normal DSH profile.
type DSH struct {
	Binary      string
	Timeout     time.Duration
	IdleWarning time.Duration
	IdleTimeout time.Duration
	Heartbeat   time.Duration
}

func (adapter DSH) Execute(ctx context.Context, request Request, emit func(Event)) (Result, error) {
	binary := strings.TrimSpace(adapter.Binary)
	if binary == "" {
		binary = "dsh"
	}
	timeout := adapter.Timeout
	if timeout == 0 {
		timeout = defaultBackendTurnTimeout
	}
	patchPath, err := adapter.ensureProfilePatch(ctx, request)
	if err != nil {
		return Result{}, err
	}
	idleWarning, idleTimeout, heartbeat := backendWatchdogDurations(adapter.IdleWarning, adapter.IdleTimeout, adapter.Heartbeat)
	args := dshArguments(request, patchPath)
	environment := filterDSHEnvironment(request.Environment)
	environment["DSH_PERMISSION_MODE"] = dshPermissionMode(request.Filesystem, request.Approval)

	var reply, sessionID, providerError string
	var usage map[string]any
	var emitMu sync.Mutex
	send := func(event Event) {
		if event.Type == "" || emit == nil {
			return
		}
		emitMu.Lock()
		emit(event)
		emitMu.Unlock()
	}
	runContext, cancelRun := context.WithCancelCause(ctx)
	activity := make(chan string, 1)
	monitorDone := make(chan struct{})
	go monitorBackendActivity(runContext, idleWarning, idleTimeout, heartbeat, activity, send, cancelRun, monitorDone)
	stream := &dshStream{}
	result, runErr := request.Runner.Run(runContext, workspace.Command{
		Executable:     binary,
		Args:           args,
		Dir:            request.WorkDir,
		Env:            environment,
		Stdin:          request.Prompt,
		Timeout:        timeout,
		KillTree:       true,
		ReverseForward: request.ReverseForward,
	}, func(line string) {
		for _, event := range stream.Consume(line) {
			select {
			case activity <- event.Type:
			default:
			}
			if event.Type == "agent_error" {
				providerError = strings.TrimSpace(fmt.Sprint(event.Data["message"]))
				if providerError == "<nil>" {
					providerError = ""
				}
			}
			if event.Type == "agent_usage" {
				if next, ok := event.Data["usage"].(map[string]any); ok {
					usage = addDSHUsage(usage, next)
					event.Data["usage"] = cloneDSHUsage(usage)
				}
			}
			send(event)
		}
	})
	cause := context.Cause(runContext)
	cancelRun(context.Canceled)
	<-monitorDone
	if leftover := stream.Drain(); leftover != "" {
		send(dshCommentaryEvent(leftover))
	}
	reply = stream.Reply()
	sessionID = stream.SessionID()
	if sessionID == "" {
		sessionID = request.ProviderSessionID
	}
	if errors.Is(cause, ErrBackendIdleTimeout) {
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, ErrBackendIdleTimeout
	}
	if runErr != nil {
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, runErr
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = providerError
		}
		if message == "" {
			message = fmt.Sprintf("dsh exited with code %d", result.ExitCode)
		}
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%s", tail(message, 2000))
	}
	if strings.TrimSpace(reply) == "" {
		return Result{ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("dsh returned no agent message")
	}
	return Result{Reply: strings.TrimSpace(reply), ExitCode: result.ExitCode, ProviderSessionID: sessionID}, nil
}

func dshArguments(request Request, patchPath string) []string {
	args := []string{"--profile", "headless", "--patch", patchPath, "--json"}
	if request.ProviderSessionID != "" {
		args = append(args, "--session-id", request.ProviderSessionID)
	}
	return append(args, "-")
}

func dshPermissionMode(filesystem, approval string) string {
	if filesystem == "danger-full-access" || approval == "auto" {
		return "danger-full-access"
	}
	if filesystem == "read-only" {
		return "read-only"
	}
	return "workspace-write"
}

func filterDSHEnvironment(values map[string]string) map[string]string {
	allowed := map[string]bool{
		"DSH_HOME": true, "DSH_PERMISSION_MODE": true,
		"OPENAI_API_KEY": true, "OPENAI_BASE_URL": true, "OPENAI_MODEL": true,
		"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true,
		"ANTHROPIC_BASE_URL": true, "ANTHROPIC_MODEL": true,
		"CODEX_WIRE_API": true, "CODEX_ENV_KEY": true, "AHA_PROVIDER_ID": true,
		"AHA2_AGENT_API_URL": true, "AHA2_AGENT_API_TOKEN": true,
		"AHA2_LIVE_INBOX": true, "AHA2_LIVE_INBOX_DEBUG": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	}
	result := map[string]string{}
	credentialEnv := strings.TrimSpace(values["CODEX_ENV_KEY"])
	if !safeCredentialEnvName(credentialEnv) {
		credentialEnv = ""
	}
	for key, value := range values {
		if allowed[strings.ToUpper(key)] || key == credentialEnv {
			result[key] = value
		}
	}
	return result
}

func (adapter DSH) ensureProfilePatch(ctx context.Context, request Request) (string, error) {
	homeDir := strings.TrimSpace(request.Environment["DSH_HOME"])
	if homeDir == "" {
		homeDir = filepath.Join(request.WorkDir, ".aha2-context", "runtime", "dsh-home", "ad-hoc")
	}
	name := "aha-backend.yml"
	pluginPath := filepath.Join(homeDir, "aha-agent-api-env.mjs")
	liveInboxPath := filepath.Join(homeDir, "aha-live-inbox.mjs")
	payload, err := dshProfilePatch(
		request, dshPluginSpecifier(pluginPath), dshPluginSpecifier(liveInboxPath),
	)
	if err != nil {
		return "", err
	}
	if _, ok := request.Runner.(workspace.LocalRunner); ok {
		if err := os.MkdirAll(homeDir, 0o700); err != nil {
			return "", fmt.Errorf("prepare DSH home: %w", err)
		}
		if err := os.WriteFile(pluginPath, []byte(dshAgentAPIEnvPlugin()), 0o600); err != nil {
			return "", fmt.Errorf("write DSH Agent API environment plugin: %w", err)
		}
		if err := os.WriteFile(liveInboxPath, []byte(dshLiveInboxPluginSource), 0o600); err != nil {
			return "", fmt.Errorf("write DSH live inbox plugin: %w", err)
		}
		path := filepath.Join(homeDir, name)
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			return "", fmt.Errorf("write DSH profile patch: %w", err)
		}
		return path, nil
	}
	if err := workspace.EnsureRemoteDirectory(ctx, request.Runner, homeDir); err != nil {
		return "", fmt.Errorf("prepare DSH home: %w", err)
	}
	remoteDir := strings.ReplaceAll(homeDir, `\`, "/")
	remotePluginPath := pathpkg.Join(remoteDir, "aha-agent-api-env.mjs")
	if err := workspace.WriteRemoteTextFile(ctx, request.Runner, remotePluginPath, dshAgentAPIEnvPlugin()); err != nil {
		return "", fmt.Errorf("write DSH Agent API environment plugin: %w", err)
	}
	remoteLiveInboxPath := pathpkg.Join(remoteDir, "aha-live-inbox.mjs")
	if err := workspace.WriteRemoteTextFile(ctx, request.Runner, remoteLiveInboxPath, dshLiveInboxPluginSource); err != nil {
		return "", fmt.Errorf("write DSH live inbox plugin: %w", err)
	}
	path := pathpkg.Join(remoteDir, name)
	if err := workspace.WriteRemoteTextFile(ctx, request.Runner, path, payload); err != nil {
		return "", fmt.Errorf("write DSH profile patch: %w", err)
	}
	return path, nil
}

func dshPluginSpecifier(path string) string {
	return strings.ReplaceAll(filepath.ToSlash(path), `\`, "/")
}

func dshAgentAPIEnvPlugin() string {
	return `export const name = 'aha-agent-api-env'
export const inject = ['shellEnv']

export function apply(ctx) {
  ctx.shellEnv.register({
    name: 'aha-agent-api-env',
    variables: {
      DSH_AHA_AGENT_API_URL: { description: 'Current AHA Agent API base URL.' },
      DSH_AHA_AGENT_API_TOKEN: { description: 'Current Turn-scoped AHA Agent API capability.' },
    },
    resolve() {
      return {
        DSH_AHA_AGENT_API_URL: process.env.AHA2_AGENT_API_URL ?? '',
        DSH_AHA_AGENT_API_TOKEN: process.env.AHA2_AGENT_API_TOKEN ?? '',
      }
    },
  })
}
`
}

func dshProfilePatch(request Request, pluginURL, liveInboxURL string) (string, error) {
	api, baseURL, apiKeyEnv, err := dshProviderRuntime(request)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	output.WriteString("# Generated by AHA2. This file contains no credentials.\n")
	output.WriteString("- id: llm-pi-ai\n")
	output.WriteString("  config:\n")
	output.WriteString("    providers:\n")
	output.WriteString("      aha-env:\n")
	output.WriteString("        displayName: AHA Env Provider\n")
	output.WriteString("        api: " + api + "\n")
	output.WriteString("        baseURL: " + strconv.Quote(baseURL) + "\n")
	if apiKeyEnv != "" {
		output.WriteString("        apiKeyEnv: " + apiKeyEnv + "\n")
	}
	output.WriteString("        models:\n")
	output.WriteString("          - id: " + strconv.Quote(request.Model) + "\n")
	if request.ContextWindow > 0 {
		output.WriteString("            contextWindow: " + strconv.FormatInt(request.ContextWindow, 10) + "\n")
	}
	if request.SupportsImageInput {
		// Image input is the model default; only an explicit capability=false
		// arrives here as false, and then the modality is omitted so DSH keeps
		// its text-only fallback.
		output.WriteString("            input: [text, image]\n")
	}
	output.WriteString("            reasoningEfforts:\n")
	for _, effort := range dshReasoningEfforts {
		value := effort
		if effort == "off" && strings.HasPrefix(api, "openai-") {
			value = "none"
		}
		output.WriteString("              " + effort + ": " + value + "\n")
	}
	output.WriteString("\n- id: agent-default-model\n")
	output.WriteString("  config:\n")
	output.WriteString("    provider: aha-env\n")
	output.WriteString("    model: " + strconv.Quote(request.Model) + "\n")
	if effort := strings.ToLower(strings.TrimSpace(request.ReasoningEffort)); slices.Contains(dshReasoningEfforts, effort) {
		output.WriteString("    reasoningEffort: " + strconv.Quote(effort) + "\n")
	}
	output.WriteString("\n- id: approval\n")
	output.WriteString("  config:\n")
	output.WriteString("    policy: never\n")
	output.WriteString("\n- id: permission\n")
	output.WriteString("  config:\n")
	output.WriteString("    presets:\n")
	for _, preset := range []string{"read-only", "workspace-write", "danger-full-access"} {
		output.WriteString("      " + preset + ":\n")
		output.WriteString("        sandbox: " + preset + "\n")
		output.WriteString("        approval: never\n")
	}
	output.WriteString("\n- insert:\n")
	output.WriteString("    - id: aha-agent-api-env\n")
	output.WriteString("      name: " + strconv.Quote(pluginURL) + "\n")
	output.WriteString("    - id: aha-live-inbox\n")
	output.WriteString("      name: " + strconv.Quote(liveInboxURL) + "\n")
	for _, id := range []string{
		"tool-subagent", "tool-subagent-fork", "tool-subagent-control", "tool-workflow",
	} {
		output.WriteString("\n- id: " + id + "\n")
		output.WriteString("  disabled: true\n")
	}
	return output.String(), nil
}

var dshReasoningEfforts = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

func dshProviderRuntime(request Request) (string, string, string, error) {
	wire := strings.ToLower(strings.TrimSpace(request.WireAPI))
	if wire == "" {
		wire = strings.ToLower(strings.TrimSpace(request.Environment["CODEX_WIRE_API"]))
	}
	switch wire {
	case "responses", "chat", "chat_completions", "anthropic_messages":
	default:
		return "", "", "", fmt.Errorf("unsupported DSH wire API %q", wire)
	}

	if wire == "anthropic_messages" {
		baseURL := strings.TrimSpace(request.Environment["ANTHROPIC_BASE_URL"])
		if baseURL == "" {
			return "", "", "", fmt.Errorf("DSH Anthropic provider has no base URL")
		}
		apiKeyEnv := ""
		for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
			if strings.TrimSpace(request.Environment[name]) != "" {
				apiKeyEnv = name
				break
			}
		}
		return "anthropic-messages", baseURL, apiKeyEnv, nil
	}

	baseURL := strings.TrimSpace(request.Environment["OPENAI_BASE_URL"])
	if baseURL == "" {
		return "", "", "", fmt.Errorf("DSH OpenAI provider has no base URL")
	}
	apiKeyEnv := strings.TrimSpace(request.Environment["CODEX_ENV_KEY"])
	if !safeCredentialEnvName(apiKeyEnv) {
		apiKeyEnv = "OPENAI_API_KEY"
	}
	if strings.TrimSpace(request.Environment[apiKeyEnv]) == "" {
		apiKeyEnv = ""
	}
	api := "openai-responses"
	if wire == "chat" || wire == "chat_completions" {
		api = "openai-completions"
	}
	return api, baseURL, apiKeyEnv, nil
}

func parseDSHLine(line string) (Event, string, string) {
	var payload map[string]any
	if json.Unmarshal([]byte(line), &payload) != nil {
		return Event{}, "", ""
	}
	rawType, _ := payload["type"].(string)
	switch rawType {
	case "session":
		sessionID, _ := payload["sessionId"].(string)
		if sessionID == "" {
			return Event{}, "", ""
		}
		return Event{Type: "agent_session", Data: map[string]any{"provider_session_id": sessionID, "cwd": payload["cwd"]}}, "", sessionID
	case "text":
		text, _ := payload["text"].(string)
		return Event{Type: "agent_message", Data: map[string]any{"text": text, "intermediate": true}}, "", ""
	case "thinking":
		text, _ := payload["text"].(string)
		if output := dshReasoningText(text); output != "" {
			return Event{Type: "agent_reasoning", Data: map[string]any{"text": output, "reasoning": true}}, "", ""
		}
	case "tool_call":
		input, _ := payload["input"].(map[string]any)
		command, _ := input["command"].(string)
		return Event{Type: "agent_command_started", Data: map[string]any{
			"tool_call_id": payload["callId"], "tool_name": payload["tool"], "command": command, "status": "in_progress",
		}}, "", ""
	case "tool_result":
		result, _ := payload["result"].(string)
		return Event{Type: "agent_command_finished", Data: map[string]any{
			"tool_call_id": payload["callId"], "status": payload["status"], "output_tail": tail(result, 2000),
		}}, "", ""
	case "status":
		if phase, _ := payload["phase"].(string); phase == "step_end" {
			if usage, ok := payload["usage"].(map[string]any); ok {
				return Event{Type: "agent_usage", Data: map[string]any{"usage": normalizeDSHUsage(usage)}}, "", ""
			}
		}
	case "final":
		sessionID, _ := payload["sessionId"].(string)
		text, _ := payload["text"].(string)
		reason, _ := payload["reason"].(map[string]any)
		kind, _ := reason["kind"].(string)
		if kind == "" || kind == "completed" {
			return Event{Type: "agent_message", Data: map[string]any{"text": text, "final": true}}, text, sessionID
		}
		message := "DSH turn did not complete"
		if detail, ok := reason["error"].(map[string]any); ok {
			if value, _ := detail["message"].(string); value != "" {
				message = value
			}
		}
		return Event{Type: "agent_error", Data: map[string]any{"message": message}}, text, sessionID
	case "error":
		message, _ := payload["message"].(string)
		return Event{Type: "agent_error", Data: map[string]any{"message": message}}, "", ""
	}
	return Event{}, "", ""
}

const dshReasoningUpdateLimit = 2000

type dshStream struct {
	pending   []string
	reply     string
	sessionID string
}

// Consume projects one DSH JSON line into zero or more AHA events. DSH commits
// assistant text per step, so a text line is either interim commentary or the
// final answer. Text is buffered until the next action, and a buffered copy
// that only repeats the final answer is dropped to avoid a duplicate update.
func (stream *dshStream) Consume(line string) []Event {
	event, reply, session := parseDSHLine(line)
	if reply != "" {
		stream.reply = reply
	}
	if session != "" {
		stream.sessionID = session
	}
	if event.Type == "" {
		return nil
	}
	if event.Type == "agent_message" {
		if intermediate, _ := event.Data["intermediate"].(bool); intermediate {
			if text := dshEventText(event.Data, "text"); text != "" {
				stream.pending = append(stream.pending, text)
			}
			return nil
		}
		if final, _ := event.Data["final"].(bool); final {
			events := stream.flushEvents(dshEventText(event.Data, "text"))
			return append(events, event)
		}
	}
	if event.Type == "agent_session" || event.Type == "agent_usage" {
		return []Event{event}
	}
	return append(stream.flushEvents(""), event)
}

func (stream *dshStream) flushEvents(trimTail string) []Event {
	text := stream.takePending(trimTail)
	if text == "" {
		return nil
	}
	return []Event{dshCommentaryEvent(text)}
}

func (stream *dshStream) takePending(trimTail string) string {
	texts := stream.pending
	stream.pending = nil
	if trimTail != "" && len(texts) > 0 && strings.TrimSpace(texts[len(texts)-1]) == strings.TrimSpace(trimTail) {
		texts = texts[:len(texts)-1]
	}
	cleaned := make([]string, 0, len(texts))
	for _, text := range texts {
		if value := strings.TrimSpace(text); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return strings.Join(cleaned, "\n\n")
}

func (stream *dshStream) Drain() string {
	return stream.takePending("")
}

func (stream *dshStream) Reply() string {
	return strings.TrimSpace(stream.reply)
}

func (stream *dshStream) SessionID() string {
	return stream.sessionID
}

func dshCommentaryEvent(text string) Event {
	return Event{Type: "agent_message", Data: map[string]any{"text": text, "intermediate": true}}
}

func dshEventText(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return strings.TrimSpace(value)
}

func dshReasoningText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= dshReasoningUpdateLimit {
		return text
	}
	return string(runes[:dshReasoningUpdateLimit]) + "…"
}

func normalizeDSHUsage(input map[string]any) map[string]any {
	result := map[string]any{}
	mapping := map[string]string{
		"inputTokens": "input_tokens", "outputTokens": "output_tokens",
		"cacheReadTokens": "cached_input_tokens", "cacheWriteTokens": "cache_creation_input_tokens",
		"reasoningOutputTokens": "reasoning_output_tokens", "totalTokens": "total_tokens",
	}
	for source, target := range mapping {
		if value, ok := input[source]; ok {
			result[target] = value
		}
	}
	return result
}

func addDSHUsage(total, next map[string]any) map[string]any {
	result := cloneDSHUsage(total)
	for key, value := range next {
		result[key] = usageNumber(value) + usageNumber(result[key])
	}
	return result
}

func cloneDSHUsage(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func usageNumber(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
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
