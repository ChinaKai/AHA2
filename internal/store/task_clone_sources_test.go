package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestListTaskCloneSourcesReturnsAllLocalTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	project := domain.Project{ID: "project-clone-sources", Name: "Clone", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	local := domain.Workspace{
		ID: "workspace-clone-local", ProjectID: project.ID, Name: "Local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	remote := domain.Workspace{
		ID: "workspace-clone-remote", ProjectID: project.ID, Name: "Remote", Locality: "remote",
		Transport: "ssh", RootPath: "/repo", Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, local); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, remote); err != nil {
		t.Fatal(err)
	}
	provider := domain.Provider{ID: "provider-clone-sources", Name: "Clone", CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{
		ID: "env-clone-sources", Name: "Clone", ProviderID: provider.ID, Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model-clone-sources", DisplayName: "Clone", ProviderID: provider.ID, Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []domain.RuntimeConfigSnapshot{
		{
			ID: "snapshot-local", WorkspaceID: local.ID, Backend: model.Backend, ModelID: model.ID,
			WireModel: model.WireModel, EnvGroupID: env.ID, EnvGroupRevision: env.Revision,
			PermissionsJSON: "{}", CreatedAt: now,
		},
		{
			ID: "snapshot-remote", WorkspaceID: remote.ID, Backend: model.Backend, ModelID: model.ID,
			WireModel: model.WireModel, EnvGroupID: env.ID, EnvGroupRevision: env.Revision,
			PermissionsJSON: "{}", CreatedAt: now,
		},
	} {
		if err := database.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []domain.Task{
		{
			ID: "task-clone-local", ProjectID: project.ID, WorkspaceID: local.ID, Title: "Local", OriginalRequest: "request",
			CurrentGoal: "request", Status: domain.TaskActive, RuntimeConfigSnapshotID: "snapshot-local", CreatedAt: now, UpdatedAt: now.Add(time.Minute),
		},
		{
			ID: "task-clone-remote", ProjectID: project.ID, WorkspaceID: remote.ID, Title: "Remote", OriginalRequest: "request",
			CurrentGoal: "request", Status: domain.TaskActive, RuntimeConfigSnapshotID: "snapshot-remote", CreatedAt: now, UpdatedAt: now.Add(2 * time.Minute),
		},
	} {
		if err := database.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	sources, err := database.ListTaskCloneSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].ID != "task-clone-local" {
		t.Fatalf("clone sources=%#v", sources)
	}
}
