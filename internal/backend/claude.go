package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

type Claude struct {
	Binary      string
	Timeout     time.Duration
	IdleWarning time.Duration
	IdleTimeout time.Duration
	Heartbeat   time.Duration
	// LivePoll overrides how often a stream-input run claims live messages.
	LivePoll time.Duration
	// LiveReplayGrace overrides how long stdin stays open after a result for
	// a written live message that has not been replayed yet.
	LiveReplayGrace time.Duration
}

func (adapter Claude) Execute(ctx context.Context, request Request, emit func(Event)) (Result, error) {
	binary := adapter.Binary
	if binary == "" {
		binary = "claude"
	}
	timeout := adapter.Timeout
	if timeout == 0 {
		timeout = defaultBackendTurnTimeout
	}
	idleWarning, idleTimeout, heartbeat := backendWatchdogDurations(adapter.IdleWarning, adapter.IdleTimeout, adapter.Heartbeat)
	args := []string{
		"-p", "--output-format", "stream-json", "--verbose",
	}
	var live *claudeLiveInput
	stdin := request.Prompt
	// Live inbox delivery is the default whenever the transport can carry it.
	// The one-shot path remains for a runner that cannot stream stdin or a Turn
	// with no inbox to deliver into.
	if request.LiveInbox != nil && workspace.StreamsInput(request.Runner) {
		live = newClaudeLiveInput(request.LiveInbox, adapter.LivePoll, adapter.LiveReplayGrace)
		args = append(args, "--input-format", "stream-json", "--replay-user-messages")
		stdin = claudeUserMessage(newClaudeMessageUUID(), request.Prompt)
	}
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		args = append(args, "--effort", effort)
	}
	args = append(args, claudePermissionArgs(request.Filesystem, request.Approval)...)
	if request.ProviderSessionID != "" {
		args = append(args, "--resume", request.ProviderSessionID)
	}
	if config := claudeMCPConfig(request.MCPServers); config != "" {
		args = append(args, "--mcp-config", config)
	}
	if len(request.AllowedTools) > 0 {
		args = append(args, "--allowedTools")
		args = append(args, request.AllowedTools...)
	}
	startedAt := time.Now().UTC()
	var reply, sessionID, providerError string
	var terminalError bool
	var stream claudeStream
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
	command := workspace.Command{
		Executable:     binary,
		Args:           args,
		Dir:            request.WorkDir,
		Env:            filterClaudeEnvironment(request.Environment),
		Stdin:          stdin,
		Timeout:        timeout,
		KillTree:       true,
		ReverseForward: request.ReverseForward,
	}
	if live != nil {
		command.Input = live.input
		go live.run(runContext)
	}
	result, err := request.Runner.Run(runContext, command, func(line string) {
		if live != nil {
			live.observe(runContext, line)
		}
		events, parsedReply, parsedSession := parseClaudeLine(line)
		if parsedReply != "" {
			if reply != "" {
				// A live message queued behind a result is answered by a
				// further result in the same run. Only the last one becomes
				// the Turn reply, so keep the earlier answer visible.
				send(Event{Type: "agent_message", Data: map[string]any{"text": reply, "intermediate": true}})
			}
			reply = parsedReply
			// A live-inbox run can produce several results. A later success
			// supersedes an earlier failed result; without this reset the first
			// failure would fail a run that actually recovered (the same run
			// keeps running while more input is queued).
			terminalError = false
			providerError = ""
		}
		if parsedSession != "" {
			sessionID = parsedSession
		}
		for _, event := range stream.Consume(events) {
			select {
			case activity <- event.Type:
			default:
			}
			if event.Type == "agent_error" {
				providerError = strings.TrimSpace(fmt.Sprint(event.Data["message"]))
				if providerError == "<nil>" {
					providerError = ""
				}
				if terminal, _ := event.Data["terminal"].(bool); terminal {
					terminalError = true
				}
			}
			if usage, ok := event.Data["usage"].(map[string]any); ok && len(usage) > 0 {
				send(Event{Type: "agent_usage", Data: map[string]any{"usage": usage}})
			}
			send(event)
		}
	})
	if live != nil {
		live.finish(ctx)
	}
	for _, event := range stream.Flush() {
		send(event)
	}
	cause := context.Cause(runContext)
	cancelRun(context.Canceled)
	<-monitorDone
	if errors.Is(cause, ErrBackendIdleTimeout) {
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, ErrBackendIdleTimeout
	}
	if err != nil {
		if providerError != "" && claudeContextWindowExceeded(providerError) {
			return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%w: %s", ErrContextWindowExceeded, clipError(providerError, 8000))
		}
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, err
	}
	if sessionID == "" {
		sessionID = request.ProviderSessionID
	}
	since := startedAt.Add(-2 * time.Second)
	configDir := ""
	transcriptError := ""
	if result.ExitCode != 0 || strings.TrimSpace(reply) == "" {
		configDir = claudeTranscriptDir(ctx, request)
		transcriptError = workspace.ClaudeTranscriptLastErrorText(ctx, request.Runner, configDir, request.WorkDir, sessionID, since)
	}
	if result.ExitCode != 0 {
		message := providerErrorText(result.Stderr)
		if transcriptError != "" {
			// The CLI can hide the provider's error in the transcript while
			// exiting non-zero with an empty stderr. Prefer the transcript so
			// every provider error is shown verbatim, not just the 400 case.
			message = providerErrorText(transcriptError)
		} else if message == "" {
			message = providerError
		}
		if message == "" {
			message = fmt.Sprintf("claude exited with code %d", result.ExitCode)
		}
		message = clipError(message, 8000)
		if claudeContextWindowExceeded(message) {
			return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%w: %s", ErrContextWindowExceeded, message)
		}
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%s", message)
	}
	// A result event with an error subtype is terminal even when commentary
	// text arrived earlier. Returning that partial reply as success would hide
	// the provider failure and drop the only diagnostic the CLI sent.
	if terminalError && strings.TrimSpace(providerError) != "" {
		message := clipError(providerError, 8000)
		if claudeContextWindowExceeded(message) {
			return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%w: %s", ErrContextWindowExceeded, message)
		}
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%s", message)
	}
	if providerError != "" && claudeContextWindowExceeded(providerError) {
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%w: %s", ErrContextWindowExceeded, clipError(providerError, 8000))
	}
	if strings.TrimSpace(reply) == "" && transcriptError != "" {
		message := providerErrorText(transcriptError)
		if claudeContextWindowExceeded(message) {
			return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%w: %s", ErrContextWindowExceeded, message)
		}
		return Result{ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%s", message)
	}
	if providerError != "" && strings.TrimSpace(reply) == "" {
		return Result{ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%s", clipError(providerError, 8000))
	}
	if strings.TrimSpace(reply) == "" {
		// A clean exit without a result line can still have written the reply
		// to its transcript (observed with Claude Code on Windows). Recover the
		// final assistant text before reporting a missing agent message.
		for attempt := 0; attempt < 3 && strings.TrimSpace(reply) == ""; attempt++ {
			reply = workspace.ClaudeTranscriptLastAssistantText(
				ctx, request.Runner, configDir, request.WorkDir, sessionID, since,
			)
			if strings.TrimSpace(reply) == "" {
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	if strings.TrimSpace(reply) == "" {
		return Result{ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("claude returned no agent message")
	}
	return Result{Reply: strings.TrimSpace(reply), ExitCode: result.ExitCode, ProviderSessionID: sessionID}, nil
}

// claudeMCPConfig renders the neutral MCP registrations as Claude Code's
// --mcp-config JSON. A bearer-token server references its environment variable
// instead of embedding the value, so the turn credential never appears in argv.
// Claude Code expands ${VAR} in the config it loads.
func claudeMCPConfig(servers []MCPServer) string {
	configs := map[string]any{}
	for _, server := range servers {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(server.Transport)) {
		case "stdio":
			if strings.TrimSpace(server.Command) == "" {
				continue
			}
			entry := map[string]any{"type": "stdio", "command": server.Command}
			if len(server.Args) > 0 {
				entry["args"] = server.Args
			}
			if len(server.Env) > 0 {
				entry["env"] = server.Env
			}
			configs[name] = entry
		case "http":
			if strings.TrimSpace(server.URL) == "" {
				continue
			}
			url := server.URL
			if env := strings.TrimSpace(server.URLFromEnv); env != "" {
				url = "${" + env + "}" + server.URLPath
			}
			entry := map[string]any{"type": "http", "url": url}
			headers := map[string]string{}
			for key, value := range server.Headers {
				headers[key] = value
			}
			if env := strings.TrimSpace(server.BearerTokenEnv); env != "" {
				headers["Authorization"] = "Bearer ${" + env + "}"
			}
			if len(headers) > 0 {
				entry["headers"] = headers
			}
			configs[name] = entry
		}
	}
	if len(configs) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(map[string]any{"mcpServers": configs})
	return string(encoded)
}

const claudeCoordinatorDisallowedTools = "Task,Agent,Bash,Edit,Write,NotebookEdit,WebFetch,WebSearch,BashOutput,KillBash"

// claudePermissionArgs maps the AHA2 sandbox (filesystem) + approval settings
// to Claude Code permission flags. danger-full-access or approval=full bypass
// all permission checks. Read-only deliberately does not use plan mode: plan
// mode blocks MCP tools such as pet_update_core, so the coordinator needs a
// write-denied shell/edit surface with MCP tools still callable.
func claudePermissionArgs(filesystem, approval string) []string {
	if filesystem == "danger-full-access" || approval == "full" || approval == "auto" {
		return []string{"--dangerously-skip-permissions", "--disallowedTools", "Task,Agent"}
	}
	switch filesystem {
	case "read-only":
		return []string{"--permission-mode", "bypassPermissions", "--disallowedTools", claudeCoordinatorDisallowedTools}
	default:
		return []string{"--permission-mode", "acceptEdits", "--disallowedTools", "Task,Agent"}
	}
}

// parseClaudeLine maps one stream-json line to the AHA events it carries. An
// assistant message can hold several content blocks (thinking, text, tool_use)
// and they must all be projected: returning only the first match used to drop
// the visible text and the tool start that followed a thinking block, which is
// why Claude turns showed almost no Agent Updates.
func parseClaudeLine(line string) ([]Event, string, string) {
	var payload map[string]any
	if json.Unmarshal([]byte(line), &payload) != nil {
		return nil, "", ""
	}
	rawType, _ := payload["type"].(string)
	switch rawType {
	case "system":
		subtype, _ := payload["subtype"].(string)
		if subtype == "init" {
			if sessionID, ok := payload["session_id"].(string); ok && sessionID != "" {
				return []Event{{Type: "agent_session", Data: map[string]any{"provider_session_id": sessionID}}}, "", sessionID
			}
		}
		// Claude emits api_retry (and some builds api_error) when a request is
		// retried automatically. It is transient progress, not a Turn failure:
		// projecting it as agent_error would paint every retry as an error and
		// poison the final providerError.
		if subtype == "api_retry" || subtype == "api_error" {
			return []Event{{Type: "agent_progress", Data: claudeAPIRetryData(payload)}}, "", ""
		}
	case "result":
		sessionID, _ := payload["session_id"].(string)
		subtype, _ := payload["subtype"].(string)
		isError := boolValue(payload["is_error"]) || boolValue(payload["isError"])
		if subtype == "success" && !isError {
			if text, ok := payload["result"].(string); ok && text != "" {
				data := map[string]any{"text": text, "final": true}
				if usage, ok := payload["usage"].(map[string]any); ok {
					data["usage"] = usage
				}
				return []Event{{Type: "agent_message", Data: data}}, text, sessionID
			}
			// A success result without text is not an error: Execute falls back
			// to the transcript before reporting a missing agent message.
			return nil, "", sessionID
		}
		// Claude 2.1.x puts result failures in errors[] (array of strings),
		// while some builds/gateways use error with a string or object. Keep
		// every form plus the subtype/status/stop reason; terminal marks a
		// failed result that must not be reported as a successful Turn just
		// because commentary arrived earlier.
		data := map[string]any{"message": claudeResultErrorText(payload), "terminal": true}
		if subtype != "" {
			data["subtype"] = subtype
		}
		if status, ok := claudeErrorStatus(payload); ok {
			data["status"] = status
		}
		return []Event{{Type: "agent_error", Data: data}}, "", sessionID
	case "assistant":
		// Claude Code reports provider failures (for example a context-window
		// 400) as a synthetic assistant message instead of a result event. The
		// text is the only place that carries the provider's explanation, so
		// project it as an error and let Execute surface it verbatim.
		if boolValue(payload["isApiErrorMessage"]) || boolValue(payload["is_api_error_message"]) {
			text := claudeAssistantText(payload)
			// errorDetails carries the raw provider text (for example the exact
			// prompt-too-long token counts) that the user-facing synthetic
			// message may have replaced with a short summary.
			if details := claudeString(payload, "errorDetails", "error_details"); details != "" && !strings.Contains(text, details) {
				if text == "" {
					text = details
				} else {
					text += " — " + details
				}
			}
			if meta := claudeErrorMeta(payload); len(meta) > 0 {
				if text == "" {
					text = "Claude API error"
				}
				text += " [" + strings.Join(meta, "; ") + "]"
			}
			if text == "" {
				text = "Claude API error"
			}
			data := map[string]any{"message": providerErrorText(text)}
			if status, ok := claudeErrorStatus(payload); ok {
				data["status"] = status
			}
			if code := claudeString(payload, "apiError", "api_error"); code != "" {
				data["code"] = code
			}
			return []Event{{Type: "agent_error", Data: data}}, "", ""
		}
		message, _ := payload["message"].(map[string]any)
		content, _ := message["content"].([]any)
		events := make([]Event, 0, len(content))
		for _, item := range content {
			block, _ := item.(map[string]any)
			switch block["type"] {
			case "thinking":
				// Extended thinking arrives as a separate content block. Project
				// it the same way DSH does so the operator sees the reasoning
				// card instead of a silent gap before the tool call. Redacted
				// thoughts carry no readable text and are skipped.
				if text, ok := block["thinking"].(string); ok && strings.TrimSpace(text) != "" {
					events = append(events, Event{Type: "agent_reasoning", Data: map[string]any{"text": text, "reasoning": true}})
				}
			case "tool_use":
				input, _ := block["input"].(map[string]any)
				command := strings.TrimSpace(fmt.Sprint(input["command"]))
				if command == "<nil>" {
					command = ""
				}
				events = append(events, Event{Type: "agent_command_started", Data: map[string]any{
					"tool_name": block["name"], "tool_use_id": block["id"], "command": command, "status": "in_progress",
				}})
			case "text":
				if text, ok := block["text"].(string); ok && text != "" {
					events = append(events, Event{Type: "agent_message", Data: map[string]any{"text": text, "intermediate": true}})
				}
			}
		}
		return events, "", ""
	case "user":
		message, _ := payload["message"].(map[string]any)
		content, _ := message["content"].([]any)
		for _, item := range content {
			block, _ := item.(map[string]any)
			if block["type"] != "tool_result" {
				continue
			}
			output := strings.TrimSpace(fmt.Sprint(block["content"]))
			if output == "<nil>" {
				output = ""
			}
			return []Event{{Type: "agent_command_finished", Data: map[string]any{
				"tool_use_id": block["tool_use_id"], "status": "completed", "output_tail": tail(output, 2000),
			}}}, "", ""
		}
	}
	// Any other stream event that marks itself as an error must not disappear:
	// an unknown type, a system event or an explicit type:"error" line can
	// still carry the provider's only explanation.
	if boolValue(payload["is_error"]) || boolValue(payload["isError"]) || errorValueText(payload["error"]) != "" {
		return []Event{{Type: "agent_error", Data: map[string]any{
			"message": claudeEventErrorText(payload, rawType),
		}}}, "", ""
	}
	return nil, "", ""
}

// claudeResultErrorText renders a failed result event. It accepts the forms
// observed across Claude Code builds and gateways: errors[] of strings,
// error as a string/object/array, and a result string that holds the error
// text. Subtype, HTTP status, API error code and stop reasons are appended as
// diagnostics instead of being dropped.
func claudeResultErrorText(payload map[string]any) string {
	parts := make([]string, 0, 3)
	for _, key := range []string{"errors", "error"} {
		if detail := errorValueText(payload[key]); detail != "" && !containsErrorPart(parts, detail) {
			parts = append(parts, detail)
		}
	}
	if result, ok := payload["result"].(string); ok {
		if result = strings.TrimSpace(result); result != "" && !containsErrorPart(parts, result) {
			parts = append(parts, result)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "claude result reported an error")
	}
	text := strings.Join(parts, "\n")
	if meta := claudeErrorMeta(payload); len(meta) > 0 {
		text += " [" + strings.Join(meta, "; ") + "]"
	}
	return providerErrorText(text)
}

// claudeEventErrorText renders an arbitrary stream event that carries an
// error. It deliberately ignores non-string message objects (assistant
// content), which are not error text.
func claudeEventErrorText(payload map[string]any, rawType string) string {
	parts := make([]string, 0, 3)
	if detail := errorValueText(payload["errors"]); detail != "" {
		parts = append(parts, detail)
	}
	if detail := errorValueText(payload["error"]); detail != "" && !containsErrorPart(parts, detail) {
		parts = append(parts, detail)
	}
	if message, ok := payload["message"].(string); ok {
		if message = strings.TrimSpace(message); message != "" && !containsErrorPart(parts, message) {
			parts = append(parts, message)
		}
	}
	if len(parts) == 0 {
		kind := strings.TrimSpace(rawType)
		if kind == "" {
			kind = "stream"
		}
		parts = append(parts, fmt.Sprintf("claude %s event reported an error", kind))
	}
	text := strings.Join(parts, "\n")
	if meta := claudeErrorMeta(payload); len(meta) > 0 {
		text += " [" + strings.Join(meta, "; ") + "]"
	}
	return providerErrorText(text)
}

// claudeErrorMeta lists the diagnostic fields that accompany Claude error
// payloads. Both camelCase (assistant events) and snake_case (result events)
// spellings are accepted because Claude Code mixes them across event types.
func claudeErrorMeta(payload map[string]any) []string {
	meta := make([]string, 0, 5)
	add := func(label string, keys ...string) {
		for _, key := range keys {
			text := objectValueText(payload[key])
			if text == "" {
				continue
			}
			meta = append(meta, label+": "+text)
			return
		}
	}
	add("subtype", "subtype")
	add("status", "apiErrorStatus", "api_error_status")
	add("code", "apiError", "api_error", "code")
	add("stop_reason", "stop_reason", "stopReason")
	add("terminal_reason", "terminal_reason", "terminalReason")
	return meta
}

// claudeErrorStatus returns the HTTP/API status field for event Data.
func claudeErrorStatus(payload map[string]any) (any, bool) {
	return claudeFirstValue(payload, "apiErrorStatus", "api_error_status")
}

// claudeAPIRetryData projects a transient retry notification as progress.
// The real event (Claude 2.1.220) is
// {"type":"system","subtype":"api_retry","attempt":N,"max_retries":N,
// "retry_delay_ms":N,"error_status":529|null,"error":"overloaded"}.
// It is not a Turn failure; each retry must not create an error row or set
// the final providerError.
func claudeAPIRetryData(payload map[string]any) map[string]any {
	errorText := providerErrorText(errorValueText(payload["error"]))
	status, hasStatus := claudeFirstValue(payload, "error_status", "errorStatus")
	attempt, hasAttempt := claudeFirstValue(payload, "attempt")
	maxRetries, hasMaxRetries := claudeFirstValue(payload, "max_retries", "maxRetries")
	delay, hasDelay := claudeFirstValue(payload, "retry_delay_ms", "retryDelayMs")

	data := map[string]any{"phase": "api_retry"}
	if errorText != "" {
		data["error"] = errorText
	}
	if hasStatus {
		data["status"] = status
	}
	if hasAttempt {
		data["attempt"] = attempt
	}
	if hasMaxRetries {
		data["max_retries"] = maxRetries
	}
	if hasDelay {
		data["retry_delay_ms"] = delay
	}

	detail := make([]string, 0, 2)
	if errorText != "" {
		detail = append(detail, errorText)
	}
	if hasStatus {
		detail = append(detail, fmt.Sprintf("HTTP %v", status))
	}
	message := "Claude API 请求失败，正在自动重试"
	if len(detail) > 0 {
		message = "Claude API 请求失败（" + strings.Join(detail, ", ") + "），正在自动重试"
	}
	if hasAttempt && hasMaxRetries {
		message += fmt.Sprintf("（第 %v/%v 次）", attempt, maxRetries)
	} else if hasAttempt {
		message += fmt.Sprintf("（第 %v 次）", attempt)
	}
	if hasDelay {
		message += fmt.Sprintf("，等待 %vms", delay)
	}
	data["message"] = message
	return data
}

func claudeFirstValue(payload map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := payload[key]; ok && value != nil {
			return value, true
		}
	}
	return nil, false
}

// claudeString returns the first non-empty string-valued field.
func claudeString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := payload[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

// objectValueText renders a diagnostic field, dropping empty and null values.
func objectValueText(value any) string {
	if value == nil {
		return ""
	}
	return errorValueText(value)
}

func containsErrorPart(parts []string, detail string) bool {
	for _, part := range parts {
		if part == detail {
			return true
		}
	}
	return false
}

// claudeAssistantText joins the text blocks of one assistant message. Claude
// Code splits thinking and text into separate blocks (sometimes separate
// stream lines) but only the text blocks are user-visible.
func claudeAssistantText(payload map[string]any) string {
	message, _ := payload["message"].(map[string]any)
	content, _ := message["content"].([]any)
	parts := make([]string, 0, len(content))
	for _, item := range content {
		block, _ := item.(map[string]any)
		if block["type"] != "text" {
			continue
		}
		if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// claudeContextWindowExceeded recognizes the provider messages that mean the
// resumed context no longer fits. The LiteLLM gateway used by this project
// returns a ContextWindowExceededError or "maximum context length" text; the
// other forms cover Anthropic-compatible gateways that phrase it differently.
func claudeTranscriptDir(ctx context.Context, request Request) string {
	if dir := strings.TrimSpace(request.ClaudeConfigDir); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(request.Environment["CLAUDE_CONFIG_DIR"]); dir != "" {
		return dir
	}
	return workspace.ClaudeDefaultConfigDirOn(ctx, request.Runner)
}

func claudeContextWindowExceeded(message string) bool {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "contextwindowexceeded"),
		strings.Contains(lower, "context_length_exceeded"),
		strings.Contains(lower, "context length exceeded"),
		strings.Contains(lower, "maximum context length"):
		return true
	case strings.Contains(lower, "context window") && strings.Contains(lower, "exceed"):
		return true
	default:
		return false
	}
}

func filterClaudeEnvironment(values map[string]string) map[string]string {
	allowed := map[string]bool{
		"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true, "ANTHROPIC_BASE_URL": true,
		"ANTHROPIC_MODEL": true, "CLAUDE_CODE_MAX_CONTEXT_TOKENS": true,
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW": true,
		// The variable Claude Code reads to authenticate as the operator's own
		// account. A machine whose login lives in a shell profile has it nowhere
		// else, so dropping it here would leave detection reporting a login the
		// run could not use.
		"CLAUDE_CODE_OAUTH_TOKEN": true,
		"CLAUDE_CONFIG_DIR":       true,
		"AHA2_AGENT_API_URL":      true, "AHA2_AGENT_API_TOKEN": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	}
	result := map[string]string{}
	for key, value := range values {
		if allowed[strings.ToUpper(key)] {
			result[key] = value
		}
	}
	return result
}

// claudeStream buffers intermediate assistant text until the next action and
// drops the copy that only repeats the final answer. Claude Code sends the same
// text twice — once as an assistant content block and again as result.result —
// so emitting both put a duplicate Update above an identical final message.
type claudeStream struct {
	pending []string
}

func (stream *claudeStream) Consume(events []Event) []Event {
	projected := make([]Event, 0, len(events))
	for _, event := range events {
		if event.Type == "agent_message" {
			text := strings.TrimSpace(fmt.Sprint(event.Data["text"]))
			if text == "<nil>" {
				text = ""
			}
			if intermediate, _ := event.Data["intermediate"].(bool); intermediate {
				if text != "" {
					stream.pending = append(stream.pending, text)
				}
				continue
			}
			if final, _ := event.Data["final"].(bool); final {
				projected = append(projected, stream.flush(text)...)
				projected = append(projected, event)
				continue
			}
		}
		projected = append(projected, stream.flush("")...)
		projected = append(projected, event)
	}
	return projected
}

// Flush emits whatever commentary is still buffered, for a run that ends
// without a final result line.
func (stream *claudeStream) Flush() []Event {
	return stream.flush("")
}

func (stream *claudeStream) flush(trimTail string) []Event {
	texts := stream.pending
	stream.pending = nil
	if trimTail != "" && len(texts) > 0 && texts[len(texts)-1] == trimTail {
		texts = texts[:len(texts)-1]
	}
	joined := strings.TrimSpace(strings.Join(texts, "\n\n"))
	if joined == "" {
		return nil
	}
	return []Event{{Type: "agent_message", Data: map[string]any{"text": joined, "intermediate": true}}}
}
