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
		"--disallowedTools", "Task,Agent",
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
	var reply, sessionID, providerError string
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
	result, err := request.Runner.Run(runContext, workspace.Command{
		Executable:     binary,
		Args:           args,
		Dir:            request.WorkDir,
		Env:            filterClaudeEnvironment(request.Environment),
		Stdin:          request.Prompt,
		Timeout:        timeout,
		KillTree:       true,
		ReverseForward: request.ReverseForward,
	}, func(line string) {
		events, parsedReply, parsedSession := parseClaudeLine(line)
		if parsedReply != "" {
			reply = parsedReply
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
			}
			if usage, ok := event.Data["usage"].(map[string]any); ok && len(usage) > 0 {
				send(Event{Type: "agent_usage", Data: map[string]any{"usage": usage}})
			}
			send(event)
		}
	})
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
			message = fmt.Sprintf("claude exited with code %d", result.ExitCode)
		}
		message = tail(message, 2000)
		return Result{Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("%s", message)
	}
	if strings.TrimSpace(reply) == "" {
		return Result{ExitCode: result.ExitCode, ProviderSessionID: sessionID}, fmt.Errorf("claude returned no agent message")
	}
	return Result{Reply: strings.TrimSpace(reply), ExitCode: result.ExitCode, ProviderSessionID: sessionID}, nil
}

// claudePermissionArgs maps the AHA2 sandbox (filesystem) + approval settings
// to Claude Code permission flags. danger-full-access or approval=full bypass
// all permission checks; read-only uses plan mode; workspace-write uses
// acceptEdits (auto-accepts file edits within the workspace).
func claudePermissionArgs(filesystem, approval string) []string {
	if filesystem == "danger-full-access" || approval == "full" || approval == "auto" {
		return []string{"--dangerously-skip-permissions"}
	}
	switch filesystem {
	case "read-only":
		return []string{"--permission-mode", "plan"}
	default:
		return []string{"--permission-mode", "acceptEdits"}
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
	case "result":
		sessionID, _ := payload["session_id"].(string)
		subtype, _ := payload["subtype"].(string)
		if subtype == "success" {
			if text, ok := payload["result"].(string); ok && text != "" {
				data := map[string]any{"text": text, "final": true}
				if usage, ok := payload["usage"].(map[string]any); ok {
					data["usage"] = usage
				}
				return []Event{{Type: "agent_message", Data: data}}, text, sessionID
			}
		} else if message, ok := payload["error"].(string); ok && message != "" {
			return []Event{{Type: "agent_error", Data: map[string]any{"message": message}}}, "", sessionID
		}
	case "assistant":
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
	return nil, "", ""
}

func filterClaudeEnvironment(values map[string]string) map[string]string {
	allowed := map[string]bool{
		"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true, "ANTHROPIC_BASE_URL": true,
		"ANTHROPIC_MODEL": true, "CLAUDE_CODE_MAX_CONTEXT_TOKENS": true,
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
