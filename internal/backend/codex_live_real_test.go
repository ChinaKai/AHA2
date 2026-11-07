package backend

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

// TestCodexLiveInboxRealCLI is the opt-in end-to-end check against the
// installed Codex CLI. It needs a real provider, so it skips unless explicitly
// enabled:
//
//	AHA2_CODEX_LIVE_TEST=1
//	AHA2_CODEX_LIVE_MODEL=<wire model>
//	OPENAI_API_KEY=<credential>
//	CODEX_HOME=<isolated codex home>
//	AHA2_CODEX_LIVE_PROVIDER / _BASE_URL / _WIRE_API (optional provider config)
//
// The Turn is instructed to run a long sleep. The inbox hands the message over
// only after the first command has started, so a reply with the marker proves
// the message reached the model while the same Turn was still running.
func TestCodexLiveInboxRealCLI(t *testing.T) {
	if os.Getenv("AHA2_CODEX_LIVE_TEST") != "1" {
		t.Skip("set AHA2_CODEX_LIVE_TEST=1 with a real provider to run this test")
	}
	binary := strings.TrimSpace(os.Getenv("AHA2_CODEX_LIVE_BIN"))
	if binary == "" {
		binary = "codex"
	}
	model := strings.TrimSpace(os.Getenv("AHA2_CODEX_LIVE_MODEL"))
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if model == "" || codexHome == "" || apiKey == "" {
		t.Fatal("AHA2_CODEX_LIVE_MODEL, CODEX_HOME and OPENAI_API_KEY are required")
	}
	environment := map[string]string{
		"CODEX_HOME": codexHome, "OPENAI_API_KEY": apiKey,
		"CODEX_ENV_KEY": "OPENAI_API_KEY", "CODEX_WIRE_API": "responses",
	}
	for source, target := range map[string]string{
		"AHA2_CODEX_LIVE_PROVIDER": "AHA_PROVIDER_ID",
		"AHA2_CODEX_LIVE_BASE_URL": "OPENAI_BASE_URL",
	} {
		if value := strings.TrimSpace(os.Getenv(source)); value != "" {
			environment[target] = value
		}
	}
	const marker = "QUEUED-LIVE-6c1d94"
	commandStarted := make(chan struct{})
	inbox := &realLiveInbox{commandStarted: commandStarted, text: marker + ". Delivery test: if this message is visible while the current turn is still running, stop immediately and reply exactly SAW-" + marker + ". Do not run any more commands."}
	var mu sync.Mutex
	type timedEvent struct {
		At    time.Time
		Event Event
	}
	var events []timedEvent
	started := time.Now()
	result, err := (Codex{
		Binary: binary, LiveInput: true, LivePoll: 200 * time.Millisecond, Timeout: 3 * time.Minute,
	}).Execute(context.Background(), Request{
		Runner: workspace.LocalRunner{}, WorkDir: t.TempDir(), Model: model,
		Environment: environment, StreamInput: true, LiveInbox: inbox,
		// The Windows sandbox blocks process launches in workspace-write mode,
		// so the long command this test needs could never start.
		Filesystem: "danger-full-access",
		Prompt: "You are participating in a message-delivery test. Follow these steps exactly, in order, and do nothing else:\n\n" +
			"1. Use your shell tool to run: Start-Sleep -Seconds 20\n" +
			"2. Use your shell tool to run: Start-Sleep -Seconds 20\n" +
			"3. Reply with exactly: FINAL-NO-QUEUE\n\n" +
			"Exception: if at any point you receive a user message whose text contains a token beginning with QUEUED-, immediately stop the current task, run no further shell commands, and reply with exactly: SAW-<token> where <token> is the complete QUEUED- token from that message.\n",
	}, func(event Event) {
		mu.Lock()
		events = append(events, timedEvent{At: time.Now(), Event: event})
		mu.Unlock()
		if event.Type == "agent_command_started" {
			inbox.markCommandStarted()
		}
	})
	ended := time.Now()
	if err != nil {
		t.Fatalf("live Turn failed: %v", err)
	}
	if result.ProviderSessionID == "" {
		t.Fatal("no provider session id was reported")
	}
	if !strings.Contains(result.Reply, "SAW-"+marker) {
		claimAt, ackDelivered := inbox.settlement()
		mu.Lock()
		lines := make([]string, 0, len(events))
		for _, timed := range events {
			lines = append(lines, timed.At.Format("15:04:05.000")+" "+timed.Event.Type+" "+fmt.Sprint(timed.Event.Data))
		}
		mu.Unlock()
		t.Fatalf("the model did not see the steered message: reply=%q claim=%v delivered=%t events=\n%s",
			result.Reply, claimAt, ackDelivered, strings.Join(lines, "\n"))
	}
	if strings.Contains(result.Reply, "FINAL-NO-QUEUE") {
		t.Fatalf("the model completed the original plan instead of applying the steer: reply=%q", result.Reply)
	}
	claimAt, ackDelivered := inbox.settlement()
	if claimAt.IsZero() || !ackDelivered {
		t.Fatalf("claim was not acknowledged as delivered: at=%v delivered=%t", claimAt, ackDelivered)
	}
	if claimAt.Before(started) || claimAt.After(ended) {
		t.Fatalf("claim timestamp %v is outside the Turn [%v, %v]", claimAt, started, ended)
	}
	var commandsStarted, markerAt time.Time
	var sleepCommands int
	mu.Lock()
	for _, timed := range events {
		if timed.Event.Type == "agent_command_started" {
			if commandsStarted.IsZero() {
				commandsStarted = timed.At
			}
			command := strings.ToLower(fmt.Sprint(timed.Event.Data["command"]))
			if strings.Contains(command, "sleep") {
				sleepCommands++
			}
		}
		if timed.Event.Type == "agent_message" {
			if text, _ := timed.Event.Data["text"].(string); strings.Contains(text, "SAW-"+marker) {
				markerAt = timed.At
			}
		}
	}
	mu.Unlock()
	if sleepCommands != 1 {
		t.Fatalf("expected the model to stop after the first long command, saw %d sleep commands", sleepCommands)
	}
	if markerAt.IsZero() || !markerAt.After(claimAt) || !markerAt.After(commandsStarted) || markerAt.After(ended) {
		t.Fatalf("timeline is not claim < model-sees < turn-end: claim=%v command=%v message=%v end=%v", claimAt, commandsStarted, markerAt, ended)
	}
	t.Logf("live steer timeline: claim=%v command_start=%v saw=%v turn_end=%v", claimAt, commandsStarted, markerAt, ended)
}

// realLiveInbox releases its batch only once the Turn has started a command, so
// the steer lands while the Turn is demonstrably still working.
type realLiveInbox struct {
	mu             sync.Mutex
	commandStarted chan struct{}
	text           string
	sent           bool
	claimAt        time.Time
	delivered      bool
	acked          bool
}

func (inbox *realLiveInbox) markCommandStarted() {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	select {
	case <-inbox.commandStarted:
	default:
		close(inbox.commandStarted)
	}
}

func (inbox *realLiveInbox) Claim(ctx context.Context) (LiveBatch, error) {
	inbox.mu.Lock()
	if inbox.sent {
		inbox.mu.Unlock()
		return LiveBatch{}, nil
	}
	inbox.mu.Unlock()
	select {
	case <-inbox.commandStarted:
	case <-ctx.Done():
		return LiveBatch{}, ctx.Err()
	default:
		return LiveBatch{}, nil
	}
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if inbox.sent {
		return LiveBatch{}, nil
	}
	inbox.sent = true
	inbox.claimAt = time.Now()
	return LiveBatch{IDs: []string{"live-1"}, Text: inbox.text}, nil
}

func (inbox *realLiveInbox) Ack(_ context.Context, _ LiveBatch, delivered bool) error {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	inbox.acked = true
	inbox.delivered = delivered
	return nil
}

func (inbox *realLiveInbox) settlement() (time.Time, bool) {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	return inbox.claimAt, inbox.acked && inbox.delivered
}
