package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestTaskGoalHistoryIsChronologicalAndBounded(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, t.TempDir()+"/aha2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	task := createStoreTestTask(t, database)
	memory, err := database.TaskMemory(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(memory.GoalHistory) != 1 || memory.GoalHistory[0].ChangeType != "set" ||
		memory.GoalHistory[0].GoalAfter != task.CurrentGoal {
		t.Fatalf("initial goal history=%#v", memory.GoalHistory)
	}

	now := time.Now().UTC()
	for index := 0; index < 22; index++ {
		goal := fmt.Sprintf("goal-%02d", index)
		if err := database.UpdateTaskGoalWithEvent(ctx, domain.TaskGoalChange{
			TaskID: task.ID, RoundID: fmt.Sprintf("round-%02d", index),
			TurnID: fmt.Sprintf("turn-%02d", index), GoalAfter: goal,
			ChangeType: "revise", Reason: fmt.Sprintf("reason-%02d", index),
			CreatedAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	history, err := database.TaskGoalHistory(ctx, task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 20 {
		t.Fatalf("history length=%d want=20", len(history))
	}
	if history[0].GoalAfter != "goal-02" || history[len(history)-1].GoalAfter != "goal-21" {
		t.Fatalf("history bounds first=%q last=%q", history[0].GoalAfter, history[len(history)-1].GoalAfter)
	}
	for index := 1; index < len(history); index++ {
		if history[index].Sequence <= history[index-1].Sequence {
			t.Fatalf("history is not chronological: %#v", history)
		}
	}
}

func TestTaskGoalEventSkipsNoopRevision(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, t.TempDir()+"/aha2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	task := createStoreTestTask(t, database)
	before, err := database.TaskGoalHistory(ctx, task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskGoalWithEvent(ctx, domain.TaskGoalChange{
		TaskID: task.ID, GoalAfter: task.CurrentGoal, ChangeType: "revise",
		Reason: "No actual change", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	after, err := database.TaskGoalHistory(ctx, task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("no-op revision added an event: before=%d after=%d", len(before), len(after))
	}
}
