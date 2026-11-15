package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// blockingRaceExecutor keeps a started Turn in the running state until the
// test releases it, so CompleteTask really races an active Turn rather than a
// Turn that already finished inside the 75ms stub window.
type blockingRaceExecutor struct {
	mu      sync.Mutex
	release chan struct{}
}

func (e *blockingRaceExecutor) arm() chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.release = make(chan struct{})
	return e.release
}

func (e *blockingRaceExecutor) Execute(ctx context.Context, request ExecutionRequest, _ func(ExecutionEvent)) (ExecutionResult, error) {
	e.mu.Lock()
	release := e.release
	e.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ExecutionResult{}, ctx.Err()
		}
	}
	session := request.ProviderSessionID
	if session == "" {
		session = "race-session"
	}
	return ExecutionResult{Reply: "race reply", ProviderSessionID: session}, nil
}

func waitForNoActiveTurn(t *testing.T, database *store.Store, taskID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := database.ActiveTurn(context.Background(), taskID)
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s still has an active turn", taskID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForRoundSettled waits for the Turn's post-execution chain to finish.
// afterTurnTerminal marks the Turn terminal, then calls scheduleAgent and
// settleRound; the Round only becomes terminal at the very end of that chain.
// Waiting on the Turn alone lets the next iteration enqueue into a Round that
// the previous settlement is still about to close.
func waitForRoundSettled(t *testing.T, database *store.Store, roundID string) domain.TaskRound {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		round, err := database.Round(context.Background(), roundID)
		if err != nil {
			t.Fatal(err)
		}
		if round.Status.Terminal() {
			return round
		}
		if time.Now().After(deadline) {
			t.Fatalf("round %s did not settle: status=%s", roundID, round.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// closeUnprocessedRound finishes the Round for a message the scheduler did not
// turn into a Turn. The scheduler may already have closed it (the round-terminal
// no-op path), so a terminal Round is accepted as-is.
func closeUnprocessedRound(t *testing.T, database *store.Store, roundID string) {
	t.Helper()
	ctx := context.Background()
	round, err := database.Round(ctx, roundID)
	if err != nil {
		t.Fatal(err)
	}
	if round.Status.Terminal() {
		return
	}
	if err := database.CancelPendingInboxForRound(ctx, round.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	previous := round.Status
	round.Status = domain.RoundCompleted
	round.FinishedAt = time.Now().UTC()
	if err := database.UpdateRound(ctx, round, previous); err != nil {
		t.Fatal(err)
	}
}

// assertInboxNotPending proves the completed Task did not leave the message
// queued for a later scheduler to pick up behind the test's back.
func assertInboxNotPending(t *testing.T, database *store.Store, taskID, agentID, inboxID string) {
	t.Helper()
	items, err := database.PendingAgentInbox(context.Background(), taskID, agentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == inboxID {
			t.Fatalf("inbox item %s is still pending after the round settled", inboxID)
		}
	}
}

func TestCompleteTaskRefusesActiveTurn(t *testing.T) {
	executor := &blockingRaceExecutor{}
	fixture := newPetTaskFixtureWithExecutor(t, executor)
	fixture.createPetDraft(t, "complete refuses active turn")
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: petTaskID, Role: "user", Sender: "owner",
		Content: "active", Source: "voice", ClientRequestID: "active-request-1", CreatedAt: now,
	}
	if _, _, _, _, _, _, err := fixture.database.EnqueueLocalOwnerMessage(fixture.ctx, message, "main", nil); err != nil {
		t.Fatal(err)
	}
	release := executor.arm()
	defer func() {
		close(release)
		waitForNoActiveTurn(t, fixture.database, petTaskID)
	}()
	if _, started, err := fixture.service.scheduleAgent(fixture.ctx, petTaskID, "main"); err != nil || !started {
		t.Fatalf("schedule started=%t err=%v", started, err)
	}
	if err := fixture.service.CompleteTask(fixture.ctx, petTaskID); !errors.Is(err, store.ErrActiveTurn) {
		t.Fatalf("CompleteTask err=%v, want ErrActiveTurn", err)
	}
	task, err := fixture.database.Task(fixture.ctx, petTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status == domain.TaskCompleted {
		t.Fatal("task was completed while a Turn was active")
	}
}

func TestCompleteTaskAndScheduleAgentRace(t *testing.T) {
	const iterations = 10
	executor := &blockingRaceExecutor{}
	fixture := newPetTaskFixtureWithExecutor(t, executor)
	fixture.createPetDraft(t, "complete vs schedule race")
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	ctx := fixture.ctx
	for iteration := 0; iteration < iterations; iteration++ {
		waitForNoActiveTurn(t, fixture.database, petTaskID)
		task := fixture.petTask(t)
		if task.Status == domain.TaskCompleted {
			if err := fixture.database.UpdateTaskStatus(ctx, petTaskID, domain.TaskCompleted, domain.TaskWaitingUser, time.Now().UTC().Format(time.RFC3339Nano), ""); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Now().UTC()
		message := domain.Message{
			ID: domain.NewID("message"), TaskID: petTaskID, Role: "user", Sender: "owner",
			Content: fmt.Sprintf("race-%d", iteration), Source: "voice",
			ClientRequestID: fmt.Sprintf("race-request-%d", iteration), CreatedAt: now,
		}
		round, inbox, _, _, _, _, err := fixture.database.EnqueueLocalOwnerMessage(ctx, message, "main", nil)
		if err != nil {
			t.Fatal(err)
		}
		beforeTurns, err := fixture.database.TaskTurnCount(ctx, petTaskID)
		if err != nil {
			t.Fatal(err)
		}
		release := executor.arm()
		start := make(chan struct{})
		var wait sync.WaitGroup
		var completeErr, scheduleErr error
		var started bool
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			completeErr = fixture.service.CompleteTask(ctx, petTaskID)
		}()
		go func() {
			defer wait.Done()
			<-start
			_, started, scheduleErr = fixture.service.scheduleAgent(ctx, petTaskID, "main")
		}()
		close(start)
		wait.Wait()
		afterTurns, err := fixture.database.TaskTurnCount(ctx, petTaskID)
		if err != nil {
			t.Fatal(err)
		}

		switch {
		case scheduleErr == nil && started:
			if !errors.Is(completeErr, store.ErrActiveTurn) {
				t.Fatalf("iteration %d: claim won but complete err=%v", iteration, completeErr)
			}
			if afterTurns != beforeTurns+1 {
				t.Fatalf("iteration %d: turns %d -> %d", iteration, beforeTurns, afterTurns)
			}
			close(release)
			// Wait for the whole settlement, not only the Turn status: the Round
			// closes after afterTurnTerminal has already run its own scheduler.
			settled := waitForRoundSettled(t, fixture.database, round.ID)
			if settled.Status != domain.RoundCompleted {
				t.Fatalf("iteration %d: round settled as %s", iteration, settled.Status)
			}
			waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
			reloaded, err := fixture.database.Task(ctx, petTaskID)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Status == domain.TaskCompleted {
				t.Fatalf("iteration %d: completed task with a Turn", iteration)
			}
		case completeErr == nil && !started &&
			(errors.Is(scheduleErr, store.ErrTaskNotRunnable) || scheduleErr == nil):
			// CompleteTask won. The scheduler can observe that either as a refused
			// claim (ErrTaskNotRunnable) or as a no-op when the Round it read was
			// already closed around the message. Both are the same terminal state:
			// the Task is completed, no Turn was started for this message, and the
			// message must not stay pending for a later scheduler.
			if afterTurns != beforeTurns {
				t.Fatalf("iteration %d: turns %d -> %d after complete won", iteration, beforeTurns, afterTurns)
			}
			close(release)
			closeUnprocessedRound(t, fixture.database, round.ID)
			assertInboxNotPending(t, fixture.database, petTaskID, "main", inbox.ID)
			waitForNoActiveTurn(t, fixture.database, petTaskID)
			reloaded, err := fixture.database.Task(ctx, petTaskID)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Status != domain.TaskCompleted {
				t.Fatalf("iteration %d: complete won but status=%s", iteration, reloaded.Status)
			}
			if err := fixture.database.UpdateTaskStatus(ctx, petTaskID, domain.TaskCompleted, domain.TaskWaitingUser, time.Now().UTC().Format(time.RFC3339Nano), ""); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("iteration %d: schedule err=%v started=%t complete err=%v", iteration, scheduleErr, started, completeErr)
		}
	}
}
