package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func TestRemoteConfigurationIsReadOnlyUntilExplicitTakeover(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "takeover-project", Name: "Takeover", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	remoteWorkspace := domain.Workspace{
		ID: "remote-workspace", ProjectID: project.ID, Name: "Remote SSH", Locality: "remote", Transport: "ssh",
		SSHHost: "remote.example", SSHUser: "builder", SSHPort: 22, SSHAuth: "password", SSHPasswordConfigured: true,
		OwnerDeviceID: "device-remote", ReadOnly: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertSyncedWorkspaceFromSource(ctx, remoteWorkspace, "source-workspace"); err != nil {
		t.Fatal(err)
	}
	remoteTask := domain.Task{ID: "source-task", ProjectID: project.ID, WorkspaceID: "source-workspace", Title: "Remote task", OriginalRequest: "continue work", Status: domain.TaskWaitingUser, CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "inherit", OwnerDeviceID: "device-remote", ReadOnly: true, CreatedAt: now, UpdatedAt: now}
	taskPayload, _ := json.Marshal(remoteTask)
	if err := database.UpsertRemoteTaskObject(ctx, store.RemoteTaskObject{OwnerDeviceID: "device-remote", ObjectType: "task", ObjectID: remoteTask.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: taskPayload, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	remoteHardware := domain.HardwareGroup{
		TaskID: remoteTask.ID, ID: "board", Description: "Remote board", Mode: domain.HardwareModeBoth,
		Serial:   domain.HardwareSerialConfig{Device: "/dev/cu.remote", Baudrate: 115200},
		Network:  domain.HardwareNetworkConfig{Host: "board.remote", Port: 22, Protocol: domain.HardwareProtocolSSH, SSHAuth: domain.HardwareSSHAuthPassword},
		Username: "root", PasswordConfigured: true, Access: domain.HardwareAccessReadOnly, CreatedAt: now, UpdatedAt: now,
	}
	hardwarePayload, _ := json.Marshal(remoteHardware)
	if err := database.UpsertRemoteTaskObject(ctx, store.RemoteTaskObject{OwnerDeviceID: "device-remote", ObjectType: "hardware", ObjectID: "device-remote:source-task:board", TaskID: remoteTask.ID, ProjectID: project.ID, Payload: hardwarePayload, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	mirrors, err := database.RemoteTaskMirrors(ctx, project.ID)
	if err != nil || len(mirrors) != 1 {
		t.Fatalf("remote mirror=%#v err=%v", mirrors, err)
	}
	publicTaskID := mirrors[0].Task.ID
	env := domain.EnvGroup{ID: "takeover-env", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "takeover-model", DisplayName: "Model", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	secretStore := &fakeSecretStore{}
	if err := secretStore.PutMany(map[string]string{
		syncer.MirrorWorkspaceSecretRef(remoteWorkspace.ID):                               "synced-workspace-password",
		syncer.MirrorHardwareSecretRef("device-remote", remoteTask.ID, remoteHardware.ID): "synced-hardware-password",
	}); err != nil {
		t.Fatal(err)
	}
	capabilities := agentapi.NewCapabilities()
	appService := app.NewService(database, nil, app.StubExecutor{})
	server := httptest.NewServer(New(Config{Store: database, Auth: authServiceForTest(database), App: appService, Secrets: secretStore, AgentCapabilities: capabilities}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+remoteWorkspace.ID+"/takeover", map[string]any{"transport": "ssh"}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing local root status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+remoteWorkspace.ID+"/takeover", map[string]any{
		"transport": "ssh", "root_path": t.TempDir(), "ssh_host": "local.example", "ssh_user": "local", "ssh_auth": "password",
	}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing local SSH credential status=%d", response.StatusCode)
	}
	response.Body.Close()
	localRoot := t.TempDir()
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+remoteWorkspace.ID+"/takeover", map[string]any{
		"name": "Local SSH", "locality": "local", "transport": "ssh", "root_path": localRoot,
		"ssh_host": "local.example", "ssh_user": "local", "ssh_port": 2222, "ssh_auth": "password", "reuse_remote_credential": true,
	}, csrf)
	var workspaceResponse struct {
		Workspace domain.Workspace `json:"workspace"`
	}
	decodeResponse(t, response, &workspaceResponse)
	if response.StatusCode != http.StatusCreated || workspaceResponse.Workspace.ID == remoteWorkspace.ID || workspaceResponse.Workspace.ReadOnly || workspaceResponse.Workspace.RootPath != localRoot {
		t.Fatalf("workspace takeover status=%d payload=%#v", response.StatusCode, workspaceResponse)
	}
	localWorkspace := workspaceResponse.Workspace
	if value, ok := secretStore.Get(workspaceSSHCredentialRef(localWorkspace.ID)); !ok || value != "synced-workspace-password" {
		t.Fatal("local SSH credential was not stored separately")
	}
	if original, err := database.Workspace(ctx, remoteWorkspace.ID); err != nil || !original.ReadOnly || original.RootPath != "" || original.SSHHost != "remote.example" {
		t.Fatalf("remote workspace was overwritten: %#v err=%v", original, err)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+localWorkspace.ID+"/takeover", map[string]any{"transport": "native", "root_path": t.TempDir()}, csrf)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("local workspace takeover status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+remoteWorkspace.ID+"/takeover", map[string]any{"transport": "native", "root_path": t.TempDir(), "sync_token": "must-never-be-accepted"}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("takeover accepted sync token field: status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+publicTaskID, nil, "")
	var detail struct {
		Task     domain.Task            `json:"task"`
		Hardware []domain.HardwareGroup `json:"hardware"`
	}
	decodeResponse(t, response, &detail)
	if response.StatusCode != http.StatusOK || !detail.Task.ReadOnly || len(detail.Hardware) != 1 || detail.Hardware[0].Access != domain.HardwareAccessReadOnly || detail.Hardware[0].CredentialRef != "" {
		t.Fatalf("remote task detail status=%d payload=%#v", response.StatusCode, detail)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+publicTaskID+"/hardware", nil, "")
	var remoteGroups struct {
		Groups   []domain.HardwareGroup `json:"groups"`
		ReadOnly bool                   `json:"read_only"`
	}
	decodeResponse(t, response, &remoteGroups)
	if response.StatusCode != http.StatusOK || !remoteGroups.ReadOnly || len(remoteGroups.Groups) != 1 {
		t.Fatalf("remote hardware list status=%d payload=%#v", response.StatusCode, remoteGroups)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+publicTaskID+"/hardware", map[string]any{"groups": []any{}}, csrf)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("remote hardware update status=%d", response.StatusCode)
	}
	response.Body.Close()
	for _, endpoint := range []string{"connect", "send"} {
		response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+publicTaskID+"/hardware/board/"+endpoint+"?transport=serial", map[string]any{"data": "x", "encoding": "text"}, csrf)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("remote hardware %s status=%d", endpoint, response.StatusCode)
		}
		response.Body.Close()
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+publicTaskID+"/messages", map[string]any{"content": "must not run"}, csrf)
	if response.StatusCode == http.StatusAccepted {
		t.Fatal("remote task accepted execution")
	}
	response.Body.Close()
	token, err := capabilities.Issue(publicTaskID, "main", "remote-turn", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/hardware/board/login?transport=serial", http.MethodPost, token, map[string]any{})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("remote agent hardware login status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+publicTaskID+"/takeover", map[string]any{
		"workspace_id": localWorkspace.ID, "model_id": model.ID, "collaboration_mode": "single", "groups": []any{},
	}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing local hardware confirmation status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+publicTaskID+"/takeover", map[string]any{
		"workspace_id": localWorkspace.ID, "model_id": model.ID, "collaboration_mode": "single",
		"groups": []map[string]any{{"id": "board", "mode": "both", "serial": map[string]any{"device": ""}, "network": map[string]any{"host": "192.0.2.9"}, "password": "local-board-password"}},
	}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing local serial confirmation status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+publicTaskID+"/takeover", map[string]any{
		"workspace_id": localWorkspace.ID, "model_id": model.ID, "collaboration_mode": "single",
		"groups": []map[string]any{{"id": "board", "mode": "both", "serial": map[string]any{"device": "COM9", "baudrate": 115200}, "network": map[string]any{"host": "192.0.2.9", "port": 22, "protocol": "ssh", "ssh_auth": "password"}, "username": "local-root", "reuse_remote_credential": true}},
	}, csrf)
	var taskResponse struct {
		Task     domain.Task            `json:"task"`
		Hardware []domain.HardwareGroup `json:"hardware"`
	}
	decodeResponse(t, response, &taskResponse)
	if response.StatusCode != http.StatusCreated || taskResponse.Task.ID == publicTaskID || taskResponse.Task.ReadOnly || taskResponse.Task.WorkspaceID != localWorkspace.ID || len(taskResponse.Hardware) != 1 {
		t.Fatalf("task takeover status=%d payload=%#v", response.StatusCode, taskResponse)
	}
	localHardware, err := database.HardwareGroup(ctx, taskResponse.Task.ID, "board")
	if err != nil || localHardware.Serial.Device != "COM9" || localHardware.Network.Host != "192.0.2.9" || localHardware.Access != domain.HardwareAccessReadWrite || localHardware.CredentialRef == "" {
		t.Fatalf("local hardware takeover=%#v err=%v", localHardware, err)
	}
	if value, ok := secretStore.Get(localHardware.CredentialRef); !ok || value != "synced-hardware-password" {
		t.Fatal("local hardware credential was not stored separately")
	}
	remoteAfter, err := database.RemoteTaskMirror(ctx, publicTaskID)
	if err != nil || remoteAfter.Hardware[0].Serial.Device != "/dev/cu.remote" || remoteAfter.Hardware[0].Access != domain.HardwareAccessReadOnly {
		t.Fatalf("remote task was overwritten during takeover: %#v err=%v", remoteAfter, err)
	}
}

func authServiceForTest(database *store.Store) *auth.Service {
	return auth.NewService(database, "setup-test", time.Hour)
}
