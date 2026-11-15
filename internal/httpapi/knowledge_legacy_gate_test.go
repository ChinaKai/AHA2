package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// The legacy knowledge API and library management used to read and write the
// old tables unconditionally. Once the vault is the live source for a project
// they must refuse with a diagnosable code, while an unbound project keeps its
// historical behavior.
func TestLegacyKnowledgeAPIIsGatedByTheVaultSource(t *testing.T) {
	ctx := context.Background()
	manager := knowledgeVaultManagerFixture(t)
	server, client, csrf, database := knowledgeVaultServerAt(t, manager, t.TempDir())

	now := time.Now().UTC()
	vaultProject := domain.Project{ID: "project-1", Name: "Vault Project", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, vaultProject); err != nil {
		t.Fatal(err)
	}
	// Requirement 4: creating a project while the vault reads must not seed the
	// legacy root. A later legacy fallback would create it lazily.
	indexes, err := database.KnowledgeIndexes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if root, found := indexes[vaultProject.ID]; found {
		t.Fatalf("vault switch created a legacy root: %#v", root)
	}

	// Simulate the unmigrated leftovers every deployment still has: a legacy
	// root and document for the vault-bound project.
	root, err := database.EnsureKnowledgeRoot(ctx, "project", vaultProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyEntry := domain.KnowledgeEntry{
		ID: "knowledge_legacy_project_1", Scope: "project", ProjectID: vaultProject.ID, ParentID: root.ID,
		Slug: "legacy-doc", Type: "practice", Title: "Legacy doc", Body: "Legacy body",
		Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}
	if err := database.CreateKnowledge(ctx, legacyEntry); err != nil {
		t.Fatal(err)
	}

	// An unbound project keeps reading and managing the legacy tables.
	unbound := domain.Project{ID: "project-2", Name: "Unbound", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, unbound); err != nil {
		t.Fatal(err)
	}
	unboundRoot, err := database.EnsureKnowledgeRoot(ctx, "project", unbound.ID)
	if err != nil {
		t.Fatal(err)
	}
	unboundEntry := domain.KnowledgeEntry{
		ID: "knowledge_legacy_project_2", Scope: "project", ProjectID: unbound.ID, ParentID: unboundRoot.ID,
		Slug: "unbound-doc", Type: "practice", Title: "Unbound doc", Body: "Unbound body",
		Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}
	if err := database.CreateKnowledge(ctx, unboundEntry); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/api/v1/knowledge?scope=project&project_id=project-1",
		"/api/v1/knowledge?scope=global",
		"/api/v1/knowledge",
	} {
		assertLegacyError(t, client, http.MethodGet, server.URL+path, nil, "", http.StatusConflict, knowledgeLegacyReadDisabled)
	}
	// A migrated ID resolves in the vault and is refused even if a legacy copy
	// still exists; a legacy-only ID in a vault-bound project is refused after
	// the legacy lookup.
	for _, id := range []string{"knowledge_http_deploy", "knowledge_legacy_project_1"} {
		assertLegacyError(t, client, http.MethodGet, server.URL+"/api/v1/knowledge/"+id, nil, "", http.StatusConflict, knowledgeLegacyReadDisabled)
	}
	// Unbound projects keep the legacy detail response.
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge/"+unboundEntry.ID, nil, "")
	var detail map[string]any
	decodeResponse(t, response, &detail)
	if response.StatusCode != http.StatusOK || detail["knowledge"].(map[string]any)["id"] != unboundEntry.ID {
		t.Fatalf("unbound detail status=%d payload=%#v", response.StatusCode, detail)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge?scope=project&project_id=project-2", nil, "")
	var listing map[string]any
	decodeResponse(t, response, &listing)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unbound list status=%d payload=%#v", response.StatusCode, listing)
	}
	foundUnbound := false
	for _, raw := range listing["knowledge"].([]any) {
		if raw.(map[string]any)["id"] == unboundEntry.ID {
			foundUnbound = true
		}
	}
	if !foundUnbound {
		t.Fatalf("unbound list lost its legacy entry: %#v", listing)
	}

	// The index endpoint drops the vault-bound project but keeps the unbound
	// root, which is still a valid channel knowledge source.
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge/indexes", nil, "")
	var indexesPayload map[string]any
	decodeResponse(t, response, &indexesPayload)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("indexes status=%d payload=%#v", response.StatusCode, indexesPayload)
	}
	indexed := indexesPayload["indexes"].([]any)
	if len(indexed) != 1 || indexed[0].(map[string]any)["project_id"] != unbound.ID {
		t.Fatalf("indexes leaked a vault project: %#v", indexed)
	}

	// Legacy library management is refused for the vault project, including
	// via the library's bound project.
	container := domain.Project{ID: "container-1", Name: "Vault Library", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, container); err != nil {
		t.Fatal(err)
	}
	containerTwo := domain.Project{ID: "container-2", Name: "Unbound Library", ProjectType: "knowledge", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, containerTwo); err != nil {
		t.Fatal(err)
	}
	libraryID := "library_" + container.ID
	if _, err := database.BindKnowledgeLibrary(ctx, libraryID, vaultProject.ID, "external", now); err != nil {
		t.Fatal(err)
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge/libraries", nil, "")
	var libraries map[string]any
	decodeResponse(t, response, &libraries)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("libraries status=%d payload=%#v", response.StatusCode, libraries)
	}
	for _, raw := range libraries["libraries"].([]any) {
		if raw.(map[string]any)["id"] == libraryID {
			t.Fatalf("vault-bound library leaked into the legacy list: %#v", libraries)
		}
	}

	for _, call := range []struct {
		method, path string
		payload      any
	}{
		{http.MethodPost, "/api/v1/knowledge/libraries/" + libraryID + "/bind", map[string]any{"project_id": vaultProject.ID, "binding_mode": "external"}},
		{http.MethodPost, "/api/v1/knowledge/libraries/" + libraryID + "/unbind", map[string]any{}},
		{http.MethodDelete, "/api/v1/knowledge/libraries/" + libraryID, nil},
		{http.MethodPost, "/api/v1/projects/" + vaultProject.ID + "/knowledge/detach", map[string]any{}},
		{http.MethodDelete, "/api/v1/projects/" + vaultProject.ID + "/knowledge", nil},
	} {
		assertLegacyError(t, client, call.method, server.URL+call.path, call.payload, csrf, http.StatusConflict, knowledgeLegacyManagementDisabled)
	}

	// Binding an unbound library to an unbound project still works.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge/libraries/library_"+containerTwo.ID+"/bind", map[string]any{"project_id": unbound.ID, "binding_mode": "external"}, csrf)
	var bound map[string]any
	decodeResponse(t, response, &bound)
	if response.StatusCode != http.StatusOK || bound["library"].(map[string]any)["bound_project_id"] != unbound.ID {
		t.Fatalf("unbound bind status=%d payload=%#v", response.StatusCode, bound)
	}
}

func assertLegacyError(t *testing.T, client *http.Client, method, url string, payload any, csrf string, status int, code string) {
	t.Helper()
	response := requestJSON(t, client, method, url, payload, csrf)
	var body map[string]any
	decodeResponse(t, response, &body)
	if response.StatusCode != status || body["error"] != code {
		t.Fatalf("%s %s status=%d payload=%#v, want %d %s", method, url, response.StatusCode, body, status, code)
	}
	if message, _ := body["message"].(string); message == "" {
		t.Fatalf("%s %s has no diagnostic message: %#v", method, url, body)
	}
}
