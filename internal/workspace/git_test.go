package workspace

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestWorktreePathUsesTaskLevelRoot(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{RootPath: "/repos/project", Locality: "remote", Transport: "ssh"}
	if got := worktreePath(item, "task-002", "/custom/worktrees"); got != "/custom/worktrees/task-002" {
		t.Fatalf("custom worktree path = %q", got)
	}
	if got := worktreePath(item, "task-002", ""); got != "/repos/.aha2-worktrees/task-002" {
		t.Fatalf("default worktree path = %q", got)
	}
}

func TestWorktreePathDefaultsBesideLocalRepository(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		item := domain.Workspace{RootPath: `E:\repos\project`, Locality: "local", Transport: "native"}
		want := filepath.Join(`E:\repos`, ".aha2-worktrees", "task-002")
		if got := worktreePath(item, "task-002", ""); got != want {
			t.Fatalf("default local worktree path = %q, want %q", got, want)
		}
	}
}

func TestWindowsSSHWorktreePathsUseRemoteDriveAndUNCSemantics(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{RootPath: `C:\repos\project`, Locality: "remote", Transport: "ssh", Platform: "windows/amd64"}
	if got := worktreePath(item, "task-002", ""); got != `C:\repos\.aha2-worktrees\task-002` {
		t.Fatalf("Windows SSH worktree path = %q", got)
	}
	item.RootPath = `\\server\share\project`
	if got := worktreePath(item, "task-002", ""); got != `\\server\share\.aha2-worktrees\task-002` {
		t.Fatalf("Windows SSH UNC worktree path = %q", got)
	}
}
