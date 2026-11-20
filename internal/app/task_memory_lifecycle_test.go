package app

import (
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
