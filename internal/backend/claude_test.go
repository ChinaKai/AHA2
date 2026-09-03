package backend

import (
	"context"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

type claudeErrorRunner struct{}

func (claudeErrorRunner) Run(_ context.Context, _ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	onLine(`{"type":"result","subtype":"error_during_execution","error":"provider authentication rejected"}`)
	return workspace.Result{ExitCode: 1}, nil
}

type claudeArgsRunner struct {
	args []string
}

func (runner *claudeArgsRunner) Run(_ context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	runner.args = append([]string(nil), command.Args...)
	onLine(`{"type":"result","subtype":"success","result":"done","session_id":"sess-args"}`)
	return workspace.Result{ExitCode: 0}, nil
}

func TestParseClaudeLineResult(t *testing.T) {
	t.Parallel()
	event, reply, session := parseClaudeLine(`{"type":"result","subtype":"success","result":"done","session_id":"sess-1"}`)
	if event.Type != "agent_message" || reply != "done" || session != "sess-1" {
		t.Fatalf("unexpected parse: event=%q reply=%q session=%q", event.Type, reply, session)
	}
}

func TestParseClaudeLineResultUsage(t *testing.T) {
	t.Parallel()
	event, _, _ := parseClaudeLine(`{"type":"result","subtype":"success","result":"done","session_id":"sess-1","usage":{"input_tokens":120,"output_tokens":8}}`)
	usage, _ := event.Data["usage"].(map[string]any)
	if usage["input_tokens"] != float64(120) {
		t.Fatalf("usage missing from result: %#v", event.Data)
	}
}

func TestParseClaudeToolEvents(t *testing.T) {
	t.Parallel()
	started, _, _ := parseClaudeLine(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"go test ./..."}}]}}`)
	if started.Type != "agent_command_started" || started.Data["command"] != "go test ./..." {
		t.Fatalf("unexpected tool start: %#v", started)
	}
	finished, _, _ := parseClaudeLine(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]}}`)
	if finished.Type != "agent_command_finished" || finished.Data["output_tail"] != "ok" {
		t.Fatalf("unexpected tool result: %#v", finished)
	}
}

func TestParseClaudeIntermediateMessage(t *testing.T) {
	t.Parallel()
	event, reply, _ := parseClaudeLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"正在检查代码"}]}}`)
	if event.Type != "agent_message" || event.Data["text"] != "正在检查代码" || reply != "" {
		t.Fatalf("unexpected intermediate message: %#v %q", event, reply)
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
	joined := strings.Join(runner.args, " ")
	if !strings.Contains(joined, "--disallowedTools Task,Agent") {
		t.Fatalf("native agent tools were not disabled: %s", joined)
	}
}

func TestParseClaudeLineInitSession(t *testing.T) {
	t.Parallel()
	event, reply, session := parseClaudeLine(`{"type":"system","subtype":"init","session_id":"sess-2"}`)
	if event.Type != "agent_session" || reply != "" || session != "sess-2" {
		t.Fatalf("unexpected parse: event=%q reply=%q session=%q", event.Type, reply, session)
	}
}

func TestParseClaudeLineError(t *testing.T) {
	t.Parallel()
	event, _, _ := parseClaudeLine(`{"type":"result","subtype":"error_max_turns","error":"too many turns"}`)
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
