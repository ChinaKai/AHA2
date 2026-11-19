package httpapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/prompt"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

type promptReadOnlyFixture struct {
	server   *httptest.Server
	database *store.Store
	client   *http.Client
	csrf     string
}

func newPromptReadOnlyFixture(t *testing.T) promptReadOnlyFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = database.Close() })
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	return promptReadOnlyFixture{server: server, database: database, client: client, csrf: csrf}
}

func TestPromptEntryCatalogIsReadOnlyAndLegacyTemplateAPIStillWorks(t *testing.T) {
	t.Parallel()
	fixture := newPromptReadOnlyFixture(t)

	response := requestJSON(t, fixture.client, http.MethodGet, fixture.server.URL+"/api/v1/prompts/entries", nil, "")
	var catalogPayload struct {
		OK      bool                 `json:"ok"`
		Entries []prompt.PromptEntry `json:"entries"`
	}
	decodeResponse(t, response, &catalogPayload)
	if response.StatusCode != http.StatusOK || !catalogPayload.OK {
		t.Fatalf("entry catalog failed: status=%d payload=%#v", response.StatusCode, catalogPayload)
	}
	if len(catalogPayload.Entries) != 2 {
		t.Fatalf("expected exactly two top-level entries, got %d", len(catalogPayload.Entries))
	}
	if catalogPayload.Entries[0].ID != prompt.EntryCore || catalogPayload.Entries[1].ID != prompt.EntryTaskContext {
		t.Fatalf("unexpected entry order: %#v", []string{catalogPayload.Entries[0].ID, catalogPayload.Entries[1].ID})
	}
	seenAnchors := map[string]bool{}
	seenLegacy := map[string]bool{}
	required := 0
	dynamic := 0
	readOnly := 0
	for _, entry := range catalogPayload.Entries {
		if strings.TrimSpace(entry.Name) == "" || strings.TrimSpace(entry.Description) == "" {
			t.Fatalf("entry %s is missing display metadata", entry.ID)
		}
		for _, anchor := range entry.Anchors {
			key := entry.ID + "#" + anchor.Key
			if anchor.EntryID != entry.ID || seenAnchors[key] {
				t.Fatalf("duplicate or misplaced anchor %s: %#v", key, anchor)
			}
			seenAnchors[key] = true
			if seenLegacy[anchor.LegacyID] {
				t.Fatalf("legacy ID %s is mapped more than once", anchor.LegacyID)
			}
			seenLegacy[anchor.LegacyID] = true
			ref, ok := prompt.ResolveLegacyTemplateID(anchor.LegacyID)
			if !ok || ref.EntryID != entry.ID || ref.Key != anchor.Key {
				t.Fatalf("legacy ID %s does not round-trip to %s: %#v", anchor.LegacyID, key, ref)
			}
			if anchor.Layer == "" || anchor.Version < 1 || anchor.FrozenVersion != anchor.Version {
				t.Fatalf("anchor %s lost layer/version metadata: %#v", key, anchor)
			}
			if anchor.Required {
				required++
			}
			if anchor.Dynamic {
				dynamic++
				if anchor.Kind != "generated" || anchor.Editable || !anchor.Protected {
					t.Fatalf("generated anchor %s is not preview-only: %#v", key, anchor)
				}
			}
			if !anchor.Editable {
				readOnly++
				if !anchor.Protected {
					t.Fatalf("non-editable anchor %s is not marked protected", key)
				}
			}
		}
	}
	if len(seenAnchors) != 29 || len(seenLegacy) != 29 {
		t.Fatalf("expected 29 anchors/legacy IDs, got anchors=%d legacy=%d", len(seenAnchors), len(seenLegacy))
	}
	if required != 12 {
		t.Fatalf("expected 12 required anchors, got %d", required)
	}
	if dynamic != 7 {
		t.Fatalf("expected 7 generated anchors, got %d", dynamic)
	}
	if readOnly != 11 {
		t.Fatalf("expected 11 protected anchors (7 generated + attachment + 3 resources), got %d", readOnly)
	}

	// The old ID list remains the editable list for at least this release.
	response = requestJSON(t, fixture.client, http.MethodGet, fixture.server.URL+"/api/v1/prompts/templates", nil, "")
	var legacyPayload struct {
		Templates []domain.PromptTemplate `json:"templates"`
	}
	decodeResponse(t, response, &legacyPayload)
	if response.StatusCode != http.StatusOK || len(legacyPayload.Templates) != 29 {
		t.Fatalf("legacy template list changed: status=%d count=%d", response.StatusCode, len(legacyPayload.Templates))
	}
	for _, item := range legacyPayload.Templates {
		if _, ok := prompt.ResolveLegacyTemplateID(item.ID); !ok {
			t.Fatalf("legacy template %s no longer resolves to an anchor", item.ID)
		}
	}
	response = requestJSON(t, fixture.client, http.MethodPut, fixture.server.URL+"/api/v1/prompts/templates/role.main", map[string]any{
		"content": "CUSTOM MAIN API",
	}, fixture.csrf)
	var updated map[string]any
	decodeResponse(t, response, &updated)
	if response.StatusCode != http.StatusOK || updated["ok"] != true {
		t.Fatalf("legacy PUT semantics changed: status=%d payload=%#v", response.StatusCode, updated)
	}
	response = requestJSON(t, fixture.client, http.MethodGet, fixture.server.URL+"/api/v1/prompts/entries/core/anchors/role-main", nil, "")
	var previewPayload struct {
		Preview prompt.AnchorPreview `json:"preview"`
	}
	decodeResponse(t, response, &previewPayload)
	if response.StatusCode != http.StatusOK || previewPayload.Preview.Content != "CUSTOM MAIN API" || previewPayload.Preview.Source != "override" {
		t.Fatalf("legacy override is not visible through the read-only preview: status=%d preview=%#v", response.StatusCode, previewPayload.Preview)
	}
}

func TestPromptEntryPreviewRejectsUnknownBlankAndWriteRequests(t *testing.T) {
	t.Parallel()
	fixture := newPromptReadOnlyFixture(t)

	assertPreview := func(path string, wantDynamic, wantEditable bool) prompt.AnchorPreview {
		t.Helper()
		response := requestJSON(t, fixture.client, http.MethodGet, fixture.server.URL+path, nil, "")
		var payload struct {
			OK      bool                 `json:"ok"`
			Preview prompt.AnchorPreview `json:"preview"`
		}
		decodeResponse(t, response, &payload)
		if response.StatusCode != http.StatusOK || !payload.OK {
			t.Fatalf("preview %s failed: status=%d payload=%#v", path, response.StatusCode, payload)
		}
		if payload.Preview.Content == "" || payload.Preview.Rendered == "" {
			t.Fatalf("preview %s returned no content or sample rendering: %#v", path, payload.Preview)
		}
		if payload.Preview.Dynamic != wantDynamic || payload.Preview.Editable != wantEditable {
			t.Fatalf("preview %s has wrong editability: %#v", path, payload.Preview)
		}
		return payload.Preview
	}
	main := assertPreview("/api/v1/prompts/entries/core/anchors/main", false, true)
	if main.Layer != "core" || main.Protected || main.Kind != "resident" {
		t.Fatalf("core#main metadata is wrong: %#v", main)
	}
	generated := assertPreview("/api/v1/prompts/entries/task-context/anchors/task", true, false)
	if generated.Protected != true || generated.Source != "builtin" {
		t.Fatalf("generated preview is not code-owned: %#v", generated)
	}
	attachment := assertPreview("/api/v1/prompts/entries/core/anchors/attachment", false, false)
	if !attachment.Protected {
		t.Fatalf("attachment anchor lost its protected flag: %#v", attachment)
	}

	for _, testCase := range []struct {
		name string
		path string
		code int
	}{
		{"unknown entry", "/api/v1/prompts/entries/unknown/anchors/main", http.StatusNotFound},
		{"unknown anchor", "/api/v1/prompts/entries/core/anchors/unknown", http.StatusNotFound},
		{"blank entry", "/api/v1/prompts/entries/%20/anchors/main", http.StatusBadRequest},
		{"blank anchor", "/api/v1/prompts/entries/core/anchors/%20", http.StatusBadRequest},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := requestJSON(t, fixture.client, http.MethodGet, fixture.server.URL+testCase.path, nil, "")
			var payload map[string]any
			decodeResponse(t, response, &payload)
			if response.StatusCode != testCase.code || payload["ok"] != false || payload["error"] == "" {
				t.Fatalf("expected %d with explicit error, got status=%d payload=%#v", testCase.code, response.StatusCode, payload)
			}
		})
	}

	// The new entry view deliberately has no write route, including for editable
	// anchors. The legacy PUT/POST routes above remain the only write paths.
	for _, path := range []string{
		"/api/v1/prompts/entries/core/anchors/role-main",
		"/api/v1/prompts/entries/core/anchors/attachment",
		"/api/v1/prompts/entries/task-context/anchors/task",
	} {
		response := requestJSON(t, fixture.client, http.MethodPut, fixture.server.URL+path, map[string]any{"content": "SHOULD NOT WRITE"}, fixture.csrf)
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("entry write path %s was not rejected: %d", path, response.StatusCode)
		}
		if allow := response.Header.Get("Allow"); !strings.Contains(allow, http.MethodGet) {
			t.Fatalf("write rejection for %s did not advertise a read-only method: %q", path, allow)
		}
		response.Body.Close()
	}
	response := requestJSON(t, fixture.client, http.MethodPost, fixture.server.URL+"/api/v1/prompts/migration-preview", map[string]any{}, fixture.csrf)
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("migration preview accepted a write method: %d", response.StatusCode)
	}
	response.Body.Close()

	anonymous := &http.Client{}
	response = requestJSON(t, anonymous, http.MethodGet, fixture.server.URL+"/api/v1/prompts/entries", nil, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("entry catalog is not behind session auth: %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestPromptMigrationPreviewReadsWithoutWrites(t *testing.T) {
	t.Parallel()
	fixture := newPromptReadOnlyFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	overrides := []struct {
		id      string
		content string
	}{
		{"role.main", "CUSTOM MAIN"},
		{"context.task", "CUSTOM TASK"},
		{"protocol.attachment-delivery", "CUSTOM ATTACHMENT"},
		{"retired.prompt", "ORPHAN"},
	}
	for _, override := range overrides {
		if err := fixture.database.UpsertPromptTemplateOverride(ctx, override.id, override.content, now); err != nil {
			t.Fatalf("seed override %s: %v", override.id, err)
		}
	}
	before, err := fixture.database.PromptTemplateOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, fixture.client, http.MethodGet, fixture.server.URL+"/api/v1/prompts/migration-preview", nil, "")
	var payload struct {
		OK        bool                    `json:"ok"`
		Migration prompt.MigrationPreview `json:"migration"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || !payload.OK {
		t.Fatalf("migration preview failed: status=%d payload=%#v", response.StatusCode, payload)
	}
	after, err := fixture.database.PromptTemplateOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("migration preview mutated override storage:\nbefore=%#v\nafter=%#v", before, after)
	}
	if len(payload.Migration.Mappings) != 29 {
		t.Fatalf("migration preview must expose all 29 legacy mappings, got %d", len(payload.Migration.Mappings))
	}

	seen := map[string]int{}
	for _, merged := range payload.Migration.Merged {
		for _, id := range merged.LegacyIDs {
			seen[id]++
		}
	}
	for _, conflict := range payload.Migration.Conflicts {
		seen[conflict.LegacyID]++
	}
	for _, orphan := range payload.Migration.Orphans {
		seen[orphan.LegacyID]++
	}
	for _, ignored := range payload.Migration.Ignored {
		seen[ignored.LegacyID]++
	}
	if len(seen) != len(overrides) {
		t.Fatalf("every override must appear exactly once; got %#v", seen)
	}
	for _, override := range overrides {
		if seen[override.id] != 1 {
			t.Fatalf("override %s appeared %d times, want exactly 1", override.id, seen[override.id])
		}
	}
	if len(payload.Migration.Merged) != 1 || payload.Migration.Merged[0].EntryID != prompt.EntryCore ||
		payload.Migration.Merged[0].AnchorKey != "role-main" ||
		!reflect.DeepEqual(payload.Migration.Merged[0].LegacyIDs, []string{"role.main"}) {
		t.Fatalf("mergeable override was not mapped to core#role-main: %#v", payload.Migration.Merged)
	}
	conflictCodes := map[string]string{}
	for _, conflict := range payload.Migration.Conflicts {
		conflictCodes[conflict.LegacyID] = conflict.Code
		if strings.TrimSpace(conflict.Message) == "" {
			t.Fatalf("conflict %s has no reason: %#v", conflict.LegacyID, conflict)
		}
	}
	if conflictCodes["context.task"] != "dynamic_anchor" || conflictCodes["protocol.attachment-delivery"] != "read_only_anchor" {
		t.Fatalf("unexpected conflict classification: %#v", conflictCodes)
	}
	if len(payload.Migration.Orphans) != 1 || payload.Migration.Orphans[0].LegacyID != "retired.prompt" {
		t.Fatalf("orphan override was not preserved for review: %#v", payload.Migration.Orphans)
	}
}
