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

func TestDeleteProviderTombstonesModelsWithoutAllowingBackfill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	project := domain.Project{ID: "project-provider-delete", Name: "Project", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-provider-delete", ProjectID: project.ID, Name: "Workspace",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	provider := domain.Provider{
		ID: "provider-delete", Name: "Delete Me", BaseURL: "https://example.test/v1",
		CredentialRef: "provider/provider-delete/credential", CredentialConfigured: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	group := domain.EnvGroup{
		ID: "env-provider-delete", Name: "Delete Me / Model", ProviderID: provider.ID,
		Backend: "codex", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model-provider-delete", DisplayName: "Model", ProviderID: provider.ID,
		Backend: "codex", WireModel: "model", DefaultEnvGroupID: group.ID,
		Capabilities: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "snapshot-provider-delete", WorkspaceID: workspace.ID, Backend: "codex",
		ModelID: model.ID, WireModel: model.WireModel, EnvGroupID: group.ID,
		EnvGroupRevision: group.Revision, PermissionsJSON: "{}", CreatedAt: now,
	}
	if err := database.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateTask(ctx, domain.Task{
		ID: "task-provider-delete", ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Historical", OriginalRequest: "test", CurrentGoal: "test", Status: domain.TaskDraft,
		RuntimeConfigSnapshotID: snapshot.ID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := database.DeleteProvider(ctx, provider.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Provider(ctx, provider.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted provider is still selectable: %v", err)
	}
	if models, err := database.ListModels(ctx); err != nil || len(models) != 0 {
		t.Fatalf("provider models remain visible: models=%#v err=%v", models, err)
	}
	if _, err := database.EnvGroup(ctx, group.ID); err != nil {
		t.Fatalf("historical env group was not preserved: %v", err)
	}
	if historical, err := database.RuntimeSnapshot(ctx, snapshot.ID); err != nil || historical.ModelID != model.ID {
		t.Fatalf("historical runtime snapshot was not preserved: %#v err=%v", historical, err)
	}
	if backfilled, err := database.BackfillProviders(ctx); err != nil || backfilled != 0 {
		t.Fatalf("deleted provider was recreated by backfill: count=%d err=%v", backfilled, err)
	}
}
