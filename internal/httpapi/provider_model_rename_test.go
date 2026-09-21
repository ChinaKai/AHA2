package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// A model's wire id is the only place the "[1m]" marker can go, because the CLI
// reads the marker from the name it is invoked with rather than from the
// environment. Editing it must therefore reach the stored model, and the env
// group must follow, or the two would disagree about which model to call.
func TestSyncEnvGroupModelFollowsRename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	group := domain.EnvGroup{
		ID: "env-rename", Name: "Provider / old", ProviderID: "p", Backend: "claude", Revision: 1,
		Environment: map[string]string{
			"ANTHROPIC_BASE_URL": "https://gateway.example",
			"ANTHROPIC_MODEL":    "old-model",
		},
		SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model-rename", DisplayName: "Old", ProviderID: "p", Backend: "claude",
		WireModel: "old-model", WireAPI: "anthropic_messages", DefaultEnvGroupID: group.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}

	renamed := model
	renamed.WireModel = "old-model[1m]"
	(&Server{store: database}).syncEnvGroupModel(ctx, renamed)

	updated, err := database.EnvGroup(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Environment["ANTHROPIC_MODEL"]; got != "old-model[1m]" {
		t.Fatalf("env group model = %q, want it to follow the rename", got)
	}
	// The endpoint must not be collateral damage of a model rename.
	if got := updated.Environment["ANTHROPIC_BASE_URL"]; got != "https://gateway.example" {
		t.Fatalf("endpoint changed during a rename: %q", got)
	}
}

// A model with no env group of its own must not fail the update.
func TestSyncEnvGroupModelWithoutGroup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	(&Server{store: database}).syncEnvGroupModel(ctx, domain.Model{ID: "m", WireModel: "x"})
}

// The edit dialog's save goes through the handler, so the handler is where the
// rename must actually land. Calling the sync helper directly would pass even
// with the handler wiring removed -- this drives the real request path.
func TestUpdateModelHandlerAppliesRename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	group := domain.EnvGroup{
		ID: "env-http-rename", Name: "Provider / old", ProviderID: "p", Backend: "claude", Revision: 1,
		Environment: map[string]string{
			"ANTHROPIC_BASE_URL": "https://gateway.example",
			"ANTHROPIC_MODEL":    "old-model",
		},
		SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model-http-rename", DisplayName: "Old", ProviderID: "p", Backend: "claude",
		WireModel: "old-model", WireAPI: "anthropic_messages", DefaultEnvGroupID: group.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: database}
	recorder := httptest.NewRecorder()
	body := strings.NewReader(`{"wire_model":"old-model[1m]","context_window":1000000}`)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/models/"+model.ID, body)
	request.SetPathValue("id", model.ID)
	server.updateModel(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("update failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, err := database.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.WireModel != "old-model[1m]" {
		t.Fatalf("wire model = %q, want the rename to reach the stored model", stored.WireModel)
	}
	updated, _ := database.EnvGroup(ctx, group.ID)
	if got := updated.Environment["ANTHROPIC_MODEL"]; got != "old-model[1m]" {
		t.Fatalf("env group model = %q, want it to follow", got)
	}
}

// A malformed id would be sent straight to the backend, so the handler rejects it.
func TestUpdateModelHandlerRejectsBadWireModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	model := domain.Model{
		ID: "model-bad-wire", DisplayName: "M", ProviderID: "p", Backend: "claude",
		WireModel: "ok-model", WireAPI: "anthropic_messages", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: database}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/models/"+model.ID, strings.NewReader(`{"wire_model":"bad model name"}`))
	request.SetPathValue("id", model.ID)
	server.updateModel(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a malformed model id", recorder.Code)
	}
	stored, _ := database.Model(ctx, model.ID)
	if stored.WireModel != "ok-model" {
		t.Fatalf("a rejected rename changed the model: %q", stored.WireModel)
	}
}

// The sync writes the caller's in-memory WireModel, so its correctness depends on
// the handler having assigned the new name before calling it. Reading the store
// instead would silently write the old name back, leaving the env group and the
// model disagreeing -- which is exactly what the ordering protects against.
func TestSyncEnvGroupModelWritesTheGivenWireModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	group := domain.EnvGroup{
		ID: "env-order", Name: "Provider / old", ProviderID: "p", Backend: "claude", Revision: 1,
		Environment: map[string]string{"ANTHROPIC_MODEL": "old-model"},
		SecretRefs:  map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model-order", DisplayName: "Old", ProviderID: "p", Backend: "claude",
		WireModel: "old-model", WireAPI: "anthropic_messages", DefaultEnvGroupID: group.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}

	renamed := model
	renamed.WireModel = "new-model[1m]"
	(&Server{store: database}).syncEnvGroupModel(ctx, renamed)

	updated, err := database.EnvGroup(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The stored model is still the old name: nothing was persisted yet. The sync
	// must follow the caller's value, not re-read the store.
	stored, _ := database.Model(ctx, model.ID)
	if stored.WireModel != "old-model" {
		t.Fatalf("fixture changed the stored model: %q", stored.WireModel)
	}
	if got := updated.Environment["ANTHROPIC_MODEL"]; got != "new-model[1m]" {
		t.Fatalf("env group model = %q, want it to follow the caller's rename", got)
	}
}
