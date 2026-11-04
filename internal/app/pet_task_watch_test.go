package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestIgnoredPetWatchSuppressesNotificationsAndPetCannotReactivate(t *testing.T) {
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
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "忽略项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "忽略 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "忽略任务",
		Request: "忽略后不应通知", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SetPetTaskWatchIgnored(fixture.ctx, task.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetWatchTask(fixture.ctx, task.ID); err == nil {
		t.Fatal("pet re-watched an ignored Task")
	}
	if err := fixture.service.PetUnwatchTask(fixture.ctx, task.ID); err == nil {
		t.Fatal("pet removed an ignored watch")
	}
	watches, err := fixture.service.PetTaskWatchList(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 1 || !watches[0].Watched || !watches[0].Ignored || watches[0].TaskID != task.ID {
		t.Fatalf("ignored watches=%#v", watches)
	}

	now := fixture.service.now().UTC()
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, task.ID, domain.TaskDraft, domain.TaskPreparing, now.Format(time.RFC3339Nano), ""); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, task.ID, domain.TaskPreparing, domain.TaskFailed, now.Format(time.RFC3339Nano), ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	watch, err := fixture.database.PetTaskWatch(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if watch.LastNotifiedStatus != "" {
		t.Fatalf("ignored watch was notified: %#v", watch)
	}

	if _, err := fixture.service.SetPetTaskWatchIgnored(fixture.ctx, task.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.RemovePetTaskWatch(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if watches, err := fixture.service.PetTaskWatchList(fixture.ctx); err != nil || len(watches) != 0 {
		t.Fatalf("watches after owner removal=%#v err=%v", watches, err)
	}
}

func TestPetTaskWatchListIncludesUnwatchedActiveOrdinaryTasks(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	now := fixture.service.now().UTC()
	project := domain.Project{ID: "project-follow-list", Name: "Follow list", ProjectType: "local", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-follow-list", ProjectID: project.ID, Name: "Follow list",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	create := func(title string) domain.Task {
		t.Helper()
		task, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
			ProjectID: project.ID, WorkspaceID: workspace.ID, Title: title, Request: title,
			ModelID: fixture.model.ID, Isolation: "inplace", Filesystem: "workspace-write",
			Approval: "never", CollaborationMode: "single", MaxAgents: 1,
			KnowledgePolicy: "inherit", StartMode: "manual",
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	active := create("active task")
	terminal := create("terminal task")
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, terminal.ID, domain.TaskDraft, domain.TaskCancelled, timeString(now), ""); err != nil {
		t.Fatal(err)
	}

	views, err := fixture.service.PetTaskWatchList(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]PetTaskWatchView{}
	for _, view := range views {
		byID[view.TaskID] = view
	}
	activeView, ok := byID[active.ID]
	if !ok || activeView.Watched || activeView.Ignored {
		t.Fatalf("active task view=%#v ok=%v", activeView, ok)
	}
	if _, ok := byID[terminal.ID]; ok {
		t.Fatalf("terminal task was listed: %#v", byID[terminal.ID])
	}
	if _, ok := byID[petTaskID]; ok {
		t.Fatal("pet task was listed")
	}
	if _, err := fixture.service.FollowPetTask(fixture.ctx, active.ID); err != nil {
		t.Fatal(err)
	}
	views, err = fixture.service.PetTaskWatchList(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.TaskID == active.ID {
			if !view.Watched || view.Reason != "manual" {
				t.Fatalf("followed task view=%#v", view)
			}
			return
		}
	}
	t.Fatalf("followed task disappeared from list: %#v", views)
}

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
	if !strings.Contains(message, task.Code) || !strings.Contains(message, "failed") || !strings.Contains(message, "aha_pet_work_state") || !strings.Contains(message, "core/agenda.md") || !strings.Contains(message, "owner_decision") || !strings.Contains(message, "NOOP") {
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
