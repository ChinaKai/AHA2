package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

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
	if err := database.LinkCollaborationMemberTurn(ctx, batch.ID, "sub-001", "turn-sub-1", now); err != nil {
		t.Fatal(err)
	}
	if err := database.LinkCollaborationMemberTurn(ctx, batch.ID, "sub-002", "turn-sub-2", now); err != nil {
		t.Fatal(err)
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
	stored, err := database.CollaborationBatch(ctx, batch.ID)
	if err != nil || stored.Status != domain.CollaborationBatchConsumed || stored.DeliveredCursor != progress.NextCursor {
		t.Fatalf("consumed batch=%#v err=%v", stored, err)
	}
}
