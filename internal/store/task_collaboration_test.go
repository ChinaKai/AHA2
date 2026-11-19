package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// TestUpdateTaskCollaborationRejectsDowngradeBelowCurrentCount covers the
// window a caller's own count check opens: a legitimate agent creation commits
// after that check and before the update. The store re-counts inside the write
// transaction, so the smaller limit is refused and the created agents stay.
func TestUpdateTaskCollaborationRejectsDowngradeBelowCurrentCount(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, _, _ := createMailTaskFixture(t, database)
	now := time.Now().UTC()
	if err := database.UpdateTaskCollaboration(ctx, taskID, "auto", 8, timeString(now)); err != nil {
		t.Fatal(err)
	}
	task, err := database.Task(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		outcome, err := database.CreateTaskAgentWithinLimit(ctx, domain.TaskAgent{
			TaskID: taskID, AgentID: fmt.Sprintf("sub-late-%d", index), Role: "sub", Status: "idle",
			Title: "late agent", RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InheritMain: true,
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if outcome != TaskAgentCreated {
			t.Fatalf("late agent %d outcome=%d", index, outcome)
		}
	}

	if err := database.UpdateTaskCollaboration(ctx, taskID, "auto", 2, timeString(now)); !errors.Is(err, ErrTaskAgentLimitBelowCount) {
		t.Fatalf("downgrade below the live count error=%v", err)
	}
	reloaded, err := database.Task(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.MaxAgents != 8 {
		t.Fatalf("rejected downgrade wrote max_agents=%d, want the previous 8", reloaded.MaxAgents)
	}
	agents, err := database.ListTaskAgents(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 4 {
		t.Fatalf("rejected downgrade changed agents: %#v", agents)
	}

	// The boundary itself must still pass: a limit equal to the live count is
	// not a downgrade below it.
	if err := database.UpdateTaskCollaboration(ctx, taskID, "auto", 4, timeString(now)); err != nil {
		t.Fatalf("limit equal to the agent count: %v", err)
	}
}
