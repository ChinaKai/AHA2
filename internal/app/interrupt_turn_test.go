package app

import (
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// createOrphanTurn stores an active Round and Main Turn that no goroutine of
// the Service owns, like a turn left behind by an abandoned run.
func (f *petTaskFixture) createOrphanTurn(t *testing.T, status domain.TurnStatus) (domain.Task, domain.Turn) {
	t.Helper()
	task := f.createPetDraft(t, "orphan turn")
	now := f.service.now().UTC()
	for _, transition := range [][2]domain.TaskStatus{
		{domain.TaskDraft, domain.TaskPreparing},
		{domain.TaskPreparing, domain.TaskActive},
	} {
		if err := f.database.UpdateTaskStatus(f.ctx, task.ID, transition[0], transition[1], timeString(now), ""); err != nil {
			t.Fatal(err)
		}
	}
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "stuck", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: status, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		Attempt: 1, Required: true, QueuedAt: now, PreparedAt: now,
	}
	if _, _, err := f.database.CreateMessageRoundAndTurn(f.ctx, message, round, turn); err != nil {
		t.Fatal(err)
	}
	stored, err := f.database.Turn(f.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f.petTask(t), stored
}

func (f *petTaskFixture) interruptedItems(t *testing.T, taskID, turnID string) int {
	t.Helper()
	page, err := f.database.ConversationPage(f.ctx, taskID, 0, 0, 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range page.Items {
		if item.TurnID == turnID && item.Kind == "turn_interrupted" {
			count++
		}
	}
	return count
}

func TestInterruptTurnWithoutCancelHandleSettlesOrphanTurn(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task, turn := fixture.createOrphanTurn(t, domain.TurnPreparing)
	if err := fixture.service.CompleteTask(fixture.ctx, task.ID); err == nil {
		t.Fatal("CompleteTask succeeded while the orphan turn was active")
	}

	if err := fixture.service.InterruptTurn(fixture.ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.database.Turn(fixture.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.TurnInterrupted || stored.FinishedAt.IsZero() {
		t.Fatalf("turn status=%s finished_at=%v", stored.Status, stored.FinishedAt)
	}
	if _, err := fixture.database.ActiveTurn(fixture.ctx, task.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ActiveTurn err=%v, want sql.ErrNoRows", err)
	}
	round, err := fixture.database.Round(fixture.ctx, turn.RoundID)
	if err != nil {
		t.Fatal(err)
	}
	if round.Status != domain.RoundInterrupted {
		t.Fatalf("round status=%s", round.Status)
	}
	if err := fixture.service.CompleteTask(fixture.ctx, task.ID); err != nil {
		t.Fatalf("CompleteTask after interrupt: %v", err)
	}
	if completed := fixture.petTask(t); completed.Status != domain.TaskCompleted {
		t.Fatalf("task status=%s", completed.Status)
	}
	// A repeated interrupt on the now terminal turn stays a no-op.
	if err := fixture.service.InterruptTurn(fixture.ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	if count := fixture.interruptedItems(t, task.ID, turn.ID); count != 1 {
		t.Fatalf("turn_interrupted items=%d", count)
	}
}

func TestInterruptTurnWithCancelHandleOnlyCancels(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task, turn := fixture.createOrphanTurn(t, domain.TurnRunning)
	cancelled := 0
	fixture.service.mu.Lock()
	fixture.service.cancels[turn.ID] = func() { cancelled++ }
	fixture.service.mu.Unlock()
	t.Cleanup(func() {
		fixture.service.mu.Lock()
		delete(fixture.service.cancels, turn.ID)
		fixture.service.mu.Unlock()
	})

	if err := fixture.service.InterruptTurn(fixture.ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 {
		t.Fatalf("cancel calls=%d", cancelled)
	}
	stored, err := fixture.database.Turn(fixture.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.TurnRunning || !stored.FinishedAt.IsZero() {
		t.Fatalf("turn rewritten before the run settled: status=%s finished_at=%v", stored.Status, stored.FinishedAt)
	}
	if _, err := fixture.database.ActiveTurn(fixture.ctx, task.ID); err != nil {
		t.Fatalf("ActiveTurn err=%v", err)
	}
	if count := fixture.interruptedItems(t, task.ID, turn.ID); count != 0 {
		t.Fatalf("turn_interrupted items=%d", count)
	}
}

func TestInterruptTurnLeavesTerminalTurnUntouched(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task, turn := fixture.createOrphanTurn(t, domain.TurnRunning)
	turn.Status, turn.Result = domain.TurnSucceeded, "done"
	if err := fixture.database.UpdateTurn(fixture.ctx, turn, domain.TurnRunning); err != nil {
		t.Fatal(err)
	}

	if err := fixture.service.InterruptTurn(fixture.ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.database.Turn(fixture.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.TurnSucceeded || stored.Result != "done" {
		t.Fatalf("terminal turn changed: status=%s result=%q", stored.Status, stored.Result)
	}
	if count := fixture.interruptedItems(t, task.ID, turn.ID); count != 0 {
		t.Fatalf("turn_interrupted items=%d", count)
	}
}

func TestInterruptTurnConcurrentWithCompletionKeepsOneTerminalState(t *testing.T) {
	for iteration := 0; iteration < 3; iteration++ {
		fixture := newPetTaskFixture(t)
		task, turn := fixture.createOrphanTurn(t, domain.TurnRunning)
		var wait sync.WaitGroup
		errs := make(chan error, 5)
		wait.Add(5)
		go func() {
			defer wait.Done()
			succeeded := turn
			succeeded.Status, succeeded.Result, succeeded.FinishedAt = domain.TurnSucceeded, "done", fixture.service.now().UTC()
			// Losing the compare-and-set to an interrupt is expected here.
			_ = fixture.database.UpdateTurn(fixture.ctx, succeeded, domain.TurnRunning)
		}()
		for range 4 {
			go func() {
				defer wait.Done()
				errs <- fixture.service.InterruptTurn(fixture.ctx, turn.ID)
			}()
		}
		wait.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("InterruptTurn: %v", err)
			}
		}
		stored, err := fixture.database.Turn(fixture.ctx, turn.ID)
		if err != nil {
			t.Fatal(err)
		}
		count := fixture.interruptedItems(t, task.ID, turn.ID)
		switch stored.Status {
		case domain.TurnInterrupted:
			if count != 1 || stored.Result != "" {
				t.Fatalf("interrupted turn items=%d result=%q", count, stored.Result)
			}
		case domain.TurnSucceeded:
			if count != 0 || stored.Result != "done" {
				t.Fatalf("succeeded turn items=%d result=%q", count, stored.Result)
			}
		default:
			t.Fatalf("turn status=%s", stored.Status)
		}
	}
}
