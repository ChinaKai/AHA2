package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestReconcilePendingCollaborationAssignment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		childStatus domain.TurnStatus
		extraMember bool
		wantAction  string
		wantMember  string
		wantBatch   string
	}{
		{
			name: "terminal child turn completes the batch", childStatus: domain.TurnInterrupted,
			wantAction: CollaborationRecoveryReclaimedTerminal,
			wantMember: domain.CollaborationMemberInterrupted,
			wantBatch:  domain.CollaborationBatchComplete,
		},
		{
			name:        "terminal child turn keeps batch pending for an unfinished member",
			childStatus: domain.TurnInterrupted, extraMember: true,
			wantAction: CollaborationRecoveryReclaimedTerminal,
			wantMember: domain.CollaborationMemberInterrupted,
			wantBatch:  domain.CollaborationBatchPending,
		},
		{
			name: "active child turn is reused", childStatus: domain.TurnRunning,
			wantAction: CollaborationRecoveryReusedActive,
			wantMember: domain.CollaborationMemberRunning,
			wantBatch:  domain.CollaborationBatchPending,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			taskID, batchID, inboxID, childTurnID, turnsBefore := createCollaborationReconciliationFixture(
				t, database, test.childStatus, test.extraMember,
			)
			recoveries, err := database.ReconcilePendingCollaborationAssignments(
				ctx, taskID, "sub-001", time.Now().UTC(),
			)
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if len(recoveries) != 1 {
				t.Fatalf("recoveries=%#v", recoveries)
			}
			recovery := recoveries[0]
			if recovery.InboxID != inboxID || recovery.BatchID != batchID ||
				recovery.ChildTurnID != childTurnID || recovery.Action != test.wantAction {
				t.Fatalf("recovery=%#v", recovery)
			}
			var inboxStatus, memberStatus, batchStatus string
			if err := database.db.QueryRowContext(ctx,
				`SELECT status FROM agent_inbox WHERE id=?`, inboxID,
			).Scan(&inboxStatus); err != nil {
				t.Fatal(err)
			}
			if err := database.db.QueryRowContext(ctx,
				`SELECT status FROM collaboration_members WHERE batch_id=? AND agent_id='sub-001'`, batchID,
			).Scan(&memberStatus); err != nil {
				t.Fatal(err)
			}
			if err := database.db.QueryRowContext(ctx,
				`SELECT status FROM collaboration_batches WHERE id=?`, batchID,
			).Scan(&batchStatus); err != nil {
				t.Fatal(err)
			}
			if inboxStatus != "processed" || memberStatus != test.wantMember || batchStatus != test.wantBatch {
				t.Fatalf("inbox=%q member=%q batch=%q", inboxStatus, memberStatus, batchStatus)
			}
			turnsAfter, err := database.TaskTurnCount(ctx, taskID)
			if err != nil {
				t.Fatal(err)
			}
			if turnsAfter != turnsBefore {
				t.Fatalf("reconciliation created a turn: before=%d after=%d", turnsBefore, turnsAfter)
			}
			if test.childStatus.Terminal() {
				var events, conversations int
				if err := database.db.QueryRowContext(ctx,
					`SELECT COUNT(*) FROM collaboration_events WHERE batch_id=? AND child_turn_id=?`,
					batchID, childTurnID,
				).Scan(&events); err != nil {
					t.Fatal(err)
				}
				if err := database.db.QueryRowContext(ctx,
					`SELECT COUNT(*) FROM conversation_items WHERE id=?`,
					domain.CollaborationResultConversationID(childTurnID),
				).Scan(&conversations); err != nil {
					t.Fatal(err)
				}
				if events != 1 || conversations != 1 {
					t.Fatalf("collaboration events=%d conversations=%d", events, conversations)
				}
			}
		})
	}
}

func createCollaborationReconciliationFixture(
	t *testing.T,
	database *Store,
	childStatus domain.TurnStatus,
	extraMember bool,
) (taskID, batchID, inboxID, childTurnID string, turns int) {
	t.Helper()
	ctx := context.Background()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "recover collaboration", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	parent := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnInterrupted,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InboxBatchID: "inbox-batch-parent",
		Attempt: 1, Required: true, QueuedAt: now, FinishedAt: now,
		Error: "AHA service restarted during this turn",
	}
	createdRound, createdParent, err := database.CreateMessageRoundAndTurn(ctx, message, round, parent)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.UpsertTaskAgent(ctx, domain.TaskAgent{
		TaskID: task.ID, AgentID: "sub-001", Role: "sub", Status: "idle", Title: "Sub",
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	child := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: createdRound.ID, AgentID: "sub-001",
		Sequence: createdParent.Sequence + 1, ParentTurnID: createdParent.ID,
		InputMessageID: message.ID, Status: childStatus,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InboxBatchID: "inbox-batch-child",
		Attempt: 1, Generation: 1, Required: true, QueuedAt: now,
		Error: "AHA service restarted during this turn",
	}
	if childStatus.Terminal() {
		child.FinishedAt = now
	}
	if err := database.CreateTurn(ctx, child); err != nil {
		t.Fatal(err)
	}
	batchID = domain.NewID("collaboration_batch")
	members := []domain.CollaborationMember{{
		BatchID: batchID, AgentID: "sub-001", Required: true,
		Status: domain.CollaborationMemberQueued, CreatedAt: now, UpdatedAt: now,
	}}
	if extraMember {
		members = append(members, domain.CollaborationMember{
			BatchID: batchID, AgentID: "sub-002", Required: true,
			Status: domain.CollaborationMemberQueued, CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := database.CreateCollaborationBatch(ctx, domain.CollaborationBatch{
		ID: batchID, TaskID: task.ID, RoundID: createdRound.ID, ParentTurnID: createdParent.ID,
		Status: domain.CollaborationBatchPending, CreatedAt: now, UpdatedAt: now,
	}, members); err != nil {
		t.Fatal(err)
	}
	if err := database.LinkCollaborationMemberTurn(ctx, batchID, "sub-001", child.ID, now); err != nil {
		t.Fatal(err)
	}
	inboxID = domain.NewID("inbox")
	if _, err := database.EnqueueAgentRoute(ctx, domain.AgentInboxItem{
		ID: inboxID, TaskID: task.ID, RoundID: createdRound.ID, TargetAgentID: "sub-001",
		SourceAgentID: "main", SourceKind: "assignment", SourceTurnID: createdParent.ID,
		Content: "implement the interrupted assignment",
		Payload: map[string]any{
			"collaboration_batch_id": batchID, "parent_turn_id": createdParent.ID, "required": true,
		},
		Status: "pending", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	turns, err = database.TaskTurnCount(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return task.ID, batchID, inboxID, child.ID, turns
}

// Historical data and the crash window between Inbox processing and
// routeAgentOutcome leave a terminal child Turn without a collaboration event.
// Recovery must backfill both the event and the Web conversation item, and a
// repeated recovery must not duplicate them.
func TestReconcileInterruptedCollaborationBackfillsMissingEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	_, batchID, inboxID, childTurnID, _ := createCollaborationReconciliationFixture(
		t, database, domain.TurnInterrupted, false,
	)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE agent_inbox SET status='processed',processed_at=? WHERE id=?`, []any{timeString(now), inboxID}},
		{`UPDATE collaboration_members SET status='interrupted',updated_at=? WHERE batch_id=? AND agent_id='sub-001'`, []any{timeString(now), batchID}},
		{`UPDATE collaboration_batches SET status='complete',updated_at=? WHERE id=?`, []any{timeString(now), batchID}},
	} {
		if _, err := database.db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	recoveries, err := database.ReconcileInterruptedCollaboration(ctx, now.Add(time.Second))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(recoveries) != 1 || recoveries[0].BatchID != batchID ||
		recoveries[0].AgentID != "sub-001" || recoveries[0].ChildTurnID != childTurnID ||
		recoveries[0].ChildStatus != string(domain.TurnInterrupted) ||
		recoveries[0].Action != CollaborationRecoveryEventBackfilled {
		t.Fatalf("recoveries=%#v", recoveries)
	}
	assertBackfilledResultRows(t, database, ctx, batchID, childTurnID, 1)
	var batchStatus, memberStatus string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM collaboration_batches WHERE id=?`, batchID).Scan(&batchStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx,
		`SELECT status FROM collaboration_members WHERE batch_id=? AND agent_id='sub-001'`, batchID,
	).Scan(&memberStatus); err != nil {
		t.Fatal(err)
	}
	if batchStatus != domain.CollaborationBatchComplete || memberStatus != domain.CollaborationMemberInterrupted {
		t.Fatalf("batch=%q member=%q", batchStatus, memberStatus)
	}
	recoveries, err = database.ReconcileInterruptedCollaboration(ctx, now.Add(2*time.Second))
	if err != nil || len(recoveries) != 0 {
		t.Fatalf("repeated reconcile=%#v err=%v", recoveries, err)
	}
	assertBackfilledResultRows(t, database, ctx, batchID, childTurnID, 1)
}

// Recovery must leave fallback and consumed delivery states untouched while
// still recording a missing result. Consumed batches are not scanned at all.
func TestReconcileInterruptedCollaborationPreservesTerminalBatchStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	_, batchID, _, childTurnID, _ := createCollaborationReconciliationFixture(
		t, database, domain.TurnInterrupted, false,
	)
	if _, err := database.db.ExecContext(ctx,
		`UPDATE collaboration_batches SET status=?,updated_at=? WHERE id=?`,
		domain.CollaborationBatchFallback, timeString(now), batchID,
	); err != nil {
		t.Fatal(err)
	}
	recoveries, err := database.ReconcileInterruptedCollaboration(ctx, now.Add(time.Second))
	if err != nil || len(recoveries) != 1 {
		t.Fatalf("fallback reconcile=%#v err=%v", recoveries, err)
	}
	assertBackfilledResultRows(t, database, ctx, batchID, childTurnID, 1)
	var status string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM collaboration_batches WHERE id=?`, batchID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != domain.CollaborationBatchFallback {
		t.Fatalf("fallback batch was reopened as %q", status)
	}

	if _, err := database.db.ExecContext(ctx,
		`UPDATE collaboration_batches SET status=?,updated_at=? WHERE id=?`,
		domain.CollaborationBatchConsumed, timeString(now), batchID,
	); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM collaboration_events WHERE batch_id=? AND child_turn_id=?`, []any{batchID, childTurnID}},
		{`DELETE FROM conversation_items WHERE id=?`, []any{domain.CollaborationResultConversationID(childTurnID)}},
	} {
		if _, err := database.db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	recoveries, err = database.ReconcileInterruptedCollaboration(ctx, now.Add(2*time.Second))
	if err != nil || len(recoveries) != 0 {
		t.Fatalf("consumed reconcile=%#v err=%v", recoveries, err)
	}
	assertBackfilledResultRows(t, database, ctx, batchID, childTurnID, 0)
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM collaboration_batches WHERE id=?`, batchID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != domain.CollaborationBatchConsumed {
		t.Fatalf("consumed batch was reopened as %q", status)
	}
}

func assertBackfilledResultRows(t *testing.T, database *Store, ctx context.Context, batchID, childTurnID string, want int) {
	t.Helper()
	var events, conversations int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM collaboration_events WHERE batch_id=? AND child_turn_id=?`,
		batchID, childTurnID,
	).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM conversation_items
		WHERE id=? AND kind='collaboration_error_routed' AND category='error' AND stream_agent_id='main'`,
		domain.CollaborationResultConversationID(childTurnID),
	).Scan(&conversations); err != nil {
		t.Fatal(err)
	}
	if events != want || conversations != want {
		t.Fatalf("events=%d conversations=%d want %d", events, conversations, want)
	}
}

func TestCollaborationBatchCursorAndCompletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	batch := domain.CollaborationBatch{
		ID: "batch-1", TaskID: "task-1", RoundID: "round-1", ParentTurnID: "turn-main",
		IntegrationInstructions: "combine results", Status: domain.CollaborationBatchPending,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateCollaborationBatch(ctx, batch, []domain.CollaborationMember{
		{BatchID: batch.ID, AgentID: "sub-001", Required: true, Status: domain.CollaborationMemberQueued, CreatedAt: now, UpdatedAt: now},
		{BatchID: batch.ID, AgentID: "sub-002", Required: true, Status: domain.CollaborationMemberQueued, CreatedAt: now, UpdatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	if count, err := database.OpenCollaborationBatchCountForRound(ctx, batch.RoundID); err != nil || count != 1 {
		t.Fatalf("open collaboration count=%d err=%v", count, err)
	}
	if ids, err := database.OpenCollaborationBatchIDs(ctx); err != nil || len(ids) != 1 || ids[0] != batch.ID {
		t.Fatalf("open collaboration IDs=%#v err=%v", ids, err)
	}
	if err := database.LinkCollaborationMemberTurn(ctx, batch.ID, "sub-001", "turn-sub-1", now); err != nil {
		t.Fatal(err)
	}
	if err := database.LinkCollaborationMemberTurn(ctx, batch.ID, "sub-002", "turn-sub-2", now); err != nil {
		t.Fatal(err)
	}
	members, err := database.CollaborationMembers(ctx, batch.ID)
	if err != nil || len(members) != 2 || members[0].Status != domain.CollaborationMemberRunning {
		t.Fatalf("collaboration members=%#v err=%v", members, err)
	}
	if _, inserted, err := database.RecordCollaborationResult(ctx, domain.Turn{
		ID: "turn-sub-1", TaskID: "task-1", AgentID: "sub-001",
	}, string(domain.TurnSucceeded), "first result"); err != nil || !inserted {
		t.Fatalf("record first result inserted=%v err=%v", inserted, err)
	}
	progress, err := database.CollaborationProgress(ctx, batch.ID, 0)
	if err != nil || progress.Complete || len(progress.Results) != 1 || len(progress.PendingAgentIDs) != 1 {
		t.Fatalf("partial progress=%#v err=%v", progress, err)
	}
	cursor := progress.NextCursor
	if _, inserted, err := database.RecordCollaborationResult(ctx, domain.Turn{
		ID: "turn-sub-2", TaskID: "task-1", AgentID: "sub-002",
	}, string(domain.TurnSucceeded), "second result"); err != nil || !inserted {
		t.Fatalf("record second result inserted=%v err=%v", inserted, err)
	}
	if _, inserted, err := database.RecordCollaborationResult(ctx, domain.Turn{
		ID: "turn-sub-2", TaskID: "task-1", AgentID: "sub-002",
	}, string(domain.TurnSucceeded), "second result"); err != nil || inserted {
		t.Fatalf("duplicate result inserted=%v err=%v", inserted, err)
	}
	progress, err = database.CollaborationProgress(ctx, batch.ID, cursor)
	if err != nil || !progress.Complete || len(progress.Results) != 1 || progress.Results[0].AgentID != "sub-002" {
		t.Fatalf("complete progress=%#v err=%v", progress, err)
	}
	if err := database.MarkCollaborationDelivered(ctx, batch.ID, progress.NextCursor, domain.CollaborationBatchComplete, now); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkCollaborationConsumed(ctx, batch.ID, now); err != nil {
		t.Fatal(err)
	}
	if count, err := database.OpenCollaborationBatchCountForRound(ctx, batch.RoundID); err != nil || count != 0 {
		t.Fatalf("consumed collaboration count=%d err=%v", count, err)
	}
	if ids, err := database.OpenCollaborationBatchIDs(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("consumed collaboration IDs=%#v err=%v", ids, err)
	}
	stored, err := database.CollaborationBatch(ctx, batch.ID)
	if err != nil || stored.Status != domain.CollaborationBatchConsumed || stored.DeliveredCursor != progress.NextCursor {
		t.Fatalf("consumed batch=%#v err=%v", stored, err)
	}
}
