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
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
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
	watch, err := fixture.database.PetTaskWatch(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if watch.LastNotifiedStatus != "" {
		t.Fatalf("ignored watch was notified: %#v", watch)
	}
	// Exercise the delivery-time gate for a callback queued before the ignore.
	fixture.service.deliverPetTaskStatusMessage(task.ID, fixture.service.petTaskStatusMessage(fixture.ctx, task, domain.TaskCompleted), true)
	if watch, err := fixture.database.PetTaskWatch(fixture.ctx, task.ID); err != nil || !watch.Ignored {
		t.Fatalf("deferred delivery removed ignore: watch=%+v err=%v", watch, err)
	}
	statusMessages := func() []string {
		t.Helper()
		messages, err := fixture.database.ListMessages(fixture.ctx, petTaskID)
		if err != nil {
			t.Fatal(err)
		}
		var updates []string
		for _, message := range messages {
			if strings.Contains(message.Content, task.Code) && strings.HasPrefix(message.Content, "[任务动态]") {
				updates = append(updates, message.Content)
			}
		}
		return updates
	}
	if messages := statusMessages(); len(messages) != 0 {
		t.Fatalf("ignored task woke pet: %q", messages)
	}

	if _, err := fixture.service.SetPetTaskWatchIgnored(fixture.ctx, task.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, task.ID, domain.TaskFailed, domain.TaskWaitingUser, timeString(fixture.service.now().UTC()), ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		messages := statusMessages()
		if len(messages) == 1 && strings.Contains(messages[0], "waiting_user") {
			break
		}
		if len(messages) > 0 || time.Now().After(deadline) {
			t.Fatalf("expected one waiting_user wake after restore, got %q", messages)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := fixture.service.RemovePetTaskWatch(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if watches, err := fixture.service.PetTaskWatchList(fixture.ctx); err != nil || len(watches) != 1 ||
		watches[0].TaskID != task.ID || watches[0].Ignored || watches[0].Watched {
		t.Fatalf("watches after owner removal=%#v err=%v", watches, err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
}

func TestRestoredPetWatchNotifiesOnSameStatusAndTurn(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietEnabled: boolPointer(false), QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
	fixture.service.StartPetTaskNotifications()
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "Restore watch"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "Restore workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// PetCreateTask follows the Task before starting its first Turn.
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Followed task",
		Request: "Report when ready", Start: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, task.ID, domain.TaskWaitingUser)
	turn, err := fixture.database.LatestTurnForAgent(fixture.ctx, task.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	statusMessages := func() []string {
		t.Helper()
		messages, err := fixture.database.ListMessages(fixture.ctx, petTaskID)
		if err != nil {
			t.Fatal(err)
		}
		var updates []string
		for _, message := range messages {
			if strings.HasPrefix(message.Content, "[任务动态]") && strings.Contains(message.Content, task.Code) {
				updates = append(updates, message.Content)
			}
		}
		return updates
	}
	waitForWakes := func(want int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			messages := statusMessages()
			if len(messages) == want && strings.Contains(messages[want-1], "waiting_user") {
				return
			}
			if len(messages) >= want || time.Now().After(deadline) {
				t.Fatalf("want %d waiting_user wakes, got %q", want, messages)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	assertNotified := func() {
		t.Helper()
		watch, err := fixture.database.PetTaskWatch(fixture.ctx, task.ID)
		if err != nil || watch.LastNotifiedStatus != string(domain.TaskWaitingUser) || watch.LastNotifiedTurnID != turn.ID {
			t.Fatalf("want waiting_user notification for Turn %s, got watch=%+v err=%v", turn.ID, watch, err)
		}
	}
	waitForWakes(1)
	assertNotified()

	ignored, err := fixture.service.SetPetTaskWatchIgnored(fixture.ctx, task.ID, true)
	if err != nil || !ignored.Ignored {
		t.Fatalf("ignored follow=%+v err=%v", ignored, err)
	}
	if err := fixture.service.CompleteTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	assertNotified()
	if messages := statusMessages(); len(messages) != 1 {
		t.Fatalf("completed Task woke the pet while ignored: %q", messages)
	}
	restored, err := fixture.service.SetPetTaskWatchIgnored(fixture.ctx, task.ID, false)
	if err != nil || restored.Ignored || restored.Reason != ignored.Reason {
		t.Fatalf("restored follow=%+v err=%v", restored, err)
	}
	// Reopen re-enters waiting_user without creating a new Main Turn.
	if err := fixture.service.ReopenTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if latest, err := fixture.database.LatestTurnForAgent(fixture.ctx, task.ID, "main"); err != nil || latest.ID != turn.ID {
		t.Fatalf("reopen changed the latest Main Turn: got %s want %s err=%v", latest.ID, turn.ID, err)
	}
	waitForWakes(2)
	assertNotified()
	if _, err := fixture.service.SetPetTaskWatchIgnored(fixture.ctx, task.ID, false); err != nil {
		t.Fatal(err)
	}
	assertNotified()
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
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

func TestPetTaskWatchIgnoreWithoutFollowRestoresToUnfollowed(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	now := fixture.service.now().UTC()
	project := domain.Project{ID: "project-ignore-unwatched", Name: "Ignore unwatched", ProjectType: "local", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-ignore-unwatched", ProjectID: project.ID, Name: "Ignore unwatched",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "ignored without follow", Request: "ignore me",
		ModelID: fixture.model.ID, Isolation: "inplace", Filesystem: "workspace-write",
		Approval: "never", CollaborationMode: "single", MaxAgents: 1,
		KnowledgePolicy: "inherit", StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Ignoring an unfollowed Task stores an ignore-only row instead of starting
	// a follow.
	watch, err := fixture.service.SetPetTaskWatchIgnored(fixture.ctx, task.ID, true)
	if err != nil || !watch.Ignored || watch.Reason != petWatchReasonIgnored {
		t.Fatalf("ignore without follow watch=%#v err=%v", watch, err)
	}
	views, err := fixture.service.PetTaskWatchList(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	ignoredView, found := PetTaskWatchView{}, false
	for _, view := range views {
		if view.TaskID == task.ID {
			ignoredView, found = view, true
		}
	}
	if !found || ignoredView.Watched || !ignoredView.Ignored {
		t.Fatalf("ignored-unwatched view=%#v found=%v", ignoredView, found)
	}
	if _, err := fixture.service.PetWatchTask(fixture.ctx, task.ID); err == nil {
		t.Fatal("pet re-watched an ignored but unfollowed Task")
	}
	// Automatic watching must respect the ignore decision too.
	fixture.service.watchPetTask(fixture.ctx, task.ID, "auto")
	if stored, err := fixture.database.PetTaskWatch(fixture.ctx, task.ID); err != nil || !stored.Ignored || stored.Reason != petWatchReasonIgnored {
		t.Fatalf("auto watch revived ignore row=%#v err=%v", stored, err)
	}
	assistant, err := fixture.service.petAssistant(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateOwnerPetPermissions(fixture.ctx, assistant.ID, false, true, true, true, true, true, true, true, now); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.service.PetWorkState(fixture.ctx)
	want := PetIgnoredTask{ID: task.ID, Code: task.Code, IgnoredAt: watch.IgnoredAt}
	if err != nil || len(state.IgnoredTasks) != 1 || state.IgnoredTasks[0] != want {
		t.Fatalf("ignore should retain the task reference without private details: got=%+v want=%+v err=%v", state.IgnoredTasks, want, err)
	}

	// Restoring returns the Task to the unfollowed list rather than following it.
	restored, err := fixture.service.SetPetTaskWatchIgnored(fixture.ctx, task.ID, false)
	if err != nil || restored.Ignored {
		t.Fatalf("restore watch=%#v err=%v", restored, err)
	}
	if _, err := fixture.database.PetTaskWatch(fixture.ctx, task.ID); err == nil {
		t.Fatal("restore of an ignore-only row kept the watch row")
	}
	views, err = fixture.service.PetTaskWatchList(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.TaskID == task.ID {
			if view.Watched || view.Ignored {
				t.Fatalf("restored-unwatched view=%#v", view)
			}
			return
		}
	}
	t.Fatalf("restored Task disappeared from the unfollowed list: %#v", views)
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

func TestCompletedPetWatchIsRemovedAfterNotification(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietEnabled: boolPointer(false), QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
		AllowManageModels: boolPointer(true), AllowManageKnowledge: boolPointer(true),
		AllowManageChannels: boolPointer(true), AllowManageSystem: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	fixture.service.StartPetTaskNotifications()
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "完成关注项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "完成关注 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "完成关注任务",
		Request: "完成后自动取消关注", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetWatchTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	now := fixture.service.now().UTC()
	for _, step := range []struct{ from, to domain.TaskStatus }{
		{domain.TaskDraft, domain.TaskPreparing},
		{domain.TaskPreparing, domain.TaskActive},
		{domain.TaskActive, domain.TaskCompleted},
	} {
		if err := fixture.database.UpdateTaskStatus(fixture.ctx, task.ID, step.from, step.to, now.Format(time.RFC3339Nano), ""); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := fixture.database.PetTaskWatch(fixture.ctx, task.ID); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completed watch was not removed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	views, err := fixture.service.PetTaskWatchList(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.TaskID == task.ID {
			t.Fatalf("completed task still listed: %#v", view)
		}
	}
}

func TestCompletedTaskIsHiddenFromFollowsWithLegacyWatch(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietEnabled: boolPointer(false), QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "历史关注项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "历史关注 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "历史完成任务",
		Request: "完成后的旧关注不应展示", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := fixture.service.now().UTC()
	for _, step := range []struct{ from, to domain.TaskStatus }{
		{domain.TaskDraft, domain.TaskPreparing},
		{domain.TaskPreparing, domain.TaskActive},
		{domain.TaskActive, domain.TaskCompleted},
	} {
		if err := fixture.database.UpdateTaskStatus(fixture.ctx, task.ID, step.from, step.to, now.Format(time.RFC3339Nano), ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.database.UpsertPetTaskWatch(fixture.ctx, task.ID, "manual", now); err != nil {
		t.Fatal(err)
	}
	views, err := fixture.service.PetTaskWatchList(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.TaskID == task.ID {
			t.Fatalf("completed task with legacy watch was listed: %#v", view)
		}
	}
}
