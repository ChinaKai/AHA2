package httpapi

import (
	"context"
	"database/sql"
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

func TestKnowledgeLibraryDeleteErrorMapping(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, id := range []string{"normal", "partial", "backend-error", "in-use"} {
		if err := database.CreateProject(ctx, domain.Project{ID: id, Name: id, ProjectType: "knowledge", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	target := domain.Project{ID: "target", Name: "Target", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, target); err != nil {
		t.Fatal(err)
	}
	if _, err := database.BindKnowledgeLibrary(ctx, "library_backend-error", target.ID, "external", now); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, domain.Workspace{ID: "workspace", ProjectID: "in-use", Name: "Workspace", Locality: "local", Transport: "native", RootPath: t.TempDir(), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// A separate test-only connection can recreate legacy corruption without FK cascades.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, `INSERT INTO knowledge_libraries(id,container_project_id,name,created_at,updated_at) VALUES(?,?,?,?,?)`, "library_orphan", "missing-container", "Orphan", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM knowledge_entries WHERE project_id='partial' AND is_index=1`); err != nil {
		t.Fatal(err)
	}
	// Force a downstream sql.ErrNoRows, not a missing library lookup.
	if _, err := raw.ExecContext(ctx, `CREATE TRIGGER block_binding_delete BEFORE DELETE ON project_knowledge_bindings WHEN OLD.library_id='library_backend-error' BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{Store: database, Auth: auth.NewService(database, "setup-test", time.Hour), App: app.NewService(database, secretStore, app.StubExecutor{})}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	for _, test := range []struct {
		id        string
		status    int
		code      string
		knowledge float64
	}{
		{"library_orphan", http.StatusOK, "", 0},
		{"library_orphan", http.StatusNotFound, "knowledge_library_not_found", 0},
		{"truly-missing", http.StatusNotFound, "knowledge_library_not_found", 0},
		{"library_partial", http.StatusOK, "", 0},
		{"library_normal", http.StatusOK, "", 1},
		{"library_backend-error", http.StatusInternalServerError, "delete_knowledge_library_failed", 0},
		{"library_in-use", http.StatusConflict, "delete_knowledge_library_failed", 0},
	} {
		response := requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/knowledge/libraries/"+test.id, nil, csrf)
		var payload map[string]any
		decodeResponse(t, response, &payload)
		if response.StatusCode != test.status {
			t.Fatalf("%s: status=%d want=%d payload=%#v", test.id, response.StatusCode, test.status, payload)
		}
		if test.code != "" {
			if payload["error"] != test.code {
				t.Fatalf("%s: payload=%#v", test.id, payload)
			}
		} else if deleted := payload["deleted"].(map[string]any); deleted["knowledge"] != test.knowledge || deleted["proposals"] != float64(0) || deleted["skills"] != float64(0) {
			t.Fatalf("%s: incorrect deletion counts: %#v", test.id, deleted)
		}
	}
	if _, err := database.KnowledgeLibrary(ctx, "library_backend-error"); err != nil {
		t.Fatalf("backend failure removed library: %v", err)
	}
	if _, err := database.KnowledgeLibraryBinding(ctx, "library_backend-error"); err != nil {
		t.Fatalf("backend failure removed binding: %v", err)
	}
	if _, err := database.Workspace(ctx, "workspace"); err != nil {
		t.Fatalf("safety check removed workspace: %v", err)
	}
}
