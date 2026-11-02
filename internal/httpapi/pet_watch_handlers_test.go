package httpapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestPetWatchAPIListIgnoreAndRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	now := time.Now().UTC()
	project := domain.Project{ID: "project-watch", Name: "Watch", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-watch", ProjectID: project.ID, Name: "Watch", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	envGroup := domain.EnvGroup{
		ID: "env-watch", Name: "Watch", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, envGroup); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model-watch", DisplayName: "Stub", ProviderID: "stub", Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "runtime-watch", WorkspaceID: workspace.ID, Backend: "stub",
		ModelID: model.ID, WireModel: "stub", EnvGroupID: envGroup.ID,
		EnvGroupRevision: 1, PermissionsJSON: "{}", CreatedAt: now,
	}
	if err := database.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{
		ID: "task-watch", ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Watched task", OriginalRequest: "request", CurrentGoal: "request",
		Status: domain.TaskActive, RuntimeConfigSnapshotID: snapshot.ID,
		CollaborationMode: "single", MaxAgents: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/workbench", nil, "")
	var workbench struct {
		Watches []struct {
			TaskID  string `json:"task_id"`
			Watched bool   `json:"watched"`
			Ignored bool   `json:"ignored"`
		} `json:"watches"`
	}
	decodeResponse(t, response, &workbench)
	if response.StatusCode != http.StatusOK || len(workbench.Watches) != 1 ||
		workbench.Watches[0].TaskID != task.ID || workbench.Watches[0].Watched || workbench.Watches[0].Ignored {
		t.Fatalf("workbench=%d %#v", response.StatusCode, workbench)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/watches/"+task.ID, nil, csrf)
	var followed struct {
		Watch struct {
			TaskID string `json:"task_id"`
		} `json:"watch"`
	}
	decodeResponse(t, response, &followed)
	if response.StatusCode != http.StatusOK || followed.Watch.TaskID != task.ID {
		t.Fatalf("follow=%d %#v", response.StatusCode, followed)
	}

	response = requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/pet/watches/"+task.ID,
		map[string]any{"ignored": true}, csrf)
	var ignored struct {
		Watch struct {
			Ignored bool `json:"ignored"`
		} `json:"watch"`
	}
	decodeResponse(t, response, &ignored)
	if response.StatusCode != http.StatusOK || !ignored.Watch.Ignored {
		t.Fatalf("ignore=%d %#v", response.StatusCode, ignored)
	}

	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/pet/watches/"+task.ID, nil, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("remove=%d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/workbench", nil, "")
	decodeResponse(t, response, &workbench)
	if len(workbench.Watches) != 1 || workbench.Watches[0].TaskID != task.ID || workbench.Watches[0].Watched {
		t.Fatalf("watches after remove=%#v", workbench.Watches)
	}
}
