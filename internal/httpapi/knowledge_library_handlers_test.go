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

func TestKnowledgeLibraryAPIHidesContainersAndBindsWithoutMovingContent(t *testing.T) {
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
	now := time.Now().UTC()
	target := domain.Project{ID: "project-target", Name: "Target", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	libraryProject := domain.Project{ID: "project-library", Name: "Pending library", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateProject(ctx, libraryProject); err != nil {
		t.Fatal(err)
	}
	library, err := database.KnowledgeLibrary(ctx, "library_"+libraryProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, secretStore, app.StubExecutor{})}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/projects", nil, "")
	var projects map[string]any
	decodeResponse(t, response, &projects)
	if response.StatusCode != http.StatusOK || len(projects["projects"].([]any)) != 1 {
		t.Fatalf("knowledge container leaked into projects: status=%d payload=%#v", response.StatusCode, projects)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge/libraries", nil, "")
	var libraries map[string]any
	decodeResponse(t, response, &libraries)
	if response.StatusCode != http.StatusOK || len(libraries["libraries"].([]any)) != 1 {
		t.Fatalf("libraries status=%d payload=%#v", response.StatusCode, libraries)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge/libraries/"+library.ID+"/bind", map[string]any{"project_id": target.ID, "binding_mode": "project"}, csrf)
	var bound map[string]any
	decodeResponse(t, response, &bound)
	if response.StatusCode != http.StatusOK || bound["library"].(map[string]any)["bound_project_id"] != target.ID || bound["library"].(map[string]any)["binding_mode"] != "project" {
		t.Fatalf("bind status=%d payload=%#v", response.StatusCode, bound)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge/libraries/"+library.ID+"/unbind", map[string]any{}, csrf)
	var unbound map[string]any
	decodeResponse(t, response, &unbound)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unbind status=%d payload=%#v", response.StatusCode, unbound)
	}
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/knowledge/libraries/"+library.ID, nil, csrf)
	var deleted map[string]any
	decodeResponse(t, response, &deleted)
	if response.StatusCode != http.StatusOK || deleted["deleted"].(map[string]any)["knowledge"].(float64) != 1 {
		t.Fatalf("delete status=%d payload=%#v", response.StatusCode, deleted)
	}
}
