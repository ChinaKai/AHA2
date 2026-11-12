package app

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetProjectWorkspaceAdmin(t *testing.T) {
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
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "待管理项目"})
	if err != nil {
		t.Fatal(err)
	}
	name := "已改名项目"
	description := "更新后的描述"
	policy := "disabled"
	updatedProject, err := fixture.service.PetUpdateProject(fixture.ctx, PetUpdateProjectInput{
		ProjectID: project.ID, Name: &name, Description: &description, KnowledgePolicy: &policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updatedProject.Name != name || updatedProject.Description != description || updatedProject.KnowledgePolicy != policy {
		t.Fatalf("updated project=%#v", updatedProject)
	}

	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "待管理 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	workspaceName := "已改名 Workspace"
	updatedWorkspace, err := fixture.service.PetUpdateWorkspace(fixture.ctx, PetUpdateWorkspaceInput{
		WorkspaceID: workspace.ID, Name: &workspaceName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updatedWorkspace.Name != workspaceName {
		t.Fatalf("updated workspace=%#v", updatedWorkspace)
	}
	if _, err := fixture.service.PetDeleteWorkspace(fixture.ctx, workspace.ID); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("delete workspace without reason was accepted: %v", err)
	}
	workspaceApproval, err := fixture.service.PetDeleteWorkspace(fixture.ctx, workspace.ID, "清理测试工作区需要")
	if err != nil {
		t.Fatal(err)
	}
	if workspaceApproval.Status != petApprovalPending {
		t.Fatalf("workspace approval=%#v", workspaceApproval)
	}
	if _, err := fixture.database.Workspace(fixture.ctx, workspace.ID); err != nil {
		t.Fatal("workspace was deleted before approval")
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, workspaceApproval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Workspace(fixture.ctx, workspace.ID); err == nil {
		t.Fatal("workspace still exists after approval")
	}
	if _, err := fixture.service.PetDeleteProject(fixture.ctx, project.ID); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("delete project without reason was accepted: %v", err)
	}
	projectApproval, err := fixture.service.PetDeleteProject(fixture.ctx, project.ID, "清理测试项目需要")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, projectApproval.ID, "reject", "该项目仍需要保留"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Project(fixture.ctx, project.ID); err != nil {
		t.Fatal("project was deleted after rejection")
	}
	projectApproval, err = fixture.service.PetDeleteProject(fixture.ctx, project.ID, "清理测试项目需要")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, projectApproval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Project(fixture.ctx, project.ID); err == nil {
		t.Fatal("project still exists after approval")
	}
}

func TestPetDeleteWorkspaceRefusesActiveTurn(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowCreateTasks: boolPointer(true), AllowCreateWorkspaces: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "执行中项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "执行中 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "执行中任务", Request: "执行中",
		ModelID: fixture.model.ID, Isolation: "inplace", Filesystem: "workspace-write",
		Approval: "never", CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := fixture.service.now().UTC()
	message := domain.Message{ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner", Content: "开始", CreatedAt: now}
	if err := fixture.database.AddMessage(fixture.ctx, message); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateTurn(fixture.ctx, domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, AgentID: "main", Sequence: 1,
		InputMessageID: message.ID, Status: domain.TurnRunning,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, QueuedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	approval, err := fixture.service.PetDeleteWorkspace(fixture.ctx, workspace.ID, "清理执行中的测试工作区")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err == nil || !strings.Contains(err.Error(), "执行中") {
		t.Fatalf("approving deletion with an active turn: err=%v", err)
	}
	if _, err := fixture.database.Workspace(fixture.ctx, workspace.ID); err != nil {
		t.Fatalf("workspace was deleted while a turn was running: %v", err)
	}
	if _, err := fixture.database.Task(fixture.ctx, task.ID); err != nil {
		t.Fatalf("task was deleted while its turn was running: %v", err)
	}
}
