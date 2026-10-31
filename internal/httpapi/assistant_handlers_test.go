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

func TestOwnerAssistantAPIRequiresAuthAndReturnsSingleton(t *testing.T) {
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

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assistant", nil, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated assistant request=%d", response.StatusCode)
	}
	response.Body.Close()

	csrf := registerOwner(t, client, server.URL)
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assistant", nil, "")
	var payload struct {
		OK        bool `json:"ok"`
		Assistant struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Character string `json:"character"`
		} `json:"assistant"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	decodeResponse(t, response, &payload)
	hasPetSay := false
	for _, tool := range payload.Tools {
		if tool.Name == "pet_say" {
			hasPetSay = true
		}
	}
	if response.StatusCode != http.StatusOK || payload.Assistant.ID != "assistant_default" || payload.Assistant.Name == "" || payload.Assistant.Character != "gugugaga" || len(payload.Tools) < 8 || !hasPetSay {
		t.Fatalf("assistant response=%d %#v", response.StatusCode, payload)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/assistant", map[string]any{"character": "deepseek_fish"}, csrf)
	var updated struct {
		Assistant struct {
			Character string `json:"character"`
		} `json:"assistant"`
	}
	decodeResponse(t, response, &updated)
	if response.StatusCode != http.StatusOK || updated.Assistant.Character != "deepseek_fish" {
		t.Fatalf("change character=%d %#v", response.StatusCode, updated)
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assistant/hints", nil, "")
	var hints struct {
		Hints []any `json:"hints"`
	}
	decodeResponse(t, response, &hints)
	if response.StatusCode != http.StatusOK || len(hints.Hints) != 0 {
		t.Fatalf("hints response=%d %#v", response.StatusCode, hints)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/assistant/messages", map[string]any{"content": "你好"}, csrf)
	var ask map[string]any
	decodeResponse(t, response, &ask)
	if response.StatusCode != http.StatusBadRequest || ask["error"] != "assistant_ask_failed" {
		t.Fatalf("ask without model=%d %#v", response.StatusCode, ask)
	}
}
