package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestAuthenticationAndCSRF(t *testing.T) {
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

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/projects", nil, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/register", map[string]any{
		"setup_token": "setup-test", "username": "owner", "password": "correct-horse-battery",
	}, "")
	var registration map[string]any
	decodeResponse(t, response, &registration)
	csrf, _ := registration["csrf_token"].(string)
	if response.StatusCode != http.StatusCreated || csrf == "" {
		t.Fatalf("registration failed: status=%d body=%v", response.StatusCode, registration)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/projects", map[string]any{"name": "AHA2"}, "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected csrf rejection, got %d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/projects", map[string]any{"name": "AHA2"}, csrf)
	var project map[string]any
	decodeResponse(t, response, &project)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create project failed: %d %v", response.StatusCode, project)
	}
}

func TestCatalogProjectTypeAndManualModels(t *testing.T) {
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

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/projects", map[string]any{"name": "Folder"}, csrf)
	var project map[string]any
	decodeResponse(t, response, &project)
	if response.StatusCode != http.StatusCreated || project["project"].(map[string]any)["project_type"] != "folder" {
		t.Fatalf("folder project failed: %d %v", response.StatusCode, project)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/projects", map[string]any{
		"name": "Repo", "project_type": "git", "repository_identity": "https://example.com/r.git",
	}, csrf)
	decodeResponse(t, response, &project)
	if response.StatusCode != http.StatusCreated || project["project"].(map[string]any)["project_type"] != "git" {
		t.Fatalf("git project failed: %d %v", response.StatusCode, project)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/projects", map[string]any{"name": "Bad", "project_type": "svn"}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid project type rejection, got %d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/env-groups", map[string]any{
		"name": "Codex Anthropic", "provider_id": "anthropic", "backend": "codex",
		"environment": map[string]string{"OPENAI_MODEL": "claude-opus-5"},
	}, csrf)
	var envGroup map[string]any
	decodeResponse(t, response, &envGroup)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("env group creation failed: %d %v", response.StatusCode, envGroup)
	}
	envGroupID := envGroup["env_group"].(map[string]any)["id"].(string)

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/models", map[string]any{
		"display_name": "Opus", "provider_id": "anthropic", "backend": "codex", "wire_model": "claude-opus-5",
		"default_env_group_id": envGroupID,
	}, csrf)
	var model map[string]any
	decodeResponse(t, response, &model)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("model creation failed: %d %v", response.StatusCode, model)
	}
	if _, present := model["model"].(map[string]any)["default_env_group_id"]; present {
		t.Fatalf("default_env_group_id leaked to browser: %v", model)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/models", map[string]any{
		"display_name": "Bad", "provider_id": "x", "backend": "codex", "wire_model": "x",
		"default_env_group_id": "env_missing",
	}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected missing env group rejection, got %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestTaskAgentAPIIsolationAndConfigInheritance(t *testing.T) {
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
	project := domain.Project{ID: "project-agent-api", Name: "Agents", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-agent-api", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	env1 := domain.EnvGroup{
		ID: "env-agent-api-1", Name: "env 1", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	env2 := env1
	env2.ID = "env-agent-api-2"
	env2.Name = "env 2"
	model1 := domain.Model{
		ID: "model-agent-api-1", DisplayName: "Model 1", ProviderID: "stub", Backend: "stub",
		WireModel: "stub-1", DefaultEnvGroupID: env1.ID, CreatedAt: now, UpdatedAt: now,
	}
	model2 := model1
	model2.ID = "model-agent-api-2"
	model2.DisplayName = "Model 2"
	model2.WireModel = "stub-2"
	model2.DefaultEnvGroupID = env2.ID
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, env1) },
		func() error { return database.UpsertEnvGroup(ctx, env2) },
		func() error { return database.UpsertModel(ctx, model1) },
		func() error { return database.UpsertModel(ctx, model2) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	task, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "agent api", Request: "test routing",
		ModelID: model1.ID, CollaborationMode: "auto", MaxAgents: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpsertTaskAgent(ctx, domain.TaskAgent{
		TaskID: task.ID, AgentID: "sub-001", Role: "sub", Status: "idle", Title: "Child",
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InheritMain: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []domain.ConversationItem{
		{
			ID: domain.NewID("conversation"), TaskID: task.ID, AgentID: "main", StreamAgentID: "main",
			Category: "chat", Kind: "agent_message", Summary: "main only", CreatedAt: now,
		},
		{
			ID: domain.NewID("conversation"), TaskID: task.ID, AgentID: "sub-001", StreamAgentID: "sub-001",
			Category: "chat", Kind: "agent_message", Summary: "child only", CreatedAt: now,
		},
	} {
		if _, err := database.AddConversationItem(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID, nil, "")
	var taskDetail map[string]any
	decodeResponse(t, response, &taskDetail)
	memory := taskDetail["memory"].(map[string]any)
	if memory["current_goal"] != "test routing" {
		t.Fatalf("task detail memory missing: %#v", taskDetail)
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/prompts/templates", nil, "")
	var templatesResponse map[string]any
	decodeResponse(t, response, &templatesResponse)
	if response.StatusCode != http.StatusOK || len(templatesResponse["templates"].([]any)) != 8 {
		t.Fatalf("prompt templates failed: %d %#v", response.StatusCode, templatesResponse)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/prompts/templates/role.main", map[string]any{
		"content": "CUSTOM MAIN {{.AgentID}}",
	}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("prompt template update failed: %d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/prompts/templates/role.main/reset", nil, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("prompt template reset failed: %d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/agents/main/conversation", nil, "")
	var mainConversation map[string]any
	decodeResponse(t, response, &mainConversation)
	mainItems := mainConversation["conversation"].(map[string]any)["items"].([]any)
	if len(mainItems) != 1 || mainItems[0].(map[string]any)["summary"] != "main only" {
		t.Fatalf("main conversation leaked: %#v", mainConversation)
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/agents/sub-001/conversation", nil, "")
	var childConversation map[string]any
	decodeResponse(t, response, &childConversation)
	childItems := childConversation["conversation"].(map[string]any)["items"].([]any)
	if len(childItems) != 1 || childItems[0].(map[string]any)["summary"] != "child only" {
		t.Fatalf("child conversation leaked: %#v", childConversation)
	}

	response = requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/tasks/"+task.ID+"/agents/main", map[string]any{
		"model_id": model2.ID, "reasoning_effort": "high", "filesystem": "workspace-write", "approval": "never",
	}, csrf)
	if response.StatusCode != http.StatusOK {
		var failure map[string]any
		decodeResponse(t, response, &failure)
		t.Fatalf("main config update failed: %#v", failure)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/agents", nil, "")
	var agentsResponse map[string]any
	decodeResponse(t, response, &agentsResponse)
	agents := agentsResponse["agents"].([]any)
	if len(agents) != 2 {
		t.Fatalf("unexpected agent list: %#v", agentsResponse)
	}
	for _, value := range agents {
		agent := value.(map[string]any)
		if agent["model_id"] != model2.ID {
			t.Fatalf("main config did not propagate: %#v", agentsResponse)
		}
	}

	for _, session := range []domain.BackendSession{
		{
			ID: "session-api-main", TaskID: task.ID, AgentID: "main", WorkspaceID: workspace.ID,
			Backend: "stub", ModelID: model2.ID, EnvGroupRevision: env2.Revision,
			ProviderSession: "provider-main", Status: "active", CreatedAt: now, LastUsedAt: now,
		},
		{
			ID: "session-api-child", TaskID: task.ID, AgentID: "sub-001", WorkspaceID: workspace.ID,
			Backend: "stub", ModelID: model2.ID, EnvGroupRevision: env2.Revision,
			ProviderSession: "provider-child", Status: "active", CreatedAt: now, LastUsedAt: now,
		},
	} {
		if err := database.UpsertBackendSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+task.ID+"/agents/main/session/compact", nil, csrf)
	var compactResponse map[string]any
	decodeResponse(t, response, &compactResponse)
	if response.StatusCode != http.StatusOK || compactResponse["status"] != "compacted" {
		t.Fatalf("main compact endpoint failed: %d %#v", response.StatusCode, compactResponse)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+task.ID+"/agents/sub-001/session/reset", nil, csrf)
	var resetResponse map[string]any
	decodeResponse(t, response, &resetResponse)
	if response.StatusCode != http.StatusOK || resetResponse["status"] != "reset" {
		t.Fatalf("child reset endpoint failed: %d %#v", response.StatusCode, resetResponse)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+task.ID+"/agents/sub-001/messages", map[string]any{
		"content": "child follow-up",
	}, csrf)
	if response.StatusCode != http.StatusAccepted {
		var failure map[string]any
		decodeResponse(t, response, &failure)
		t.Fatalf("child message failed: %#v", failure)
	}
	response.Body.Close()
}

func registerOwner(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	response := requestJSON(t, client, http.MethodPost, baseURL+"/api/v1/auth/register", map[string]any{
		"setup_token": "setup-test", "username": "owner", "password": "correct-horse-battery",
	}, "")
	var registration map[string]any
	decodeResponse(t, response, &registration)
	csrf, _ := registration["csrf_token"].(string)
	if response.StatusCode != http.StatusCreated || csrf == "" {
		t.Fatalf("registration failed: status=%d body=%v", response.StatusCode, registration)
	}
	return csrf
}

type fakeSecretStore struct {
	values map[string]string
}

func (f *fakeSecretStore) PutMany(values map[string]string) error {
	if f.values == nil {
		f.values = map[string]string{}
	}
	for key, value := range values {
		f.values[key] = value
	}
	return nil
}

func (f *fakeSecretStore) DeleteMany(keys []string) error {
	if f.values == nil {
		return nil
	}
	for _, key := range keys {
		delete(f.values, key)
	}
	return nil
}

func (f *fakeSecretStore) Get(key string) (string, bool) {
	value, ok := f.values[key]
	return value, ok
}

func TestAddModelsGeneratesEnvGroupsAndSecrets(t *testing.T) {
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
	secretsStore := &fakeSecretStore{}
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService, Secrets: secretsStore}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/providers", map[string]any{
		"name": "Gateway", "base_url": "https://api.example.com/v1", "api_key": "secret-key", "auth_style": "auto",
	}, csrf)
	var providerPayload map[string]any
	decodeResponse(t, response, &providerPayload)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create provider failed: %d %v", response.StatusCode, providerPayload)
	}
	providerID := providerPayload["provider"].(map[string]any)["id"].(string)

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/providers/add-models", map[string]any{
		"provider_id": providerID,
		"models": []map[string]any{
			{"id": "gpt-4o", "wire_apis": []string{"responses"}, "max_input_tokens": 128000, "max_output_tokens": 16000},
			{"id": "gpt-4o-mini", "wire_apis": []string{"responses"}, "max_input_tokens": 64000},
		},
	}, csrf)
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("add models failed: %d %v", response.StatusCode, payload)
	}
	models, _ := payload["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].(map[string]any)["context_window"] != float64(128000) || models[0].(map[string]any)["max_output_tokens"] != float64(16000) {
		t.Fatalf("detected token limits were not persisted: %#v", models[0])
	}
	if _, present := models[0].(map[string]any)["default_env_group_id"]; present {
		t.Fatalf("default_env_group_id leaked: %v", models[0])
	}
	groups, err := database.ListEnvGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 auto-generated env groups, got %d", len(groups))
	}
	if groups[0].SecretConfigured != true || len(groups[0].SecretRefs) != 1 {
		t.Fatalf("env group secrets not configured: %#v", groups[0])
	}
	if len(secretsStore.values) != 1 {
		t.Fatalf("expected 1 provider secret, got %d: %#v", len(secretsStore.values), secretsStore.values)
	}
	for _, value := range secretsStore.values {
		if value != "secret-key" {
			t.Fatalf("secret value mismatch: %q", value)
		}
	}

	// Deleting a model removes its env group but keeps the shared provider credential.
	modelID := models[0].(map[string]any)["id"].(string)
	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/models/"+modelID, nil, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete model failed: %d", response.StatusCode)
	}
	response.Body.Close()
	groupsAfter, _ := database.ListEnvGroups(ctx)
	if len(groupsAfter) != 1 {
		t.Fatalf("expected 1 env group after model delete, got %d", len(groupsAfter))
	}
	if len(secretsStore.values) != 1 {
		t.Fatalf("provider credential should survive model delete, got %d secrets", len(secretsStore.values))
	}
}

func TestMissingStaticAssetReturnsNotFound(t *testing.T) {
	t.Parallel()
	server := New(Config{
		Web: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<html>app</html>")},
		},
	})
	request := httptest.NewRequest(http.MethodGet, "http://example.test/missing.js", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d with %q", response.Code, response.Body.String())
	}
}

func TestStableStaticAssetNamesAreRevalidated(t *testing.T) {
	t.Parallel()
	server := New(Config{
		Web: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<html>app</html>")},
			"app.js":     &fstest.MapFile{Data: []byte("console.log('app')")},
		},
	})
	request := httptest.NewRequest(http.MethodGet, "http://example.test/app.js", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("stable asset was not revalidated: status=%d cache=%q", response.Code, response.Header().Get("Cache-Control"))
	}
}

func TestJSONResponsesAreNotCached(t *testing.T) {
	t.Parallel()
	response := httptest.NewRecorder()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("JSON response cache policy = %q", response.Header().Get("Cache-Control"))
	}
}

func TestOriginPolicyCanBeExplicitlyRelaxed(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodPost, "http://internal.test/api/v1/auth/login", nil)
	request.Header.Set("Origin", "https://public.example")

	if New(Config{}).sameOrigin(request) {
		t.Fatal("strict origin policy accepted a mismatched host")
	}
	if !New(Config{AllowCrossOrigin: true}).sameOrigin(request) {
		t.Fatal("relaxed origin policy rejected a mismatched host")
	}
}

func TestWriteSSEIncludesSequenceID(t *testing.T) {
	t.Parallel()
	response := httptest.NewRecorder()
	writeSSE(response, domain.Event{Sequence: 42, Type: "turn_running"})
	body := response.Body.String()
	if !strings.Contains(body, "id: 42\n") || !strings.Contains(body, "event: update\n") {
		t.Fatalf("SSE cursor missing: %q", body)
	}
}

func TestApplyTurnTimingsUsesServerClock(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	turns := []domain.Turn{{
		Status: domain.TurnRunning, QueuedAt: start, PreparedAt: start.Add(time.Second),
		StartedAt: start.Add(2 * time.Second),
	}}
	applyTurnTimings(turns, start.Add(12*time.Second))
	if turns[0].ElapsedMS != 12000 || turns[0].QueueDurationMS != 1000 ||
		turns[0].PrepareDurationMS != 1000 || turns[0].RunDurationMS != 10000 {
		t.Fatalf("unexpected server timings: %#v", turns[0])
	}
}

func TestContextUsageUsesCodexTurnDelta(t *testing.T) {
	t.Parallel()
	current := map[string]any{"input_tokens": float64(1200082)}
	previous := map[string]any{"input_tokens": float64(1149164)}
	if actual := contextTokensForUsage("codex", current, previous); actual != 50918 {
		t.Fatalf("context token delta = %v", actual)
	}
	if baseline := contextTokensForUsage("codex", previous, map[string]any{"input_tokens": float64(1100038)}); baseline != 49126 {
		t.Fatalf("active turn baseline = %v", baseline)
	}
	if active := activeCodexContextTokens(
		1200082,
		map[string]any{"input_tokens": float64(1200082)},
		map[string]any{"input_tokens": float64(1149164)},
		4000,
	); active != 51918 {
		t.Fatalf("active context reused cumulative input: %v", active)
	}
	turn := domain.Turn{
		ContextWindow: 1050000,
		Usage: map[string]any{
			"input_tokens":   float64(1200082),
			"context_tokens": float64(50918),
		},
	}
	if actual := turnContextPercent(turn); actual != 4.8 {
		t.Fatalf("context percent = %v", actual)
	}
	turn.Usage = map[string]any{"input_tokens": float64(1200082)}
	if actual := turnContextPercent(turn); actual != 100 {
		t.Fatalf("context percent was not clamped: %v", actual)
	}
}

func TestContextMetricsAggregateSessionHistory(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	turn := domain.Turn{
		BackendSessionID: "current", ContextWindow: 1000, PromptChars: 800,
		Usage: map[string]any{
			"input_tokens": float64(200), "cached_input_tokens": float64(160),
			"output_tokens": float64(30), "reasoning_output_tokens": float64(5),
			"context_tokens": float64(50),
		},
	}
	sessions := []domain.BackendSession{
		{
			ID: "old", Backend: "codex", Status: "compacted",
			ContextUsageJSON: `{"input_tokens":100,"cached_input_tokens":80,"output_tokens":10}`,
			CreatedAt:        now.Add(-time.Hour), LastUsedAt: now.Add(-time.Hour),
		},
		{
			ID: "current", Backend: "codex", Status: "active",
			ContextUsageJSON: `{"input_tokens":150,"cached_input_tokens":120,"output_tokens":20}`,
			CreatedAt:        now, LastUsedAt: now,
		},
	}
	metrics := contextMetrics(turn, sessions, domain.Workspace{Transport: "ssh"})
	for key, want := range map[string]float64{
		"total_tokens": 340, "history_tokens": 110, "current_total_tokens": 230,
		"input_tokens": 300, "cached_input_tokens": 240, "output_tokens": 40,
		"reasoning_output_tokens": 5, "context_tokens": 50,
	} {
		if got := usageNumber(metrics, key); got != want {
			t.Fatalf("%s = %v, want %v; metrics=%#v", key, got, want, metrics)
		}
	}
	claude := map[string]any{
		"input_tokens": float64(100), "cache_read_input_tokens": float64(80),
		"cache_creation_input_tokens": float64(5), "output_tokens": float64(10),
		"reasoning_output_tokens": float64(3),
	}
	if got := usageTotalTokens(claude, "claude"); got != 190 {
		t.Fatalf("claude total = %v", got)
	}
	if got := usageCachedTokens(claude); got != 85 {
		t.Fatalf("claude cached = %v", got)
	}
	turn.Status = domain.TurnSucceeded
	sessions[1].Status = "reset"
	metrics = contextMetrics(turn, sessions, domain.Workspace{Transport: "ssh"})
	if got := usageNumber(metrics, "context_tokens"); got != 0 || metrics["session_active"] != false {
		t.Fatalf("reset session retained active context: %#v", metrics)
	}
	if got := usageNumber(metrics, "total_tokens"); got != 280 {
		t.Fatalf("reset history total = %v, want 280; metrics=%#v", got, metrics)
	}
}

func TestLatestTurnForAgentUsesTaskHistory(t *testing.T) {
	t.Parallel()
	turns := []domain.Turn{
		{ID: "sub-old", AgentID: "sub-001", Sequence: 2},
		{ID: "main-new", AgentID: "main", Sequence: 3},
		{ID: "sub-latest", AgentID: "sub-001", Sequence: 4},
		{ID: "main-latest", AgentID: "main", Sequence: 5},
	}
	if got := latestTurnForAgent(turns, "sub-001"); got.ID != "sub-latest" {
		t.Fatalf("latest sub-agent turn = %#v", got)
	}
	if got := latestTurnForAgent(turns, "main"); got.ID != "main-latest" {
		t.Fatalf("latest main turn = %#v", got)
	}
}

func requestJSON(t *testing.T, client *http.Client, method, url string, payload any, csrf string) *http.Response {
	t.Helper()
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}
