package app

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Completing and reopening a Task are status changes the server performs, so
// the goal timeline must show them without waiting for an Agent to declare
// them. Before this, "complete" and "reopen" appeared only when an Agent
// remembered to write the event itself.
func TestTaskCompletionAndReopenAreRecordedOnTheGoalTimeline(t *testing.T) {
	fixture := newPetTaskFixture(t)
	project := domain.Project{ID: "project-memory-lifecycle", Name: "Lifecycle", CreatedAt: fixture.createdAt, UpdatedAt: fixture.createdAt}
	workspace := domain.Workspace{
		ID: "workspace-memory-lifecycle", ProjectID: project.ID, Name: "lifecycle", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: fixture.createdAt, UpdatedAt: fixture.createdAt,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "lifecycle", Request: "fix the login timeout",
		ModelID: fixture.model.ID, Isolation: "inplace", Filesystem: "workspace-write", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.CompleteTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.ReopenTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	memory, err := fixture.database.TaskMemory(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(memory.GoalHistory))
	for _, event := range memory.GoalHistory {
		kinds = append(kinds, event.ChangeType)
		if event.GoalAfter != "fix the login timeout" || event.Reason == "" {
			t.Fatalf("lifecycle event changed the goal or has no reason: %#v", event)
		}
	}
	if len(kinds) != 3 || kinds[0] != "set" || kinds[1] != "complete" || kinds[2] != "reopen" {
		t.Fatalf("goal timeline=%v, want set, complete, reopen", kinds)
	}
	if memory.CurrentGoal != "fix the login timeout" {
		t.Fatalf("a lifecycle event rewrote the goal: %q", memory.CurrentGoal)
	}
}

func TestTaskMemoryCarriesPersistedLinesIntoNextTurnPrompt(t *testing.T) {
	fixture := newPetDeskFixture(t)
	task, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ProjectID: fixture.project.ID, WorkspaceID: fixture.workspace.ID, Title: "Parallel work",
		Request: "Verify release", ModelID: fixture.model.ID, Isolation: "inplace", Filesystem: "workspace-write",
		Approval: "never", CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	first, err := fixture.service.SubmitAgentMessage(fixture.ctx, task.ID, "main", "Start verification")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, fixture.database, first.ID, domain.TurnSucceeded)

	title, owner, state, step := "Verify on a device", "self", "open", "Build sent to the device"
	if err := fixture.database.UpdateTaskMemory(fixture.ctx, task.ID, first, fixture.now,
		[]domain.TaskLinePatch{{ID: "device", Title: &title, Owner: &owner, State: &state, Step: &step}},
		func(memory *domain.TaskMemory) (*domain.TaskGoalChange, error) {
			memory.Status, memory.StatusTurnID, memory.StatusUpdatedAt = "Release verification underway", first.ID, fixture.now
			return nil, nil
		}); err != nil {
		t.Fatal(err)
	}
	next, err := fixture.service.SubmitAgentMessage(fixture.ctx, task.ID, "main", "Continue")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, fixture.database, next.ID, domain.TurnSucceeded)
	prompts, _ := fixture.service.executor.(*stubExecutor).requestHistory()
	if len(prompts) < 2 {
		t.Fatalf("received %d executor prompts for two Turns", len(prompts))
	}
	patch := "device: Verify on a device (owner: self, state: open) | step: Build sent to the device"
	if strings.Contains(prompts[0], patch) || !strings.Contains(prompts[len(prompts)-1], patch) {
		t.Fatalf("expected the persisted line in the second prompt: %q", prompts[len(prompts)-1])
	}
}
