package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetWatchedTaskStatusNotification(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
		AllowManageModels: boolPointer(true), AllowManageKnowledge: boolPointer(true),
		AllowManageChannels: boolPointer(true), AllowManageSystem: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	fixture.service.StartPetTaskNotifications()
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "关注项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "关注 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "关注任务",
		Request: "等待失败通知", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetWatchTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetUnwatchTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetWatchTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	now := fixture.service.now().UTC()
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, task.ID, domain.TaskDraft, domain.TaskPreparing, now.Format(time.RFC3339Nano), ""); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, task.ID, domain.TaskPreparing, domain.TaskFailed, now.Format(time.RFC3339Nano), ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		watch, watchErr := fixture.database.PetTaskWatch(fixture.ctx, task.ID)
		if watchErr == nil && watch.LastNotifiedStatus == string(domain.TaskFailed) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watch was not notified: %#v err=%v", watch, watchErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	message := fixture.service.petTaskStatusMessage(fixture.ctx, task, domain.TaskFailed)
	if !strings.Contains(message, task.Code) || !strings.Contains(message, "failed") || !strings.Contains(message, "aha_pet_work_state") {
		t.Fatalf("notification message=%q", message)
	}
	if _, err := fixture.database.UpdateGeneralSettings(fixture.ctx, domain.GeneralSettings{Language: domain.LanguageEnglish}); err != nil {
		t.Fatal(err)
	}
	english := fixture.service.petTaskStatusMessage(fixture.ctx, task, domain.TaskFailed)
	if !strings.Contains(english, "[Task update]") || !strings.Contains(english, "changed status to failed") || strings.Contains(english, "任务动态") {
		t.Fatalf("English task update=%q", english)
	}

	// The store listener should not notify for intermediate states.
	watch, err := fixture.database.PetTaskWatch(fixture.ctx, task.ID)
	if err != nil || watch.LastNotifiedStatus != string(domain.TaskFailed) {
		t.Fatalf("watch=%#v err=%v", watch, err)
	}
}
