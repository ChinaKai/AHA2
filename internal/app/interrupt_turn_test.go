package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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

// blockingSecretResolver stalls the preparation phase exactly where runTurn
// reads the workspace SSH credential. The turn status is already preparing at
// that point, which makes the interruption deterministic instead of relying on
// a scheduling race.
type blockingSecretResolver struct {
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (r *blockingSecretResolver) Get(string) (string, bool) {
	r.once.Do(func() { close(r.entered) })
	<-r.release
	return "", false
}

func (r *blockingSecretResolver) PutMany(map[string]string) error { return nil }
func (r *blockingSecretResolver) DeleteMany([]string) error       { return nil }

// blockingSuccessExecutor ignores its context on purpose: it models a backend
// that already produced a successful result while the owner interrupted the
// turn, so the success must not be overwritten by the interrupt.
type blockingSuccessExecutor struct {
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (e *blockingSuccessExecutor) Execute(_ context.Context, _ ExecutionRequest, emit func(ExecutionEvent)) (ExecutionResult, error) {
	e.once.Do(func() { close(e.entered) })
	<-e.release
	emit(ExecutionEvent{Type: "agent_message", Data: map[string]any{"text": "done"}})
	return ExecutionResult{Reply: "done", ProviderSessionID: "interrupted-then-succeeded"}, nil
}

// blockPreparation points the workspace at a secret ref and replaces the
// secret resolver with one that stalls runTurn right after it enters preparing.
func (f *petTaskFixture) blockPreparation(t *testing.T) (entered, release chan struct{}) {
	t.Helper()
	workspace, err := f.database.Workspace(f.ctx, petWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	workspace.SSHCredentialRef = "workspace/" + domain.NewID("interrupt")
	workspace.UpdatedAt = f.service.now().UTC()
	if err := f.database.UpdateWorkspaceConfig(f.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	entered, release = make(chan struct{}), make(chan struct{})
	f.service.secrets = &blockingSecretResolver{entered: entered, release: release}
	return entered, release
}

func (f *petTaskFixture) assertTurnSettledTerminally(t *testing.T, taskID, turnID string, want domain.TurnStatus) {
	t.Helper()
	waitForTurn(t, f.database, turnID, want)
	stored, err := f.database.Turn(f.ctx, turnID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.FinishedAt.IsZero() {
		t.Fatalf("turn %s is %s but has no finished_at", turnID, stored.Status)
	}
	if _, err := f.database.ActiveTurn(f.ctx, taskID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ActiveTurn err=%v, want sql.ErrNoRows", err)
	}
	wantItems := 0
	if want == domain.TurnInterrupted {
		wantItems = 1
	}
	if count := f.interruptedItems(t, taskID, turnID); count != wantItems {
		t.Fatalf("turn_interrupted items=%d, want %d", count, wantItems)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.service.mu.Lock()
		_, hasHandle := f.service.cancels[turnID]
		f.service.mu.Unlock()
		if !hasHandle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("cancel handle remained registered after the terminal write")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestInterruptTurnBeforePreparationSettlesQueuedTurn(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task, turn := fixture.createOrphanTurn(t, domain.TurnQueued)
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.service.mu.Lock()
	fixture.service.cancels[turn.ID] = cancel
	fixture.service.mu.Unlock()

	// InterruptTurn owns the cancel handle, so it only cancels and returns; the
	// turn goroutine must still write the interrupted terminal state itself.
	if err := fixture.service.InterruptTurn(fixture.ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fixture.service.runTurn(runCtx, turn.ID)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runTurn did not return after cancel")
	}
	fixture.assertTurnSettledTerminally(t, task.ID, turn.ID, domain.TurnInterrupted)
}

func TestInterruptTurnDuringPreparationSettlesPreparingTurn(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task, turn := fixture.createOrphanTurn(t, domain.TurnQueued)
	entered, release := fixture.blockPreparation(t)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.service.mu.Lock()
	fixture.service.cancels[turn.ID] = cancel
	fixture.service.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fixture.service.runTurn(runCtx, turn.ID)
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("runTurn never reached the preparation secret lookup")
	}
	stored, err := fixture.database.Turn(fixture.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.TurnPreparing {
		t.Fatalf("interrupt race started at status=%s, want preparing", stored.Status)
	}

	if err := fixture.service.InterruptTurn(fixture.ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runTurn did not return after cancel")
	}
	fixture.assertTurnSettledTerminally(t, task.ID, turn.ID, domain.TurnInterrupted)
}

func TestInterruptTurnWhileRunningDoesNotOverwriteSuccessfulResult(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "旧的宠物需求")
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	fixture.service.executor = &blockingSuccessExecutor{entered: entered, release: release}

	turn, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "主人发来一条消息")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("turn never reached the executor")
	}
	// A handled running turn only gets canceled; InterruptTurn must not rewrite
	// the stored status before the executor settles.
	if err := fixture.service.InterruptTurn(fixture.ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.database.Turn(fixture.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.TurnRunning {
		t.Fatalf("InterruptTurn rewrote running turn to %s", stored.Status)
	}
	close(release)
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
	fixture.assertTurnSettledTerminally(t, petTaskID, turn.ID, domain.TurnSucceeded)
	stored, err = fixture.database.Turn(fixture.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Result != "done" {
		t.Fatalf("successful result was lost: %q", stored.Result)
	}
}

// A SIGTERM cancels the shared run context before Shutdown can set closing.
// The canceled preparation must stay non-terminal so RecoverInterrupted can
// requeue the claimed inbox instead of the turn's terminal settlement silently
// consuming the owner messages queued behind it.
func TestShutdownCancelWindowLeavesQueuedOwnerMessageRecoverable(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task, turn := fixture.createOrphanTurn(t, domain.TurnQueued)
	if _, err := fixture.service.SubmitMessage(fixture.ctx, task.ID, "queued owner message"); err != nil {
		t.Fatal(err)
	}
	if pending, err := fixture.database.PendingInboxCount(fixture.ctx, task.ID); err != nil || pending != 1 {
		t.Fatalf("queued owner message pending=%d err=%v, want 1", pending, err)
	}
	entered, release := fixture.blockPreparation(t)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	fixture.service.SetRunContext(runCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fixture.service.runTurn(runCtx, turn.ID)
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("runTurn never reached the preparation secret lookup")
	}
	stored, err := fixture.database.Turn(fixture.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.TurnPreparing {
		t.Fatalf("signal window started at status=%s, want preparing", stored.Status)
	}

	// Signal window: the shared run context is canceled while closing is still
	// false, exactly the ordering used by main on SIGTERM.
	cancelRun()
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runTurn did not return after the signal cancellation")
	}
	// Wait for any successor turn the old ordering scheduled.
	fixture.service.runWG.Wait()
	stored, err = fixture.database.Turn(fixture.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status.Terminal() {
		t.Fatalf("shutdown cancellation settled the turn as %s", stored.Status)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := fixture.service.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	recovered, err := fixture.database.RecoverInterrupted(fixture.ctx, fixture.service.now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Turns == 0 {
		t.Fatal("recovery did not settle the canceled preparation turn")
	}
	stored, err = fixture.database.Turn(fixture.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.TurnInterrupted {
		t.Fatalf("recovered turn status=%s, want interrupted", stored.Status)
	}
	pending, err := fixture.database.PendingInboxCount(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("queued owner message pending after recovery=%d, want 1 (message was consumed during shutdown)", pending)
	}
}

// A real user interruption is not a shutdown cancellation: it must still
// settle the turn as interrupted and let its afterTurnTerminal consume the
// queued owner message through a successor turn.
func TestUserInterruptStillSettlesAndConsumesQueuedOwnerMessage(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task, turn := fixture.createOrphanTurn(t, domain.TurnQueued)
	if _, err := fixture.service.SubmitMessage(fixture.ctx, task.ID, "queued owner message"); err != nil {
		t.Fatal(err)
	}
	entered, release := fixture.blockPreparation(t)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	fixture.service.SetRunContext(runCtx)
	turnCtx, cancelTurn := context.WithCancel(runCtx)
	defer cancelTurn()
	fixture.service.mu.Lock()
	fixture.service.cancels[turn.ID] = cancelTurn
	fixture.service.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fixture.service.runTurn(turnCtx, turn.ID)
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("runTurn never reached the preparation secret lookup")
	}
	if err := fixture.service.InterruptTurn(fixture.ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runTurn did not return after the user interruption")
	}
	stored, err := fixture.database.Turn(fixture.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.TurnInterrupted || stored.FinishedAt.IsZero() {
		t.Fatalf("user interrupt left status=%s finished_at=%v", stored.Status, stored.FinishedAt)
	}
	if count := fixture.interruptedItems(t, task.ID, turn.ID); count != 1 {
		t.Fatalf("turn_interrupted items=%d, want 1", count)
	}
	waitForTask(t, fixture.database, task.ID, domain.TaskWaitingUser)
	turns, err := fixture.database.ListTurns(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns after user interruption=%d, want successor turn for the queued message", len(turns))
	}
	if turns[1].Status != domain.TurnSucceeded || !strings.Contains(turns[1].Instruction, "queued owner message") {
		t.Fatalf("queued owner message was not delivered to a successor turn: %#v", turns[1])
	}
	if pending, err := fixture.database.PendingInboxCount(fixture.ctx, task.ID); err != nil || pending != 0 {
		t.Fatalf("pending after user interruption=%d err=%v, want 0", pending, err)
	}
}

func TestInterruptTurnConcurrentWithCompletionKeepsOneTerminalState(t *testing.T) {
	for iteration := 0; iteration < 3; iteration++ {
		fixture := newPetTaskFixture(t)
		task, turn := fixture.createOrphanTurn(t, domain.TurnRunning)
		var wait, ready sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, 5)
		wait.Add(5)
		ready.Add(5)
		go func() {
			defer wait.Done()
			succeeded := turn
			succeeded.Status, succeeded.Result, succeeded.FinishedAt = domain.TurnSucceeded, "done", fixture.service.now().UTC()
			// The barrier releases the completion and all interrupts together so
			// the compare-and-set race is actually exercised.
			ready.Done()
			<-start
			// Losing the compare-and-set to an interrupt is expected here.
			_ = fixture.database.UpdateTurn(fixture.ctx, succeeded, domain.TurnRunning)
		}()
		for range 4 {
			go func() {
				defer wait.Done()
				ready.Done()
				<-start
				errs <- fixture.service.InterruptTurn(fixture.ctx, turn.ID)
			}()
		}
		ready.Wait()
		close(start)
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
