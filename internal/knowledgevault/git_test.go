package knowledgevault

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testGitCommand(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	command.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Git Test", "GIT_AUTHOR_EMAIL=git-test@example.com",
		"GIT_COMMITTER_NAME=Git Test", "GIT_COMMITTER_EMAIL=git-test@example.com",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func newGitTestVault(t *testing.T) (Config, *Git) {
	t.Helper()
	config := DefaultConfig(t.TempDir())
	config.AllowLocalRemote = true
	normalized, err := config.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(normalized, InitOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(normalized)
	if err != nil {
		t.Fatal(err)
	}
	return normalized, manager.Git()
}

func writeGitTestFile(t *testing.T, vaultPath, name, body string) {
	t.Helper()
	path := filepath.Join(vaultPath, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newBareRemote(t *testing.T) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	testGitCommand(t, t.TempDir(), "init", "--bare", "-b", "main", remote)
	return remote
}

func cloneRemote(t *testing.T, remote, name string) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), name)
	testGitCommand(t, t.TempDir(), "clone", remote, directory)
	testGitCommand(t, directory, "config", "user.name", "Git Test")
	testGitCommand(t, directory, "config", "user.email", "git-test@example.com")
	return directory
}

func TestGitInitIsIdempotentAndCreatesMain(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)

	status, err := git.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.Initialized || status.Branch != "main" {
		t.Fatalf("unexpected fresh status: %+v", status)
	}

	result, err := git.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || !result.Status.Initialized || !result.Status.HasCommits || !result.Status.Clean {
		t.Fatalf("unexpected init result: %+v", result)
	}
	if result.Status.Branch != "main" || result.Status.CurrentBranch != "main" {
		t.Fatalf("expected main branch, got %+v", result.Status)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, ".gitignore")); err != nil {
		t.Fatalf("missing .gitignore: %v", err)
	}
	firstCommit := result.Status.LastCommit

	again, err := git.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.Created || again.Status.LastCommit != firstCommit {
		t.Fatalf("second init was not idempotent: first=%+v second=%+v", result, again)
	}

	// A missing .gitignore is restored without rewriting the existing repo.
	if err := os.Remove(filepath.Join(config.VaultPath, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, ".gitignore")); err != nil {
		t.Fatalf(".gitignore was not restored: %v", err)
	}
}

func TestGitRemoteValidationRejectsUnsafeAndCredentialURLs(t *testing.T) {
	invalid := []string{
		"https://user:token@example.com/org/repo.git",
		"https://token@example.com/org/repo.git",
		"git@example.com:../escape.git",
		"git@example.com:repo;touch /tmp/pwned",
		"git@example.com:repo$(id)",
		"ssh://user:password@example.com/repo.git",
		"ssh://example.com/../../etc",
		"ssh://example.com",
		"-oProxyCommand=evil",
		"git@example.com:repo with space",
	}
	for _, remote := range invalid {
		if err := ValidateGitRemote(remote, false); err == nil {
			t.Errorf("remote %q was accepted", remote)
		}
	}
	for _, remote := range []string{
		"git@example.com:org/repo.git",
		"git@example.com:/srv/repos/repo.git",
		"ssh://git@example.com/org/repo.git",
		"ssh://git@example.com:2222/org/repo.git",
	} {
		if err := ValidateGitRemote(remote, false); err != nil {
			t.Errorf("remote %q was rejected: %v", remote, err)
		}
	}

	for _, branch := range []string{"--force", "-main", "main..backup", "foo/../bar", "main~1", "main@{1}", "main.lock", "a//b"} {
		if err := ValidateGitBranch(branch); err == nil {
			t.Errorf("branch %q was accepted", branch)
		}
	}
	for _, branch := range []string{"main", "release/0.5", "feature-git"} {
		if err := ValidateGitBranch(branch); err != nil {
			t.Errorf("branch %q was rejected: %v", branch, err)
		}
	}
}

func TestGitCommitPushPullAndSyncWithLocalBareRemote(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	remote := newBareRemote(t)

	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := git.SetRemote(ctx, remote, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Status.RemoteSet || result.Status.Remote == remote || strings.Contains(result.Status.Remote, remote) {
		t.Fatalf("remote must be masked in status: %+v", result.Status)
	}
	if result.Status.Branch != "main" {
		t.Fatalf("unexpected branch: %+v", result.Status)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), remote) {
		t.Fatalf("full local path leaked through JSON: %s", encoded)
	}

	writeGitTestFile(t, config.VaultPath, "global/general/push.md", "# Push me\n")
	commit, err := git.Commit(ctx, "Add push note")
	if err != nil {
		t.Fatal(err)
	}
	if commit.Status.LastCommit == "" || !commit.Status.Clean || commit.Status.Changed != 0 {
		t.Fatalf("unexpected commit status: %+v", commit)
	}
	if !strings.Contains(commit.Status.LastCommitMsg, "Add push note") {
		t.Fatalf("unexpected commit message: %+v", commit.Status)
	}
	noChange, err := git.Commit(ctx, "nothing")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(noChange.Message, "没有需要提交") {
		t.Fatalf("unexpected no-op commit message: %+v", noChange)
	}

	push, err := git.Push(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if push.Status.Ahead != 0 || push.Status.LastSyncAt == "" {
		t.Fatalf("unexpected push status: %+v", push.Status)
	}
	remoteHead := testGitCommand(t, remote, "rev-parse", "refs/heads/main")
	if remoteHead != push.Status.LastCommit && !strings.HasPrefix(remoteHead, push.Status.LastCommit) {
		// LastCommit is abbreviated; compare with the full local HEAD.
		localHead := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
		if remoteHead != localHead {
			t.Fatalf("remote head %s != local %s", remoteHead, localHead)
		}
	}

	clone := cloneRemote(t, remote, "owner-two")
	writeGitTestFile(t, clone, "global/general/pull.md", "# Pulled\n")
	testGitCommand(t, clone, "add", "-A")
	testGitCommand(t, clone, "commit", "-m", "Add pull note")
	testGitCommand(t, clone, "push", "origin", "main")
	testGitCommand(t, config.VaultPath, "fetch", "origin")
	behind, err := git.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if behind.Behind != 1 || behind.Ahead != 0 {
		t.Fatalf("expected one behind, got %+v", behind)
	}

	pull, err := git.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pull.Status.Behind != 0 || pull.Status.Ahead != 0 {
		t.Fatalf("unexpected pull status: %+v", pull.Status)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "global/general/pull.md")); err != nil {
		t.Fatalf("pulled file missing: %v", err)
	}

	writeGitTestFile(t, config.VaultPath, "global/general/sync.md", "# Sync\n")
	if _, err := git.Commit(ctx, "Add sync note"); err != nil {
		t.Fatal(err)
	}
	syncResult, err := git.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if syncResult.Status.Ahead != 0 || syncResult.Status.Behind != 0 || syncResult.Status.LastSyncAt == "" {
		t.Fatalf("unexpected sync status: %+v", syncResult.Status)
	}
	remoteHead = testGitCommand(t, remote, "rev-parse", "refs/heads/main")
	localHead := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
	if remoteHead != localHead {
		t.Fatalf("sync did not push: remote %s local %s", remoteHead, localHead)
	}
}

func TestGitDirtyPullAndSyncStopWithoutChangingContent(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	remote := newBareRemote(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := git.SetRemote(ctx, remote, "main"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/base.md", "# Base\n")
	if _, err := git.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Push(ctx); err != nil {
		t.Fatal(err)
	}

	writeGitTestFile(t, config.VaultPath, "global/general/base.md", "# Local edit\n")
	before, err := os.ReadFile(filepath.Join(config.VaultPath, "global/general/base.md"))
	if err != nil {
		t.Fatal(err)
	}
	status, err := git.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Clean || status.Changed == 0 {
		t.Fatalf("expected dirty status, got %+v", status)
	}
	if _, err := git.Pull(ctx); !errors.Is(err, ErrGitDirty) {
		t.Fatalf("pull should refuse a dirty tree, got %v", err)
	}
	if _, err := git.Sync(ctx); !errors.Is(err, ErrGitDirty) {
		t.Fatalf("sync should refuse a dirty tree, got %v", err)
	}
	after, err := os.ReadFile(filepath.Join(config.VaultPath, "global/general/base.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("failed operations changed local content: %q -> %q", before, after)
	}
}

func TestGitDivergedPullAndRejectedPushStopWithoutForce(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	remote := newBareRemote(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := git.SetRemote(ctx, remote, "main"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/base.md", "# Base\n")
	if _, err := git.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Push(ctx); err != nil {
		t.Fatal(err)
	}

	clone := cloneRemote(t, remote, "diverge")
	writeGitTestFile(t, clone, "global/general/remote.md", "# Remote\n")
	testGitCommand(t, clone, "add", "-A")
	testGitCommand(t, clone, "commit", "-m", "remote side")
	testGitCommand(t, clone, "push", "origin", "main")

	writeGitTestFile(t, config.VaultPath, "global/general/local.md", "# Local\n")
	if _, err := git.Commit(ctx, "local side"); err != nil {
		t.Fatal(err)
	}
	testGitCommand(t, config.VaultPath, "fetch", "origin")
	status, err := git.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Ahead != 1 || status.Behind != 1 {
		t.Fatalf("expected divergence, got %+v", status)
	}
	localBefore := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
	if _, err := git.Pull(ctx); !errors.Is(err, ErrGitDiverged) {
		t.Fatalf("pull should report divergence, got %v", err)
	}
	localAfter := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
	if localBefore != localAfter {
		t.Fatalf("failed pull changed HEAD: %s -> %s", localBefore, localAfter)
	}
	if _, err := git.Push(ctx); !errors.Is(err, ErrGitPushRejected) {
		t.Fatalf("push should be rejected, got %v", err)
	}
	if remoteHead := testGitCommand(t, remote, "rev-parse", "refs/heads/main"); remoteHead == localAfter {
		t.Fatal("rejected push must not change the remote branch")
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "global/general/local.md")); err != nil {
		t.Fatalf("local content disappeared: %v", err)
	}
}

func TestGitUnreachableRemoteAndUninitializedVault(t *testing.T) {
	ctx := context.Background()

	emptyConfig := DefaultConfig(t.TempDir())
	emptyConfig.AllowLocalRemote = true
	emptyManager, err := NewManager(emptyConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := emptyManager.Git().Init(ctx); !errors.Is(err, ErrVaultNotInitialized) {
		t.Fatalf("init without a vault should fail, got %v", err)
	}

	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := git.SetRemote(ctx, "ssh://git@127.0.0.1:1/repo.git", "main"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/offline.md", "# Offline\n")
	if _, err := git.Commit(ctx, "offline note"); err != nil {
		t.Fatal(err)
	}
	before := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
	if _, err := git.Push(ctx); err == nil {
		t.Fatal("push to an unreachable SSH remote unexpectedly succeeded")
	}
	after := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD")
	if before != after {
		t.Fatalf("failed push changed local HEAD: %s -> %s", before, after)
	}
	status, err := git.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(status.Remote, "/repo.git") || !strings.Contains(status.Remote, "127.0.0.1") {
		t.Fatalf("unexpected masked remote: %q", status.Remote)
	}
}

func TestGitSetRemoteKeepsExistingRemoteAndRejectsTraversal(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	remote := newBareRemote(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := git.SetRemote(ctx, remote, "main"); err != nil {
		t.Fatal(err)
	}
	// Branch-only update keeps the stored remote.
	result, err := git.SetRemote(ctx, "", "release/0.5")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Branch != "release/0.5" || !result.Status.RemoteSet {
		t.Fatalf("unexpected branch-only update: %+v", result.Status)
	}
	if _, err := git.SetRemote(ctx, "", ""); err == nil {
		t.Fatal("empty remote and branch should be rejected")
	}
	if _, err := git.SetRemote(ctx, t.TempDir()+"/../escape.git", "main"); err == nil {
		t.Fatal("local path traversal should be rejected")
	}
	// The rejected values must not leak into the repository config.
	configured := testGitCommand(t, config.VaultPath, "remote", "get-url", "origin")
	if configured != remote {
		t.Fatalf("rejected update changed remote: %q", configured)
	}
}
