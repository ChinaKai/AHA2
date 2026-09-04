package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type recordingWorkspacePreparer struct {
	isolation   string
	worktreeDir string
	target      string
	branch      string
}

func (p *recordingWorkspacePreparer) Prepare(_ context.Context, _ domain.Workspace, _ string, target, branch, isolation, worktreeDir string) (PreparedWorkspace, error) {
	p.target = target
	p.branch = branch
	p.isolation = isolation
	p.worktreeDir = worktreeDir
	return PreparedWorkspace{Path: "/tasks/task", BaseCommit: "base", Branch: branch}, nil
}

func TestCreateTaskOwnsWorkspaceIsolationConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-task-isolation", Name: "Git", ProjectType: "git", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: "workspace-task-isolation", ProjectID: project.ID, Name: "local", Locality: "local", Transport: "native", RootPath: "/repo", Health: "ready", CreatedAt: now, UpdatedAt: now}
	envGroup := domain.EnvGroup{ID: "env-task-isolation", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-task-isolation", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}

	preparer := &recordingWorkspacePreparer{}
	service := NewService(database, nil, &stubExecutor{})
	service.SetWorkspacePreparer(preparer)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "isolated", Request: "change code",
		ModelID: model.ID, Isolation: "worktree", WorktreeDir: " /tasks ", TargetBranch: "main", TaskBranch: "aha/custom",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Isolation != "worktree" || task.WorktreeDir != "/tasks" || task.TaskWorkspacePath != "/tasks/task" {
		t.Fatalf("task did not persist its workspace config: %#v", task)
	}
	if preparer.isolation != "worktree" || preparer.worktreeDir != "/tasks" || preparer.target != "main" || preparer.branch != "aha/custom" {
		t.Fatalf("preparer did not receive task config: %#v", preparer)
	}
	stored, err := database.Task(ctx, task.ID)
	if err != nil || stored.Isolation != "worktree" || stored.WorktreeDir != "/tasks" {
		t.Fatalf("stored task config mismatch: %#v %v", stored, err)
	}
}

func TestCreateInplaceTaskClearsWorktreeSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-inplace", Name: "Git", ProjectType: "git", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: "workspace-inplace", ProjectID: project.ID, Name: "local", Locality: "local", Transport: "native", RootPath: "/repo", Health: "ready", CreatedAt: now, UpdatedAt: now}
	envGroup := domain.EnvGroup{ID: "env-inplace", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-inplace", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}

	preparer := &recordingWorkspacePreparer{}
	service := NewService(database, nil, &stubExecutor{})
	service.SetWorkspacePreparer(preparer)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "in place", Request: "analyze",
		ModelID: model.ID, Isolation: "inplace", WorktreeDir: "/ignored", TargetBranch: "main", TaskBranch: "aha/ignored",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Isolation != "inplace" || task.WorktreeDir != "" || task.TargetBranch != "" || task.TaskBranch != "" {
		t.Fatalf("inplace task retained worktree settings: %#v", task)
	}
	if preparer.isolation != "inplace" || preparer.worktreeDir != "" || preparer.target != "" || preparer.branch != "" {
		t.Fatalf("preparer received stale worktree settings: %#v", preparer)
	}
}

func TestCreateWorktreeTaskPersistsDefaultRoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-default-root", Name: "Git", ProjectType: "git", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: "workspace-default-root", ProjectID: project.ID, Name: "remote", Locality: "remote", Transport: "ssh", RootPath: "/repos/project", Health: "ready", CreatedAt: now, UpdatedAt: now}
	envGroup := domain.EnvGroup{ID: "env-default-root", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-default-root", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	preparer := &recordingWorkspacePreparer{}
	service := NewService(database, nil, &stubExecutor{})
	service.SetWorkspacePreparer(preparer)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "default root", Request: "change code",
		ModelID: model.ID, Isolation: "worktree",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.WorktreeDir != "/repos/.aha2-worktrees" || preparer.worktreeDir != task.WorktreeDir {
		t.Fatalf("default worktree root was not frozen on task: task=%#v preparer=%#v", task, preparer)
	}
}
