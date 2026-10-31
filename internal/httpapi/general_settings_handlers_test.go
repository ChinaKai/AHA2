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
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestGeneralLanguageSettingPersists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
	defer server.Close()
	client := newCookieClient(t)
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/settings/general", nil, "")
	var payload struct {
		General domain.GeneralSettings `json:"general"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload.General.Language != domain.LanguageAuto {
		t.Fatalf("default general settings = %d %#v", response.StatusCode, payload.General)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/general", map[string]any{"language": "en-US"}, csrf)
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload.General.Language != domain.LanguageEnglish {
		t.Fatalf("update general settings = %d %#v", response.StatusCode, payload.General)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/settings/general", map[string]any{"language": "fr-FR"}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid language status = %d", response.StatusCode)
	}
	response.Body.Close()

	stored, err := database.GeneralSettings(ctx)
	if err != nil || stored.Language != domain.LanguageEnglish {
		t.Fatalf("stored language = %#v err=%v", stored, err)
	}
}
