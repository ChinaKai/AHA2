package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

func TestClaudeIdleWatchdogReportsAndCancels(t *testing.T) {
	events := []string{}
	_, err := (Claude{IdleWarning: 10 * time.Millisecond, IdleTimeout: 35 * time.Millisecond, Heartbeat: 10 * time.Millisecond}).Execute(
		context.Background(), Request{Runner: idleCodexRunner{}, WorkDir: t.TempDir()}, func(event Event) { events = append(events, event.Type) },
	)
	if !errors.Is(err, ErrBackendIdleTimeout) {
		t.Fatalf("idle error = %v", err)
	}
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "agent_stalled") || !strings.Contains(joined, "agent_idle_timeout") {
		t.Fatalf("watchdog events = %v", events)
	}
}

type claudeErrorRunner struct{}

func (claudeErrorRunner) Run(_ context.Context, _ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	onLine(`{"type":"result","subtype":"error_during_execution","error":"provider authentication rejected"}`)
	return workspace.Result{ExitCode: 1}, nil
}

type claudeArgsRunner struct {
	args     []string
	killTree bool
}

func (runner *claudeArgsRunner) Run(_ context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	runner.args = append([]string(nil), command.Args...)
	runner.killTree = command.KillTree
	onLine(`{"type":"result","subtype":"success","result":"done","session_id":"sess-args"}`)
	return workspace.Result{ExitCode: 0}, nil
}

func firstClaudeEvent(events []Event) Event {
	if len(events) == 0 {
		return Event{}
	}
	return events[0]
}

func TestParseClaudeLineResult(t *testing.T) {
	t.Parallel()
	events, reply, session := parseClaudeLine(`{"type":"result","subtype":"success","result":"done","session_id":"sess-1"}`)
	event := firstClaudeEvent(events)
	if event.Type != "agent_message" || reply != "done" || session != "sess-1" {
		t.Fatalf("unexpected parse: event=%q reply=%q session=%q", event.Type, reply, session)
	}
}

func TestParseClaudeLineResultUsage(t *testing.T) {
	t.Parallel()
	events, _, _ := parseClaudeLine(`{"type":"result","subtype":"success","result":"done","session_id":"sess-1","usage":{"input_tokens":120,"output_tokens":8}}`)
	event := firstClaudeEvent(events)
	usage, _ := event.Data["usage"].(map[string]any)
	if usage["input_tokens"] != float64(120) {
		t.Fatalf("usage missing from result: %#v", event.Data)
	}
}

func TestParseClaudeToolEvents(t *testing.T) {
	t.Parallel()
	startedEvents, _, _ := parseClaudeLine(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"go test ./..."}}]}}`)
	started := firstClaudeEvent(startedEvents)
	if started.Type != "agent_command_started" || started.Data["command"] != "go test ./..." {
		t.Fatalf("unexpected tool start: %#v", started)
	}
	finishedEvents, _, _ := parseClaudeLine(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]}}`)
	finished := firstClaudeEvent(finishedEvents)
	if finished.Type != "agent_command_finished" || finished.Data["output_tail"] != "ok" {
		t.Fatalf("unexpected tool result: %#v", finished)
	}
}

func TestParseClaudeIntermediateMessage(t *testing.T) {
	t.Parallel()
	events, reply, _ := parseClaudeLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"正在检查代码"}]}}`)
	event := firstClaudeEvent(events)
	if event.Type != "agent_message" || event.Data["text"] != "正在检查代码" || reply != "" {
		t.Fatalf("unexpected intermediate message: %#v %q", event, reply)
	}
}

func TestParseClaudeThinkingBecomesReasoning(t *testing.T) {
	t.Parallel()
	events, _, _ := parseClaudeLine(`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"先看仓库结构","signature":"sig"},{"type":"text","text":"先检查一下"},{"type":"tool_use","id":"tool-2","name":"Bash","input":{"command":"ls"}}]}}`)
	if len(events) != 3 {
		t.Fatalf("expected thinking, text and tool events, got %#v", events)
	}
	if events[0].Type != "agent_reasoning" || events[0].Data["text"] != "先看仓库结构" || events[0].Data["reasoning"] != true {
		t.Fatalf("thinking block was not projected: %#v", events[0])
	}
	if events[1].Type != "agent_message" || events[1].Data["intermediate"] != true || events[1].Data["text"] != "先检查一下" {
		t.Fatalf("text block was lost: %#v", events[1])
	}
	if events[2].Type != "agent_command_started" || events[2].Data["command"] != "ls" {
		t.Fatalf("tool block was lost: %#v", events[2])
	}
}

func TestParseClaudeRedactedThinkingIsSkipped(t *testing.T) {
	t.Parallel()
	events, _, _ := parseClaudeLine(`{"type":"assistant","message":{"content":[{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"ok"}]}}`)
	if len(events) != 1 || events[0].Type != "agent_message" {
		t.Fatalf("redacted thinking must not produce an event: %#v", events)
	}
}

type claudeThinkingRunner struct{}

func (claudeThinkingRunner) Run(_ context.Context, _ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	onLine(`{"type":"system","subtype":"init","session_id":"sess-think"}`)
	onLine(`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"先读一下代码","signature":"sig"}]}}`)
	onLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"我看一下实现"}]}}`)
	onLine(`{"type":"result","subtype":"success","result":"改好了","session_id":"sess-think"}`)
	return workspace.Result{ExitCode: 0}, nil
}

func TestClaudeExecuteProjectsThinkingAsReasoning(t *testing.T) {
	var events []Event
	result, err := (Claude{}).Execute(context.Background(), Request{Runner: claudeThinkingRunner{}, WorkDir: t.TempDir()}, func(event Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "改好了" || result.ProviderSessionID != "sess-think" {
		t.Fatalf("unexpected result: %#v", result)
	}
	var sawReasoning, sawUpdate bool
	for _, event := range events {
		if event.Type == "agent_reasoning" && event.Data["text"] == "先读一下代码" {
			sawReasoning = true
		}
		if event.Type == "agent_message" && event.Data["intermediate"] == true && event.Data["text"] == "我看一下实现" {
			sawUpdate = true
		}
	}
	if !sawReasoning || !sawUpdate {
		t.Fatalf("thinking/update events missing: %#v", events)
	}
}

type claudeRepeatRunner struct{}

func (claudeRepeatRunner) Run(_ context.Context, _ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	onLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"同样的话"}]}}`)
	onLine(`{"type":"result","subtype":"success","result":"同样的话","session_id":"sess-repeat"}`)
	return workspace.Result{ExitCode: 0}, nil
}

func TestClaudeExecuteDropsCommentaryThatRepeatsFinalText(t *testing.T) {
	var messages []Event
	_, err := (Claude{}).Execute(context.Background(), Request{Runner: claudeRepeatRunner{}, WorkDir: t.TempDir()}, func(event Event) {
		if event.Type == "agent_message" {
			messages = append(messages, event)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("repeated commentary must be dropped, got %#v", messages)
	}
	if messages[0].Data["final"] != true || messages[0].Data["text"] != "同样的话" {
		t.Fatalf("unexpected message: %#v", messages[0])
	}
}

type claudeDistinctCommentaryRunner struct{}

func (claudeDistinctCommentaryRunner) Run(_ context.Context, _ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	onLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"先看一下代码"},{"type":"tool_use","id":"tool-9","name":"Bash","input":{"command":"ls"}}]}}`)
	onLine(`{"type":"result","subtype":"success","result":"做完了","session_id":"sess-distinct"}`)
	return workspace.Result{ExitCode: 0}, nil
}

func TestClaudeExecuteKeepsDistinctCommentary(t *testing.T) {
	var types []string
	var commentary string
	_, err := (Claude{}).Execute(context.Background(), Request{Runner: claudeDistinctCommentaryRunner{}, WorkDir: t.TempDir()}, func(event Event) {
		types = append(types, event.Type)
		if event.Type == "agent_message" && event.Data["intermediate"] == true {
			commentary = event.Data["text"].(string)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if commentary != "先看一下代码" {
		t.Fatalf("distinct commentary was lost: %q", commentary)
	}
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, "agent_message,agent_command_started") {
		t.Fatalf("commentary should be emitted before the tool start: %v", types)
	}
}

func TestClaudeExitUsesProviderError(t *testing.T) {
	t.Parallel()
	_, err := (Claude{}).Execute(context.Background(), Request{
		Runner: claudeErrorRunner{}, WorkDir: "/workspace", Model: "model",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "provider authentication rejected") {
		t.Fatalf("provider error was lost: %v", err)
	}
}

func TestClaudeDisablesNativeAgentTools(t *testing.T) {
	t.Parallel()
	runner := &claudeArgsRunner{}
	_, err := (Claude{}).Execute(context.Background(), Request{
		Runner: runner, WorkDir: "/workspace", Model: "model",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !runner.killTree {
		t.Fatal("Claude backend descendants would survive turn or service cancellation")
	}
	joined := strings.Join(runner.args, " ")
	if !strings.Contains(joined, "--disallowedTools Task,Agent") {
		t.Fatalf("native agent tools were not disabled: %s", joined)
	}
}

func TestClaudeReadOnlyKeepsMCPToolsCallable(t *testing.T) {
	t.Parallel()
	joined := strings.Join(claudePermissionArgs("read-only", "never"), " ")
	if !strings.Contains(joined, "--permission-mode bypassPermissions") {
		t.Fatalf("read-only should not use plan mode: %s", joined)
	}
	if !strings.Contains(joined, "--disallowedTools") || !strings.Contains(joined, "Bash") || !strings.Contains(joined, "Write") {
		t.Fatalf("read-only write tools were not disabled: %s", joined)
	}
	if strings.Contains(joined, "--permission-mode plan") {
		t.Fatalf("plan mode blocks MCP coordinator tools: %s", joined)
	}
}

func TestParseClaudeLineInitSession(t *testing.T) {
	t.Parallel()
	events, reply, session := parseClaudeLine(`{"type":"system","subtype":"init","session_id":"sess-2"}`)
	event := firstClaudeEvent(events)
	if event.Type != "agent_session" || reply != "" || session != "sess-2" {
		t.Fatalf("unexpected parse: event=%q reply=%q session=%q", event.Type, reply, session)
	}
}

func TestParseClaudeLineError(t *testing.T) {
	t.Parallel()
	events, _, _ := parseClaudeLine(`{"type":"result","subtype":"error_max_turns","error":"too many turns"}`)
	event := firstClaudeEvent(events)
	if event.Type != "agent_error" {
		t.Fatalf("expected agent_error, got %q", event.Type)
	}
}

// Claude 2.1.x result failures carry errors[] of strings, not a singular
// error field. The old parser only read error as a string, so these
// explanations were dropped and the Turn ended with a generic exit message.
func TestParseClaudeResultErrorKeepsErrorsArray(t *testing.T) {
	t.Parallel()
	events, reply, session := parseClaudeLine(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["API Error: 400 litellm.BadRequestError: prompt is too long"],"stop_reason":"end_turn","session_id":"sess-fail"}`)
	event := firstClaudeEvent(events)
	if event.Type != "agent_error" || reply != "" || session != "sess-fail" {
		t.Fatalf("unexpected parse: %#v reply=%q session=%q", event.Type, reply, session)
	}
	message := fmt.Sprint(event.Data["message"])
	for _, want := range []string{"prompt is too long", "subtype: error_during_execution", "stop_reason: end_turn"} {
		if !strings.Contains(message, want) {
			t.Fatalf("result error lost %q: %s", want, message)
		}
	}
	if event.Data["terminal"] != true {
		t.Fatalf("result failure must be terminal: %#v", event.Data)
	}
}

func TestParseClaudeResultErrorAcceptsObjectAndArray(t *testing.T) {
	t.Parallel()
	objectEvents, _, _ := parseClaudeLine(`{"type":"result","subtype":"error_max_turns","error":{"code":"rate_limit","message":"slow down","details":{"retry_after":5}}}`)
	objectMessage := fmt.Sprint(firstClaudeEvent(objectEvents).Data["message"])
	for _, want := range []string{"rate_limit", "slow down", "retry_after", "5"} {
		if !strings.Contains(objectMessage, want) {
			t.Fatalf("object error lost %q: %s", want, objectMessage)
		}
	}
	arrayEvents, _, _ := parseClaudeLine(`{"type":"result","subtype":"error_during_execution","error":["first failure","second failure"]}`)
	arrayMessage := fmt.Sprint(firstClaudeEvent(arrayEvents).Data["message"])
	if !strings.Contains(arrayMessage, "first failure") || !strings.Contains(arrayMessage, "second failure") {
		t.Fatalf("array error lost entries: %s", arrayMessage)
	}
}

func TestParseClaudeAssistantAPIErrorKeepsDetailsAndStatus(t *testing.T) {
	t.Parallel()
	line := `{"type":"assistant","isApiErrorMessage":true,"apiErrorStatus":413,"apiError":"max_output_tokens","errorDetails":"requested 1055361 tokens but limit is 1048576","message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 413 maximum output tokens exceeded"}]}}`
	events, reply, _ := parseClaudeLine(line)
	event := firstClaudeEvent(events)
	if event.Type != "agent_error" || reply != "" {
		t.Fatalf("unexpected parse: %#v reply=%q", event.Type, reply)
	}
	message := fmt.Sprint(event.Data["message"])
	for _, want := range []string{"maximum output tokens exceeded", "requested 1055361 tokens", "status: 413", "code: max_output_tokens"} {
		if !strings.Contains(message, want) {
			t.Fatalf("API error message lost %q: %s", want, message)
		}
	}
	if event.Data["status"] != float64(413) || event.Data["code"] != "max_output_tokens" {
		t.Fatalf("status/code were dropped: %#v", event.Data)
	}
}

type claudeRecoveringResultRunner struct{}

func (claudeRecoveringResultRunner) Run(_ context.Context, _ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	onLine(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["boom"],"session_id":"sess-live"}`)
	onLine(`{"type":"result","subtype":"success","result":"done","session_id":"sess-live"}`)
	return workspace.Result{ExitCode: 0}, nil
}

// A live-inbox run keeps the same process across queued messages, so more than
// one result can arrive. A failure followed by a success must end
// successfully; the old code latched terminalError for the rest of the run.
func TestClaudeLiveRunRecoversAfterFailedResult(t *testing.T) {
	result, err := (Claude{}).Execute(context.Background(), Request{Runner: claudeRecoveringResultRunner{}, WorkDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("later success must clear the earlier terminal error: %v", err)
	}
	if result.Reply != "done" {
		t.Fatalf("reply = %q, want done", result.Reply)
	}
}

func TestParseClaudeAPIRetryIsProgressNotError(t *testing.T) {
	t.Parallel()
	line := `{"type":"system","subtype":"api_retry","attempt":2,"max_retries":5,"retry_delay_ms":1500,"error_status":529,"error":"overloaded","session_id":"sess-retry"}`
	events, reply, _ := parseClaudeLine(line)
	event := firstClaudeEvent(events)
	if event.Type != "agent_progress" || reply != "" {
		t.Fatalf("api_retry must be progress, got %#v reply=%q", event.Type, reply)
	}
	message := fmt.Sprint(event.Data["message"])
	for _, want := range []string{"overloaded", "529", "2/5", "1500"} {
		if !strings.Contains(message, want) {
			t.Fatalf("retry progress lost %q: %s", want, message)
		}
	}
	if event.Data["status"] != float64(529) || event.Data["attempt"] != float64(2) {
		t.Fatalf("retry diagnostics were dropped: %#v", event.Data)
	}
}

type claudeRetryThenSuccessRunner struct{}

func (claudeRetryThenSuccessRunner) Run(_ context.Context, _ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	onLine(`{"type":"system","subtype":"api_retry","attempt":1,"max_retries":5,"retry_delay_ms":900,"error_status":529,"error":"overloaded","session_id":"sess-retry"}`)
	onLine(`{"type":"result","subtype":"success","result":"recovered","session_id":"sess-retry"}`)
	return workspace.Result{ExitCode: 0}, nil
}

func TestClaudeExecuteRetryEventIsNotAnError(t *testing.T) {
	var sawError bool
	result, err := (Claude{}).Execute(context.Background(), Request{Runner: claudeRetryThenSuccessRunner{}, WorkDir: t.TempDir()}, func(event Event) {
		if event.Type == "agent_error" {
			sawError = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if sawError {
		t.Fatal("an automatic retry must not be projected as agent_error")
	}
	if result.Reply != "recovered" {
		t.Fatalf("reply = %q", result.Reply)
	}
}

type claudeTerminalErrorRunner struct{}

func (claudeTerminalErrorRunner) Run(_ context.Context, _ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	onLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"正在处理"}]}}`)
	onLine(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["provider exploded during execution"]}`)
	return workspace.Result{ExitCode: 0}, nil
}

func TestClaudeTerminalResultErrorOverridesPartialReply(t *testing.T) {
	var sawTerminal bool
	_, err := (Claude{}).Execute(context.Background(), Request{Runner: claudeTerminalErrorRunner{}, WorkDir: t.TempDir()}, func(event Event) {
		if event.Type == "agent_error" && event.Data["terminal"] == true {
			sawTerminal = true
		}
	})
	if err == nil || !strings.Contains(err.Error(), "provider exploded during execution") {
		t.Fatalf("terminal result error was hidden: %v", err)
	}
	if strings.Contains(err.Error(), "no agent message") {
		t.Fatalf("generic no-message error replaced the provider detail: %v", err)
	}
	if !sawTerminal {
		t.Fatal("terminal agent_error event was not emitted")
	}
}

func TestClaudeErrorRedactsCredentialsAndClips(t *testing.T) {
	t.Parallel()
	secret := "sk-ant-super-secret-token-123456"
	long := strings.Repeat("y", 6000)
	line, err := json.Marshal(map[string]any{
		"type": "result", "subtype": "error_during_execution",
		"errors": []string{"failed: Authorization: Bearer " + secret + " api_key=another-secret-value " + long},
	})
	if err != nil {
		t.Fatal(err)
	}
	events, _, _ := parseClaudeLine(string(line))
	message := fmt.Sprint(firstClaudeEvent(events).Data["message"])
	if strings.Contains(message, secret) || strings.Contains(message, "another-secret-value") {
		t.Fatalf("credential leaked into the error message: %s", message)
	}
	if !strings.Contains(message, "[redacted]") {
		t.Fatalf("redaction marker missing: %s", message)
	}
	if length := len([]rune(message)); length > providerDetailLimit+1 {
		t.Fatalf("error message is not bounded: %d", length)
	}
}

func TestParseClaudeLineSyntheticAPIErrorKeepsProviderText(t *testing.T) {
	t.Parallel()
	line := `{"type":"assistant","isApiErrorMessage":true,"apiErrorStatus":400,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 400 litellm.ContextWindowExceededError: maximum context length is 1048576 tokens"}]}}`
	events, reply, _ := parseClaudeLine(line)
	event := firstClaudeEvent(events)
	if event.Type != "agent_error" {
		t.Fatalf("expected agent_error, got %q", event.Type)
	}
	if reply != "" {
		t.Fatalf("synthetic error produced a reply: %q", reply)
	}
	message, _ := event.Data["message"].(string)
	if !strings.Contains(message, "ContextWindowExceededError") || event.Data["status"] != float64(400) && event.Data["status"] != 400 {
		t.Fatalf("provider error text was lost: %#v", event.Data)
	}
}

type claudeContextWindowRunner struct{}

func (claudeContextWindowRunner) Run(_ context.Context, _ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	onLine(`{"type":"assistant","isApiErrorMessage":true,"apiErrorStatus":400,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 400 litellm.ContextWindowExceededError: This model's maximum context length is 1048576 tokens. However, you requested 1055361 tokens"}]}}`)
	return workspace.Result{ExitCode: 1}, nil
}

func TestClaudeContextWindowErrorIsClassifiedAndVisible(t *testing.T) {
	var events []Event
	_, err := (Claude{}).Execute(context.Background(), Request{Runner: claudeContextWindowRunner{}, WorkDir: t.TempDir()}, func(event Event) {
		events = append(events, event)
	})
	if !errors.Is(err, ErrContextWindowExceeded) {
		t.Fatalf("context window error was not classified: %v", err)
	}
	if !strings.Contains(err.Error(), "ContextWindowExceededError") || strings.Contains(err.Error(), "claude exited with code 1") {
		t.Fatalf("provider text was replaced by the generic exit error: %v", err)
	}
	sawError := false
	for _, event := range events {
		if event.Type == "agent_error" && strings.Contains(fmt.Sprint(event.Data["message"]), "ContextWindowExceededError") {
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("agent_error event missing: %#v", events)
	}
}

type claudeSyntheticErrorExitZeroRunner struct{}

func (claudeSyntheticErrorExitZeroRunner) Run(_ context.Context, _ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	onLine(`{"type":"assistant","isApiErrorMessage":true,"apiErrorStatus":500,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 500 provider unavailable"}]}}`)
	return workspace.Result{ExitCode: 0}, nil
}

func TestClaudeSyntheticErrorWithoutReplyIsNotReportedAsNoMessage(t *testing.T) {
	_, err := (Claude{}).Execute(context.Background(), Request{Runner: claudeSyntheticErrorExitZeroRunner{}, WorkDir: t.TempDir()}, nil)
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") || strings.Contains(err.Error(), "no agent message") {
		t.Fatalf("synthetic error was hidden: %v", err)
	}
}

type claudeTranscriptRecoveryRunner struct {
	transcript string
}

func (runner *claudeTranscriptRecoveryRunner) Run(_ context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	if command.Executable == "tail" || command.Executable == "powershell.exe" {
		return workspace.Result{Stdout: runner.transcript, ExitCode: 0}, nil
	}
	onLine(`{"type":"system","subtype":"init","session_id":"sess-recovery"}`)
	return workspace.Result{ExitCode: 0}, nil
}

type claudeTranscriptErrorRunner struct {
	transcript string
}

func (runner *claudeTranscriptErrorRunner) Run(_ context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	if command.Executable == "tail" || command.Executable == "powershell.exe" {
		return workspace.Result{Stdout: runner.transcript, ExitCode: 0}, nil
	}
	onLine(`{"type":"system","subtype":"init","session_id":"sess-403"}`)
	return workspace.Result{ExitCode: 1}, nil
}

func TestClaudeShowsProviderErrorFromTranscriptOnExitOne(t *testing.T) {
	t.Parallel()
	transcript := `{"type":"assistant","timestamp":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","isApiErrorMessage":true,"apiErrorStatus":403,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 403 litellm.PermissionDeniedError: You've reached your concurrent request limit"}]}}`
	runner := &claudeTranscriptErrorRunner{transcript: transcript}
	_, err := (Claude{}).Execute(context.Background(), Request{
		Runner:            runner,
		WorkDir:           t.TempDir(),
		ProviderSessionID: "sess-403",
		Environment:       map[string]string{"CLAUDE_CONFIG_DIR": t.TempDir()},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "concurrent request limit") || strings.Contains(err.Error(), "claude exited with code 1") {
		t.Fatalf("provider error was not surfaced: %v", err)
	}
}

type claudeNoInitTranscriptRunner struct {
	transcriptPath string
}

func (runner claudeNoInitTranscriptRunner) Run(ctx context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	switch command.Executable {
	case "sh", "tail", "powershell.exe":
		return workspace.LocalRunner{}.Run(ctx, command, onLine)
	default:
		// Write during the simulated CLI run so scheduling before Execute cannot
		// age the provider error past the transcript recovery cutoff.
		transcript := `{"type":"assistant","timestamp":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","isApiErrorMessage":true,"apiErrorStatus":403,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 403 You've reached your concurrent request limit"}]}}`
		if err := os.WriteFile(runner.transcriptPath, []byte(transcript), 0o600); err != nil {
			return workspace.Result{}, err
		}
		return workspace.Result{ExitCode: 1}, nil
	}
}

func (claudeNoInitTranscriptRunner) DetectedPlatform() string {
	return (workspace.LocalRunner{}).DetectedPlatform()
}

func TestClaudeNoInitTranscriptRunnerReportsHostPlatform(t *testing.T) {
	t.Parallel()
	runner := claudeNoInitTranscriptRunner{}
	if got, want := runner.DetectedPlatform(), runtime.GOOS+"/"+runtime.GOARCH; got != want {
		t.Fatalf("platform=%q, want %q", got, want)
	}
	if got, want := workspace.IsWindowsRunner(runner), runtime.GOOS == "windows"; got != want {
		t.Fatalf("Windows transcript routing=%t, want %t", got, want)
	}
}

func TestClaudeFindsTranscriptErrorWithoutSessionID(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	configDir := t.TempDir()
	projectDir := filepath.Join(configDir, "projects", workspace.ClaudeProjectSlug(workDir))
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := (Claude{}).Execute(context.Background(), Request{
		Runner:          claudeNoInitTranscriptRunner{transcriptPath: filepath.Join(projectDir, "orphan-session.jsonl")},
		WorkDir:         workDir,
		ClaudeConfigDir: configDir,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "concurrent request limit") || strings.Contains(err.Error(), "claude exited with code 1") {
		t.Fatalf("provider error was not surfaced without a session id: %v", err)
	}
	if result.ProviderSessionID != "" || result.ExitCode != 1 {
		t.Fatalf("expected exit one without a session id, got %#v", result)
	}
}

func TestClaudeRecoversReplyFromTranscriptWhenResultIsMissing(t *testing.T) {
	t.Parallel()
	transcript := `{"type":"assistant","timestamp":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","message":{"model":"m","content":[{"type":"text","text":"recovered from transcript"}]}}` + "\n" +
		`{"type":"last-prompt","lastPrompt":"..."}`
	runner := &claudeTranscriptRecoveryRunner{transcript: transcript}
	result, err := (Claude{}).Execute(context.Background(), Request{
		Runner:            runner,
		WorkDir:           t.TempDir(),
		ProviderSessionID: "sess-recovery",
		Environment:       map[string]string{"CLAUDE_CONFIG_DIR": t.TempDir()},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "recovered from transcript" {
		t.Fatalf("reply=%q", result.Reply)
	}
}

func TestFilterClaudeEnvironment(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"ANTHROPIC_BASE_URL": "https://api.example.com",
		"ANTHROPIC_API_KEY":  "sk-123",
		"OPENAI_API_KEY":     "should-drop",
	}
	filtered := filterClaudeEnvironment(values)
	if filtered["ANTHROPIC_BASE_URL"] != "https://api.example.com" || filtered["ANTHROPIC_API_KEY"] != "sk-123" {
		t.Fatalf("claude env not filtered correctly: %#v", filtered)
	}
	if _, present := filtered["OPENAI_API_KEY"]; present {
		t.Fatalf("OPENAI_API_KEY should not leak into claude env: %#v", filtered)
	}
}

// The operator's borrowed Claude login travels in this variable. Filtering is a
// deliberate allowlist, so a variable that authenticates the run has to be on
// it: leaving it out discards the credential silently, and the Turn then fails
// with "Not logged in" even though detection reported a login.
func TestFilterClaudeEnvironmentKeepsOperatorLogin(t *testing.T) {
	t.Parallel()
	const token = "sk-ant-oat01-EXAMPLE-NOT-REAL"
	filtered := filterClaudeEnvironment(map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN": token,
		"HOME":                    "/srv/build/users/kaikai",
	})
	if filtered["CLAUDE_CODE_OAUTH_TOKEN"] != token {
		t.Fatalf("operator login was dropped: %#v", filtered)
	}
	if _, present := filtered["HOME"]; present {
		t.Fatalf("the allowlist stopped being an allowlist: %#v", filtered)
	}
}

type claudeEnvRunner struct {
	env map[string]string
}

func (runner *claudeEnvRunner) Run(_ context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	runner.env = map[string]string{}
	for key, value := range command.Env {
		runner.env[key] = value
	}
	onLine(`{"type":"result","subtype":"success","result":"done","session_id":"sess-env"}`)
	return workspace.Result{ExitCode: 0}, nil
}

// The whole point of borrowing the operator's login is that the running backend
// receives it. Asserting this through the adapter -- not through the filter
// alone -- is what catches an allowlist that quietly drops the credential: the
// symptom is a Turn that fails with "Not logged in" while detection, which takes
// a different path, reports a healthy login.
func TestClaudeAdapterPassesOperatorLoginToTheBackend(t *testing.T) {
	t.Parallel()
	const token = "sk-ant-oat01-EXAMPLE-NOT-REAL"
	runner := &claudeEnvRunner{}

	if _, err := (Claude{}).Execute(context.Background(), Request{
		Runner:      runner,
		WorkDir:     t.TempDir(),
		Environment: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": token},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if got := runner.env["CLAUDE_CODE_OAUTH_TOKEN"]; got != token {
		t.Fatalf("backend received %q, want the operator's login", got)
	}
}

func TestClaudeAdapterPassesAutoCompactWindow(t *testing.T) {
	t.Parallel()
	runner := &claudeEnvRunner{}
	if _, err := (Claude{}).Execute(context.Background(), Request{
		Runner:      runner,
		WorkDir:     t.TempDir(),
		Environment: map[string]string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "800000"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if got := runner.env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "800000" {
		t.Fatalf("backend received auto-compact window %q, want 800000", got)
	}
}

func TestClaudeMCPConfigKeepsBearerTokenInEnvironment(t *testing.T) {
	t.Parallel()
	config := claudeMCPConfig([]MCPServer{
		{
			Name: "aha2_channel", Transport: "http",
			URL:            "http://127.0.0.1:8766/api/v1/agent/channel/mcp",
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        "/api/v1/agent/channel/mcp",
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
			Headers:        map[string]string{"X-Test": "header-value"},
		},
		{
			Name: "mcp_local", Transport: "stdio", Command: "node",
			Args: []string{"server.mjs"}, Env: map[string]string{"MODE": "test"},
		},
	})
	if !strings.Contains(config, `"url":"${AHA2_AGENT_API_URL}/api/v1/agent/channel/mcp"`) {
		t.Fatalf("runtime URL placeholder missing: %s", config)
	}
	if !strings.Contains(config, `"Authorization":"Bearer ${AHA2_AGENT_API_TOKEN}"`) {
		t.Fatalf("bearer placeholder missing: %s", config)
	}
	if !strings.Contains(config, `"X-Test":"header-value"`) {
		t.Fatalf("HTTP header missing from the delivered config: %s", config)
	}
	if !strings.Contains(config, `"command":"node"`) || !strings.Contains(config, `"MODE":"test"`) {
		t.Fatalf("stdio server missing: %s", config)
	}
	if strings.Contains(config, "Bearer secret") {
		t.Fatalf("credential value leaked into argv config: %s", config)
	}
	if got := claudeMCPConfig(nil); got != "" {
		t.Fatalf("empty MCP list produced %q", got)
	}
}
