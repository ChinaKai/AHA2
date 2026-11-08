package backend

import (
	"context"
	"encoding/json"
	"errors"
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
//	recover      the first process exits after turn/start, the resumed one completes
//	exhaust      every process exits after turn/start
//	steer        the first process exits with a steer in flight, the resumed one
//	             receives the re-claimed message and completes
//	steer-empty  the first process exits with a steer in flight, the resumed one
//	             completes right after turn/start with an empty inbox
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
				switch {
				case runner.mode == "exhaust", runner.mode == "recover" && call == 1:
					return exit()
				case runner.mode == "recover", runner.mode == "steer-empty" && call > 1:
					finish()
					continue
				}
			case "turn/steer":
				runner.mu.Lock()
				runner.steers = append(runner.steers, message.Params)
				runner.mu.Unlock()
				if call == 1 && (runner.mode == "steer" || runner.mode == "steer-empty") {
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

// recoveryFallbackRunner emulates the failure path where the first app-server
// exits mid-turn, the resumed app-server cannot finish its handshake, and the
// adapter continues the same thread through `codex exec` instead of retrying.
type recoveryFallbackRunner struct {
	mu             sync.Mutex
	appServerCalls int
	execCommands   []workspace.Command
}

func (*recoveryFallbackRunner) StreamsInput() bool { return true }

func (runner *recoveryFallbackRunner) Run(ctx context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	if !contains(command.Args, "app-server") {
		runner.mu.Lock()
		runner.execCommands = append(runner.execCommands, command)
		runner.mu.Unlock()
		onLine(`{"type":"thread.started","thread_id":"thread-exec-fallback"}`)
		onLine(`{"type":"item.completed","item":{"type":"agent_message","text":"EXEC-FALLBACK"}}`)
		return workspace.Result{ExitCode: 0}, nil
	}
	runner.mu.Lock()
	runner.appServerCalls++
	call := runner.appServerCalls
	runner.mu.Unlock()
	if call > 1 {
		// Fail before turn/start, so started stays false and the adapter must
		// fall back to exec rather than resuming a third time.
		return workspace.Result{ExitCode: 2, Stderr: "recovery app-server unavailable"}, nil
	}
	threadID := "thread-fallback-1"
	turnID := "turn-fallback-1"
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
			switch message.Method {
			case "initialize":
				onLine(`{"id":` + itoa(*message.ID) + `,"result":{"userAgent":"fake"}}`)
			case "thread/start":
				onLine(`{"id":` + itoa(*message.ID) + `,"result":{"thread":{"id":"` + threadID + `"}}}`)
			case "turn/start":
				onLine(`{"id":` + itoa(*message.ID) + `,"result":{"turn":{"id":"` + turnID + `"}}}`)
				onLine(`{"method":"item/started","params":{"threadId":"` + threadID + `","turnId":"` + turnID + `","item":{"id":"cmd-1","type":"commandExecution","command":"long silent tool","status":"inProgress"}}}`)
				// The long silent tool is still running when the process dies.
				return workspace.Result{ExitCode: 1, Stderr: "first app-server crashed"}, nil
			}
		}
	}
}

func (runner *recoveryFallbackRunner) snapshot() (int, []workspace.Command) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.appServerCalls, append([]workspace.Command(nil), runner.execCommands...)
}

// timeoutExitRunner kills its process with the runner's own command timeout
// while a steer is in flight, so the adapter sees runDone close without having
// lost the process on its own.
type timeoutExitRunner struct {
	mu             sync.Mutex
	appServerCalls int
	execCalls      int
	prompts        []string
}

func (*timeoutExitRunner) StreamsInput() bool { return true }

func (runner *timeoutExitRunner) Run(ctx context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	if !contains(command.Args, "app-server") {
		runner.mu.Lock()
		runner.execCalls++
		runner.mu.Unlock()
		onLine(`{"type":"thread.started","thread_id":"thread-exec-timeout"}`)
		onLine(`{"type":"item.completed","item":{"type":"agent_message","text":"EXEC-AFTER-TIMEOUT"}}`)
		return workspace.Result{ExitCode: 0}, nil
	}
	runner.mu.Lock()
	runner.appServerCalls++
	call := runner.appServerCalls
	runner.mu.Unlock()
	threadID := "thread-timeout-" + itoa(int64(call))
	turnID := "turn-timeout-" + itoa(int64(call))
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
			switch message.Method {
			case "initialize":
				onLine(`{"id":` + itoa(*message.ID) + `,"result":{"userAgent":"fake"}}`)
			case "thread/start":
				onLine(`{"id":` + itoa(*message.ID) + `,"result":{"thread":{"id":"` + threadID + `"}}}`)
			case "turn/start":
				runner.mu.Lock()
				runner.prompts = append(runner.prompts, turnStartText(message.Params))
				runner.mu.Unlock()
				onLine(`{"id":` + itoa(*message.ID) + `,"result":{"turn":{"id":"` + turnID + `"}}}`)
				onLine(`{"method":"item/started","params":{"threadId":"` + threadID + `","turnId":"` + turnID + `","item":{"id":"cmd-1","type":"commandExecution","command":"long silent tool","status":"inProgress"}}}`)
			case "turn/steer":
				// The harness killed the process for exceeding its own timeout;
				// the steer is still unacknowledged.
				return workspace.Result{ExitCode: 1}, context.DeadlineExceeded
			}
		}
	}
}

func (runner *timeoutExitRunner) snapshot() (int, int, []string) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.appServerCalls, runner.execCalls, append([]string(nil), runner.prompts...)
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
		Model: "gpt-live", LiveInbox: inbox,
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

// When the resumed app-server cannot start, the adapter does not attempt a
// second recovery: it continues the same thread through the one-shot exec path.
func TestCodexLiveRecoveryFallsBackToExecWhenResumeCannotStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runner := &recoveryFallbackRunner{}
	var events []Event
	result, err := (Codex{LivePoll: time.Millisecond}).Execute(
		ctx, recoveryRequest(runner, newFakeLiveInbox()),
		func(event Event) { events = append(events, event) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "EXEC-FALLBACK" {
		t.Fatalf("reply = %q", result.Reply)
	}
	appServerCalls, execs := runner.snapshot()
	if appServerCalls != 2 {
		t.Fatalf("app-server processes = %d, want exactly one recovery attempt", appServerCalls)
	}
	if len(execs) != 1 {
		t.Fatalf("exec calls = %d, want 1", len(execs))
	}
	if !contains(execs[0].Args, "exec") || !contains(execs[0].Args, "resume") || !contains(execs[0].Args, "thread-fallback-1") {
		t.Fatalf("fallback did not resume the thread through exec: %v", execs[0].Args)
	}
	if !strings.Contains(execs[0].Stdin, codexAppServerRecoveryPrompt) {
		t.Fatalf("exec fallback lost the recovery prompt: %q", execs[0].Stdin)
	}
	resumed, fellBack := false, false
	for _, event := range events {
		if event.Type != "agent_progress" {
			continue
		}
		if event.Data["recovery"] == "app_server_resume" {
			resumed = true
		}
		if event.Data["fallback"] == "codex_exec" {
			fellBack = true
		}
	}
	if !resumed || !fellBack {
		t.Fatalf("recovery/fallback progress was not observable: %#v", events)
	}
}

// A hard Turn timeout is not an app-server crash. The runner returns
// context.DeadlineExceeded after turn/start, so the adapter must neither resume
// the thread nor retry through exec with a fresh timeout.
func TestCodexLiveHardTimeoutDoesNotRecoverOrFallBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"m1"}, Text: "STEER"})
	runner := &timeoutExitRunner{}
	_, err := (Codex{LivePoll: time.Millisecond}).Execute(
		ctx, recoveryRequest(runner, inbox), nil,
	)
	if err == nil {
		t.Fatal("a hard Turn timeout must fail")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %q is not a deadline error", err)
	}
	if strings.Contains(err.Error(), "app-server exited") {
		t.Fatalf("hard timeout was reported as an app-server exit: %q", err)
	}
	appServerCalls, execCalls, prompts := runner.snapshot()
	if appServerCalls != 1 {
		t.Fatalf("app-server processes = %d, want exactly 1 (no recovery)", appServerCalls)
	}
	if execCalls != 0 {
		t.Fatalf("exec calls = %d, want 0 (no fallback with a fresh timeout)", execCalls)
	}
	if len(prompts) != 1 || prompts[0] == codexAppServerRecoveryPrompt {
		t.Fatalf("turn prompts = %#v, want no recovery turn", prompts)
	}
	if _, acks := inbox.snapshot(); len(acks) != 1 || acks[0] != "m1=false" {
		t.Fatalf("in-flight steer was not rolled back: %#v", acks)
	}
}
