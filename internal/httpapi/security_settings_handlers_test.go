package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestOriginValidationSettingPersistsAndAppliesImmediately(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	authService := auth.NewService(database, "setup-test", time.Hour)
	apiServer := New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})})
	server := httptest.NewServer(apiServer.Handler())
	defer server.Close()
	client := newCookieClient(t)
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/security", nil, "")
	var payload struct {
		Security struct {
			ValidateOrigin  bool `json:"validate_origin"`
			StartupOverride bool `json:"startup_override"`
		} `json:"security"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || !payload.Security.ValidateOrigin || payload.Security.StartupOverride {
		t.Fatalf("default security response = %d %#v", response.StatusCode, payload)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/security", map[string]any{"validate_origin": false}, csrf)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload.Security.ValidateOrigin {
		t.Fatalf("disable origin validation response = %d %#v", response.StatusCode, payload)
	}
	mismatch := httptest.NewRequest(http.MethodPost, "http://internal.test/api", nil)
	mismatch.Header.Set("Origin", "https://proxy.example")
	if !apiServer.sameOrigin(mismatch) {
		t.Fatal("disabled origin validation still rejected mismatched host")
	}
	if restarted := New(Config{Store: database}); !restarted.sameOrigin(mismatch) {
		t.Fatal("persisted origin validation setting was not loaded")
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/security", map[string]any{"validate_origin": true}, csrf)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || !payload.Security.ValidateOrigin || apiServer.sameOrigin(mismatch) {
		t.Fatalf("enable origin validation response = %d %#v", response.StatusCode, payload)
	}
	forced := New(Config{Store: database, AllowCrossOrigin: true})
	if !forced.sameOrigin(mismatch) || !forced.originStartupOverride {
		t.Fatal("startup allow-cross-origin override was not preserved")
	}
}
