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
