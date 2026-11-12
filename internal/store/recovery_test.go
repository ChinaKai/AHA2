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
	recoveryTurn, _, err = database.ClaimInboxAndCreateTurn(ctx, recoveryTurn, pendingItems, nil)
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

func TestRecoverInterruptedRestoresExecutingPetApprovals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	interrupted := domain.PetApproval{
		ID: "pet_approval_interrupted", TaskID: "task_pet", Kind: "delete_workspace",
		Title: "删除 Workspace", Summary: "重启时仍在执行", Risk: "high", Payload: "{}",
		Status: "executing", CreatedAt: now,
	}
	resolved := domain.PetApproval{
		ID: "pet_approval_resolved", TaskID: "task_pet", Kind: "delete_task",
		Title: "已批准", Summary: "已经完成", Risk: "high", Payload: "{}",
		Status: "approved", Resolution: "确认删除", DecisionReason: "确认删除", CreatedAt: now, ResolvedAt: now,
	}
	for _, item := range []domain.PetApproval{interrupted, resolved} {
		if err := database.CreatePetApproval(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	recovered, err := database.RecoverInterrupted(ctx, now.Add(time.Second))
	if err != nil || recovered.PetApprovals != 1 {
		t.Fatalf("recovery=%#v err=%v, want one restored approval", recovered, err)
	}
	restored, err := database.PetApproval(ctx, interrupted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != "pending" || restored.Resolution != PetApprovalRestartInterrupted ||
		restored.DecisionReason != PetApprovalRestartInterrupted || !restored.ResolvedAt.IsZero() {
		t.Fatalf("restored approval=%#v, want a retryable pending row with the restart note", restored)
	}
	untouched, err := database.PetApproval(ctx, resolved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Status != "approved" || untouched.Resolution != "确认删除" {
		t.Fatalf("resolved approval was modified: %#v", untouched)
	}
}

func TestRecoverInterruptedRequeuesLiveInTurnInbox(t *testing.T) {
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
		Content: "live instruction", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnRunning, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		InboxBatchID: "batch-live", Attempt: 1, Required: true, QueuedAt: now,
	}
	if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn); err != nil {
		t.Fatal(err)
	}
	inboxID := domain.NewID("inbox")
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO agent_inbox(
			id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
			content,payload_json,status,batch_id,created_at,claimed_at,processed_at,
			delivered_turn_id,delivered_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		inboxID, task.ID, round.ID, "main", "owner", "owner_message", "",
		message.ID, message.Content, "{}", "delivered_in_turn", "", timeString(now),
		timeString(now), "", turn.ID, timeString(now),
	); err != nil {
		t.Fatal(err)
	}
	recovered, err := database.RecoverInterrupted(ctx, now.Add(time.Second))
	if err != nil || recovered.Turns != 1 || recovered.RequeuedInboxItems != 1 {
		t.Fatalf("recovery=%#v err=%v", recovered, err)
	}
	var status, deliveredTurnID, deliveredAt string
	if err := database.db.QueryRowContext(ctx, `
		SELECT status,delivered_turn_id,delivered_at FROM agent_inbox WHERE id=?`,
		inboxID,
	).Scan(&status, &deliveredTurnID, &deliveredAt); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || deliveredTurnID != "" || deliveredAt != "" {
		t.Fatalf("live inbox status=%q delivered_turn=%q delivered_at=%q", status, deliveredTurnID, deliveredAt)
	}
}
