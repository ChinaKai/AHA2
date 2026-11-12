package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestAgentScratchDirUsesTaskContextLayout(t *testing.T) {
	t.Parallel()
	native := domain.Workspace{Transport: "native"}
	root := t.TempDir()
	got, err := AgentScratchDir(native, root, "task-1", "main")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".aha2-context", "task-1", "scratch", "main")
	if got != want {
		t.Fatalf("native scratch = %q, want %q", got, want)
	}
	remote := domain.Workspace{Transport: "ssh", Platform: "linux/amd64"}
	got, err = AgentScratchDir(remote, "/srv/repo", "task-1", "sub-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/srv/repo/.aha2-context/task-1/scratch/sub-1" {
		t.Fatalf("remote scratch = %q", got)
	}
	windows := domain.Workspace{Transport: "ssh", Platform: "windows/amd64"}
	got, err = AgentScratchDir(windows, `\\server\share\repo`, "task-1", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got != `\\server\share\repo\.aha2-context\task-1\scratch\main` {
		t.Fatalf("Windows scratch = %q", got)
	}
}

func TestScratchIDsMustBeSinglePathSegments(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{Transport: "native"}
	root := t.TempDir()
	for _, invalid := range []string{"", ".", "..", "../escape", "task/child", `task\child`, "/absolute", "C:drive"} {
		if _, err := AgentScratchDir(item, root, invalid, "main"); err == nil {
			t.Fatalf("invalid task id %q was accepted", invalid)
		}
		if _, err := AgentScratchDir(item, root, "task-1", invalid); err == nil {
			t.Fatalf("invalid agent id %q was accepted", invalid)
		}
		if err := RemoveTaskScratch(context.Background(), item, root, invalid); err == nil {
			t.Fatalf("cleanup accepted invalid task id %q", invalid)
		}
	}
}

func TestEnsureScratchDirCreatesOwnerOnlyDirectory(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{Transport: "native"}
	root := t.TempDir()
	directory, err := EnsureScratchDir(context.Background(), item, root, "task-1", "main")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		t.Fatalf("scratch directory: %v %v", info, err)
	}
	if runtime.GOOS != "windows" {
		if permission := info.Mode().Perm(); permission != 0o700 {
			t.Fatalf("scratch permissions = %#o, want 0700", permission)
		}
	}
	// A second call is idempotent and keeps existing files.
	probe := filepath.Join(directory, "probe.log")
	if err := os.WriteFile(probe, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureScratchDir(context.Background(), item, root, "task-1", "main"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(probe); err != nil || string(data) != "keep" {
		t.Fatalf("idempotent ensure changed scratch: %q %v", data, err)
	}
}

func TestRemoveTaskScratchLeavesOtherContextAndWorkspaceFiles(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{Transport: "native"}
	root := t.TempDir()
	taskRoot := filepath.Join(root, ".aha2-context", "task-1")
	for _, path := range []string{
		filepath.Join(taskRoot, "main", "task.md"),
		filepath.Join(taskRoot, "shared-"+strings.Repeat("a", sha256HexLength), "agent-api.md"),
		filepath.Join(taskRoot, "scratch", "main", "probe.log"),
		filepath.Join(taskRoot, "scratch", "sub-1", "nested", "probe.log"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	workspaceFile := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(workspaceFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveTaskScratch(context.Background(), item, root, "task-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(taskRoot, "scratch")); !os.IsNotExist(err) {
		t.Fatalf("scratch survived cleanup: %v", err)
	}
	for _, retained := range []string{
		filepath.Join(taskRoot, "main", "task.md"),
		filepath.Join(taskRoot, "shared-"+strings.Repeat("a", sha256HexLength), "agent-api.md"),
		workspaceFile,
	} {
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("cleanup removed %s: %v", retained, err)
		}
	}
	// Cleanup on an absent scratch directory is a no-op.
	if err := RemoveTaskScratch(context.Background(), item, root, "task-1"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveTaskScratchRefusesSymlinkedContextEscape(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{Transport: "native"}
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "task-1", "scratch", "main", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(victim), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".aha2-context")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if err := RemoveTaskScratch(context.Background(), item, root, "task-1"); err == nil {
		t.Fatal("cleanup followed a context symlink outside the workdir")
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatalf("outside data was changed: %q %v", data, err)
	}
}

func TestScratchStaysIgnoredByGit(t *testing.T) {
	t.Parallel()
	if err := exec.Command("git", "--version").Run(); err != nil {
		t.Skipf("git is unavailable: %v", err)
	}
	root := t.TempDir()
	if output, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	item := domain.Workspace{Transport: "native"}
	contextRoot := filepath.Join(root, ".aha2-context", "task-1", "main")
	if err := MaterializeContext(context.Background(), item, root, contextRoot, map[string]string{
		filepath.Join(contextRoot, "task.md"): "task",
	}); err != nil {
		t.Fatal(err)
	}
	directory, err := EnsureScratchDir(context.Background(), item, root, "task-1", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "probe.log"), []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(status)) != "" {
		t.Fatalf("scratch polluted git status: %q", status)
	}
	ignored := filepath.Join(".aha2-context", "task-1", "scratch", "main", "probe.log")
	if err := exec.Command("git", "-C", root, "check-ignore", "-q", ignored).Run(); err != nil {
		t.Fatalf("scratch file is not git-ignored: %v", err)
	}
}

func TestRemoteScratchHelpersKeepPathsAndGuards(t *testing.T) {
	t.Parallel()
	var create, remove Command
	runner := contextRunnerFunc(func(command Command) (Result, error) {
		switch command.Args[len(command.Args)-1] {
		case "/srv/repo/.aha2-context/task-1/scratch/main":
			create = command
		default:
			remove = command
		}
		return Result{}, nil
	})
	item := domain.Workspace{Transport: "ssh", Platform: "linux/amd64"}
	if err := ensureRemoteScratchDir(context.Background(), item, runner, "/srv/repo/.aha2-context/task-1/scratch/main"); err != nil {
		t.Fatal(err)
	}
	if create.Executable != "sh" || !strings.Contains(strings.Join(create.Args, " "), "umask 077") ||
		!strings.Contains(strings.Join(create.Args, " "), "chmod 700") {
		t.Fatalf("remote scratch create is not owner-only: %#v", create)
	}
	if err := removeRemoteTaskScratch(context.Background(), item, runner, "/srv/repo", "task-1"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(remove.Args, " ")
	if remove.Executable != "sh" || !strings.Contains(joined, "pwd -P") ||
		!strings.Contains(joined, "refusing to remove scratch outside task boundary") ||
		!strings.Contains(joined, "rm -rf --") {
		t.Fatalf("remote scratch cleanup lacks its boundary guard: %#v", remove)
	}
	if remove.Dir != "/srv/repo" {
		t.Fatalf("remote scratch cleanup dir = %q", remove.Dir)
	}
}

func TestRemoteWindowsScratchCleanupRefusesReparsePoints(t *testing.T) {
	t.Parallel()
	var command Command
	runner := contextRunnerFunc(func(value Command) (Result, error) {
		command = value
		return Result{}, nil
	})
	item := domain.Workspace{Transport: "ssh", Platform: "windows/amd64"}
	if err := removeRemoteTaskScratch(context.Background(), item, runner, `C:\repo`, "task-1"); err != nil {
		t.Fatal(err)
	}
	script := strings.Join(command.Args, " ")
	if command.Executable != "powershell.exe" ||
		!strings.Contains(script, "ReparsePoint") ||
		!strings.Contains(script, "refusing to remove scratch outside task boundary") {
		t.Fatalf("Windows scratch cleanup lacks its boundary guard: %#v", command)
	}
}
