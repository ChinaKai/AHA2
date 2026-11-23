package httpapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type onboardingTestExecutor struct {
	app.StubExecutor
	codex, missing string
}

func (e onboardingTestExecutor) BackendBinary(name string) string {
	if name == "codex" {
		return e.codex
	}
	return e.missing
}

func TestOnboardingHTTPManagedWorkspaceReadOnlyDraftAndPersistentDismissal(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "aha2.db")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executor := onboardingTestExecutor{codex: executable, missing: filepath.Join(dataDir, "missing-cli")}
	open := func() (*store.Store, *app.Service, *httptest.Server) {
		database, err := store.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		service := app.NewService(database, nil, executor)
		server := httptest.NewServer(New(Config{Store: database, App: service, Auth: auth.NewService(database, "setup-test", time.Hour)}).Handler())
		t.Cleanup(func() { server.Close(); _ = service.Shutdown(ctx); _ = database.Close() })
		return database, service, server
	}
	newClient := func() *http.Client {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Client{Jar: jar}
	}
	database, service, server := open()
	client := newClient()
	base := server.URL + "/api/v1/onboarding"
	response := requestJSON(t, client, http.MethodGet, base, nil, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous onboarding status = %d", response.StatusCode)
	}
	response.Body.Close()
	csrf := registerOwner(t, client, server.URL)
	read := func() app.OnboardingView {
		t.Helper()
		response := requestJSON(t, client, http.MethodGet, base, nil, "")
		var view app.OnboardingView
		decodeResponse(t, response, &view)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("onboarding status = %d", response.StatusCode)
		}
		return view
	}
	if view := read(); !view.ShowAutomatically || view.DismissedAt != nil || len(view.Models) != 0 ||
		len(view.Workspaces) != 0 || !view.BackendCLI["codex"] || view.BackendCLI["claude"] {
		t.Fatalf("unexpected fresh state: %+v", view)
	}

	response = requestJSON(t, client, http.MethodPost, base+"/workspace", map[string]string{"root_path": "relative/path"}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("relative server directory status = %d", response.StatusCode)
	}
	response.Body.Close()
	var added struct {
		Workspace domain.Workspace `json:"workspace"`
	}
	for attempt := 0; attempt < 2; attempt++ {
		response = requestJSON(t, client, http.MethodPost, base+"/workspace", map[string]string{"name": "Starter Workspace"}, csrf)
		var reply struct {
			Workspace domain.Workspace `json:"workspace"`
		}
		decodeResponse(t, response, &reply)
		if response.StatusCode != http.StatusOK || reply.Workspace.ID == "" {
			t.Fatalf("managed workspace: %d %+v", response.StatusCode, reply)
		}
		if attempt == 1 && added.Workspace.ID != reply.Workspace.ID {
			t.Fatal("the retry created a second workspace")
		}
		added = reply
	}
	workspace := added.Workspace
	readme, err := os.ReadFile(filepath.Join(workspace.RootPath, "README.md"))
	if err != nil || filepath.Dir(workspace.RootPath) != filepath.Join(dataDir, "starter-workspaces") || !strings.Contains(string(readme), workspace.ID) {
		t.Fatalf("managed workspace path/README = %q, %q, %v", workspace.RootPath, readme, err)
	}
	if _, err := database.KnowledgeRootForProject(ctx, workspace.ProjectID); err != nil {
		t.Fatalf("legacy knowledge root missing: %v", err)
	}

	payload := map[string]string{"workspace_id": workspace.ID, "model_id": "model", "title": "Read the workspace", "request": "Summarize README.md"}
	response = requestJSON(t, client, http.MethodPost, base+"/task", payload, csrf)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("no model task status = %d", response.StatusCode)
	}
	response.Body.Close()

	now := time.Now().UTC()
	provider := domain.Provider{ID: "provider", Name: "Provider", BaseURL: "http://127.0.0.1:1", CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{ID: "model", DisplayName: "Model", ProviderID: provider.ID, Source: domain.ModelSourceProvider,
		Backend: "claude", WireAPI: "anthropic_messages", WireModel: "example", CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodPost, base+"/task", payload, csrf)
	var missing struct{ Error string }
	decodeResponse(t, response, &missing)
	if response.StatusCode != http.StatusConflict || missing.Error != "onboarding_cli_missing" {
		t.Fatalf("missing CLI status = %d, %+v", response.StatusCode, missing)
	}
	model.Backend, model.WireAPI = "codex", "responses"
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	if view := read(); !view.ShowAutomatically || len(view.Models) != 1 || len(view.Workspaces) != 1 {
		t.Fatalf("after model and workspace: %+v", view)
	}
	var taskID string
	for attempt := 0; attempt < 2; attempt++ {
		response = requestJSON(t, client, http.MethodPost, base+"/task", payload, csrf)
		var reply struct{ Task domain.Task }
		decodeResponse(t, response, &reply)
		if response.StatusCode != http.StatusOK || reply.Task.ID == "" || reply.Task.Status != domain.TaskDraft ||
			reply.Task.Isolation != "inplace" || reply.Task.CollaborationMode != "single" || reply.Task.MaxAgents != 1 {
			t.Fatalf("read-only task: %d %+v", response.StatusCode, reply)
		}
		if attempt == 1 && taskID != reply.Task.ID {
			t.Fatal("the retry created a second task")
		}
		taskID = reply.Task.ID
	}
	agents, err := service.TaskAgents(ctx, taskID)
	if err != nil || len(agents) != 1 || agents[0].Filesystem != "read-only" || agents[0].Approval != "never" {
		t.Fatalf("runtime permission readback = %+v, %v", agents, err)
	}
	if view := read(); view.TaskID != taskID || view.ShowAutomatically || view.DismissedAt != nil {
		t.Fatalf("after creating verified task: %+v", view)
	}
	if _, err := service.UpdateAgentConfig(ctx, taskID, "main", app.UpdateAgentConfigInput{Filesystem: "workspace-write"}); err != nil {
		t.Fatal(err)
	}
	if view := read(); view.TaskID != "" || view.TaskError != "onboarding_permissions_mismatch" {
		t.Fatalf("permission mismatch was not reported: %+v", view)
	}

	response = requestJSON(t, client, http.MethodPut, base+"/dismiss", nil, "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("dismissal without CSRF status = %d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPut, base+"/dismiss", nil, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("dismissal = %d", response.StatusCode)
	}
	response.Body.Close()
	firstDismissed := read().DismissedAt
	if firstDismissed == nil {
		t.Fatal("dismissal was not persisted")
	}
	server.Close()
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, server = open()
	client = newClient()
	base = server.URL + "/api/v1/onboarding"
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]string{"username": "owner", "password": "correct-horse-battery"}, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login after restart = %d", response.StatusCode)
	}
	response.Body.Close()
	if view := read(); view.ShowAutomatically || view.DismissedAt == nil || !view.DismissedAt.Equal(*firstDismissed) {
		t.Fatalf("dismissal after restart/new browser: %+v", view)
	}
}
