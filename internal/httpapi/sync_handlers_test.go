package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestSyncSettingsAPIKeepsTokenOutOfResponses(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore := &fakeSecretStore{}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}), Secrets: secretStore}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/sync", map[string]any{"enabled": true, "endpoint": "https://sync.example", "device_id": "device-one", "interval_seconds": 60, "token": "top-secret-token", "passphrase": "long-secret-passphrase", "provider_ids": []string{"provider-one"}}, csrf)
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update failed: %d %s", response.StatusCode, body)
	}
	if strings.Contains(body, "top-secret-token") || strings.Contains(body, "long-secret-passphrase") {
		t.Fatal("sync token leaked in update response")
	}
	if secretStore.values[localSyncTokenRef] != "top-secret-token" {
		t.Fatal("sync token was not written to secret store")
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/sync", nil, "")
	body = readBody(t, response)
	if response.StatusCode != http.StatusOK || strings.Contains(body, "top-secret-token") {
		t.Fatalf("unsafe settings response: %d %s", response.StatusCode, body)
	}
	var payload struct {
		Sync struct {
			TokenConfigured      bool     `json:"token_configured"`
			PassphraseConfigured bool     `json:"passphrase_configured"`
			DeviceID             string   `json:"device_id"`
			ProviderIDs          []string `json:"provider_ids"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Sync.TokenConfigured || !payload.Sync.PassphraseConfigured || payload.Sync.DeviceID != "device-one" || len(payload.Sync.ProviderIDs) != 1 {
		t.Fatalf("unexpected settings: %#v", payload.Sync)
	}
}

func TestRunSyncUsesStoredTokenAndAuthenticatedRoutes(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stored-token" || r.Header.Get("X-Device-ID") != "device-one" {
			t.Errorf("missing remote credentials")
		}
		if r.URL.Path != "/v1/sync/pull" {
			t.Errorf("unexpected remote path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"objects": []any{}, "cursor": "7", "has_more": false})
	}))
	defer remote.Close()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore := &fakeSecretStore{values: map[string]string{localSyncTokenRef: "stored-token"}}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}), Secrets: secretStore}).Handler())
	defer server.Close()
	unauthorized := requestJSON(t, http.DefaultClient, http.MethodGet, server.URL+"/api/v1/settings/sync/status", nil, "")
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status route is not authenticated: %d", unauthorized.StatusCode)
	}
	unauthorized.Body.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/sync", map[string]any{"enabled": true, "endpoint": remote.URL, "device_id": "device-one", "interval_seconds": 60}, csrf)
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/settings/sync/run", map[string]any{}, csrf)
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("run failed: %d %s", response.StatusCode, body)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/sync/status", nil, "")
	body = readBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, `"cursor":"7"`) {
		t.Fatalf("status failed: %d %s", response.StatusCode, body)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/sync/conflicts", nil, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("conflicts failed: %d", response.StatusCode)
	}
	response.Body.Close()
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	var value json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return string(value)
}
