package app

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

// PetFinalizeTaskWorktreeResult reports what the worktree closeout actually did.
type PetFinalizeTaskWorktreeResult struct {
	TaskID          string `json:"task_id"`
	TargetBranch    string `json:"target_branch"`
	Commits         int    `json:"commits"`
	Merged          bool   `json:"merged"`
	NoChanges       bool   `json:"no_changes"`
	WorktreeRemoved bool   `json:"worktree_removed"`
	BranchDeleted   bool   `json:"branch_deleted"`
}

// PetFinalizeTaskWorktree merges a completed Task's worktree back into its
// target branch, then removes the worktree and task branch. It refuses to
// touch a dirty worktree, a dirty primary working tree, or a merge conflict.
func (s *Service) PetFinalizeTaskWorktree(ctx context.Context, taskID string) (PetFinalizeTaskWorktreeResult, error) {
	task, _, err := s.petManageableTask(ctx, taskID)
	if err != nil {
		return PetFinalizeTaskWorktreeResult{}, err
	}
	if task.Status != domain.TaskCompleted {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("只有已完成的 Task 才能收尾 worktree；当前状态为 %s", task.Status)
	}
	worktreePath := strings.TrimSpace(task.TaskWorkspacePath)
	taskBranch := strings.TrimSpace(task.TaskBranch)
	if task.Isolation != "worktree" || worktreePath == "" || taskBranch == "" {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("该 Task 不是 worktree 隔离，或没有可清理的 worktree")
	}
	if err := validateGitRef(taskBranch); err != nil {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("Task 分支名不合法：%w", err)
	}
	item, err := s.store.Workspace(ctx, task.WorkspaceID)
	if err != nil {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("Workspace 不存在")
	}
	runner := workspace.RunnerFor(item)
	root := item.RootPath
	if sameGitPath(worktreePath, root) {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("Task 没有实际创建独立 worktree（路径等于 Workspace 根目录），拒绝合并/清理")
	}

	registered, err := gitWorktreePaths(ctx, runner, root)
	if err != nil {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("读取 Workspace worktree 列表失败：%w", err)
	}
	if !containsGitPath(registered, worktreePath) {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("worktree 未注册在 Workspace 仓库中，拒绝清理：%s", worktreePath)
	}
	toplevel, err := gitText(ctx, runner, worktreePath, "rev-parse", "--show-toplevel")
	if err != nil || !sameGitPath(toplevel, worktreePath) {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("worktree 路径与仓库记录不一致，拒绝清理：%s", worktreePath)
	}
	status, err := gitText(ctx, runner, worktreePath, "status", "--porcelain")
	if err != nil {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("检查 worktree 状态失败：%w", err)
	}
	if strings.TrimSpace(status) != "" {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("worktree 有未提交改动；请先在 Task 内提交或处理，未合并/清理")
	}

	target := strings.TrimSpace(task.TargetBranch)
	if target == "" {
		target, err = gitText(ctx, runner, root, "symbolic-ref", "--short", "HEAD")
		if err != nil || strings.TrimSpace(target) == "" {
			return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("无法确定目标分支；请在 Task 中设置 Target Branch")
		}
	}
	target = strings.TrimSpace(target)
	if err := validateGitRef(target); err != nil {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("目标分支名不合法：%w", err)
	}
	if _, err := gitText(ctx, runner, root, "rev-parse", "--verify", target+"^{commit}"); err != nil {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("目标分支 %s 不存在：%w", target, err)
	}
	countText, err := gitText(ctx, runner, worktreePath, "rev-list", "--count", target+"..HEAD")
	if err != nil {
		return PetFinalizeTaskWorktreeResult{}, fmt.Errorf("比较目标分支与 Task 分支失败：%w", err)
	}
	commits, _ := strconv.Atoi(strings.TrimSpace(countText))
	result := PetFinalizeTaskWorktreeResult{TaskID: task.ID, TargetBranch: target, Commits: commits}
	previousBranch := ""
	switched := false
	if commits == 0 {
		result.NoChanges = true
		current, currentErr := gitText(ctx, runner, root, "symbolic-ref", "--short", "HEAD")
		if currentErr != nil || strings.TrimSpace(current) == "" {
			return result, fmt.Errorf("原 Workspace 处于 detached HEAD，无法安全清理")
		}
		current = strings.TrimSpace(current)
		if current != target {
			if output, checkoutErr := gitRun(ctx, runner, root, "checkout", target); checkoutErr != nil {
				return result, fmt.Errorf("切换到目标分支 %s 清理失败：%s", target, gitErrorText(output, checkoutErr))
			}
			previousBranch, switched = current, true
		}
	} else {
		previousBranch, switched, err = s.mergeTaskWorktree(ctx, runner, root, target, taskBranch, &result)
		if err != nil {
			return result, err
		}
	}

	removed, err := gitRun(ctx, runner, root, "worktree", "remove", "--", worktreePath)
	if err != nil {
		return result, fmt.Errorf("合并已完成，但删除 worktree 失败：%s", gitErrorText(removed, err))
	}
	result.WorktreeRemoved = true
	deleted, err := gitRun(ctx, runner, root, "branch", "-d", taskBranch)
	if err != nil {
		return result, fmt.Errorf("worktree 已删除，但删除临时分支 %s 失败：%s", taskBranch, gitErrorText(deleted, err))
	}
	result.BranchDeleted = true
	if switched {
		if output, checkoutErr := gitRun(ctx, runner, root, "checkout", previousBranch); checkoutErr != nil {
			return result, fmt.Errorf("worktree 和分支已清理，但切回原分支 %s 失败：%s", previousBranch, gitErrorText(output, checkoutErr))
		}
	}
	if err := s.store.UpdateTaskWorkspace(ctx, task.ID, "", task.BaseCommit, task.TaskBranch, task.TargetBranch, timeString(s.now().UTC())); err != nil {
		return result, fmt.Errorf("worktree 已清理，但更新 Task 记录失败：%w", err)
	}
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "pet",
		Kind: "worktree_finalized", Detail: task.ID, Status: "completed",
	})
	return result, nil
}

func (s *Service) mergeTaskWorktree(ctx context.Context, runner workspace.Runner, root, target, taskBranch string, result *PetFinalizeTaskWorktreeResult) (string, bool, error) {
	rootStatus, err := gitText(ctx, runner, root, "status", "--porcelain")
	if err != nil {
		return "", false, fmt.Errorf("检查原 Workspace 状态失败：%w", err)
	}
	if strings.TrimSpace(rootStatus) != "" {
		return "", false, fmt.Errorf("原 Workspace 有未提交改动；未合并/清理")
	}
	current, err := gitText(ctx, runner, root, "symbolic-ref", "--short", "HEAD")
	if err != nil || strings.TrimSpace(current) == "" {
		return "", false, fmt.Errorf("原 Workspace 处于 detached HEAD，无法安全合并")
	}
	current = strings.TrimSpace(current)
	switched := current != target
	if switched {
		if output, err := gitRun(ctx, runner, root, "checkout", target); err != nil {
			return "", false, fmt.Errorf("切换到目标分支 %s 失败：%s", target, gitErrorText(output, err))
		}
	}
	if _, ffErr := gitRun(ctx, runner, root, "merge", "--ff-only", taskBranch); ffErr == nil {
		result.Merged = true
	} else {
		message := fmt.Sprintf("merge %s into %s", taskBranch, target)
		if output, err := gitRun(ctx, runner, root, "merge", "--no-ff", "-m", message, taskBranch); err != nil {
			_, _ = gitRun(ctx, runner, root, "merge", "--abort")
			if switched {
				_, _ = gitRun(ctx, runner, root, "checkout", current)
			}
			return "", false, fmt.Errorf("合并失败（可能有冲突），worktree 已保留：%s", gitErrorText(output, err))
		}
		result.Merged = true
	}
	return current, switched, nil
}

func gitWorktreePaths(ctx context.Context, runner workspace.Runner, root string) ([]string, error) {
	output, err := gitText(ctx, runner, root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, "worktree ")))
		}
	}
	return paths, nil
}

func containsGitPath(paths []string, want string) bool {
	for _, candidate := range paths {
		if sameGitPath(candidate, want) {
			return true
		}
	}
	return false
}

func sameGitPath(left, right string) bool {
	return strings.EqualFold(cleanGitPath(left), cleanGitPath(right))
}

func cleanGitPath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	value = strings.TrimRight(value, "/")
	if value == "" {
		return ""
	}
	return path.Clean(value)
}

func validateGitRef(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("不能为空")
	}
	if strings.HasPrefix(value, "-") || strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("包含非法字符")
	}
	return nil
}

func gitText(ctx context.Context, runner workspace.Runner, dir string, args ...string) (string, error) {
	result, err := gitRun(ctx, runner, dir, args...)
	if err != nil {
		return "", fmt.Errorf("%s", gitErrorText(result, err))
	}
	return strings.TrimSpace(result.Stdout), nil
}

func gitRun(ctx context.Context, runner workspace.Runner, dir string, args ...string) (workspace.Result, error) {
	command := append([]string{"-C", dir}, args...)
	result, err := runner.Run(ctx, workspace.Command{
		Executable: "git", Args: command, Timeout: 2 * time.Minute,
	}, nil)
	if err != nil {
		return result, err
	}
	if result.ExitCode != 0 {
		return result, fmt.Errorf("git exited with code %d", result.ExitCode)
	}
	return result, nil
}

func gitErrorText(result workspace.Result, err error) string {
	message := strings.TrimSpace(result.Stderr)
	if message == "" {
		message = strings.TrimSpace(result.Stdout)
	}
	if message != "" {
		return message
	}
	if err != nil {
		return err.Error()
	}
	return "git command failed"
}
