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
	"testing"
	"testing/fstest"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
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
		"provider_id": providerID, "wire_api": "responses", "model_ids": []string{"gpt-4o", "gpt-4o-mini"},
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
