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

// appServerExitRunner emulates `codex app-server` processes for the mid-turn
// exit recovery path. The mode decides which call exits and how the following
// process finishes:
//
//	recover  the first process exits after turn/start, the resumed one completes
//	exhaust  every process exits after turn/start
//	steer    the first process exits with a steer in flight, the resumed one
//	         receives the re-claimed message and completes
type appServerExitRunner struct {
	mu     sync.Mutex
	mode   string
	calls  int
	thread []string
	prompt []string
	steers []map[string]any
}

func (*appServerExitRunner) StreamsInput() bool { return true }

func (runner *appServerExitRunner) Run(ctx context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	runner.mu.Lock()
	runner.calls++
	call := runner.calls
	runner.mu.Unlock()
	threadID := "thread-exit-" + itoa(int64(call))
	turnID := "turn-exit-" + itoa(int64(call))
	exit := func() (workspace.Result, error) {
		return workspace.Result{ExitCode: 1, Stderr: "app-server crashed during the silent tool"}, nil
	}
	finish := func() {
		onLine(`{"method":"item/completed","params":{"threadId":"` + threadID + `","turnId":"` + turnID + `","item":{"type":"agentMessage","id":"answer-` + itoa(int64(call)) + `","text":"` + runner.completionText() + `","phase":"final_answer"}}}`)
		onLine(`{"method":"turn/completed","params":{"threadId":"` + threadID + `","turn":{"id":"` + turnID + `","status":"completed"}}}`)
	}
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
			response := func(result string) {
				onLine(`{"id":` + itoa(*message.ID) + `,"result":` + result + `}`)
			}
			switch message.Method {
			case "initialize":
				response(`{"userAgent":"fake"}`)
			case "thread/start", "thread/resume":
				runner.mu.Lock()
				runner.thread = append(runner.thread, fmtString(message.Params["threadId"]))
				runner.mu.Unlock()
				response(`{"thread":{"id":"` + threadID + `"}}`)
			case "turn/start":
				runner.mu.Lock()
				runner.prompt = append(runner.prompt, turnStartText(message.Params))
				runner.mu.Unlock()
				response(`{"turn":{"id":"` + turnID + `"}}`)
				onLine(`{"method":"item/started","params":{"threadId":"` + threadID + `","turnId":"` + turnID + `","item":{"id":"cmd-1","type":"commandExecution","command":"long silent tool","status":"inProgress"}}}`)
				if runner.mode == "exhaust" || (runner.mode == "recover" && call == 1) {
					return exit()
				}
				if runner.mode == "recover" {
					finish()
					continue
				}
			case "turn/steer":
				runner.mu.Lock()
				runner.steers = append(runner.steers, message.Params)
				runner.mu.Unlock()
				if call == 1 && runner.mode == "steer" {
					// The claim is in flight when the process dies; the adapter
					// must roll it back so the resumed process can re-deliver it.
					return exit()
				}
				response(`{"turnId":"` + turnID + `"}`)
				finish()
			}
		}
	}
}

func (runner *appServerExitRunner) completionText() string {
	if runner.mode == "steer" {
		return "SAW:STEER"
	}
	return "RECOVERED"
}

func (runner *appServerExitRunner) snapshot() (int, []string, []string, []map[string]any) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.calls, append([]string(nil), runner.thread...),
		append([]string(nil), runner.prompt...), append([]map[string]any(nil), runner.steers...)
}

func fmtString(value any) string {
	text, _ := value.(string)
	return text
}

func turnStartText(params map[string]any) string {
	input, _ := params["input"].([]any)
	for _, entry := range input {
		if object, ok := entry.(map[string]any); ok {
			if text, _ := object["text"].(string); text != "" {
				return text
			}
		}
	}
	return ""
}

// requeueInbox keeps a batch pending until it is acknowledged as delivered, so
// a rollback makes the same message claimable by the resumed process.
type requeueInbox struct {
	mu      sync.Mutex
	pending []LiveBatch
	acks    []string
}

func (inbox *requeueInbox) Claim(context.Context) (LiveBatch, error) {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if len(inbox.pending) == 0 {
		return LiveBatch{}, nil
	}
	batch := inbox.pending[0]
	inbox.pending = inbox.pending[1:]
	return batch, nil
}

func (inbox *requeueInbox) Ack(_ context.Context, batch LiveBatch, delivered bool) error {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	inbox.acks = append(inbox.acks, strings.Join(batch.IDs, ",")+"="+itoaBool(delivered))
	if !delivered {
		inbox.pending = append(inbox.pending, batch)
	}
	return nil
}

func (inbox *requeueInbox) snapshot() []string {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	return append([]string(nil), inbox.acks...)
}

func itoaBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func recoveryRequest(runner workspace.Runner, inbox LiveInbox) Request {
	return Request{
		Runner: runner, WorkDir: "/workspace", Prompt: "do the long task",
		Model: "gpt-live", StreamInput: true, LiveInbox: inbox,
	}
}

// A process that exits on its own mid-turn is resumed on the same thread, so a
// long silent tool does not fail the Turn. The resumed turn must not restart
// the original prompt.
func TestCodexLiveResumesThreadAfterProcessExit(t *testing.T) {
	runner := &appServerExitRunner{mode: "recover"}
	var events []Event
	result, err := (Codex{LivePoll: time.Millisecond}).Execute(
		context.Background(), recoveryRequest(runner, newFakeLiveInbox()),
		func(event Event) { events = append(events, event) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "RECOVERED" || result.ProviderSessionID != "thread-exit-2" {
		t.Fatalf("result = %#v", result)
	}
	calls, threads, prompts, _ := runner.snapshot()
	if calls != 2 {
		t.Fatalf("app-server processes = %d, want 2", calls)
	}
	if len(threads) != 2 || threads[1] != "thread-exit-1" {
		t.Fatalf("resume did not reuse the first thread: %#v", threads)
	}
	if len(prompts) != 2 || prompts[0] != "do the long task" || prompts[1] != codexAppServerRecoveryPrompt {
		t.Fatalf("turn prompts = %#v", prompts)
	}
	resumed := false
	for _, event := range events {
		if event.Type == "agent_progress" && event.Data["recovery"] == "app_server_resume" {
			resumed = true
			if !strings.Contains(fmtString(event.Data["cause"]), "app-server crashed during the silent tool") {
				t.Fatalf("recovery event dropped the exit diagnostics: %#v", event.Data)
			}
		}
	}
	if !resumed {
		t.Fatalf("recovery was not observable: %#v", events)
	}
}

// Recovery is bounded: if the resumed process also exits, the Turn fails with
// the exit diagnostics instead of restarting forever.
func TestCodexLiveRecoveryIsBoundedToASecondProcess(t *testing.T) {
	runner := &appServerExitRunner{mode: "exhaust"}
	_, err := (Codex{LivePoll: time.Millisecond}).Execute(
		context.Background(), recoveryRequest(runner, newFakeLiveInbox()), nil,
	)
	if err == nil {
		t.Fatal("a second mid-turn exit must fail the Turn")
	}
	for _, want := range []string{"codex app-server exited before the turn completed", "exit code 1", "app-server crashed during the silent tool"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
	if calls, _, _, _ := runner.snapshot(); calls != 2 {
		t.Fatalf("app-server processes = %d, want exactly 2", calls)
	}
}

// A live inbox message whose steer was in flight when the process died is
// rolled back and re-delivered inside the resumed turn.
func TestCodexLiveRecoveryRedeliversInFlightSteer(t *testing.T) {
	inbox := &requeueInbox{pending: []LiveBatch{{IDs: []string{"m1"}, Text: "STEER"}}}
	runner := &appServerExitRunner{mode: "steer"}
	result, err := (Codex{LivePoll: time.Millisecond}).Execute(
		context.Background(), recoveryRequest(runner, inbox), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "SAW:STEER" {
		t.Fatalf("reply = %q", result.Reply)
	}
	// The message is steered into both processes: the first attempt dies with
	// its process, the rollback makes the resumed process claim it again.
	if calls, _, _, steers := runner.snapshot(); calls != 2 || len(steers) != 2 || steerText(steers[1]) != "STEER" {
		t.Fatalf("calls=%d steers=%d (%#v), want two steer attempts", calls, len(steers), steers)
	}
	if acks := inbox.snapshot(); len(acks) != 2 || acks[0] != "m1=false" || acks[1] != "m1=true" {
		t.Fatalf("inbox acks = %#v, want rollback then delivery", acks)
	}
}
