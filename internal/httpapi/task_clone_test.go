package httpapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestCreateTaskClonesLocalSourceHardware(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authServiceForTest(database), App: appService,
		Secrets: secretStore, AgentCapabilities: agentapi.NewCapabilities(),
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	now := time.Now().UTC()
	project := domain.Project{ID: "clone-http-project", Name: "Clone", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "clone-http-workspace", ProjectID: project.ID, Name: "Local", Locality: "local",
		Transport: "native", RootPath: dataDir, Health: "ready", AgentAPIMode: "global",
		AgentAPIStatus: "unknown", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{
		ID: "clone-http-env", Name: "Clone", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "clone-http-model", DisplayName: "Model", ProviderID: "stub", Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	source, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Source", Request: "source",
		Isolation: "inplace", Backend: model.Backend, ModelID: model.ID, WireModel: model.WireModel,
		Filesystem: "workspace-write", Approval: "never", CollaborationMode: "single", MaxAgents: 1,
		StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	credentialRef := "hardware/" + source.ID + "/console/credential"
	if err := secretStore.PutMany(map[string]string{credentialRef: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := database.ReplaceHardwareGroups(ctx, source.ID, []domain.HardwareGroup{{
		TaskID: source.ID, ID: "console", Position: 0, Description: "Console",
		Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{
			Host: "192.0.2.10", Port: 22, Protocol: domain.HardwareProtocolSSH, SSHAuth: domain.HardwareSSHAuthPassword,
		},
		Username: "root", CredentialRef: credentialRef, PasswordConfigured: true,
		Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/task-clone-sources", nil, "")
	var sources struct {
		Tasks []domain.Task `json:"tasks"`
	}
	decodeResponse(t, response, &sources)
	if response.StatusCode != http.StatusOK || len(sources.Tasks) != 1 || sources.Tasks[0].ID != source.ID {
		t.Fatalf("clone sources status=%d payload=%#v", response.StatusCode, sources)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks", map[string]any{
		"title": "Target", "request": "target", "project_id": project.ID, "workspace_id": workspace.ID,
		"isolation": "inplace", "backend": model.Backend, "model_source": domain.ModelSourceProvider,
		"model_id": model.ID, "wire_model": model.WireModel, "filesystem": "workspace-write",
		"approval": "never", "collaboration_mode": "single", "max_agents": 1,
		"start_mode": "manual", "source_task_id": source.ID, "clone_hardware": true,
	}, csrf)
	var created struct {
		Task          domain.Task `json:"task"`
		CloneWarnings []string    `json:"clone_warnings"`
	}
	decodeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated || created.Task.ID == "" || len(created.CloneWarnings) != 0 {
		t.Fatalf("create cloned task status=%d payload=%#v", response.StatusCode, created)
	}
	groups, err := database.HardwareGroups(ctx, created.Task.ID)
	if err != nil || len(groups) != 1 || groups[0].TaskID != created.Task.ID {
		t.Fatalf("target hardware groups=%#v err=%v", groups, err)
	}
	if groups[0].ResourceID == "" || groups[0].CredentialRef != credentialRef {
		t.Fatalf("target hardware did not reuse system resource: %#v", groups[0])
	}
	if value, ok := secretStore.Get(groups[0].CredentialRef); !ok || value != "secret" {
		t.Fatalf("shared resource credential missing value=%q ok=%v", value, ok)
	}
}
