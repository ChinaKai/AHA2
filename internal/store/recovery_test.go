package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestRecoverInterruptedRequeuesClaimedInbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "recover after restart", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnRunning, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		InboxBatchID: "batch-restart", Attempt: 1, Required: true, QueuedAt: now,
	}
	if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn); err != nil {
		t.Fatal(err)
	}
	inboxID := domain.NewID("inbox")
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO agent_inbox(
			id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
			content,payload_json,status,batch_id,created_at,claimed_at,processed_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		inboxID, task.ID, round.ID, "main", "owner", "owner_message", "",
		message.ID, message.Content, "{}", "claimed", turn.InboxBatchID, timeString(now), timeString(now), "",
	); err != nil {
		t.Fatal(err)
	}

	recovered, err := database.RecoverInterrupted(ctx, now.Add(time.Second))
	if err != nil || recovered.Turns != 1 || recovered.Tasks != 0 ||
		recovered.RequeuedInboxItems != 1 {
		t.Fatalf("recovery=%#v err=%v", recovered, err)
	}
	var turnStatus, inboxStatus, processedAt string
	var recoveryAttempts int
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM turns WHERE id=?`, turn.ID).Scan(&turnStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status,processed_at,recovery_attempts FROM agent_inbox WHERE id=?`, inboxID).Scan(&inboxStatus, &processedAt, &recoveryAttempts); err != nil {
		t.Fatal(err)
	}
	var roundStatus, taskStatus string
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM task_rounds WHERE id=?`, round.ID).Scan(&roundStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id=?`, task.ID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if turnStatus != string(domain.TurnInterrupted) || inboxStatus != "pending" || processedAt != "" || recoveryAttempts != 1 ||
		roundStatus != string(domain.RoundRunning) || taskStatus != string(domain.TaskActive) {
		t.Fatalf("recovery statuses turn=%q inbox=%q processed=%q attempts=%d round=%q task=%q", turnStatus, inboxStatus, processedAt, recoveryAttempts, roundStatus, taskStatus)
	}
	pending, err := database.AllPendingAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].TaskID != task.ID || pending[0].AgentID != "main" {
		t.Fatalf("restart did not preserve the pending main Inbox: %#v", pending)
	}

	pendingItems, err := database.PendingAgentInbox(ctx, task.ID, "main")
	if err != nil || len(pendingItems) != 1 {
		t.Fatalf("pending inbox=%#v err=%v", pendingItems, err)
	}
	recoveryTurn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnRunning, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		InboxBatchID: "batch-recovery", Attempt: 1, Generation: 1, Required: true, QueuedAt: now.Add(2 * time.Second),
	}
	recoveryTurn, err = database.ClaimInboxAndCreateTurn(ctx, recoveryTurn, pendingItems)
	if err != nil {
		t.Fatal(err)
	}

	recovered, err = database.RecoverInterrupted(ctx, now.Add(3*time.Second))
	if err != nil || recovered.Turns != 1 || recovered.Tasks != 0 ||
		recovered.RequeuedInboxItems != 1 {
		t.Fatalf("repeated recovery=%#v err=%v", recovered, err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status,processed_at,recovery_attempts FROM agent_inbox WHERE id=?`, inboxID).Scan(&inboxStatus, &processedAt, &recoveryAttempts); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM task_rounds WHERE id=?`, round.ID).Scan(&roundStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id=?`, task.ID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if inboxStatus != "pending" || processedAt != "" || recoveryAttempts != 2 ||
		roundStatus != string(domain.RoundRunning) || taskStatus != string(domain.TaskActive) {
		t.Fatalf("repeated recovery statuses inbox=%q processed=%q attempts=%d round=%q task=%q", inboxStatus, processedAt, recoveryAttempts, roundStatus, taskStatus)
	}
	pending, err = database.AllPendingAgents(ctx)
	if err != nil || len(pending) != 1 || pending[0].TaskID != task.ID || pending[0].AgentID != "main" {
		t.Fatalf("repeated restart left pending agents=%#v err=%v", pending, err)
	}
}
