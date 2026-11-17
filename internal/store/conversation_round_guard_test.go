package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestCreateAgentTurnRejectsTerminalRound(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	task, round, _ := liveInboxTestTurn(t, database)
	round.Status = domain.RoundInterrupted
	round.FinishedAt = time.Now().UTC()
	if err := database.UpdateRound(ctx, round, domain.RoundRunning); err != nil {
		t.Fatal(err)
	}

	_, err = database.CreateAgentTurn(ctx, domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		Status: domain.TurnQueued, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		Attempt: 2, Required: true, QueuedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrRoundTerminal) {
		t.Fatalf("CreateAgentTurn err=%v, want ErrRoundTerminal", err)
	}
	turns, err := database.TurnsForRound(ctx, round.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns in Round=%d, want the original Turn only", len(turns))
	}
}

// Turns written by old fixtures and legacy data can carry a RoundID that has no
// Round row. That is not a terminal Round, so the insert must still work.
func TestCreateAgentTurnAllowsMissingRoundRow(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	task, _, existing := liveInboxTestTurn(t, database)
	turn, err := database.CreateAgentTurn(ctx, domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: domain.NewID("round"),
		AgentID: "main", Status: domain.TurnQueued,
		InputMessageID:          existing.InputMessageID,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		Attempt:                 1, Required: true, QueuedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateAgentTurn with a missing Round row: %v", err)
	}
	if turn.ID == "" {
		t.Fatal("CreateAgentTurn returned an empty Turn")
	}
}
