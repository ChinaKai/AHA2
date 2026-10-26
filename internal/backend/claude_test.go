package backend

import (
	"context"
	"errors"
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
