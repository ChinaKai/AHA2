package store

import (
	"context"
	"path/filepath"
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
	id := domain.NewID("inbox")
	if _, err := database.db.ExecContext(context.Background(), `
		INSERT INTO agent_inbox(
			id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
			content,payload_json,status,batch_id,created_at,claimed_at,processed_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, task.ID, round.ID, "main", "owner", "owner_message", "", "",
		content, "{}", "pending", "", timeString(time.Now().UTC()), "", "",
	); err != nil {
		t.Fatal(err)
	}
	return id
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
