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

// The settings API has one writable model. A hostile or old client that sends
// a different chat_model_id must still get the source-first selection, and the
// legacy assistant endpoint must refuse an independent chat model.
func TestOwnerPetSettingsAPIUnifiesChatAndPetModel(t *testing.T) {
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
	provider := domain.Provider{
		ID: "provider-unified-api", Name: "Unified API", BaseURL: "https://example.test/v1",
		AuthStyle: "bearer", CredentialRef: "provider/unified/credential", CredentialConfigured: true,
		CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-unified-api", Name: "Unified", ProviderID: provider.ID, Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	modelA := domain.Model{
		ID: "model-unified-a", DisplayName: "Unified A", ProviderID: provider.ID,
		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "unified-a", WireAPI: "chat",
		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	modelB := domain.Model{
		ID: "model-unified-b", DisplayName: "Unified B", ProviderID: provider.ID,
		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "unified-b", WireAPI: "chat",
		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.UpsertProvider(ctx, provider) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, modelA) },
		func() error { return database.UpsertModel(ctx, modelB) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet/settings", map[string]any{
		"enabled": true, "character": "gugugaga", "actions": []string{"walk"},
		"idle_minutes": 5, "hourly_limit": 6, "quiet_start": 23, "quiet_end": 8,
		"pet_model_source": "provider", "pet_model_id": modelA.ID,
		"chat_model_id": modelB.ID,
	}, csrf)
	var saved struct {
		Assistant struct {
			ModelID      string `json:"model_id"`
			BrainModelID string `json:"brain_model_id"`
		} `json:"assistant"`
		PetModelID    string `json:"pet_model_id"`
		PetModelLabel string `json:"pet_model_label"`
	}
	decodeResponse(t, response, &saved)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save settings=%d %#v", response.StatusCode, saved)
	}
	if saved.Assistant.BrainModelID != modelA.ID || saved.Assistant.ModelID != modelA.ID || saved.PetModelID != modelA.ID {
		t.Fatalf("chat_model_id overrode the unified model: %#v", saved)
	}
	if saved.PetModelLabel == "" {
		t.Fatalf("read-only chat label is empty: %#v", saved)
	}

	// A fresh read must return the same single model.
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assistant", nil, "")
	var read struct {
		Assistant struct {
			ModelID      string `json:"model_id"`
			BrainModelID string `json:"brain_model_id"`
		} `json:"assistant"`
		PetModelID string `json:"pet_model_id"`
	}
	decodeResponse(t, response, &read)
	if response.StatusCode != http.StatusOK || read.Assistant.ModelID != modelA.ID ||
		read.Assistant.BrainModelID != modelA.ID || read.PetModelID != modelA.ID {
		t.Fatalf("readback=%d %#v", response.StatusCode, read)
	}

	// The legacy assistant endpoint cannot move the chat model alone.
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/assistant", map[string]any{"model_id": modelB.ID}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("independent chat model change=%d", response.StatusCode)
	}
	response.Body.Close()
}
