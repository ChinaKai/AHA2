package app

import "testing"

func TestPetSSHWorkspaceCreateAndUpdate(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
		AllowManageModels: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "SSH 项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "SSH Workspace", Transport: "ssh",
		RootPath: "/home/tester/project", SSHHost: "192.0.2.10", SSHUser: "tester",
		SSHPort: 22, SSHAuth: "password", SSHPassword: "secret-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Locality != "remote" || !workspace.SSHPasswordConfigured || workspace.SSHCredentialRef == "" {
		t.Fatalf("workspace=%#v", workspace)
	}
	if value, ok := fixture.service.secrets.Get(workspace.SSHCredentialRef); !ok || value != "secret-password" {
		t.Fatalf("ssh credential not stored")
	}
	name := "SSH Workspace 改名"
	updated, err := fixture.service.PetUpdateWorkspace(fixture.ctx, PetUpdateWorkspaceInput{
		WorkspaceID: workspace.ID, Name: &name, SSHPort: intPointer(2222),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != name || updated.SSHPort != 2222 {
		t.Fatalf("updated workspace=%#v", updated)
	}
	auth := "auto"
	if _, err := fixture.service.PetUpdateWorkspace(fixture.ctx, PetUpdateWorkspaceInput{
		WorkspaceID: workspace.ID, ClearSSHPassword: true, SSHAuth: &auth,
	}); err != nil {
		t.Fatal(err)
	}
}
