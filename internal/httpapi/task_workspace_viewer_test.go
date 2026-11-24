package httpapi

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskWorkspaceViewerHTTP(t *testing.T) {
	fixture := newTaskDetailFixture(t, false)
	base := fixture.server.URL + "/api/v1/tasks/" + fixture.task.ID + "/workspace/"
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "example.txt"), []byte("from task worktree"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateTaskWorkspace(t.Context(), fixture.task.ID, worktree, "", "", "", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		client       *http.Client
		status       int
		body         string
	}{
		{"GET", "state", http.DefaultClient, 401, "authentication_required"},
		{"GET", "state", fixture.client, 200, `"isGit":false`},
		{"GET", "state?availability=1", fixture.client, 200, `"available":true`},
		{"GET", "tree", fixture.client, 200, "example.txt"},
		{"GET", "file?path=example.txt", fixture.client, 200, "from task worktree"},
		{"HEAD", "file?path=example.txt", fixture.client, 200, ""},
		{"GET", "file?path=%2e%2e%2fexample.txt", fixture.client, 400, "workspace_viewer_invalid_path"},
		{"GET", "state?root=" + url.QueryEscape(worktree), fixture.client, 400, "workspace_viewer_invalid_path"},
		{"POST", "file?path=example.txt", fixture.client, 405, "method_not_allowed"},
		{"DELETE", "file?path=example.txt", fixture.client, 405, "method_not_allowed"},
		{"OPTIONS", "file", fixture.client, 405, "method_not_allowed"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), tc.method, base+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := tc.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status || !strings.Contains(string(body), tc.body) {
				t.Fatalf("HTTP %d, %s", response.StatusCode, body)
			}
			if tc.method == http.MethodHead && len(body) != 0 {
				t.Fatalf("HEAD returned a body: %s", body)
			}
			if response.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("cache policy: %s", response.Header.Get("Cache-Control"))
			}
		})
	}
	missing := filepath.Join(t.TempDir(), "removed-worktree")
	if err := fixture.database.UpdateTaskWorkspace(t.Context(), fixture.task.ID, missing, "", "", "", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	response := requestJSON(t, fixture.client, http.MethodGet, base+"state?availability=1", nil, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing worktree: HTTP %d", response.StatusCode)
	}
}
