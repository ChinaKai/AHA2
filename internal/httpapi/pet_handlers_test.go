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
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestOwnerPetStateAPIAndVersionConflict(t *testing.T) {
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

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet", nil, "")
	var current struct {
		State struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
		} `json:"state"`
	}
	decodeResponse(t, response, &current)
	if response.StatusCode != http.StatusOK || current.State.ID != "owner_pet" || current.State.Version != 1 {
		t.Fatalf("pet state=%d %#v", response.StatusCode, current)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet", map[string]any{
		"action": "jump", "mood": "happy", "x": 0.4, "y": 0.5, "version": current.State.Version,
	}, csrf)
	var updated struct {
		State struct {
			Action  string  `json:"action"`
			Version int64   `json:"version"`
			X       float64 `json:"x"`
		} `json:"state"`
	}
	decodeResponse(t, response, &updated)
	if response.StatusCode != http.StatusOK || updated.State.Action != "jump" || updated.State.Version != 2 || updated.State.X != 0.4 {
		t.Fatalf("update pet state=%d %#v", response.StatusCode, updated)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet", map[string]any{
		"action": "walk", "version": 1,
	}, csrf)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale pet update=%d", response.StatusCode)
	}
	response.Body.Close()
}

func TestOwnerPetSettingsAPI(t *testing.T) {
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

	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet/settings", map[string]any{
		"enabled": false, "character": "deepseek_fish", "actions": []string{"walk", "jump"},
		"identity": "should be ignored", "principles": "should be ignored",
		"idle_minutes": 10, "hourly_limit": 4, "quiet_start": 22, "quiet_end": 7,
	}, csrf)
	var payload struct {
		Assistant struct {
			PetEnabled   bool     `json:"pet_enabled"`
			Character    string   `json:"character"`
			PetActions   []string `json:"pet_actions"`
			BrainModelID string   `json:"brain_model_id"`
			PetIdentity  string   `json:"pet_identity"`
			IdleMinutes  int      `json:"pet_idle_minutes"`
			HourlyLimit  int      `json:"pet_hourly_limit"`
		} `json:"assistant"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload.Assistant.PetEnabled || payload.Assistant.Character != "deepseek_fish" || len(payload.Assistant.PetActions) != 2 || payload.Assistant.PetIdentity != "" || payload.Assistant.IdleMinutes != 10 || payload.Assistant.HourlyLimit != 4 {
		t.Fatalf("pet settings=%d %#v", response.StatusCode, payload)
	}
}

func TestOwnerPetControlAPI(t *testing.T) {
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

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/control", map[string]any{
		"say": "去左边看看", "actions": []map[string]any{{"type": "move_to", "zone": "left"}, {"type": "mood", "name": "happy"}},
	}, csrf)
	var payload struct {
		OK      bool   `json:"ok"`
		Say     string `json:"say"`
		Actions []struct {
			Type string `json:"type"`
		} `json:"actions"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload.Say != "去左边看看" || len(payload.Actions) != 2 {
		t.Fatalf("pet control=%d %#v", response.StatusCode, payload)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/control", map[string]any{
		"say": "", "actions": []map[string]any{{"type": "play", "name": "explode"}},
	}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid pet control=%d", response.StatusCode)
	}
	response.Body.Close()
}
