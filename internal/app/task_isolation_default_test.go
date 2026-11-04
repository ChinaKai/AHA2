package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestCreateTaskDefaultsToInplaceIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-inplace-default", Name: "Git", ProjectType: "git", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-inplace-default", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto",
		CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{ID: "env-inplace-default", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-inplace-default", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now}
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
	service := NewService(database, nil, &stubExecutor{})
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "default isolation",
		Request: "use the new default", ModelID: model.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Isolation != "inplace" || task.WorktreeDir != "" || task.TargetBranch != "" || task.TaskBranch != "" {
		t.Fatalf("new task did not default to inplace isolation: %#v", task)
	}
}
