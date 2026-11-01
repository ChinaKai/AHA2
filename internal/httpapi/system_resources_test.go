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

func TestSystemManagedResourcesAreReadOnly(t *testing.T) {
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
	project := domain.Project{
		ID: "system-project", Name: "System", ProjectType: "system",
		KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now,
	}
	workspace := domain.Workspace{
		ID: "system-workspace", ProjectID: project.ID, Name: "system-workspace",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		AgentAPIMode: "embedded", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks", map[string]any{
		"project_id": project.ID, "workspace_id": workspace.ID, "title": "blocked", "request": "blocked",
	}, csrf)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("system task create=%d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/projects/"+project.ID, map[string]any{
		"name": "Changed", "project_type": "folder",
	}, csrf)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("system project update=%d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/projects/"+project.ID, nil, csrf)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("system project delete=%d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces", map[string]any{
		"project_id": project.ID, "name": "new", "root_path": t.TempDir(),
	}, csrf)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("system workspace create=%d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/workspaces/"+workspace.ID, map[string]any{
		"name": "Changed", "root_path": t.TempDir(),
	}, csrf)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("system workspace update=%d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/workspaces/"+workspace.ID, nil, csrf)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("system workspace delete=%d", response.StatusCode)
	}
	response.Body.Close()
}
