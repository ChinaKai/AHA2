package workspace

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type PreparedTaskWorkspace struct {
	Path       string
	BaseCommit string
	Branch     string
}

func PrepareTaskWorkspace(ctx context.Context, item domain.Workspace, taskID, targetBranch, taskBranch string, isolateGit bool) (PreparedTaskWorkspace, error) {
	runner := RunnerFor(item)
	// Plain-folder projects (or any non-git workspace) run directly in place.
	if !isolateGit {
		return PreparedTaskWorkspace{Path: item.RootPath}, nil
	}
	if targetBranch == "" {
		targetBranch = "HEAD"
	}
	if taskBranch == "" {
		taskBranch = "aha/" + taskID
	}
	gitCheck, err := runner.Run(ctx, Command{
		Executable: "git", Args: []string{"-C", item.RootPath, "rev-parse", "--show-toplevel"},
		Timeout: 20 * time.Second,
	}, nil)
	if err != nil || gitCheck.ExitCode != 0 || !sameWorkspaceRoot(item, strings.TrimSpace(gitCheck.Stdout)) {
		return PreparedTaskWorkspace{Path: item.RootPath}, nil
	}
	baseRef := targetBranch
	base, err := runner.Run(ctx, Command{
		Executable: "git", Args: []string{"-C", item.RootPath, "rev-parse", baseRef},
		Timeout: 20 * time.Second,
	}, nil)
	if err != nil || base.ExitCode != 0 {
		if baseRef != "HEAD" {
			// The requested branch may not exist (e.g. a repo whose default
			// branch is "master" while the dialog defaults to "main"). Fall
			// back to the current checkout so the task can still be created.
			head, headErr := runner.Run(ctx, Command{
				Executable: "git", Args: []string{"-C", item.RootPath, "rev-parse", "HEAD"},
				Timeout: 20 * time.Second,
			}, nil)
			if headErr == nil && head.ExitCode == 0 {
				baseRef = "HEAD"
				base = head
			} else {
				// No resolvable base (e.g. a repo with no commits yet). Run
				// directly in the workspace root instead of blocking the task.
				return PreparedTaskWorkspace{Path: item.RootPath}, nil
			}
		} else {
			// HEAD itself is unborn (repo has no commits yet).
			return PreparedTaskWorkspace{Path: item.RootPath}, nil
		}
	}
	worktree := worktreePath(item, taskID)
	if item.Locality == "remote" || item.Transport == "ssh" || item.Transport == "wsl" {
		parent := path.Dir(worktree)
		if result, err := runner.Run(ctx, Command{Executable: "mkdir", Args: []string{"-p", parent}, Timeout: 20 * time.Second}, nil); err != nil || result.ExitCode != 0 {
			return PreparedTaskWorkspace{}, fmt.Errorf("create remote worktree parent: %s", strings.TrimSpace(result.Stderr))
		}
	} else if err := os.MkdirAll(filepath.Dir(worktree), 0o700); err != nil {
		return PreparedTaskWorkspace{}, fmt.Errorf("create worktree parent: %w", err)
	}
	branchExists, _ := runner.Run(ctx, Command{
		Executable: "git", Args: []string{"-C", item.RootPath, "show-ref", "--verify", "--quiet", "refs/heads/" + taskBranch},
		Timeout: 20 * time.Second,
	}, nil)
	args := []string{"-C", item.RootPath, "worktree", "add"}
	if branchExists.ExitCode == 0 {
		args = append(args, worktree, taskBranch)
	} else {
		args = append(args, "-b", taskBranch, worktree, baseRef)
	}
	created, err := runner.Run(ctx, Command{Executable: "git", Args: args, Timeout: 2 * time.Minute}, nil)
	if err != nil || created.ExitCode != 0 {
		return PreparedTaskWorkspace{}, fmt.Errorf("create task worktree: %s", strings.TrimSpace(created.Stderr))
	}
	return PreparedTaskWorkspace{
		Path: worktree, BaseCommit: strings.TrimSpace(base.Stdout), Branch: taskBranch,
	}, nil
}

func worktreePath(item domain.Workspace, taskID string) string {
	if strings.TrimSpace(item.WorktreeDir) != "" {
		base := strings.ReplaceAll(strings.TrimSpace(item.WorktreeDir), "\\", "/")
		return path.Join(base, taskID)
	}
	if item.Locality == "remote" || item.Transport == "ssh" || item.Transport == "wsl" || runtime.GOOS != "windows" {
		return path.Join(path.Dir(strings.ReplaceAll(item.RootPath, "\\", "/")), ".aha2-worktrees", taskID)
	}
	return filepath.Join(filepath.Dir(item.RootPath), ".aha2-worktrees", taskID)
}
