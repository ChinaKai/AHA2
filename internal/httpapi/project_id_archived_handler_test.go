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
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

// A project row can arrive through sync/import without Service.CreateProject.
// Retrying its knowledge space must surface the archived-ID conflict as 409,
// not as an opaque 500.
func TestRetryKnowledgeSpaceForArchivedProjectIDReturnsConflict(t *testing.T) {
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
	vaultConfig := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(vaultConfig, knowledgevault.InitOptions{VaultName: "HTTP"}); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(vaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	appService.SetKnowledgeVault(manager, true)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	now := time.Now().UTC()
	project, err := appService.CreateProject(ctx, domain.Project{
		ID: "p529http", Name: "HTTP Reuse", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgevault.ArchiveProjectSpace(vaultConfig, project.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProjectWithSyncTombstone(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateProject(ctx, domain.Project{
		ID: project.ID, Name: "Sync Replay", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, client, http.MethodPost,
		server.URL+"/api/v1/projects/"+project.ID+"/knowledge-space", nil, csrf)
	var body map[string]any
	decodeResponse(t, response, &body)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("archived project id status = %d body=%v", response.StatusCode, body)
	}
	if body["error"] != "project_id_archived" || body["message"] == "" {
		t.Fatalf("archived project id body = %v", body)
	}
}
