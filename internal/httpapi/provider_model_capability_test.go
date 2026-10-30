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

// The model editor toggles image input through this handler, so the capability
// must survive the real request path. Absence means enabled, which makes the
// explicit false the value that has to be stored rather than deleted.
func TestUpdateModelHandlerStoresAutoCompactWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	model := domain.Model{
		ID: "model-auto-compact", DisplayName: "Compact", ProviderID: "p", Backend: "claude",
		WireModel: "compact-model", WireAPI: "anthropic_messages", ContextWindow: 200_000, MaxOutputTokens: 32_000,
		AutoCompactWindow: 152_000, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: database}
	update := func(body string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/api/v1/models/"+model.ID, strings.NewReader(body))
		request.SetPathValue("id", model.ID)
		server.updateModel(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("update failed: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	update(`{"context_window":200000,"max_output_tokens":32000,"auto_compact_window":180000}`)
	stored, err := database.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AutoCompactWindow != 180_000 {
		t.Fatalf("auto compact window was not stored: %d", stored.AutoCompactWindow)
	}
	// A rename that omits the field must keep the configured threshold.
	update(`{"display_name":"Compact Renamed"}`)
	stored, err = database.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AutoCompactWindow != 180_000 {
		t.Fatalf("rename cleared the auto compact window: %d", stored.AutoCompactWindow)
	}
	// Explicit 0 means the CLI should choose; it must not be replaced.
	update(`{"context_window":200000,"max_output_tokens":32000,"auto_compact_window":0}`)
	stored, err = database.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AutoCompactWindow != 0 {
		t.Fatalf("explicit zero was not preserved: %d", stored.AutoCompactWindow)
	}
	// A value above the window is capped to the window.
	update(`{"context_window":200000,"max_output_tokens":32000,"auto_compact_window":250000}`)
	stored, err = database.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AutoCompactWindow != 200_000 {
		t.Fatalf("auto compact window was not capped: %d", stored.AutoCompactWindow)
	}
}

func TestUpdateModelHandlerStoresImageInputCapability(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	model := domain.Model{
		ID: "model-image-capability", DisplayName: "Vision", ProviderID: "p", Backend: "dsh",
		WireModel: "vision-model", WireAPI: "anthropic_messages",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: database}
	update := func(body string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/api/v1/models/"+model.ID, strings.NewReader(body))
		request.SetPathValue("id", model.ID)
		server.updateModel(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("update failed: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}

	update(`{"supports_image_input":true}`)
	stored, err := database.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Capabilities["supports_image_input"] != true {
		t.Fatalf("image capability was not stored: %#v", stored.Capabilities)
	}

	// A rename that does not mention the capability must not clear it.
	update(`{"display_name":"Vision Renamed"}`)
	stored, err = database.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Capabilities["supports_image_input"] != true {
		t.Fatalf("rename cleared the image capability: %#v", stored.Capabilities)
	}

	update(`{"supports_image_input":false}`)
	stored, err = database.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if value, exists := stored.Capabilities["supports_image_input"]; !exists || value != false {
		t.Fatalf("disabling image input must store an explicit false: %#v", stored.Capabilities)
	}
}
