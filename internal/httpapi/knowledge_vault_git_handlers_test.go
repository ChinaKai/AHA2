package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

func gitHelper(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	command.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=HTTP Test", "GIT_AUTHOR_EMAIL=http-test@example.com",
		"GIT_COMMITTER_NAME=HTTP Test", "GIT_COMMITTER_EMAIL=http-test@example.com",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestKnowledgeVaultGitAPIRequiresOwnerAndCSRF(t *testing.T) {
	dataDir := t.TempDir()
	config := knowledgevault.DefaultConfig(dataDir)
	config.AllowLocalRemote = true
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	server, client, csrf, _ := knowledgeVaultServerAt(t, manager, dataDir)

	anonymous := &http.Client{}
	response := requestJSON(t, anonymous, http.MethodGet, server.URL+"/api/v1/knowledge-vault/git/status", nil, "")
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d, want 401", response.StatusCode)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge-vault/git/remote",
		map[string]any{"remote": "git@example.com:org/repo.git"}, "")
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusForbidden || payload["error"] != "csrf_rejected" {
		t.Fatalf("missing csrf status=%d payload=%#v", response.StatusCode, payload)
	}

	// The status endpoint is safe to read before the vault is initialized.
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/git/status", nil, "")
	var status map[string]any
	decodeResponse(t, response, &status)
	git := status["git"].(map[string]any)
	if response.StatusCode != http.StatusOK || git["initialized"] != false ||
		git["branch"] != "main" || git["available"] != true {
		t.Fatalf("fresh git status=%d payload=%#v", response.StatusCode, status)
	}
	_ = csrf
}

func TestKnowledgeVaultGitAPILocalRemoteLifecycle(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	config := knowledgevault.DefaultConfig(dataDir)
	config.AllowLocalRemote = true
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, err := manager.Initialize(ctx, false); err != nil {
		t.Fatal(err)
	}
	server, client, csrf, _ := knowledgeVaultServerAt(t, manager, dataDir)

	remote := filepath.Join(t.TempDir(), "remote.git")
	gitHelper(t, t.TempDir(), "init", "--bare", "-b", "main", remote)

	post := func(path string, payload any) map[string]any {
		t.Helper()
		response := requestJSON(t, client, http.MethodPost, server.URL+path, payload, csrf)
		var body map[string]any
		decodeResponse(t, response, &body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d payload=%#v", path, response.StatusCode, body)
		}
		return body
	}

	initialized := post("/api/v1/knowledge-vault/git/init", nil)
	if initialized["created"] != true || initialized["git"].(map[string]any)["initialized"] != true {
		t.Fatalf("init payload=%#v", initialized)
	}
	status := initialized["git"].(map[string]any)
	if status["branch"] != "main" || status["clean"] != true || status["has_commits"] != true {
		t.Fatalf("init git status=%#v", status)
	}

	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge-vault/git/remote",
		map[string]any{"remote": remote, "branch": "main"}, csrf)
	var remoteResult map[string]any
	decodeResponse(t, response, &remoteResult)
	if response.StatusCode != http.StatusOK || remoteResult["git"].(map[string]any)["remote_set"] != true {
		t.Fatalf("set remote status=%d payload=%#v", response.StatusCode, remoteResult)
	}
	if full, present := remoteResult["git"].(map[string]any)["remote_full"]; present && full != "" {
		t.Fatalf("test-only local remote must not be exposed in full: %#v", remoteResult)
	}
	encoded, _ := json.Marshal(remoteResult)
	if strings.Contains(string(encoded), remote) {
		t.Fatalf("full remote leaked in response: %s", encoded)
	}

	if err := os.WriteFile(filepath.Join(config.VaultPath, "api-note.md"), []byte("# API note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed := post("/api/v1/knowledge-vault/git/commit", map[string]any{"message": "api commit"})
	commitStatus := committed["git"].(map[string]any)
	if commitStatus["clean"] != true || !strings.Contains(commitStatus["last_commit_message"].(string), "api commit") {
		t.Fatalf("commit status=%#v", commitStatus)
	}

	pushed := post("/api/v1/knowledge-vault/git/push", nil)
	if pushed["git"].(map[string]any)["ahead"].(float64) != 0 {
		t.Fatalf("push status=%#v", pushed)
	}
	remoteHead := gitHelper(t, remote, "rev-parse", "refs/heads/main")
	localHead := gitHelper(t, config.VaultPath, "rev-parse", "HEAD")
	if remoteHead != localHead {
		t.Fatalf("push did not update remote: remote=%s local=%s", remoteHead, localHead)
	}

	synced := post("/api/v1/knowledge-vault/git/sync", nil)
	if synced["message"] != "已同步" || synced["git"].(map[string]any)["last_sync_at"] == "" {
		t.Fatalf("sync payload=%#v", synced)
	}
	if _, rebuilt := synced["rebuild"]; !rebuilt {
		t.Fatalf("sync must rebuild the index from the pulled files: %#v", synced)
	}
	if settings, ok := synced["settings"].(map[string]any); !ok || settings["readiness"] == nil {
		t.Fatalf("sync must recompute readiness after the rebuild: %#v", synced)
	}

	// A pull that actually brings a new file must rebuild the index too; a
	// successful fetch alone is not readiness.
	cloneDir := t.TempDir()
	gitHelper(t, cloneDir, "clone", remote, cloneDir)
	if err := os.WriteFile(filepath.Join(cloneDir, "pulled-note.md"), []byte("# Pulled note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitHelper(t, cloneDir, "add", "pulled-note.md")
	gitHelper(t, cloneDir, "commit", "-m", "remote note")
	gitHelper(t, cloneDir, "push", "origin", "main")
	pulled := post("/api/v1/knowledge-vault/git/pull", nil)
	rebuild, ok := pulled["rebuild"].(map[string]any)
	if !ok || rebuild["vault_path"] == "" {
		t.Fatalf("pull must rebuild the index: %#v", pulled)
	}
	pullSettings, ok := pulled["settings"].(map[string]any)
	if !ok || pullSettings["readiness"] == nil {
		t.Fatalf("pull must recompute readiness: %#v", pulled)
	}
	// The pulled file must be visible through the same reader the Web uses,
	// which proves the index was rebuilt after the pull.
	search := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge-vault/search?q=pulled-note", nil, "")
	var searchBody map[string]any
	decodeResponse(t, search, &searchBody)
	results, _ := searchBody["results"].([]any)
	if search.StatusCode != http.StatusOK || len(results) == 0 {
		t.Fatalf("pulled file was not indexed: status=%d payload=%#v", search.StatusCode, searchBody)
	}

	// Dirty worktree stops pull/sync with a conflict response, not a merge.
	if err := os.WriteFile(filepath.Join(config.VaultPath, "api-note.md"), []byte("# dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/knowledge-vault/git/pull", "/api/v1/knowledge-vault/git/sync"} {
		response := requestJSON(t, client, http.MethodPost, server.URL+path, nil, csrf)
		var body map[string]any
		decodeResponse(t, response, &body)
		if response.StatusCode != http.StatusConflict || body["error"] != "git_worktree_dirty" {
			t.Fatalf("%s dirty status=%d payload=%#v", path, response.StatusCode, body)
		}
	}
}

func TestKnowledgeVaultGitAPIRejectsInvalidRemoteAndBranch(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	config := knowledgevault.DefaultConfig(dataDir)
	config.AllowLocalRemote = true
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, err := manager.Initialize(ctx, false); err != nil {
		t.Fatal(err)
	}
	server, client, csrf, _ := knowledgeVaultServerAt(t, manager, dataDir)
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge-vault/git/init", nil, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("init status=%d", response.StatusCode)
	}

	cases := []struct {
		remote string
		branch string
		code   string
	}{
		{"https://user:secret-token@example.com/org/repo.git", "", "invalid_git_remote"},
		{"git@example.com:repo;touch /tmp/pwned", "", "invalid_git_remote"},
		{"git@example.com:../escape.git", "", "invalid_git_remote"},
		{"git@example.com:org/repo.git", "--force", "invalid_git_branch"},
	}
	for _, testCase := range cases {
		response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge-vault/git/remote",
			map[string]any{"remote": testCase.remote, "branch": testCase.branch}, csrf)
		var body map[string]any
		decodeResponse(t, response, &body)
		if response.StatusCode != http.StatusBadRequest || body["error"] != testCase.code {
			t.Fatalf("remote=%q branch=%q status=%d payload=%#v", testCase.remote, testCase.branch, response.StatusCode, body)
		}
		encoded, _ := json.Marshal(body)
		if strings.Contains(string(encoded), "secret-token") || strings.Contains(string(encoded), "pwned") {
			t.Fatalf("rejected input leaked into the error: %s", encoded)
		}
	}
}

func TestKnowledgeVaultGitAPIReportsUnreachableSSHRemoteWithoutLeakingURL(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	config := knowledgevault.DefaultConfig(dataDir)
	config.AllowLocalRemote = true
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, err := manager.Initialize(ctx, false); err != nil {
		t.Fatal(err)
	}
	server, client, csrf, _ := knowledgeVaultServerAt(t, manager, dataDir)
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge-vault/git/init", nil, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("init status=%d", response.StatusCode)
	}

	const remote = "ssh://git@127.0.0.1:1/secret/repo.git"
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/knowledge-vault/git/remote",
		map[string]any{"remote": remote, "branch": "main"}, csrf)
	var saved map[string]any
	decodeResponse(t, response, &saved)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("set remote status=%d payload=%#v", response.StatusCode, saved)
	}
	// The authenticated settings page receives the full SSH remote so the owner
	// can read and copy it, while the masked field keeps existing consumers.
	savedGit := saved["git"].(map[string]any)
	if savedGit["remote_full"] != remote {
		t.Fatalf("status must expose the full SSH remote: %#v", savedGit)
	}
	if masked, _ := savedGit["remote"].(string); masked == "" || strings.Contains(masked, "/secret/repo.git") {
		t.Fatalf("masked remote must hide the repository path: %#v", savedGit)
	}
	if err := os.WriteFile(filepath.Join(config.VaultPath, "offline.md"), []byte("# offline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge-vault/git/commit", map[string]any{"message": "offline"}, csrf)
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/knowledge-vault/git/push", nil, csrf)
	var body map[string]any
	decodeResponse(t, response, &body)
	if response.StatusCode != http.StatusBadGateway || body["error"] != "git_command_failed" {
		t.Fatalf("unreachable push status=%d payload=%#v", response.StatusCode, body)
	}
	encoded, _ := json.Marshal(body)
	if strings.Contains(string(encoded), "/secret/repo.git") || strings.Contains(string(encoded), remote) {
		t.Fatalf("remote path leaked in error: %s", encoded)
	}
	if !strings.Contains(string(encoded), "127.0.0.1") {
		t.Fatalf("masked remote should still identify the host: %s", encoded)
	}
}
