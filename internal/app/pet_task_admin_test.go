package app

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetTaskLifecycleAndRead(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "生命周期项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "生命周期 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "生命周期任务",
		Request: "先运行再完成", Filesystem: "read-only", Start: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, task.ID, domain.TaskWaitingUser)

	conversation, err := fixture.service.PetTaskConversation(fixture.ctx, task.ID, 20)
	if err != nil || len(conversation) == 0 {
		t.Fatalf("conversation=%#v err=%v", conversation, err)
	}
	turns, err := fixture.service.PetTaskTurns(fixture.ctx, task.ID, 20)
	if err != nil || len(turns) == 0 {
		t.Fatalf("turns=%#v err=%v", turns, err)
	}

	completed, err := fixture.service.PetCompleteTaskWithDecision(fixture.ctx, task.ID, "skipped", "只读任务未产生代码修改", "")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != domain.TaskCompleted {
		t.Fatalf("completed status=%s", completed.Status)
	}
	reopened, err := fixture.service.PetReopenTask(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != domain.TaskWaitingUser {
		t.Fatalf("reopened status=%s", reopened.Status)
	}
	title := "生命周期任务（已修改）"
	goal := "新的目标"
	updated, err := fixture.service.PetUpdateTask(fixture.ctx, PetUpdateTaskInput{
		TaskID: task.ID, Title: &title, CurrentGoal: &goal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != title || updated.CurrentGoal != goal {
		t.Fatalf("updated task=%#v", updated)
	}

	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(false),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetCompleteTaskWithDecision(fixture.ctx, task.ID, "skipped", "只读任务未产生代码修改", ""); err == nil {
		t.Fatal("complete task was allowed without the task management permission")
	}

	// Deleting a task must wait for an owner-approved approval.
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetDeleteTask(fixture.ctx, task.ID); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("delete task without reason was accepted: %v", err)
	}
	approval, err := fixture.service.PetDeleteTask(fixture.ctx, task.ID, "清理测试任务需要")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Task(fixture.ctx, task.ID); err != nil {
		t.Fatal("task was deleted before approval")
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Task(fixture.ctx, task.ID); err == nil {
		t.Fatal("task still exists after approval")
	}
}
