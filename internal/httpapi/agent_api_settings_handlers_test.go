package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

func TestAgentAPISettingsAndWorkspaceReverseProbe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	project := domain.Project{ID: "project-agent-api-settings", Name: "Project", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-agent-api-settings", ProjectID: project.ID, Name: "Native", Locality: "local", Transport: "native",
		RootPath: t.TempDir(), AgentAPIMode: "auto", AgentAPIStatus: "unknown", Health: "unknown", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, nil, app.StubExecutor{})
	capabilities := agentapi.NewCapabilities()
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService, DetectWorkspace: workspacepkg.Detect}).Handler())
	defer server.Close()
	appService.SetAgentAPI(capabilities, server.URL)
	client := newCookieClient(t)
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/agent-api", nil, "")
	var settings struct {
		AgentAPI domain.AgentAPISettings `json:"agent_api"`
	}
	decodeResponse(t, response, &settings)
	if response.StatusCode != http.StatusOK || settings.AgentAPI.StartupURL != server.URL || settings.AgentAPI.EffectiveURL != server.URL {
		t.Fatalf("settings status=%d body=%#v", response.StatusCode, settings)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/agent-api", map[string]any{"url": "http://192.0.2.10:8766", "allow_insecure": false}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsafe setting status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/agent-api", map[string]any{"url": server.URL, "allow_insecure": false}, csrf)
	decodeResponse(t, response, &settings)
	if response.StatusCode != http.StatusOK || settings.AgentAPI.EffectiveURL != server.URL {
		t.Fatalf("update status=%d body=%#v", response.StatusCode, settings)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/workspaces/"+workspace.ID+"/detect", nil, csrf)
	var detected struct {
		Workspace domain.Workspace `json:"workspace"`
	}
	decodeResponse(t, response, &detected)
	if response.StatusCode != http.StatusOK || detected.Workspace.AgentAPIStatus != "ready" || detected.Workspace.AgentAPIResolvedURL != server.URL {
		t.Fatalf("detect status=%d body=%#v", response.StatusCode, detected)
	}
	stored, err := database.Workspace(ctx, workspace.ID)
	if err != nil || stored.AgentAPIStatus != "ready" || stored.AgentAPIResolvedURL != server.URL {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
}
