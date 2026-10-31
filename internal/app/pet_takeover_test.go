package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardwareconfig"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func TestPetTakeoverRemoteTaskAndWorkspace(t *testing.T) {
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
	now := time.Now().UTC()
	project := domain.Project{
		ID: "project-takeover", Name: "接管项目", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	localWorkspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "本机", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	remoteWorkspace := domain.Workspace{
		ID: "workspace-remote", ProjectID: project.ID, Name: "远端", Locality: "remote",
		Transport: "ssh", RootPath: "/srv/app", SSHHost: "10.0.0.8", SSHUser: "root", SSHPort: 22,
		SSHAuth: "password", SSHPasswordConfigured: true, OwnerDeviceID: "device-remote",
		ReadOnly: true, SourceWorkspaceID: "source-workspace", Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, remoteWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.secrets.PutMany(map[string]string{
		syncer.MirrorWorkspaceSecretRef(remoteWorkspace.ID): "workspace-pass",
	}); err != nil {
		t.Fatal(err)
	}
	takenWorkspace, err := fixture.service.PetTakeoverRemoteWorkspace(fixture.ctx, PetTakeoverWorkspaceInput{
		WorkspaceID: remoteWorkspace.ID, Transport: "ssh", RootPath: "/srv/app",
		SSHHost: "10.0.0.8", SSHUser: "root", SSHPort: 22, SSHAuth: "password",
		ReuseRemoteCredential: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if takenWorkspace.ReadOnly || takenWorkspace.SourceWorkspaceID != "" || takenWorkspace.ProjectID != project.ID {
		t.Fatalf("taken workspace=%#v", takenWorkspace)
	}
	if password, ok := fixture.service.secrets.Get(takenWorkspace.SSHCredentialRef); !ok || password != "workspace-pass" {
		t.Fatalf("taken workspace credential=%q ok=%v", password, ok)
	}

	remoteTask := domain.Task{
		ID: "remote-task-1", ProjectID: project.ID, WorkspaceID: remoteWorkspace.ID,
		Title: "远端任务", OriginalRequest: "修复远端问题", Status: domain.TaskWaitingUser,
		CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: project.KnowledgePolicy,
		CreatedAt: now, UpdatedAt: now,
	}
	taskPayload, _ := json.Marshal(remoteTask)
	remoteHardware := domain.HardwareGroup{
		TaskID: remoteTask.ID, ID: "board", Description: "板卡", Mode: domain.HardwareModeNetwork,
		Network: domain.HardwareNetworkConfig{
			Host: "10.0.0.5", Port: 22, Protocol: domain.HardwareProtocolSSH, SSHAuth: domain.HardwareSSHAuthPassword,
		},
		Username: "root", PasswordConfigured: true, Access: domain.HardwareAccessReadOnly,
		CreatedAt: now, UpdatedAt: now,
	}
	hardwarePayload, _ := json.Marshal(remoteHardware)
	for _, item := range []store.RemoteTaskObject{
		{OwnerDeviceID: "device-remote", ObjectType: "task", ObjectID: remoteTask.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: taskPayload},
		{OwnerDeviceID: "device-remote", ObjectType: "hardware", ObjectID: remoteHardware.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: hardwarePayload},
	} {
		if err := fixture.database.UpsertRemoteTaskObject(fixture.ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.service.secrets.PutMany(map[string]string{
		syncer.MirrorHardwareSecretRef("device-remote", remoteTask.ID, remoteHardware.ID): "board-pass",
	}); err != nil {
		t.Fatal(err)
	}
	mirrors, err := fixture.service.PetRemoteTaskMirrors(fixture.ctx, project.ID)
	if err != nil || len(mirrors) != 1 {
		t.Fatalf("mirrors=%#v err=%v", mirrors, err)
	}
	detail, err := fixture.service.PetTaskDetail(fixture.ctx, mirrors[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.ReadOnly || detail.OwnerDeviceID != "device-remote" || len(detail.Hardware) != 1 || !detail.Hardware[0].PasswordConfigured {
		t.Fatalf("detail=%#v", detail)
	}
	takenTask, groups, err := fixture.service.PetTakeoverRemoteTask(fixture.ctx, PetTakeoverTaskInput{
		TaskID: mirrors[0].ID, WorkspaceID: localWorkspace.ID, ModelID: fixture.model.ID,
		Groups: []hardwareconfig.GroupPayload{{
			ID: "board", ReuseRemoteCredential: true,
			Network: domain.HardwareNetworkConfig{
				Host: "10.0.0.5", Port: 22, Protocol: domain.HardwareProtocolSSH, SSHAuth: domain.HardwareSSHAuthPassword,
			},
			Username: "root",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if takenTask.ProjectID != project.ID || takenTask.WorkspaceID != localWorkspace.ID || takenTask.OriginalRequest != remoteTask.OriginalRequest {
		t.Fatalf("taken task=%#v", takenTask)
	}
	if len(groups) != 1 || groups[0].ID != "board" || !groups[0].PasswordConfigured || groups[0].Access != domain.HardwareAccessReadWrite {
		t.Fatalf("taken hardware=%#v", groups)
	}
	if password, ok := fixture.service.secrets.Get(groups[0].CredentialRef); !ok || password != "board-pass" {
		t.Fatalf("taken hardware credential=%q ok=%v", password, ok)
	}
	if _, err := fixture.service.PetRetireRemoteTaskMirror(fixture.ctx, mirrors[0].ID); err != nil {
		t.Fatal(err)
	}
	if mirrors, err := fixture.service.PetRemoteTaskMirrors(fixture.ctx, project.ID); err != nil || len(mirrors) != 0 {
		t.Fatalf("mirrors after retire=%#v err=%v", mirrors, err)
	}
}
