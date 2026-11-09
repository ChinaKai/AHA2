package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func liveInboxTestTurn(t *testing.T, database *Store) (domain.Task, domain.TaskRound, domain.Turn) {
	t.Helper()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "live message", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnRunning,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		InboxBatchID:            "batch-live", Attempt: 1, Required: true, QueuedAt: now,
	}
	if _, _, err := database.CreateMessageRoundAndTurn(context.Background(), message, round, turn); err != nil {
		t.Fatal(err)
	}
	return task, round, turn
}

func insertLiveInboxItem(t *testing.T, database *Store, task domain.Task, round domain.TaskRound, content string) string {
	t.Helper()
	return insertLiveInboxItemWithSource(t, database, task, round.ID, "owner", "owner_message", "pending", content)
}

func insertLiveInboxItemWithSource(
	t *testing.T, database *Store, task domain.Task, roundID, sourceAgent, sourceKind, status, content string,
) string {
	t.Helper()
	return insertLiveInboxItemForTargetAgent(t, database, task, roundID, "main", sourceAgent, sourceKind, status, content)
}

// insertLiveInboxItemForTargetAgent writes one inbox row addressed to
// targetAgentID so tests can prove a claim only returns rows for the claiming
// agent, not every agent in the task.
func insertLiveInboxItemForTargetAgent(
	t *testing.T, database *Store, task domain.Task, roundID, targetAgentID, sourceAgent, sourceKind, status, content string,
) string {
	t.Helper()
	id := domain.NewID("inbox")
	if _, err := database.db.ExecContext(context.Background(), `
		INSERT INTO agent_inbox(
			id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
			content,payload_json,status,batch_id,created_at,claimed_at,processed_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, task.ID, roundID, targetAgentID, sourceAgent, sourceKind, "", "",
		content, "{}", status, "", timeString(time.Now().UTC()), "", "",
	); err != nil {
		t.Fatal(err)
	}
	return id
}

// insertLiveInboxTestRound adds a second, terminal round so tests can prove the
// live claim is still scoped to the running Turn's round.
func insertLiveInboxTestRound(t *testing.T, database *Store, task domain.Task) domain.TaskRound {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	messageID := domain.NewID("message")
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO messages(id,task_id,turn_id,role,sender,content,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		messageID, task.ID, "", "user", "owner", "other round", timeString(now),
	); err != nil {
		t.Fatal(err)
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: messageID,
		Status: domain.RoundCompleted, CreatedAt: now, FinishedAt: now,
	}
	if err := database.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(sequence),0)+1 FROM task_rounds WHERE task_id=?`, task.ID,
	).Scan(&round.Sequence); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO task_rounds(id,task_id,sequence,input_message_id,status,created_at,started_at,finished_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		round.ID, round.TaskID, round.Sequence, round.InputMessageID, round.Status,
		timeString(round.CreatedAt), "", timeString(round.FinishedAt),
	); err != nil {
		t.Fatal(err)
	}
	return round
}

func TestClaimAckLiveInbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task, round, turn := liveInboxTestTurn(t, database)
	first := insertLiveInboxItem(t, database, task, round, "first")
	second := insertLiveInboxItem(t, database, task, round, "second")

	items, err := database.ClaimOwnerInboxForLiveInjection(ctx, task.ID, "main", round.ID, turn.ID, 20)
	if err != nil || len(items) != 2 || items[0].ID != first || items[1].ID != second {
		t.Fatalf("claim=%#v err=%v", items, err)
	}
	var status, deliveredTurn, claimedAt string
	if err := database.db.QueryRowContext(ctx,
		`SELECT status,delivered_turn_id,claimed_at FROM agent_inbox WHERE id=?`, first,
	).Scan(&status, &deliveredTurn, &claimedAt); err != nil {
		t.Fatal(err)
	}
	if status != "claimed_in_turn" || deliveredTurn != turn.ID || claimedAt == "" {
		t.Fatalf("claim state status=%q turn=%q claimed=%q", status, deliveredTurn, claimedAt)
	}
	again, err := database.ClaimOwnerInboxForLiveInjection(ctx, task.ID, "main", round.ID, turn.ID, 20)
	if err != nil || len(again) != 0 {
		t.Fatalf("second claim must not return claimed messages: %#v err=%v", again, err)
	}

	if _, err := database.AckLiveInboxForTurn(ctx, task.ID, "main", turn.ID, []string{first}, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var steeredAt string
	if err := database.db.QueryRowContext(ctx,
		`SELECT status,steered_at FROM agent_inbox WHERE id=?`, first,
	).Scan(&status, &steeredAt); err != nil {
		t.Fatal(err)
	}
	if status != "delivered_in_turn" || steeredAt == "" {
		t.Fatalf("ack state status=%q steered=%q", status, steeredAt)
	}
	// A duplicate delivered acknowledgement is idempotent.
	if _, err := database.AckLiveInboxForTurn(ctx, task.ID, "main", turn.ID, []string{first}, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Rolling a claim back returns it to Pending for a later turn.
	if _, err := database.AckLiveInboxForTurn(ctx, task.ID, "main", turn.ID, []string{second}, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := database.db.QueryRowContext(ctx,
		`SELECT status,recovery_attempts FROM agent_inbox WHERE id=?`, second,
	).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attempts != 1 {
		t.Fatalf("rollback state status=%q attempts=%d", status, attempts)
	}
	if _, err := database.AckLiveInboxForTurn(ctx, task.ID, "main", turn.ID, []string{"missing"}, true, time.Now().UTC()); err == nil {
		t.Fatal("acknowledging an unknown id succeeded")
	}
}

func TestClaimLiveInboxAcceptsOwnerAndPetOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task, round, turn := liveInboxTestTurn(t, database)
	otherRound := insertLiveInboxTestRound(t, database, task)
	ownerID := insertLiveInboxItem(t, database, task, round, "owner note")
	petID := insertLiveInboxItemWithSource(t, database, task, round.ID, "pet", "pet_delegate_message", "pending", "pet note")
	assignmentID := insertLiveInboxItemWithSource(t, database, task, round.ID, "main", "assignment", "pending", "assignment")
	otherRoundID := insertLiveInboxItemWithSource(t, database, task, otherRound.ID, "pet", "pet_delegate_message", "pending", "other round")
	deliveredID := insertLiveInboxItemWithSource(t, database, task, round.ID, "pet", "pet_delegate_message", "delivered_in_turn", "already delivered")

	items, err := database.ClaimOwnerInboxForLiveInjection(ctx, task.ID, "main", round.ID, turn.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != ownerID || items[1].ID != petID {
		t.Fatalf("claim = %#v, want owner then pet", items)
	}
	if items[1].SourceKind != "pet_delegate_message" || items[1].SourceAgentID != "pet" {
		t.Fatalf("pet item provenance = %#v", items[1])
	}
	for id, want := range map[string]string{
		ownerID:      "claimed_in_turn",
		petID:        "claimed_in_turn",
		assignmentID: "pending",
		otherRoundID: "pending",
		deliveredID:  "delivered_in_turn",
	} {
		var status string
		if err := database.db.QueryRowContext(ctx,
			`SELECT status FROM agent_inbox WHERE id=?`, id,
		).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != want {
			t.Fatalf("inbox %s status = %q, want %q", id, status, want)
		}
	}
}

// TestClaimLiveInboxScopesTargetAgent covers the M6 mutation: dropping the
// target_agent_id constraint must not let one agent's turn claim another
// agent's owner or pet messages.
func TestClaimLiveInboxScopesTargetAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task, round, turn := liveInboxTestTurn(t, database)
	now := time.Now().UTC()
	if _, err := database.UpsertTaskAgent(ctx, domain.TaskAgent{
		TaskID: task.ID, AgentID: "worker", Role: "worker", Status: "idle",
		Title: "Worker agent", RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	mainOwnerID := insertLiveInboxItemForTargetAgent(t, database, task, round.ID, "main", "owner", "owner_message", "pending", "main owner")
	mainPetID := insertLiveInboxItemForTargetAgent(t, database, task, round.ID, "main", "pet", "pet_delegate_message", "pending", "main pet")
	workerOwnerID := insertLiveInboxItemForTargetAgent(t, database, task, round.ID, "worker", "owner", "owner_message", "pending", "worker owner")
	workerPetID := insertLiveInboxItemForTargetAgent(t, database, task, round.ID, "worker", "pet", "pet_delegate_message", "pending", "worker pet")

	items, err := database.ClaimOwnerInboxForLiveInjection(ctx, task.ID, "main", round.ID, turn.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != mainOwnerID || items[1].ID != mainPetID {
		t.Fatalf("claim = %#v, want only main-targeted owner then pet", items)
	}
	for _, item := range items {
		if item.TargetAgentID != "main" {
			t.Fatalf("claim returned item for target agent %q: %#v", item.TargetAgentID, item)
		}
	}
	for id, want := range map[string]struct{ status, deliveredTurn string }{
		mainOwnerID:   {"claimed_in_turn", turn.ID},
		mainPetID:     {"claimed_in_turn", turn.ID},
		workerOwnerID: {"pending", ""},
		workerPetID:   {"pending", ""},
	} {
		var status, deliveredTurn string
		if err := database.db.QueryRowContext(ctx,
			`SELECT status,delivered_turn_id FROM agent_inbox WHERE id=?`, id,
		).Scan(&status, &deliveredTurn); err != nil {
			t.Fatal(err)
		}
		if status != want.status || deliveredTurn != want.deliveredTurn {
			t.Fatalf("inbox %s status=%q delivered_turn_id=%q, want status=%q delivered_turn_id=%q",
				id, status, deliveredTurn, want.status, want.deliveredTurn)
		}
	}
}

func TestRequeueUnacknowledgedLiveInboxAtTurnEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task, round, turn := liveInboxTestTurn(t, database)
	id := insertLiveInboxItem(t, database, task, round, "not confirmed")
	if _, err := database.ClaimOwnerInboxForLiveInjection(ctx, task.ID, "main", round.ID, turn.ID, 20); err != nil {
		t.Fatal(err)
	}
	requeued, err := database.RequeueClaimedInboxForTurn(ctx, turn.ID)
	if err != nil || requeued != 1 {
		t.Fatalf("requeue=%d err=%v", requeued, err)
	}
	var status string
	var attempts int
	if err := database.db.QueryRowContext(ctx,
		`SELECT status,recovery_attempts FROM agent_inbox WHERE id=?`, id,
	).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attempts != 1 {
		t.Fatalf("turn-end requeue status=%q attempts=%d", status, attempts)
	}
}

// finishLiveInboxTurnRow makes the fixture Turn terminal so
// ClaimInboxAndCreateTurn's active-Turn guard lets the stale claim proceed.
func finishLiveInboxTurnRow(t *testing.T, database *Store, turnID string) {
	t.Helper()
	if _, err := database.db.ExecContext(context.Background(),
		`UPDATE turns SET status='succeeded' WHERE id=?`, turnID,
	); err != nil {
		t.Fatal(err)
	}
}

// TestClaimInboxAndCreateTurnSkipsLiveDeliveredSnapshot covers the product-side
// Submit/Claim TOCTOU: the caller holds a PendingAgentInbox snapshot, the
// running Turn's live inbox delivers and processes one of those items, and the
// stale scheduler then claims. The delivered item must not fail the claim and
// must not create a second Turn.
func TestClaimInboxAndCreateTurnSkipsLiveDeliveredSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task, round, turn := liveInboxTestTurn(t, database)
	finishLiveInboxTurnRow(t, database, turn.ID)
	now := time.Now().UTC()
	itemID := insertLiveInboxItem(t, database, task, round, "delivered while scheduling")
	items, err := database.PendingAgentInbox(ctx, task.ID, "main")
	if err != nil || len(items) != 1 {
		t.Fatalf("pending snapshot=%#v err=%v", items, err)
	}
	claimed, err := database.ClaimOwnerInboxForLiveInjection(ctx, task.ID, "main", round.ID, turn.ID, 20)
	if err != nil || len(claimed) != 1 || claimed[0].ID != itemID {
		t.Fatalf("live claim=%#v err=%v", claimed, err)
	}
	if _, err := database.AckLiveInboxForTurn(ctx, task.ID, "main", turn.ID, []string{itemID}, true, now); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkLiveInboxProcessedForTurn(ctx, turn.ID, now); err != nil {
		t.Fatal(err)
	}
	candidate := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		Status: domain.TurnQueued, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		InboxBatchID: "batch-stale", Attempt: 1, Generation: 2, Required: true, QueuedAt: now,
	}
	got, gotItems, err := database.ClaimInboxAndCreateTurn(ctx, candidate, items,
		func(claimed []domain.AgentInboxItem) (domain.Turn, error) {
			t.Fatalf("render ran for %d item(s) even though all were live-delivered", len(claimed))
			return domain.Turn{}, nil
		})
	if err != nil {
		t.Fatalf("stale snapshot of a live-delivered message failed the claim: %v", err)
	}
	if got.ID != "" || len(gotItems) != 0 {
		t.Fatalf("claim created turn=%#v items=%#v, want nothing", got, gotItems)
	}
	if turns, listErr := database.ListTurns(ctx, task.ID); listErr != nil || len(turns) != 1 {
		t.Fatalf("turns=%d err=%v, want only the original Turn", len(turns), listErr)
	}
	if pending, countErr := database.PendingInboxCount(ctx, task.ID); countErr != nil || pending != 0 {
		t.Fatalf("pending=%d err=%v, want 0", pending, countErr)
	}
	var status, deliveredTurnID string
	if err := database.db.QueryRowContext(ctx,
		`SELECT status,delivered_turn_id FROM agent_inbox WHERE id=?`, itemID,
	).Scan(&status, &deliveredTurnID); err != nil {
		t.Fatal(err)
	}
	if status != "processed" || deliveredTurnID != turn.ID {
		t.Fatalf("delivered item status=%q delivered_turn_id=%q, want processed/%q", status, deliveredTurnID, turn.ID)
	}
}

// TestClaimInboxAndCreateTurnRejectsNonLiveChanges proves the secondary check
// only accepts delivery, not every concurrent state change.
func TestClaimInboxAndCreateTurnRejectsNonLiveChanges(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(context.Context, *Store, domain.Task, domain.TaskRound, string) error
	}{
		{
			name: "cancelled",
			change: func(ctx context.Context, database *Store, task domain.Task, round domain.TaskRound, _ string) error {
				return database.CancelPendingInboxForRound(ctx, round.ID, time.Now().UTC())
			},
		},
		{
			name: "missing",
			change: func(_ context.Context, database *Store, _ domain.Task, _ domain.TaskRound, id string) error {
				_, err := database.db.ExecContext(context.Background(), `DELETE FROM agent_inbox WHERE id=?`, id)
				return err
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			task, round, turn := liveInboxTestTurn(t, database)
			finishLiveInboxTurnRow(t, database, turn.ID)
			itemID := insertLiveInboxItem(t, database, task, round, "conflicting change")
			items, err := database.PendingAgentInbox(ctx, task.ID, "main")
			if err != nil || len(items) != 1 {
				t.Fatalf("pending snapshot=%#v err=%v", items, err)
			}
			if err := test.change(ctx, database, task, round, itemID); err != nil {
				t.Fatal(err)
			}
			candidate := domain.Turn{
				ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
				Status: domain.TurnQueued, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
				InboxBatchID: "batch-conflict", Attempt: 1, Generation: 2, Required: true, QueuedAt: time.Now().UTC(),
			}
			if _, _, err := database.ClaimInboxAndCreateTurn(ctx, candidate, items, nil); err == nil ||
				!strings.Contains(err.Error(), "inbox item changed concurrently") {
				t.Fatalf("non-live change was not reported as a conflict: %v", err)
			}
			if turns, listErr := database.ListTurns(ctx, task.ID); listErr != nil || len(turns) != 1 {
				t.Fatalf("conflict created a Turn: turns=%d err=%v", len(turns), listErr)
			}
		})
	}
}

// TestClaimInboxAndCreateTurnRendersOnlyClaimedItems covers a mixed stale
// snapshot: one item was delivered live, another is still pending. The new
// Turn must contain only the pending item, so the delivered text is not
// injected a second time.
func TestClaimInboxAndCreateTurnRendersOnlyClaimedItems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task, round, turn := liveInboxTestTurn(t, database)
	finishLiveInboxTurnRow(t, database, turn.ID)
	now := time.Now().UTC()
	liveID := insertLiveInboxItem(t, database, task, round, "already steered owner text")
	assignmentID := insertLiveInboxItemWithSource(t, database, task, round.ID, "main", "assignment", "pending", "pending assignment text")
	items, err := database.PendingAgentInbox(ctx, task.ID, "main")
	if err != nil || len(items) != 2 {
		t.Fatalf("pending snapshot=%#v err=%v", items, err)
	}
	claimed, err := database.ClaimOwnerInboxForLiveInjection(ctx, task.ID, "main", round.ID, turn.ID, 20)
	if err != nil || len(claimed) != 1 || claimed[0].ID != liveID {
		t.Fatalf("live claim=%#v err=%v", claimed, err)
	}
	if _, err := database.AckLiveInboxForTurn(ctx, task.ID, "main", turn.ID, []string{liveID}, true, now); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkLiveInboxProcessedForTurn(ctx, turn.ID, now); err != nil {
		t.Fatal(err)
	}
	candidate := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: round.InputMessageID,
		Status:         domain.TurnQueued, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		InboxBatchID: "batch-mixed", Attempt: 1, Generation: 2, Required: true, QueuedAt: now,
	}
	var rendered []string
	got, gotItems, err := database.ClaimInboxAndCreateTurn(ctx, candidate, items,
		func(claimed []domain.AgentInboxItem) (domain.Turn, error) {
			for _, item := range claimed {
				rendered = append(rendered, item.Content)
			}
			candidate.Instruction = strings.Join(rendered, "\n")
			return candidate, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || len(gotItems) != 1 || gotItems[0].ID != assignmentID {
		t.Fatalf("turn=%#v claimed=%#v, want only the pending assignment", got, gotItems)
	}
	if strings.Contains(got.Instruction, "already steered owner text") {
		t.Fatalf("new Turn repeated live-delivered text: %q", got.Instruction)
	}
	if !strings.Contains(got.Instruction, "pending assignment text") {
		t.Fatalf("new Turn lost the pending assignment: %q", got.Instruction)
	}
	var liveStatus, deliveredTurnID string
	if err := database.db.QueryRowContext(ctx,
		`SELECT status,delivered_turn_id FROM agent_inbox WHERE id=?`, liveID,
	).Scan(&liveStatus, &deliveredTurnID); err != nil {
		t.Fatal(err)
	}
	if liveStatus != "processed" || deliveredTurnID != turn.ID {
		t.Fatalf("live-delivered item status=%q delivered_turn_id=%q", liveStatus, deliveredTurnID)
	}
	var assignmentStatus, batchID string
	if err := database.db.QueryRowContext(ctx,
		`SELECT status,batch_id FROM agent_inbox WHERE id=?`, assignmentID,
	).Scan(&assignmentStatus, &batchID); err != nil {
		t.Fatal(err)
	}
	if assignmentStatus != "claimed" || batchID != "batch-mixed" {
		t.Fatalf("assignment status=%q batch=%q, want claimed/batch-mixed", assignmentStatus, batchID)
	}
}

func TestRecoverInterruptedRequeuesClaimedInTurnInbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task, round, turn := liveInboxTestTurn(t, database)
	id := insertLiveInboxItem(t, database, task, round, "crash after claim")
	if _, err := database.ClaimOwnerInboxForLiveInjection(ctx, task.ID, "main", round.ID, turn.ID, 20); err != nil {
		t.Fatal(err)
	}
	recovered, err := database.RecoverInterrupted(ctx, time.Now().UTC().Add(time.Second))
	if err != nil || recovered.RequeuedInboxItems != 1 {
		t.Fatalf("recovery=%#v err=%v", recovered, err)
	}
	var status string
	if err := database.db.QueryRowContext(ctx,
		`SELECT status FROM agent_inbox WHERE id=?`, id,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("recovery did not requeue claimed_in_turn: %q", status)
	}
}
