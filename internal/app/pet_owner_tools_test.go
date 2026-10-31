package app

import (
	"encoding/json"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetMCPPermissionsEnforcedAtCallTime(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(false), AllowCreateTasks: boolPointer(false),
		AllowCreateWorkspaces: boolPointer(false), AllowMessageTasks: boolPointer(false),
	}); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range fixture.service.PetMCPTools(fixture.ctx) {
		names[tool.Name] = true
	}
	for _, advertised := range []string{"aha_overview", "aha_task_detail", "aha_create_task", "aha_send_task_message", "aha_create_project"} {
		if !names[advertised] {
			t.Fatalf("tool %s is not advertised; tools=%v", advertised, names)
		}
	}
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_task_detail", json.RawMessage(`{"task_id":"task-x"}`)); err == nil {
		t.Fatal("task detail was allowed without the view permission")
	}
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_create_task", json.RawMessage(`{}`)); err == nil {
		t.Fatal("create task was allowed without the create permission")
	}

	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	names = map[string]bool{}
	for _, tool := range fixture.service.PetMCPTools(fixture.ctx) {
		names[tool.Name] = true
	}
	for _, required := range []string{"aha_task_detail", "aha_list_projects", "aha_create_task", "aha_start_task", "aha_create_project", "aha_create_workspace", "aha_send_task_message"} {
		if !names[required] {
			t.Fatalf("missing enabled tool %s: %v", required, names)
		}
	}
}

func TestPetModelCatalogIsRedacted(t *testing.T) {
	fixture := newPetTaskFixture(t)
	catalog, err := fixture.service.PetModelCatalog(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) == 0 || len(catalog.Providers) == 0 || len(catalog.EnvGroups) == 0 {
		t.Fatalf("catalog=%#v", catalog)
	}
	for _, provider := range catalog.Providers {
		if provider.ID == "" || provider.Name == "" {
			t.Fatalf("provider=%#v", provider)
		}
	}
}

func TestPetCreatesProjectWorkspaceAndTask(t *testing.T) {
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
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{
		Name: "宠物创建的项目", ProjectType: "folder", KnowledgePolicy: "enabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "宠物创建的 Workspace", Transport: "native", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "巡检发现的问题",
		Request: "请检查并修复这个问题", Start: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ProjectID != project.ID || task.WorkspaceID != workspace.ID {
		t.Fatalf("task=%#v", task)
	}
	waitForTask(t, fixture.database, task.ID, domain.TaskWaitingUser)
	page, err := fixture.database.ConversationPage(fixture.ctx, task.ID, 0, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	foundPetMessage := false
	for _, item := range page.Items {
		if item.Kind == "user_message" && item.AgentID == "pet" {
			foundPetMessage = true
			break
		}
	}
	if !foundPetMessage {
		t.Fatalf("pet first message is not visible in the target task: %#v", page.Items)
	}
}

func TestPetSendTaskMessageKeepsPetSource(t *testing.T) {
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
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "消息目标项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "消息目标 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "目标任务",
		Request: "等待宠物消息", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetSendTaskMessage(fixture.ctx, PetSendTaskMessageInput{
		TaskID: target.ID, Content: "这是宠物发来的内部消息",
	}); err != nil {
		t.Fatal(err)
	}
	page, err := fixture.database.ConversationPage(fixture.ctx, target.ID, 0, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		if item.Kind == "user_message" && item.AgentID == "pet" && item.Summary == "这是宠物发来的内部消息" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pet message not found: %#v", page.Items)
	}
}
