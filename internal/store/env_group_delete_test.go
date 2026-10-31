package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestDeleteEnvGroupBlocksLiveReferencesAndPrunesOrphans(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-env-delete", Name: "P", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{ID: "workspace-env-delete", ProjectID: project.ID, Name: "W", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	provider := domain.Provider{ID: "provider-env-delete", Name: "P", BaseURL: "https://example.test/v1", CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	group := domain.EnvGroup{ID: "env-delete", Name: "Delete Me", ProviderID: provider.ID, Backend: "claude", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{ID: "model-env-delete", DisplayName: "Model", ProviderID: provider.ID, Backend: "claude", WireModel: "m", DefaultEnvGroupID: group.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeConfigSnapshot{ID: "snapshot-env-delete", WorkspaceID: workspace.ID, Backend: "claude", ModelID: model.ID, WireModel: model.WireModel, EnvGroupID: group.ID, EnvGroupRevision: group.Revision, PermissionsJSON: "{}", CreatedAt: now}
	if err := database.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}

	if err := database.DeleteEnvGroup(ctx, group.ID); err == nil || !strings.Contains(err.Error(), "模型 Model") {
		t.Fatalf("live model reference did not block deletion: %v", err)
	}
	if _, err := database.EnvGroup(ctx, group.ID); err != nil {
		t.Fatalf("env group disappeared after blocked delete: %v", err)
	}

	if err := database.DeleteModel(ctx, model.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteEnvGroup(ctx, group.ID); err != nil {
		t.Fatalf("orphan env group deletion failed: %v", err)
	}
	groups, err := database.ListEnvGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range groups {
		if item.ID == group.ID {
			t.Fatalf("deleted env group still appears in selectors: %#v", item)
		}
	}
	historical, err := database.RuntimeSnapshot(ctx, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if historical.EnvGroupID != group.ID {
		t.Fatalf("historical snapshot lost its env group reference: %#v", historical)
	}
}
