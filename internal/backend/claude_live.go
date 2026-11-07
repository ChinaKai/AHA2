package backend

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// defaultClaudeLivePoll is how often a stream-input Claude run asks the
// LiveInbox for new messages. Claude Code only reads a queued message at its
// next step boundary, so polling faster than this would not deliver sooner.
const defaultClaudeLivePoll = 500 * time.Millisecond

// claudeLiveReplayGrace is how long a run waits after a result for the replay
// of a message it already wrote. Claude Code replays a queued message when it
// starts the step that answers it, which takes seconds, not minutes.
const claudeLiveReplayGrace = 30 * time.Second

// claudeLiveInput feeds LiveInbox messages into a running Claude Code process
// started with --input-format stream-json --replay-user-messages.
//
// A claimed batch is written as one user message with its own uuid and stays
// outstanding until Claude Code replays that uuid, which is the only evidence
// that the CLI accepted it; it is then acknowledged as delivered. Whatever is
// still outstanding when the process ends is rolled back to the inbox.
//
// Claude Code keeps reading stdin after a result and answers messages queued
// behind it in a further step, so stdin may only be closed once a result has
// arrived with nothing outstanding. That decision and every write happen on
// the run goroutine, which is what keeps a message from being claimed after
// the decision to close.
type claudeLiveInput struct {
	inbox LiveInbox
	poll  time.Duration
	input chan string
	// resulted is signalled for every result line; stop is closed once the
	// runner has returned; done is closed when run exits.
	resulted chan struct{}
	stop     chan struct{}
	done     chan struct{}
	// grace bounds how long stdin stays open after a result while a written
	// message has not been replayed, so a message the CLI silently dropped
	// cannot keep the Turn alive until the idle watchdog fires.
	grace time.Duration

	mu          sync.Mutex
	outstanding map[string]LiveBatch
}

func newClaudeLiveInput(inbox LiveInbox, poll, grace time.Duration) *claudeLiveInput {
	if poll <= 0 {
		poll = defaultClaudeLivePoll
	}
	if grace <= 0 {
		grace = claudeLiveReplayGrace
	}
	return &claudeLiveInput{
		inbox: inbox, poll: poll, input: make(chan string),
		resulted: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
		grace: grace, outstanding: map[string]LiveBatch{},
	}
}

// claudeUserMessage encodes text as one stream-json user input line.
func claudeUserMessage(uuid, text string) string {
	encoded, _ := json.Marshal(map[string]any{
		"type": "user", "uuid": uuid,
		"message": map[string]any{"role": "user", "content": text},
	})
	return string(encoded) + "\n"
}

// newClaudeMessageUUID returns a random RFC 4122 version 4 UUID. Claude Code
// echoes the uuid of each replayed input message, so it must be unique per
// write.
func newClaudeMessageUUID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

func (live *claudeLiveInput) run(ctx context.Context) {
	defer close(live.done)
	ticker := time.NewTicker(live.poll)
	defer ticker.Stop()
	var grace <-chan time.Time
	for {
		select {
		case <-live.stop:
			return
		case <-ctx.Done():
			return
		case <-grace:
			// Unreplayed batches are rolled back by finish.
			close(live.input)
			return
		case <-live.resulted:
			live.mu.Lock()
			idle := len(live.outstanding) == 0
			live.mu.Unlock()
			if idle {
				close(live.input)
				return
			}
			if grace == nil {
				timer := time.NewTimer(live.grace)
				defer timer.Stop()
				grace = timer.C
			}
		case <-ticker.C:
			batch, err := live.inbox.Claim(ctx)
			if err != nil || len(batch.IDs) == 0 {
				continue
			}
			uuid := newClaudeMessageUUID()
			live.mu.Lock()
			live.outstanding[uuid] = batch
			live.mu.Unlock()
			select {
			case live.input <- claudeUserMessage(uuid, batch.Text):
			case <-live.stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}
}

// observe inspects one stdout line for the replay of an outstanding message
// or for a result.
func (live *claudeLiveInput) observe(ctx context.Context, line string) {
	var payload struct {
		Type     string `json:"type"`
		IsReplay bool   `json:"isReplay"`
		UUID     string `json:"uuid"`
	}
	if json.Unmarshal([]byte(line), &payload) != nil {
		return
	}
	switch {
	case payload.Type == "user" && payload.IsReplay && payload.UUID != "":
		live.mu.Lock()
		batch, ok := live.outstanding[payload.UUID]
		delete(live.outstanding, payload.UUID)
		live.mu.Unlock()
		if ok {
			_ = live.inbox.Ack(context.WithoutCancel(ctx), batch, true)
		}
	case payload.Type == "result":
		select {
		case live.resulted <- struct{}{}:
		default:
		}
	}
}

// finish stops the run goroutine after the runner has returned and rolls
// every batch Claude Code never replayed back to the inbox.
func (live *claudeLiveInput) finish(ctx context.Context) {
	close(live.stop)
	<-live.done
	live.mu.Lock()
	remaining := live.outstanding
	live.outstanding = map[string]LiveBatch{}
	live.mu.Unlock()
	for _, batch := range remaining {
		_ = live.inbox.Ack(context.WithoutCancel(ctx), batch, false)
	}
}
