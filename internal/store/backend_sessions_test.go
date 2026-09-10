package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestReusableBackendSessionRequiresExactIdentityContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var migrated bool
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=49)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v49 missing: migrated=%t err=%v", migrated, err)
	}

	now := time.Now().UTC()
	project := domain.Project{ID: "project-session", Name: "P", ProjectType: "git", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: "workspace-session", ProjectID: project.ID, Name: "W", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now}
	envGroup := domain.EnvGroup{ID: "env-session", Name: "E", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-session", DisplayName: "M", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now}
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
	snapshot := domain.RuntimeConfigSnapshot{ID: "runtime-session", WorkspaceID: workspace.ID, Backend: "stub", ModelID: model.ID, WireModel: "stub", EnvGroupID: envGroup.ID, EnvGroupRevision: 1, PermissionsJSON: "{}", CreatedAt: now}
	task := domain.Task{ID: "task-session", ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "T", OriginalRequest: "test", CurrentGoal: "test", Status: domain.TaskWaitingUser, RuntimeConfigSnapshotID: snapshot.ID, CreatedAt: now, UpdatedAt: now}
	if _, err := database.CreateTaskWithSnapshot(ctx, snapshot, task); err != nil {
		t.Fatal(err)
	}

	sessions := []domain.BackendSession{
		{ID: "legacy", TaskID: task.ID, AgentID: "main", WorkspaceID: workspace.ID, Backend: "stub", ModelID: model.ID, EnvGroupRevision: 1, ProviderSession: "legacy-provider", Status: "active", CreatedAt: now.Add(time.Minute), LastUsedAt: now.Add(time.Minute)},
		{ID: "web", TaskID: task.ID, AgentID: "main", WorkspaceID: workspace.ID, Backend: "stub", ModelID: model.ID, EnvGroupRevision: 1, IdentityContext: "task-agent:web", ProviderSession: "web-provider", Status: "active", CreatedAt: now, LastUsedAt: now},
	}
	for _, session := range sessions {
		if err := database.UpsertBackendSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}

	got, err := database.ReusableBackendSession(ctx, task.ID, "main", workspace.ID, "stub", model.ID, 1, "", "task-agent:web")
	if err != nil || got.ID != "web" || got.IdentityContext != "task-agent:web" {
		t.Fatalf("web session=%#v err=%v", got, err)
	}
	if _, err := database.ReusableBackendSession(ctx, task.ID, "main", workspace.ID, "stub", model.ID, 1, "", "task-agent:external-channel"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("mismatched identity context returned err=%v", err)
	}
}
