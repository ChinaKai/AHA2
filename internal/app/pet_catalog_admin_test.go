package app

import (
	"strings"
	"testing"
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
