package backend

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

// liveFakeRunner emulates `codex app-server` over Command.Input. It answers the
// handshake, starts one turn, and completes it only after a steer arrives, so a
// passing test proves the adapter steered a running Turn rather than a new one.
type liveFakeRunner struct {
	mu       sync.Mutex
	commands []workspace.Command
	methods  []string
	steers   []map[string]any
	fail     bool
	exitOn   string
}

func (*liveFakeRunner) StreamsInput() bool { return true }

func (runner *liveFakeRunner) Run(ctx context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	runner.mu.Lock()
	runner.commands = append(runner.commands, command)
	runner.mu.Unlock()
	turnID := "turn-live"
	for {
		select {
		case <-ctx.Done():
			return workspace.Result{ExitCode: 1}, ctx.Err()
		case line, ok := <-command.Input:
			if !ok {
				return workspace.Result{ExitCode: 0}, nil
			}
			var message struct {
				ID     *int64         `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal([]byte(line), &message) != nil {
				continue
			}
			runner.mu.Lock()
			runner.methods = append(runner.methods, message.Method)
			runner.mu.Unlock()
			switch message.Method {
			case "initialize":
				onLine(`{"id":` + itoa(*message.ID) + `,"result":{"userAgent":"fake"}}`)
			case "thread/start", "thread/resume":
				onLine(`{"id":` + itoa(*message.ID) + `,"result":{"thread":{"id":"thread-live"}}}`)
			case "turn/start":
				onLine(`{"id":` + itoa(*message.ID) + `,"result":{"turn":{"id":"` + turnID + `"}}}`)
				onLine(`{"method":"item/started","params":{"threadId":"thread-live","turnId":"` + turnID + `","item":{"id":"cmd-1","type":"commandExecution","command":"long","status":"in_progress"}}}`)
			case "turn/steer":
				runner.mu.Lock()
				runner.steers = append(runner.steers, message.Params)
				exitOn := runner.exitOn
				fail := runner.fail
				runner.mu.Unlock()
				if exitOn == "steer" {
					return workspace.Result{ExitCode: 1}, nil
				}
				if fail {
					onLine(`{"id":` + itoa(*message.ID) + `,"error":{"code":-32602,"message":"turn is no longer active"}}`)
					onLine(`{"method":"item/completed","params":{"threadId":"thread-live","turnId":"` + turnID + `","item":{"type":"agentMessage","id":"answer-1","text":"FINISHED-WITHOUT-STEER","phase":"final_answer"}}}`)
				} else {
					onLine(`{"id":` + itoa(*message.ID) + `,"result":{"turnId":"` + turnID + `"}}`)
					onLine(`{"method":"item/completed","params":{"threadId":"thread-live","turnId":"` + turnID + `","item":{"type":"userMessage","id":"steer-1","content":[]}}}`)
					onLine(`{"method":"item/completed","params":{"threadId":"thread-live","turnId":"` + turnID + `","item":{"type":"agentMessage","id":"answer-1","text":"SAW:STEER","phase":"final_answer"}}}`)
				}
				onLine(`{"method":"turn/completed","params":{"threadId":"thread-live","turn":{"id":"` + turnID + `","status":"completed"}}}`)
			}
		}
	}
}

func (runner *liveFakeRunner) command() workspace.Command {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.commands) == 0 {
		return workspace.Command{}
	}
	return runner.commands[len(runner.commands)-1]
}

func (runner *liveFakeRunner) used(method string) bool {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	for _, item := range runner.methods {
		if item == method {
			return true
		}
	}
	return false
}

func (runner *liveFakeRunner) steerParams() []map[string]any {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return append([]map[string]any(nil), runner.steers...)
}

func itoa(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func newLiveCodexRequest(runner workspace.Runner, inbox LiveInbox) Request {
	return Request{
		Runner: runner, WorkDir: "/workspace", Prompt: "do the work",
		Model: "gpt-live", StreamInput: true, LiveInbox: inbox,
	}
}

// A claimed message is delivered with turn/steer against the active turn and
// acknowledged only after app-server accepts it. The reply proves the model saw
// the steered text inside the same turn.
func TestCodexLiveInboxSteersRunningTurnAndAcks(t *testing.T) {
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"m1"}, Text: "STEER"})
	runner := &liveFakeRunner{}
	var events []Event
	result, err := (Codex{LivePoll: time.Millisecond}).Execute(
		context.Background(), newLiveCodexRequest(runner, inbox),
		func(event Event) { events = append(events, event) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "SAW:STEER" || result.ProviderSessionID != "thread-live" {
		t.Fatalf("result = %#v", result)
	}
	if !runner.used("turn/steer") {
		t.Fatalf("turn/steer was not used: %v", runner.command())
	}
	steers := runner.steerParams()
	if len(steers) != 1 {
		t.Fatalf("steers = %#v", steers)
	}
	if steers[0]["threadId"] != "thread-live" || steers[0]["expectedTurnId"] != "turn-live" {
		t.Fatalf("steer targeted the wrong turn: %#v", steers[0])
	}
	text := steerText(steers[0])
	if text != "STEER" {
		t.Fatalf("steer text = %q", text)
	}
	if _, acks := inbox.snapshot(); len(acks) != 1 || acks[0] != "m1=true" {
		t.Fatalf("acks = %#v", acks)
	}
	command := runner.command()
	if !contains(command.Args, "app-server") || contains(command.Args, "exec") {
		t.Fatalf("live Turn did not use app-server: %v", command.Args)
	}
	if command.Input == nil || !command.KillTree {
		t.Fatalf("live Turn must stream stdin and own the process tree: %#v", command)
	}
	var sessionEvents, messages int
	for _, event := range events {
		switch event.Type {
		case "agent_session":
			sessionEvents++
		case "agent_message":
			messages++
		}
	}
	if sessionEvents != 1 || messages != 1 {
		t.Fatalf("events = %#v", events)
	}
}

func steerText(params map[string]any) string {
	input, _ := params["input"].([]any)
	for _, entry := range input {
		object, _ := entry.(map[string]any)
		if text, _ := object["text"].(string); text != "" {
			return text
		}
	}
	return ""
}

// A rejected steer rolls the claim back so the message stays pending for the
// next Turn; the running Turn still completes normally.
func TestCodexLiveInboxRollsBackRejectedSteer(t *testing.T) {
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"m1"}, Text: "STEER"})
	runner := &liveFakeRunner{fail: true}
	if _, err := (Codex{LivePoll: time.Millisecond}).Execute(
		context.Background(), newLiveCodexRequest(runner, inbox), nil,
	); err != nil {
		t.Fatal(err)
	}
	if _, acks := inbox.snapshot(); len(acks) != 1 || acks[0] != "m1=false" {
		t.Fatalf("rejected steer was not rolled back: %#v", acks)
	}
}

// If app-server exits with a steer still in flight, the claim is rolled back
// rather than lost or marked delivered without acceptance.
func TestCodexLiveInboxRollsBackWhenProcessExits(t *testing.T) {
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"m1"}, Text: "STEER"})
	runner := &liveFakeRunner{exitOn: "steer"}
	_, err := (Codex{LivePoll: time.Millisecond}).Execute(
		context.Background(), newLiveCodexRequest(runner, inbox), nil,
	)
	if err == nil {
		t.Fatal("a process that exited mid-turn must fail the Turn")
	}
	if _, acks := inbox.snapshot(); len(acks) != 1 || acks[0] != "m1=false" {
		t.Fatalf("in-flight steer was not rolled back: %#v", acks)
	}
}

// Without the per-Task or deployment switch, the one-shot exec path runs and
// the inbox is never claimed.
func TestCodexLiveInboxOffKeepsExecPath(t *testing.T) {
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"m1"}, Text: "STEER"})
	runner := &recordingExecRunner{}
	request := newLiveCodexRequest(runner, inbox)
	request.StreamInput = false
	if _, err := (Codex{}).Execute(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	if !contains(runner.command.Args, "exec") || contains(runner.command.Args, "app-server") {
		t.Fatalf("disabled live input must use exec: %v", runner.command.Args)
	}
	if claims, _ := inbox.snapshot(); claims != 0 {
		t.Fatalf("inbox was claimed without the switch: %d", claims)
	}
}

// A deployment-wide force still needs a runner that can stream stdin; otherwise
// nothing is claimed and the Turn falls back to exec.
func TestCodexLiveInboxFallsBackWhenRunnerCannotStream(t *testing.T) {
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"m1"}, Text: "STEER"})
	runner := &recordingExecRunner{}
	request := newLiveCodexRequest(runner, inbox)
	request.StreamInput = false
	if _, err := (Codex{LiveInput: true}).Execute(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	if !contains(runner.command.Args, "exec") {
		t.Fatalf("non-streaming runner must fall back to exec: %v", runner.command.Args)
	}
	if claims, _ := inbox.snapshot(); claims != 0 {
		t.Fatalf("inbox was claimed without a live transport: %d", claims)
	}
}

// A resumed session uses thread/resume; the thread id comes back from the
// response and is reported as the provider session.
func TestCodexLiveInboxResumesThread(t *testing.T) {
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"m1"}, Text: "STEER"})
	runner := &liveFakeRunner{}
	request := newLiveCodexRequest(runner, inbox)
	request.ProviderSessionID = "thread-old"
	result, err := (Codex{LivePoll: time.Millisecond}).Execute(context.Background(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !runner.used("thread/resume") || runner.used("thread/start") {
		t.Fatalf("resume path not used: %v", runner.methods)
	}
	if result.ProviderSessionID != "thread-live" {
		t.Fatalf("provider session = %q", result.ProviderSessionID)
	}
}

func TestCodexAppServerArgumentsKeepProviderAndMCPConfig(t *testing.T) {
	request := Request{
		WorkDir: "/workspace", Model: "gpt-live",
		MCPServers: []MCPServer{{Name: "aha2", Transport: "http", URL: "http://127.0.0.1/mcp"}},
		Environment: map[string]string{
			"AHA_PROVIDER_ID": "internal", "OPENAI_BASE_URL": "https://example.test/v1",
			"CODEX_WIRE_API": "responses", "CODEX_ENV_KEY": "OPENAI_API_KEY",
		},
	}
	joined := strings.Join(codexAppServerArguments(request, ""), " ")
	for _, want := range []string{"app-server", "model_provider", "model_providers.internal.base_url", "mcp_servers.aha2.url", "--disable multi_agent"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("app-server arguments missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "exec") || strings.Contains(joined, "--json") {
		t.Fatalf("app-server arguments carry exec-only flags: %s", joined)
	}
}

type recordingExecRunner struct {
	command workspace.Command
}

func (*recordingExecRunner) StreamsInput() bool { return false }

func (runner *recordingExecRunner) Run(_ context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	runner.command = command
	onLine(`{"type":"thread.started","thread_id":"session-exec"}`)
	onLine(`{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`)
	return workspace.Result{ExitCode: 0}, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
