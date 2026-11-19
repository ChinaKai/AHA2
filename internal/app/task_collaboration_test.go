package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// TestUpdateTaskCollaborationRejectsAgentCreatedAfterCountCheck freezes the
// exact window task-652 reported: the service validates the requested Max
// Agents against a count of one (main only), a legitimate dispatch then creates
// and commits three agents, and only afterwards does the store write run. The
// downgrade must be rejected, keep the previous limit and leave the three
// agents in place.
func TestUpdateTaskCollaborationRejectsAgentCreatedAfterCountCheck(t *testing.T) {
	fixture := newCollabMCPFixture(t, "auto", 8)
	mainAgent, err := fixture.database.TaskAgent(fixture.ctx, fixture.task.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	created := false
	fixture.service.taskCollaborationCheckHook = func() {
		if created {
			return
		}
		created = true
		for index := 1; index <= 3; index++ {
			now := time.Now().UTC()
			outcome, err := fixture.database.CreateTaskAgentWithinLimit(fixture.ctx, domain.TaskAgent{
				TaskID: fixture.task.ID, AgentID: fmt.Sprintf("sub-late-%d", index), Role: "sub",
				Status: "idle", Title: "late agent", RuntimeConfigSnapshotID: mainAgent.RuntimeConfigSnapshotID,
				InheritMain: true, CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				t.Fatalf("late agent %d: %v", index, err)
			}
			if outcome != store.TaskAgentCreated {
				t.Fatalf("late agent %d outcome=%d", index, outcome)
			}
		}
	}

	if _, err := fixture.service.UpdateTaskCollaboration(fixture.ctx, fixture.task.ID, "auto", 2); err == nil {
		t.Fatal("downgrade to 2 succeeded although four agents existed")
	} else if !strings.Contains(err.Error(), "max_agents cannot be lower than the current agent count") {
		t.Fatalf("downgrade error=%v", err)
	}
	agents, err := fixture.database.ListTaskAgents(fixture.ctx, fixture.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 4 {
		t.Fatalf("rejected downgrade changed agents: %#v", agents)
	}
	reloaded, err := fixture.database.Task(fixture.ctx, fixture.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.MaxAgents != 8 {
		t.Fatalf("rejected downgrade wrote max_agents=%d, want the previous 8", reloaded.MaxAgents)
	}

	// The same call must still pass at the boundary: a limit equal to the live
	// count is not below it.
	updated, err := fixture.service.UpdateTaskCollaboration(fixture.ctx, fixture.task.ID, "auto", 4)
	if err != nil {
		t.Fatalf("limit equal to the agent count: %v", err)
	}
	if updated.MaxAgents != 4 {
		t.Fatalf("max_agents=%d, want 4", updated.MaxAgents)
	}
}
