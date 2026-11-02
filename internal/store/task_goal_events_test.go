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

func TestUpdateTaskGoalIfCurrentOnlyWritesOnMatch(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, t.TempDir()+"/aha2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	before, err := database.TaskGoalHistory(ctx, task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := database.UpdateTaskGoalIfCurrent(ctx, task.ID, "stale goal", "mission goal", now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("stale goal unexpectedly updated the task")
	}
	current, err := database.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.CurrentGoal != task.CurrentGoal {
		t.Fatalf("stale update changed goal to %q", current.CurrentGoal)
	}
	memory, err := database.TaskMemory(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if memory.CurrentGoal != task.CurrentGoal {
		t.Fatalf("stale update changed memory goal to %q", memory.CurrentGoal)
	}
	after, err := database.TaskGoalHistory(ctx, task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("stale update added a goal event: before=%d after=%d", len(before), len(after))
	}

	updated, err = database.UpdateTaskGoalIfCurrent(ctx, task.ID, task.CurrentGoal, "mission goal", now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("matching goal was not updated")
	}
	current, err = database.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	memory, err = database.TaskMemory(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.CurrentGoal != "mission goal" || memory.CurrentGoal != "mission goal" {
		t.Fatalf("goal task=%q memory=%q", current.CurrentGoal, memory.CurrentGoal)
	}
	history, err := database.TaskGoalHistory(ctx, task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != len(before)+1 || history[len(history)-1].GoalAfter != "mission goal" {
		t.Fatalf("matching update history=%#v", history)
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
