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

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspaceterminal"
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
		{"GET", "state?branch=HEAD%5E", fixture.client, 400, "workspace_viewer_invalid_branch"},
		{"GET", "state?branch=refs%2Fheads%2Fexample", fixture.client, 400, "workspace_viewer_not_git"},
		{"GET", "state?branch=refs%2Fheads%2Fexample&branch=", fixture.client, 400, "workspace_viewer_invalid_branch"},
		{"GET", "state?branch=%GG", fixture.client, 400, "workspace_viewer_invalid_path"},
		{"GET", "tree", fixture.client, 200, "example.txt"},
		{"GET", "file?path=example.txt", fixture.client, 200, "from task worktree"},
		{"HEAD", "file?path=example.txt", fixture.client, 200, ""},
		{"GET", "file?path=%2e%2e%2fexample.txt", fixture.client, 400, "workspace_viewer_invalid_path"},
		{"GET", "state?root=" + url.QueryEscape(worktree), fixture.client, 400, "workspace_viewer_invalid_path"},
		{"GET", "state?worktree=" + url.QueryEscape(worktree), fixture.client, 400, "workspace_viewer_invalid_worktree"},
		{"GET", "state?worktree=" + strings.Repeat("f", 64), fixture.client, 400, "workspace_viewer_invalid_worktree"},
		{"GET", "state?worktree=", fixture.client, 400, "workspace_viewer_invalid_worktree"},
		{"GET", "tree?worktree=one&worktree=two", fixture.client, 400, "workspace_viewer_invalid_worktree"},
		{"GET", "state?repo=" + url.QueryEscape(worktree), fixture.client, 400, "workspace_viewer_invalid_repository"},
		{"GET", "tree?repo=ordinary", fixture.client, 400, "workspace_viewer_invalid_repository"},
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

func TestWorkspaceViewerAndTerminalWithoutTaskHTTP(t *testing.T) {
	fixture := newTaskDetailFixture(t, false)
	ctx := t.Context()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-without-tasks", Name: "Empty project", CreatedAt: now, UpdatedAt: now}
	if err := fixture.database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "example.txt"), []byte("from the workspace root"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []domain.Workspace{
		{ID: "empty", Name: "Empty workspace", Health: "ready"},
		{ID: "mirror", Name: "Remote mirror", Health: "ready", ReadOnly: true},
		{ID: "unhealthy", Name: "Unhealthy workspace", Health: "error"},
	} {
		ws.ProjectID, ws.RootPath = project.ID, root
		ws.Locality, ws.Transport = "local", "native"
		ws.CreatedAt, ws.UpdatedAt = now, now
		if err := fixture.database.CreateWorkspace(ctx, ws); err != nil {
			t.Fatal(err)
		}
	}
	base := fixture.server.URL + "/api/v1/workspaces/"
	check := func(client *http.Client, method, path string, wantStatus int, wantBody string) {
		t.Helper()
		response := requestJSON(t, client, method, base+path, nil, "")
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != wantStatus || !strings.Contains(string(body), wantBody) {
			t.Fatalf("%s %s: HTTP %d %s (%v)", method, path, response.StatusCode, body, err)
		}
	}
	check(http.DefaultClient, "GET", "empty/viewer/state", 401, "authentication_required")
	check(http.DefaultClient, "GET", "empty/workspace-terminal", 401, "authentication_required")
	check(fixture.client, "GET", "empty/viewer/state?availability=1", 200, `"available":true`)
	check(fixture.client, "GET", "empty/viewer/file?path=example.txt", 200, "from the workspace root")
	check(fixture.client, "GET", "empty/viewer/state?root="+url.QueryEscape(root), 400, "workspace_viewer_invalid_path")
	check(fixture.client, "POST", "empty/viewer/file?path=example.txt", 405, "method_not_allowed")
	check(fixture.client, "POST", "empty/workspace-terminal", 403, "csrf_rejected")
	check(fixture.client, "GET", "empty/workspace-terminal?workspace=mirror", 400, "workspace_terminal_invalid_target")
	for _, workspace := range []string{"mirror", "unhealthy", "missing"} {
		check(fixture.client, "GET", workspace+"/viewer/state", 404, "workspace_viewer_unavailable")
		check(fixture.client, "GET", workspace+"/workspace-terminal", 404, "workspace_terminal_unavailable")
	}
	if workspaceterminal.LocalSupported() {
		response := requestJSON(t, fixture.client, "GET", base+"empty/workspace-terminal", nil, "")
		var terminal struct{ Cwd string }
		decodeResponse(t, response, &terminal)
		if response.StatusCode != http.StatusOK || terminal.Cwd != root {
			t.Fatalf("workspace terminal: HTTP %d, cwd %q, want %q", response.StatusCode, terminal.Cwd, root)
		}
	}
}
