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

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

func TestWorkspaceDetectionFailurePersistsFreshStructuredResult(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-detect-error", Name: "Detect", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspaceItem := domain.Workspace{
		ID: "workspace-detect-error", ProjectID: project.ID, Name: "Missing", Locality: "local", Transport: "native",
		RootPath: filepath.Join(t.TempDir(), "missing"), Platform: "stale-platform", Health: "ready",
		Capabilities: map[string]any{"codex": map[string]any{"status": "ready"}}, Repository: map[string]any{"is_git": true},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspaceItem); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}), DetectWorkspace: workspacepkg.Detect,
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+workspaceItem.ID+"/detect", nil, csrf)
	defer response.Body.Close()
	var body struct {
		Error     string           `json:"error"`
		Workspace domain.Workspace `json:"workspace"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway || body.Error != "workspace_unavailable" {
		t.Fatalf("status=%d error=%q", response.StatusCode, body.Error)
	}
	if body.Workspace.Platform != "" || body.Workspace.Health != "error" || len(body.Workspace.Repository) != 0 {
		t.Fatalf("response retained stale detection: %#v", body.Workspace)
	}
	stored, err := database.Workspace(ctx, workspaceItem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Platform != "" || stored.Health != "error" || len(stored.Repository) != 0 {
		t.Fatalf("store retained stale detection: %#v", stored)
	}
	probe, ok := stored.Capabilities["workspace"].(map[string]any)
	if !ok || probe["status"] != "unavailable" || probe["error"] == "" {
		t.Fatalf("structured workspace error missing: %#v", stored.Capabilities)
	}
}
