package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

// fakeClaudeModeEnv turns this test binary into a fake Claude Code CLI. The
// adapter runs it through the production LocalRunner, so the fake reads the
// same stdin pipe and writes the same stdout pipe the real CLI would.
const fakeClaudeModeEnv = "AHA2_TEST_FAKE_CLAUDE_MODE"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeClaudeModeEnv); mode != "" {
		os.Exit(runFakeClaude(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

type fakeClaudeInput struct {
	UUID    string `json:"uuid"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

// runFakeClaude imitates the stream-json protocol of Claude Code 2.1.220 that
// the adapter depends on: an input message is replayed with its uuid when the
// CLI starts the step that reads it, and stdin stays open after a result.
//
//   - live: replay the prompt, start a tool, then refuse to finish the step
//     until a second user message arrives on stdin.
//   - queued: read the second message first, finish the first step, then
//     replay and answer the queued message in a further step.
//   - drop: read the second message but never replay it.
//   - hang: replay the prompt and wait for stdin to close.
//   - answer: replay and answer the prompt, then wait for stdin to close.
func runFakeClaude(mode string, args []string) int {
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--input-format stream-json") || !strings.Contains(joined, "--replay-user-messages") {
		fmt.Fprintf(os.Stderr, "fake claude: stream input flags missing: %s\n", joined)
		return 2
	}
	lines := make(chan fakeClaudeInput)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var input fakeClaudeInput
			if json.Unmarshal(scanner.Bytes(), &input) == nil {
				lines <- input
			}
		}
	}()
	emit := func(value map[string]any) {
		encoded, _ := json.Marshal(value)
		fmt.Println(string(encoded))
	}
	replay := func(input fakeClaudeInput) {
		emit(map[string]any{"type": "user", "isReplay": true, "uuid": input.UUID, "session_id": "fake-session",
			"message": map[string]any{"role": "user", "content": input.Message.Content}})
	}
	result := func(text string) {
		emit(map[string]any{"type": "result", "subtype": "success", "result": text, "session_id": "fake-session"})
	}
	next := func() (fakeClaudeInput, bool) {
		select {
		case input, ok := <-lines:
			return input, ok
		case <-time.After(20 * time.Second):
			return fakeClaudeInput{}, false
		}
	}
	first, ok := next()
	if !ok {
		return 3
	}
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": "fake-session"})
	switch mode {
	case "live":
		replay(first)
		emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
			map[string]any{"type": "tool_use", "id": "tool-1", "name": "Bash", "input": map[string]any{"command": "sleep 60"}},
		}}})
		second, ok := next()
		if !ok {
			result("first=" + first.Message.Content + " live=none")
			break
		}
		replay(second)
		emit(map[string]any{"type": "user", "message": map[string]any{"content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "tool-1", "content": "ok"},
		}}})
		result("first=" + first.Message.Content + " live=" + second.Message.Content)
	case "queued":
		second, ok := next()
		replay(first)
		result("first answer")
		if ok {
			replay(second)
			result("second answer: " + second.Message.Content)
		}
	case "drop":
		replay(first)
		if _, ok := next(); !ok {
			return 4
		}
		result("first answer")
	case "hang":
		replay(first)
	case "answer":
		replay(first)
		result("only answer")
	default:
		return 5
	}
	// Like the CLI, keep reading until stdin is closed.
	for range lines {
	}
	return 0
}

// fakeLiveInbox hands out one batch per entry in batches and records every
// acknowledgement.
type fakeLiveInbox struct {
	mu      sync.Mutex
	batches []LiveBatch
	claims  int
	acks    []string
	claimed chan struct{}
}

func newFakeLiveInbox(batches ...LiveBatch) *fakeLiveInbox {
	return &fakeLiveInbox{batches: batches, claimed: make(chan struct{}, len(batches)+1)}
}

func (inbox *fakeLiveInbox) Claim(context.Context) (LiveBatch, error) {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	inbox.claims++
	if len(inbox.batches) == 0 {
		return LiveBatch{}, nil
	}
	batch := inbox.batches[0]
	inbox.batches = inbox.batches[1:]
	inbox.claimed <- struct{}{}
	return batch, nil
}

func (inbox *fakeLiveInbox) Ack(_ context.Context, batch LiveBatch, delivered bool) error {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	inbox.acks = append(inbox.acks, fmt.Sprintf("%s=%t", strings.Join(batch.IDs, ","), delivered))
	return nil
}

func (inbox *fakeLiveInbox) snapshot() (int, []string) {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	return inbox.claims, append([]string(nil), inbox.acks...)
}

func fakeClaudeAdapter(t *testing.T, mode string) Claude {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeClaudeModeEnv, mode)
	return Claude{
		Binary: executable, StreamInput: true, Timeout: time.Minute,
		LivePoll: 10 * time.Millisecond, LiveReplayGrace: 300 * time.Millisecond,
	}
}

// The effective switch is the deployment-wide flag OR the per-Task opt-in.
func TestClaudeStreamInputEffectiveTruthTable(t *testing.T) {
	cases := []struct {
		name        string
		globalForce bool
		perTask     bool
		want        bool
	}{
		{name: "per-task on, global off", perTask: true, want: true},
		{name: "per-task off, global on", globalForce: true, want: true},
		{name: "both off", want: false},
		{name: "both on", globalForce: true, perTask: true, want: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			adapter := Claude{StreamInput: testCase.globalForce}
			if got := adapter.streamInputEnabled(Request{StreamInput: testCase.perTask}); got != testCase.want {
				t.Fatalf("effective stream input = %t, want %t", got, testCase.want)
			}
		})
	}
}

// A per-Task opt-in must start the stream-json path and claim this Turn's inbox
// even when the deployment-wide AHA2_CLAUDE_STREAM_INPUT flag is absent.
func TestClaudePerTaskStreamInputClaimsInbox(t *testing.T) {
	adapter := fakeClaudeAdapter(t, "live")
	adapter.StreamInput = false
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"per-task-1"}, Text: "owner correction"})
	result, err := adapter.Execute(context.Background(), Request{
		Runner: workspace.LocalRunner{}, WorkDir: t.TempDir(), Prompt: "initial prompt",
		LiveInbox: inbox, StreamInput: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "first=initial prompt live=owner correction" {
		t.Fatalf("result = %#v", result)
	}
	if claims, acks := inbox.snapshot(); claims == 0 || strings.Join(acks, ";") != "per-task-1=true" {
		t.Fatalf("per-Task stream input did not claim the inbox: claims=%d acks=%v", claims, acks)
	}
}

// The core spike claim: after the process started and before its first step
// finished, AHA writes a second user message into the same stdin, the CLI
// reads it, and the replay acknowledges the claim as delivered.
func TestClaudeStreamInputDeliversLiveMessageMidTurn(t *testing.T) {
	adapter := fakeClaudeAdapter(t, "live")
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"inbox-1", "inbox-2"}, Text: "owner correction"})
	events := []Event{}
	result, err := adapter.Execute(context.Background(), Request{
		Runner: workspace.LocalRunner{}, WorkDir: t.TempDir(), Prompt: "initial prompt", LiveInbox: inbox,
	}, func(event Event) { events = append(events, event) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "first=initial prompt live=owner correction" || result.ProviderSessionID != "fake-session" {
		t.Fatalf("result = %#v", result)
	}
	if _, acks := inbox.snapshot(); strings.Join(acks, ";") != "inbox-1,inbox-2=true" {
		t.Fatalf("acks = %v", acks)
	}
	for _, event := range events {
		if event.Type == "agent_command_finished" {
			return
		}
	}
	t.Fatalf("tool step did not finish after the live message: %#v", events)
}

// A message the CLI queued behind a result is answered by a further result in
// the same run. stdin must stay open for it, the last answer is the reply and
// the earlier answer stays visible as an intermediate message.
func TestClaudeStreamInputKeepsStdinOpenForQueuedMessage(t *testing.T) {
	adapter := fakeClaudeAdapter(t, "queued")
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"inbox-q"}, Text: "late note"})
	var intermediate []string
	result, err := adapter.Execute(context.Background(), Request{
		Runner: workspace.LocalRunner{}, WorkDir: t.TempDir(), Prompt: "initial", LiveInbox: inbox,
	}, func(event Event) {
		if event.Type == "agent_message" && event.Data["intermediate"] == true {
			intermediate = append(intermediate, fmt.Sprint(event.Data["text"]))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "second answer: late note" {
		t.Fatalf("reply = %q", result.Reply)
	}
	if strings.Join(intermediate, "|") != "first answer" {
		t.Fatalf("intermediate = %v", intermediate)
	}
	if _, acks := inbox.snapshot(); strings.Join(acks, ";") != "inbox-q=true" {
		t.Fatalf("acks = %v", acks)
	}
}

// A written message that is never replayed was not accepted. The run must
// end once the replay grace expires and roll the claim back to the inbox.
func TestClaudeStreamInputRollsBackUnreplayedMessage(t *testing.T) {
	adapter := fakeClaudeAdapter(t, "drop")
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"inbox-d"}, Text: "dropped"})
	started := time.Now()
	result, err := adapter.Execute(context.Background(), Request{
		Runner: workspace.LocalRunner{}, WorkDir: t.TempDir(), Prompt: "initial", LiveInbox: inbox,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "first answer" {
		t.Fatalf("reply = %q", result.Reply)
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("run waited %s for a replay that never came", elapsed)
	}
	if _, acks := inbox.snapshot(); strings.Join(acks, ";") != "inbox-d=false" {
		t.Fatalf("acks = %v", acks)
	}
}

// A result with nothing outstanding ends the Turn: stdin is closed at once,
// so the CLI exits and Execute returns well before the replay grace, which
// only applies while a written message still waits for its replay.
func TestClaudeStreamInputClosesStdinRightAfterIdleResult(t *testing.T) {
	adapter := fakeClaudeAdapter(t, "answer")
	adapter.LiveReplayGrace = 15 * time.Second
	inbox := newFakeLiveInbox()
	var resultAt time.Time
	result, err := adapter.Execute(context.Background(), Request{
		Runner: workspace.LocalRunner{}, WorkDir: t.TempDir(), Prompt: "initial", LiveInbox: inbox,
	}, func(event Event) {
		if event.Type == "agent_message" && event.Data["final"] == true && resultAt.IsZero() {
			resultAt = time.Now()
		}
	})
	returnedAt := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "only answer" {
		t.Fatalf("reply = %q", result.Reply)
	}
	if resultAt.IsZero() {
		t.Fatal("no result was observed")
	}
	if elapsed := returnedAt.Sub(resultAt); elapsed > 2*time.Second {
		t.Fatalf("Execute returned %s after an idle result; stdin was kept open for the %s replay grace", elapsed, adapter.LiveReplayGrace)
	}
	if _, acks := inbox.snapshot(); len(acks) != 0 {
		t.Fatalf("acks = %v", acks)
	}
}

// Cancelling the Turn closes stdin, stops claiming and rolls back a claim the
// CLI never replayed.
func TestClaudeStreamInputCancelRollsBackAndStopsClaiming(t *testing.T) {
	adapter := fakeClaudeAdapter(t, "hang")
	inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"inbox-c"}, Text: "never read"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := adapter.Execute(ctx, Request{
			Runner: workspace.LocalRunner{}, WorkDir: t.TempDir(), Prompt: "initial", LiveInbox: inbox,
		}, nil)
		done <- err
	}()
	select {
	case <-inbox.claimed:
	case <-time.After(10 * time.Second):
		t.Fatal("live inbox was never claimed")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled run reported success")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Execute did not return after cancel")
	}
	claims, acks := inbox.snapshot()
	if strings.Join(acks, ";") != "inbox-c=false" {
		t.Fatalf("acks = %v", acks)
	}
	time.Sleep(50 * time.Millisecond)
	if after, _ := inbox.snapshot(); after != claims {
		t.Fatalf("live inbox still claimed after Execute returned: %d -> %d", claims, after)
	}
}

type claudeStdinRunner struct {
	command workspace.Command
}

func (runner *claudeStdinRunner) Run(_ context.Context, command workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	runner.command = command
	onLine(`{"type":"result","subtype":"success","result":"done","session_id":"sess-oneshot"}`)
	return workspace.Result{}, nil
}

// With the flag off, or on a runner that cannot stream stdin, the adapter
// keeps the one-shot -p invocation: raw prompt on stdin, no stream flags, no
// Input channel and no live claims.
func TestClaudeOneShotWhenStreamInputUnavailable(t *testing.T) {
	cases := map[string]struct {
		adapter Claude
		perTask bool
	}{
		"both off":              {},
		"global on, no stdin":   {adapter: Claude{StreamInput: true, LivePoll: time.Millisecond}},
		"per-task on, no stdin": {adapter: Claude{LivePoll: time.Millisecond}, perTask: true},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			runner := &claudeStdinRunner{}
			inbox := newFakeLiveInbox(LiveBatch{IDs: []string{"x"}, Text: "x"})
			result, err := testCase.adapter.Execute(context.Background(), Request{
				Runner: runner, WorkDir: "/workspace", Prompt: "hello prompt", LiveInbox: inbox,
				StreamInput: testCase.perTask,
			}, nil)
			if err != nil || result.Reply != "done" {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			joined := strings.Join(runner.command.Args, " ")
			if !strings.HasPrefix(joined, "-p --output-format stream-json --verbose") ||
				strings.Contains(joined, "--input-format") || strings.Contains(joined, "--replay-user-messages") {
				t.Fatalf("args = %s", joined)
			}
			if runner.command.Stdin != "hello prompt" || runner.command.Input != nil {
				t.Fatalf("stdin=%q input=%v", runner.command.Stdin, runner.command.Input)
			}
			if claims, acks := inbox.snapshot(); claims != 0 || len(acks) != 0 {
				t.Fatalf("one-shot run touched the live inbox: claims=%d acks=%v", claims, acks)
			}
		})
	}
}

func TestClaudeUserMessageIsOneStreamJSONLine(t *testing.T) {
	line := claudeUserMessage("uuid-1", "line one\nline two")
	if strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, "\n") {
		t.Fatalf("message is not a single line: %q", line)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatal(err)
	}
	message, _ := decoded["message"].(map[string]any)
	if decoded["type"] != "user" || decoded["uuid"] != "uuid-1" || message["role"] != "user" || message["content"] != "line one\nline two" {
		t.Fatalf("decoded = %#v", decoded)
	}
	if first, second := newClaudeMessageUUID(), newClaudeMessageUUID(); first == second || len(first) != 36 || first[14] != '4' {
		t.Fatalf("uuids %q %q", first, second)
	}
}
