package app

import (
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// createRoundSubTurn adds a sub-Agent Turn to an existing Round, like the
// routing path that lets a sub Agent join the Round that is already running.
func (f *petTaskFixture) createRoundSubTurn(
	t *testing.T, main domain.Turn, agentID string, status domain.TurnStatus,
) domain.Turn {
	t.Helper()
	sub, err := f.database.CreateAgentTurn(f.ctx, domain.Turn{
		ID: domain.NewID("turn"), TaskID: main.TaskID, RoundID: main.RoundID, AgentID: agentID,
		InputMessageID: main.InputMessageID, Status: status,
		RuntimeConfigSnapshotID: main.RuntimeConfigSnapshotID,
		InboxBatchID:            domain.NewID("inbox_batch"), Attempt: 1, Required: true,
		QueuedAt: f.service.now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

// insertRoundInboxItem seeds an agent_inbox row through a separate connection
// so the test can create pending work without creating a Round or Turn.
func insertRoundInboxItem(
	t *testing.T, fixture *petTaskFixture, round domain.TaskRound,
	targetAgentID, sourceAgentID, sourceKind, sourceTurnID, status, batchID, deliveredTurnID string,
) string {
	t.Helper()
	raw, err := sql.Open("sqlite", fixture.databasePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := domain.NewID("inbox")
	if _, err := raw.Exec(`
		INSERT INTO agent_inbox(
			id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
			content,payload_json,status,batch_id,created_at,claimed_at,processed_at,delivered_turn_id,delivered_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, round.TaskID, round.ID, targetAgentID, sourceAgentID, sourceKind, sourceTurnID, "",
		"pending message", "{}", status, batchID, now, "", "", deliveredTurnID, "",
	); err != nil {
		t.Fatal(err)
	}
	return id
}

func readInboxStatus(t *testing.T, fixture *petTaskFixture, inboxID string) string {
	t.Helper()
	raw, err := sql.Open("sqlite", fixture.databasePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var status string
	if err := raw.QueryRow(`SELECT status FROM agent_inbox WHERE id=?`, inboxID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func (f *petTaskFixture) mustRound(t *testing.T, roundID string) domain.TaskRound {
	t.Helper()
	round, err := f.database.Round(f.ctx, roundID)
	if err != nil {
		t.Fatal(err)
	}
	return round
}

// Close a Round directly, leaving its Turns untouched. This reproduces the
// intermediate state a first InterruptRound leaves behind when it closes the
// Round and then fails before settling the Turns.
func closeRoundDirectly(t *testing.T, fixture *petTaskFixture, roundID string) {
	t.Helper()
	current := fixture.mustRound(t, roundID)
	updated := current
	updated.Status = domain.RoundInterrupted
	updated.FinishedAt = fixture.service.now().UTC()
	if err := fixture.database.UpdateRound(fixture.ctx, updated, current.Status); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptRoundSettlesOrphanTurns(t *testing.T) {
	for _, status := range []domain.TurnStatus{domain.TurnQueued, domain.TurnPreparing} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newPetTaskFixture(t)
			_, turn := fixture.createOrphanTurn(t, status)
			if err := fixture.service.InterruptRound(fixture.ctx, turn.RoundID); err != nil {
				t.Fatalf("InterruptRound: %v", err)
			}
			stored, err := fixture.database.Turn(fixture.ctx, turn.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != domain.TurnInterrupted || stored.FinishedAt.IsZero() {
				t.Fatalf("turn status=%s finished_at=%v", stored.Status, stored.FinishedAt)
			}
			if round := fixture.mustRound(t, turn.RoundID); round.Status != domain.RoundInterrupted {
				t.Fatalf("round status=%s, want interrupted", round.Status)
			}
			if task := fixture.petTask(t); task.Status != domain.TaskWaitingUser {
				t.Fatalf("task status=%s, want waiting_user", task.Status)
			}
		})
	}
}

// A repeated interrupt must heal the state a partial first attempt left:
// Round already interrupted, Turn still preparing, Task still active.
func TestInterruptRoundFastPathHealsPartialFailure(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task, turn := fixture.createOrphanTurn(t, domain.TurnPreparing)
	closeRoundDirectly(t, fixture, turn.RoundID)
	if stored := fixture.petTask(t); stored.Status != domain.TaskActive {
		t.Fatalf("fixture task status=%s, want active", stored.Status)
	}

	if err := fixture.service.InterruptRound(fixture.ctx, turn.RoundID); err != nil {
		t.Fatalf("first retry: %v", err)
	}
	stored, err := fixture.database.Turn(fixture.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.TurnInterrupted || stored.FinishedAt.IsZero() {
		t.Fatalf("turn status=%s finished_at=%v, want interrupted", stored.Status, stored.FinishedAt)
	}
	if round := fixture.mustRound(t, turn.RoundID); round.Status != domain.RoundInterrupted {
		t.Fatalf("round status=%s, want interrupted", round.Status)
	}
	if task := fixture.petTask(t); task.Status != domain.TaskWaitingUser {
		t.Fatalf("task status=%s, want waiting_user", task.Status)
	}
	if count := fixture.interruptedItems(t, task.ID, turn.ID); count != 1 {
		t.Fatalf("turn_interrupted items=%d, want 1", count)
	}

	// A second retry is a no-op: it must not duplicate the settlement.
	if err := fixture.service.InterruptRound(fixture.ctx, turn.RoundID); err != nil {
		t.Fatalf("second retry: %v", err)
	}
	if count := fixture.interruptedItems(t, task.ID, turn.ID); count != 1 {
		t.Fatalf("turn_interrupted items after second retry=%d, want 1", count)
	}
	if stored := fixture.petTask(t); stored.Status != domain.TaskWaitingUser {
		t.Fatalf("task status after second retry=%s, want waiting_user", stored.Status)
	}
}

// The terminal fast path must drain a pending item even when no Turn is left
// to trigger the post-interrupt cleanup.
func TestInterruptRoundFastPathCancelsPendingInbox(t *testing.T) {
	fixture := newPetTaskFixture(t)
	_, turn := fixture.createOrphanTurn(t, domain.TurnPreparing)
	round := domain.TaskRound{ID: turn.RoundID, TaskID: turn.TaskID}
	inboxID := insertRoundInboxItem(t, fixture, round, "main", "owner", "owner_message", "", "pending", "", "")
	closeRoundDirectly(t, fixture, turn.RoundID)

	if err := fixture.service.InterruptRound(fixture.ctx, turn.RoundID); err != nil {
		t.Fatal(err)
	}
	if got := readInboxStatus(t, fixture, inboxID); got != "cancelled" {
		t.Fatalf("pending inbox status=%s, want cancelled", got)
	}
}

// A queued/preparing orphan sub Turn has no cancel handle. InterruptRound must
// settle it, and its routed Main result must not survive in the interrupted
// Round and block the next Owner message.
func TestInterruptRoundOrphanSubLeavesNoStaleMainInbox(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task, mainTurn := fixture.createOrphanTurn(t, domain.TurnSucceeded)
	sub := fixture.createRoundSubTurn(t, mainTurn, "sub-001", domain.TurnPreparing)

	if err := fixture.service.InterruptRound(fixture.ctx, mainTurn.RoundID); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.database.Turn(fixture.ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.TurnInterrupted {
		t.Fatalf("sub turn status=%s, want interrupted", stored.Status)
	}
	if pending, err := fixture.database.PendingInboxCount(fixture.ctx, task.ID); err != nil || pending != 0 {
		t.Fatalf("pending after interrupt=%d err=%v, want 0", pending, err)
	}

	next, err := fixture.service.SubmitMessage(fixture.ctx, task.ID, "下一条主人消息")
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == "" {
		t.Fatal("next Owner message did not start a Turn")
	}
}

// The same guarantee holds when the sub Turn is owned by a live goroutine that
// settles after InterruptRound returned: its routed result is cancelled when
// the terminal Round guard runs.
func TestInterruptRoundLiveSubLeavesNoStaleMainInbox(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task, mainTurn := fixture.createOrphanTurn(t, domain.TurnSucceeded)
	sub := fixture.createRoundSubTurn(t, mainTurn, "sub-001", domain.TurnPreparing)

	cancelled := make(chan struct{})
	var once sync.Once
	fixture.service.mu.Lock()
	fixture.service.cancels[sub.ID] = func() { once.Do(func() { close(cancelled) }) }
	fixture.service.mu.Unlock()

	if err := fixture.service.InterruptRound(fixture.ctx, mainTurn.RoundID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("live sub Turn cancel handle was not called")
	}

	// Model the run goroutine settling after it observes the cancellation.
	fixture.service.mu.Lock()
	delete(fixture.service.cancels, sub.ID)
	fixture.service.mu.Unlock()
	stored, err := fixture.database.Turn(fixture.ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !fixture.service.interruptTurn(fixture.ctx, &stored, task) {
		t.Fatal("live sub Turn was not settled after cancellation")
	}
	if pending, err := fixture.database.PendingInboxCount(fixture.ctx, task.ID); err != nil || pending != 0 {
		t.Fatalf("pending after live settlement=%d err=%v, want 0", pending, err)
	}

	next, err := fixture.service.SubmitMessage(fixture.ctx, task.ID, "下一条主人消息")
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == "" {
		t.Fatal("next Owner message did not start a Turn")
	}
}

// afterTurnTerminal requeues claimed_in_turn items. With the Round already
// terminal, the interrupt's final drain must cancel them again instead of
// reviving them as pending work.
func TestInterruptRoundDoesNotReviveClaimedInTurn(t *testing.T) {
	fixture := newPetTaskFixture(t)
	_, turn := fixture.createOrphanTurn(t, domain.TurnPreparing)
	round := domain.TaskRound{ID: turn.RoundID, TaskID: turn.TaskID}
	inboxID := insertRoundInboxItem(
		t, fixture, round, "main", "owner", "owner_message", "", "claimed_in_turn", turn.InboxBatchID, turn.ID,
	)
	if err := fixture.service.InterruptRound(fixture.ctx, turn.RoundID); err != nil {
		t.Fatal(err)
	}
	if got := readInboxStatus(t, fixture, inboxID); got != "cancelled" {
		t.Fatalf("claimed_in_turn item status=%s, want cancelled", got)
	}
}

// A pending message in the same Round must not be scheduled into a new Turn
// while InterruptRound settles a live or orphan Turn.
func TestInterruptRoundDoesNotScheduleFromPendingInbox(t *testing.T) {
	for _, status := range []domain.TurnStatus{domain.TurnQueued, domain.TurnPreparing} {
		t.Run("orphan "+string(status), func(t *testing.T) {
			fixture := newPetTaskFixture(t)
			_, turn := fixture.createOrphanTurn(t, status)
			round := domain.TaskRound{ID: turn.RoundID, TaskID: turn.TaskID}
			inboxID := insertRoundInboxItem(t, fixture, round, "main", "owner", "owner_message", "", "pending", "", "")
			if err := fixture.service.InterruptRound(fixture.ctx, turn.RoundID); err != nil {
				t.Fatal(err)
			}
			if got := readInboxStatus(t, fixture, inboxID); got != "cancelled" {
				t.Fatalf("pending inbox status=%s, want cancelled", got)
			}
			turns, err := fixture.database.TurnsForRound(fixture.ctx, turn.RoundID)
			if err != nil {
				t.Fatal(err)
			}
			if len(turns) != 1 {
				t.Fatalf("turns=%d, want the original Turn only", len(turns))
			}
		})
	}
}

// An interrupt must cancel the Main-result merge timer so it cannot schedule a
// Main Turn after the Round is already interrupted.
func TestInterruptRoundCancelsMainResultMergeTimer(t *testing.T) {
	fixture := newPetTaskFixture(t)
	_, turn := fixture.createOrphanTurn(t, domain.TurnPreparing)
	fixture.service.armMainResultMerge(turn.TaskID, turn.RoundID)
	fixture.service.mergeMu.Lock()
	armed := fixture.service.mergeTimers[turn.TaskID] != nil
	fixture.service.mergeMu.Unlock()
	if !armed {
		t.Fatal("merge timer was not armed")
	}
	if err := fixture.service.InterruptRound(fixture.ctx, turn.RoundID); err != nil {
		t.Fatal(err)
	}
	fixture.service.mergeMu.Lock()
	timer := fixture.service.mergeTimers[turn.TaskID]
	fixture.service.mergeMu.Unlock()
	if timer != nil {
		t.Fatal("main-result merge timer survived the interrupt")
	}
}

// A concurrent settlement that wins the Round CAS makes InterruptRound a no-op
// instead of returning the CAS error.
func TestInterruptRoundCASConcurrentReturnsNil(t *testing.T) {
	for iteration := 0; iteration < 5; iteration++ {
		fixture := newPetTaskFixture(t)
		task, turn := fixture.createOrphanTurn(t, domain.TurnPreparing)
		var wait, ready sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, 8)
		for range 8 {
			wait.Add(1)
			ready.Add(1)
			go func() {
				defer wait.Done()
				ready.Done()
				<-start
				errs <- fixture.service.InterruptRound(fixture.ctx, turn.RoundID)
			}()
		}
		ready.Wait()
		close(start)
		wait.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("InterruptRound: %v", err)
			}
		}
		if round := fixture.mustRound(t, turn.RoundID); round.Status != domain.RoundInterrupted {
			t.Fatalf("round status=%s, want interrupted", round.Status)
		}
		stored, err := fixture.database.Turn(fixture.ctx, turn.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != domain.TurnInterrupted {
			t.Fatalf("turn status=%s, want interrupted", stored.Status)
		}
		if count := fixture.interruptedItems(t, task.ID, turn.ID); count != 1 {
			t.Fatalf("turn_interrupted items=%d, want 1", count)
		}
	}
}

// A routed Main item that lands in an already terminal Round must be cancelled
// by the terminal guard, not left pending.
func TestScheduleMainAfterAgentResultCancelsTerminalRoundInbox(t *testing.T) {
	fixture := newPetTaskFixture(t)
	_, turn := fixture.createOrphanTurn(t, domain.TurnSucceeded)
	round := domain.TaskRound{ID: turn.RoundID, TaskID: turn.TaskID}
	inboxID := insertRoundInboxItem(
		t, fixture, round, "main", "sub-001", "agent_result", turn.ID, "pending", "", "",
	)
	closeRoundDirectly(t, fixture, turn.RoundID)

	fixture.service.scheduleMainAfterAgentResult(fixture.ctx, turn.TaskID, turn.RoundID)
	if got := readInboxStatus(t, fixture, inboxID); got != "cancelled" {
		t.Fatalf("routed inbox status=%s, want cancelled", got)
	}
	fixture.service.mergeMu.Lock()
	timer := fixture.service.mergeTimers[turn.TaskID]
	fixture.service.mergeMu.Unlock()
	if timer != nil {
		t.Fatal("terminal Round armed a merge timer")
	}
}

// A transient Round read failure must not be mistaken for a terminal Round:
// the merge timer stays armed, the routed item stays pending, and a later
// successful read drains it.
func TestScheduleMainAfterAgentResultKeepsTimerOnReadError(t *testing.T) {
	fixture := newPetTaskFixture(t)
	_, turn := fixture.createOrphanTurn(t, domain.TurnSucceeded)
	round := domain.TaskRound{ID: turn.RoundID, TaskID: turn.TaskID}
	inboxID := insertRoundInboxItem(
		t, fixture, round, "main", "sub-001", "agent_result", turn.ID, "pending", "", "",
	)
	closeRoundDirectly(t, fixture, turn.RoundID)

	fixture.service.mergeDelay = 200 * time.Millisecond
	readErr := errors.New("injected round read failure")
	var reads atomic.Int32
	fixture.service.readRoundFailure = func(string) error {
		if reads.Add(1) == 1 {
			return readErr
		}
		return nil
	}

	fixture.service.scheduleMainAfterAgentResult(fixture.ctx, turn.TaskID, turn.RoundID)
	if got := readInboxStatus(t, fixture, inboxID); got != "pending" {
		t.Fatalf("inbox status after read error=%s, want pending (not treated as terminal)", got)
	}
	fixture.service.mergeMu.Lock()
	armed := fixture.service.mergeTimers[turn.TaskID] != nil
	failures := fixture.service.mergeReadFailures[turn.TaskID]
	fixture.service.mergeMu.Unlock()
	if !armed || failures != 1 {
		t.Fatalf("after read error timer=%v failures=%d, want timer armed and 1 failure", armed, failures)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := readInboxStatus(t, fixture, inboxID); got == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("merge timer did not retry the read and drain the terminal Round inbox")
		}
		time.Sleep(10 * time.Millisecond)
	}
	fixture.service.mergeMu.Lock()
	timer := fixture.service.mergeTimers[turn.TaskID]
	failuresAfter := fixture.service.mergeReadFailures[turn.TaskID]
	fixture.service.mergeMu.Unlock()
	if timer != nil || failuresAfter != 0 {
		t.Fatalf("after successful retry timer=%v failures=%d, want cleared", timer, failuresAfter)
	}
}

// retryTurn's optimistic check is not atomic. This simulates InterruptRound
// closing the Round between that check and the insert; the transactional
// re-check must reject the retry instead of attaching it to a closed Round.
func TestRetryTurnRejectsRoundClosedBeforeInsert(t *testing.T) {
	fixture := newPetTaskFixture(t)
	_, mainTurn := fixture.createOrphanTurn(t, domain.TurnRunning)
	sub := fixture.createRoundSubTurn(t, mainTurn, "sub-001", domain.TurnFailed)

	hookRan := false
	fixture.service.retryRoundCheckHook = func(roundID string) {
		hookRan = true
		closeRoundDirectly(t, fixture, roundID)
	}
	if fixture.service.retryTurn(fixture.ctx, sub) {
		t.Fatal("retryTurn created a Turn in a Round closed before the insert")
	}
	if !hookRan {
		t.Fatal("interleaving hook did not run")
	}
	turns, err := fixture.database.TurnsForRound(fixture.ctx, mainTurn.RoundID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns in Round=%d, want the original two only", len(turns))
	}
}
