package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

func enablePetTaskCreation(t *testing.T, fixture *petTaskFixture) {
	t.Helper()
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
}

func petGitFixture(t *testing.T) (*petTaskFixture, domain.Project, domain.Workspace, string) {
	t.Helper()
	fixture := newPetTaskFixture(t)
	enablePetTaskCreation(t, fixture)
	root := realTempDir(t)
	runGitTest(t, root, "init", "-b", "main")
	runGitTest(t, root, "config", "user.name", "AHA2 Test")
	runGitTest(t, root, "config", "user.email", "aha2-test@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, root, "add", "README.md")
	runGitTest(t, root, "commit", "-m", "base")
	now := fixture.createdAt
	project := domain.Project{
		ID: "project-pet-worktree", Name: "Pet Worktree", ProjectType: "git",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	ws := domain.Workspace{
		ID: "workspace-pet-worktree", ProjectID: project.ID, Name: "Pet Worktree WS",
		Locality: "local", Transport: "native", RootPath: root, Health: "ready",
		AgentAPIMode: "embedded", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, ws); err != nil {
		t.Fatal(err)
	}
	return fixture, project, ws, root
}

// realTempDir resolves the temp root because GitHub's runner sets a symlinked
// TMPDIR while git reports the resolved path. Leaving the symlink in a
// workspace or worktree path makes sameWorkspaceRoot and the registered-
// worktree check disagree even though both point at the same directory.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

func runGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func createPreparedPetWorktreeTask(t *testing.T, fixture *petTaskFixture, project domain.Project, ws domain.Workspace) (domain.Task, string) {
	t.Helper()
	taskID := domain.NewID("task")
	taskBranch := "aha/" + taskID
	task, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ID: taskID, ProjectID: project.ID, WorkspaceID: ws.ID,
		Title: "Pet worktree task", Request: "Change a file",
		ModelID: fixture.model.ID, Isolation: "worktree",
		TargetBranch: "main", TaskBranch: taskBranch, StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := workspacepkg.PrepareTaskWorkspace(fixture.ctx, ws, task.ID, "main", taskBranch, "worktree", realTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.TargetBranch != "main" {
		t.Fatalf("prepared target branch=%q, want main", prepared.TargetBranch)
	}
	if err := fixture.database.UpdateTaskWorkspace(fixture.ctx, task.ID, prepared.Path, prepared.BaseCommit, prepared.Branch, prepared.TargetBranch, timeString(fixture.createdAt)); err != nil {
		t.Fatal(err)
	}
	return task, prepared.Path
}

func completeTaskForTest(t *testing.T, fixture *petTaskFixture, task domain.Task) {
	t.Helper()
	now := time.Now().UTC()
	status := task.Status
	for _, next := range []domain.TaskStatus{domain.TaskPreparing, domain.TaskActive, domain.TaskCompleted} {
		if status == domain.TaskCompleted {
			break
		}
		if err := domain.ValidateTaskTransition(status, next); err != nil {
			continue
		}
		if err := fixture.database.UpdateTaskStatus(fixture.ctx, task.ID, status, next, timeString(now), timeString(now)); err != nil {
			t.Fatal(err)
		}
		status = next
	}
	if status != domain.TaskCompleted {
		t.Fatalf("task did not reach completed: %s", status)
	}
}

func TestPetCreateTaskDefaultsToWorktreeForGitProject(t *testing.T) {
	fixture, project, ws, _ := petGitFixture(t)
	worktree, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: ws.ID, Title: "默认 worktree",
		Request: "改代码", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if worktree.Isolation != "worktree" {
		t.Fatalf("pet task isolation=%q, want worktree", worktree.Isolation)
	}
	inplace, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: ws.ID, Title: "显式原地",
		Request: "只读检查", Isolation: "inplace", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if inplace.Isolation != "inplace" {
		t.Fatalf("pet task isolation=%q, want inplace", inplace.Isolation)
	}
}

func TestPetFinalizeTaskWorktreeMergesAndCleans(t *testing.T) {
	fixture, project, ws, root := petGitFixture(t)
	task, worktree := createPreparedPetWorktreeTask(t, fixture, project, ws)
	if err := os.WriteFile(filepath.Join(worktree, "change.txt"), []byte("merged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, worktree, "add", "change.txt")
	runGitTest(t, worktree, "commit", "-m", "task change")
	completeTaskForTest(t, fixture, task)

	result, err := fixture.service.PetFinalizeTaskWorktree(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Merged || result.NoChanges || !result.WorktreeRemoved || !result.BranchDeleted || result.Commits != 1 {
		t.Fatalf("finalize result=%#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "change.txt")); err != nil {
		t.Fatalf("merged file missing from primary worktree: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("task worktree still exists: %v", err)
	}
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", task.TaskBranch).Run(); err == nil {
		t.Fatalf("task branch %s still exists", task.TaskBranch)
	}
}

func TestPetFinalizeTaskWorktreeRestoresCurrentBranch(t *testing.T) {
	fixture, project, ws, root := petGitFixture(t)
	runGitTest(t, root, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(root, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, root, "add", "feature.txt")
	runGitTest(t, root, "commit", "-m", "feature")
	task, worktree := createPreparedPetWorktreeTask(t, fixture, project, ws)
	if err := os.WriteFile(filepath.Join(worktree, "change.txt"), []byte("merged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, worktree, "add", "change.txt")
	runGitTest(t, worktree, "commit", "-m", "task change")
	completeTaskForTest(t, fixture, task)

	result, err := fixture.service.PetFinalizeTaskWorktree(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Merged || !result.WorktreeRemoved || !result.BranchDeleted {
		t.Fatalf("finalize result=%#v", result)
	}
	if branch := strings.TrimSpace(runGitTest(t, root, "symbolic-ref", "--short", "HEAD")); branch != "feature" {
		t.Fatalf("current branch=%q, want feature", branch)
	}
	if _, err := os.Stat(filepath.Join(root, "feature.txt")); err != nil {
		t.Fatalf("feature branch working tree changed: %v", err)
	}
	if output := runGitTest(t, root, "show", "main:change.txt"); !strings.Contains(output, "merged") {
		t.Fatalf("main branch missing merged change: %q", output)
	}
}

// stubGitRunner answers only the git calls removeFinalizedWorktree makes. The
// worktree remove call fails with the Windows long-path message, which is the
// condition the local fallback must handle.
type stubGitRunner struct {
	root     string
	worktree string
	locked   bool
	calls    []string
}

func (r *stubGitRunner) Run(_ context.Context, command workspacepkg.Command, _ workspacepkg.LineHandler) (workspacepkg.Result, error) {
	joined := strings.Join(command.Args, " ")
	r.calls = append(r.calls, joined)
	switch {
	case strings.Contains(joined, "worktree list --porcelain"):
		output := "worktree " + r.root + "\nHEAD abc\nbranch refs/heads/main\n\nworktree " + r.worktree + "\nHEAD def\nbranch refs/heads/aha/task\n"
		if r.locked {
			output += "locked maintenance\n"
		}
		return workspacepkg.Result{ExitCode: 0, Stdout: output}, nil
	case strings.Contains(joined, "submodule status --recursive"):
		return workspacepkg.Result{ExitCode: 0}, nil
	case strings.Contains(joined, "worktree remove"):
		return workspacepkg.Result{
			ExitCode: 128,
			Stderr:   "error: failed to delete '" + r.worktree + "': Filename too long",
		}, fmt.Errorf("git exited with code 128")
	case strings.Contains(joined, "worktree prune"):
		return workspacepkg.Result{ExitCode: 0}, nil
	default:
		return workspacepkg.Result{ExitCode: 0}, nil
	}
}

func TestRemoveFinalizedWorktreeFallsBackToLongPathDelete(t *testing.T) {
	root := realTempDir(t)
	worktree := filepath.Join(root, "task")
	if err := os.MkdirAll(filepath.Join(worktree, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "nested", "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	item := domain.Workspace{Locality: "local", Transport: "native", RootPath: root}
	runner := &stubGitRunner{root: root, worktree: worktree}

	service := &Service{}
	if err := service.removeFinalizedWorktree(context.Background(), item, runner, root, worktree); err != nil {
		t.Fatalf("fallback removal failed: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after fallback: %v", err)
	}
	pruned := false
	for _, call := range runner.calls {
		if strings.Contains(call, "worktree prune") {
			pruned = true
		}
	}
	if !pruned {
		t.Fatalf("stale worktree metadata was not pruned: %v", runner.calls)
	}
}

func TestRemoveFinalizedWorktreeKeepsRemoteWorktreeAndReportsPath(t *testing.T) {
	root := realTempDir(t)
	worktree := filepath.Join(root, "task")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	item := domain.Workspace{Locality: "remote", Transport: "ssh", RootPath: root}
	runner := &stubGitRunner{root: root, worktree: worktree}

	service := &Service{}
	err := service.removeFinalizedWorktree(context.Background(), item, runner, root, worktree)
	if err == nil {
		t.Fatal("remote worktree removal did not fail")
	}
	if !strings.Contains(err.Error(), worktree) || !strings.Contains(err.Error(), "Filename too long") {
		t.Fatalf("remote removal error is not actionable: %v", err)
	}
	if _, statErr := os.Stat(worktree); statErr != nil {
		t.Fatalf("remote worktree was removed locally: %v", statErr)
	}
}

func TestRemoveFinalizedWorktreeKeepsLockedWorktree(t *testing.T) {
	root := realTempDir(t)
	worktree := filepath.Join(root, "task")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	item := domain.Workspace{Locality: "local", Transport: "native", RootPath: root}
	runner := &stubGitRunner{root: root, worktree: worktree, locked: true}

	service := &Service{}
	err := service.removeFinalizedWorktree(context.Background(), item, runner, root, worktree)
	if err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("locked worktree error = %v", err)
	}
	if _, statErr := os.Stat(worktree); statErr != nil {
		t.Fatalf("locked worktree was removed: %v", statErr)
	}
}

func TestPetFinalizeTaskWorktreeRefusesDirtyWorktree(t *testing.T) {
	fixture, project, ws, root := petGitFixture(t)
	task, worktree := createPreparedPetWorktreeTask(t, fixture, project, ws)
	if err := os.WriteFile(filepath.Join(worktree, "dirty.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	completeTaskForTest(t, fixture, task)

	_, err := fixture.service.PetFinalizeTaskWorktree(fixture.ctx, task.ID)
	if err == nil {
		t.Fatal("dirty worktree was finalized")
	}
	if !strings.Contains(err.Error(), worktree) {
		t.Fatalf("dirty worktree error does not include the retained path: %v", err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("dirty worktree was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dirty.txt")); !os.IsNotExist(err) {
		t.Fatalf("dirty file leaked into primary worktree: %v", err)
	}
}

func TestPetFinalizeTaskWorktreeRefusesUnmergedWorktree(t *testing.T) {
	fixture, project, ws, root := petGitFixture(t)
	task, worktree := createPreparedPetWorktreeTask(t, fixture, project, ws)
	if err := os.WriteFile(filepath.Join(worktree, "conflict.txt"), []byte("task side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, worktree, "add", "conflict.txt")
	runGitTest(t, worktree, "commit", "-m", "task side")
	// Advance the target branch with a conflicting change so the task branch
	// cannot be merged automatically.
	if err := os.WriteFile(filepath.Join(root, "conflict.txt"), []byte("main side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, root, "add", "conflict.txt")
	runGitTest(t, root, "commit", "-m", "main side")
	completeTaskForTest(t, fixture, task)

	result, err := fixture.service.PetFinalizeTaskWorktree(fixture.ctx, task.ID)
	if err == nil {
		t.Fatal("unmerged worktree was finalized")
	}
	if !strings.Contains(err.Error(), "worktree 已保留") || !strings.Contains(err.Error(), worktree) {
		t.Fatalf("unmerged error is not actionable: %v", err)
	}
	if result.WorktreeRemoved {
		t.Fatalf("unmerged finalize reported worktree removal: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(worktree, "conflict.txt")); err != nil {
		t.Fatalf("unmerged worktree was removed: %v", err)
	}
	if output := runGitTest(t, root, "show", "main:conflict.txt"); !strings.Contains(output, "main side") {
		t.Fatalf("target branch was rewritten: %q", output)
	}
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", task.TaskBranch).Run(); err != nil {
		t.Fatalf("unmerged task branch was deleted: %v", err)
	}
}

func TestPetFinalizeTaskWorktreeRefusesPrimaryWorkspacePath(t *testing.T) {
	fixture, project, ws, root := petGitFixture(t)
	taskID := domain.NewID("task")
	taskBranch := "aha/" + taskID
	task, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ID: taskID, ProjectID: project.ID, WorkspaceID: ws.ID,
		Title: "Fallback worktree task", Request: "No real worktree",
		ModelID: fixture.model.ID, Isolation: "worktree",
		TargetBranch: "main", TaskBranch: taskBranch, StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateTaskWorkspace(fixture.ctx, task.ID, root, "", taskBranch, "main", timeString(fixture.createdAt)); err != nil {
		t.Fatal(err)
	}
	completeTaskForTest(t, fixture, task)
	if _, err := fixture.service.PetFinalizeTaskWorktree(fixture.ctx, task.ID); err == nil {
		t.Fatal("primary workspace path was finalized")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("primary workspace was removed: %v", err)
	}
}

func TestPetFinalizeTaskWorktreeCleansUnchangedTask(t *testing.T) {
	fixture, project, ws, root := petGitFixture(t)
	task, worktree := createPreparedPetWorktreeTask(t, fixture, project, ws)
	completeTaskForTest(t, fixture, task)

	result, err := fixture.service.PetFinalizeTaskWorktree(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoChanges || result.Merged || !result.WorktreeRemoved || !result.BranchDeleted {
		t.Fatalf("finalize result=%#v", result)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("task worktree still exists: %v", err)
	}
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", task.TaskBranch).Run(); err == nil {
		t.Fatalf("task branch %s still exists", task.TaskBranch)
	}
}
