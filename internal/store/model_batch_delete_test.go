package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// EnvGroup() intentionally reads tombstoned rows for historical snapshots, so
// visibility is checked on deleted_at instead.
func envGroupDeleted(t *testing.T, database *Store, ctx context.Context, id string) bool {
	t.Helper()
	var deletedAt string
	if err := database.db.QueryRowContext(ctx, `SELECT deleted_at FROM env_groups WHERE id=?`, id).Scan(&deletedAt); err != nil {
		if err == sql.ErrNoRows {
			return true
		}
		t.Fatal(err)
	}
	return deletedAt != ""
}

func newModelBatchTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	if err := database.UpsertProvider(ctx, domain.Provider{
		ID: "provider-batch", Name: "Batch Provider", BaseURL: "https://example.test/v1",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"model-batch-a", "model-batch-b", "model-batch-c"} {
		if err := database.UpsertModel(ctx, domain.Model{
			ID: id, DisplayName: id, ProviderID: "provider-batch", Backend: "codex",
			WireModel: "wire-" + id, WireAPI: "chat", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return database, ctx
}

func createBatchEnvGroup(t *testing.T, database *Store, ctx context.Context, id string, secretRefs map[string]string) {
	t.Helper()
	now := time.Now().UTC()
	if err := database.UpsertEnvGroup(ctx, domain.EnvGroup{
		ID: id, Name: id, ProviderID: "provider-batch", Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: secretRefs, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func createBatchModel(t *testing.T, database *Store, ctx context.Context, id, envGroupID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := database.UpsertModel(ctx, domain.Model{
		ID: id, DisplayName: id, ProviderID: "provider-batch", Backend: "codex",
		WireModel: "wire-" + id, WireAPI: "chat", DefaultEnvGroupID: envGroupID,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteModelsIsAtomicWhenOneModelIsGone(t *testing.T) {
	database, ctx := newModelBatchTestStore(t)
	if err := database.DeleteModel(ctx, "model-batch-b"); err != nil {
		t.Fatal(err)
	}
	_, err := database.DeleteModels(ctx, []string{"model-batch-a", "model-batch-b", "model-batch-c"})
	if err == nil || !strings.Contains(err.Error(), "model-batch-b") {
		t.Fatalf("DeleteModels() error = %v, want the missing model id", err)
	}
	for _, id := range []string{"model-batch-a", "model-batch-c"} {
		if _, err := database.Model(ctx, id); err != nil {
			t.Fatalf("failed batch delete partially removed %s: %v", id, err)
		}
		var tombstones int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_tombstones WHERE object_type='model' AND object_id=?`, id).Scan(&tombstones); err != nil {
			t.Fatal(err)
		}
		if tombstones != 0 {
			t.Fatalf("failed batch delete wrote a tombstone for %s", id)
		}
	}
}

func TestDeleteModelsDeletesEveryModelWithTombstones(t *testing.T) {
	database, ctx := newModelBatchTestStore(t)
	cleanup, err := database.DeleteModels(ctx, []string{"model-batch-a", "model-batch-c", "model-batch-a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cleanup.DeletedModelIDs, ","); got != "model-batch-a,model-batch-c" {
		t.Fatalf("DeletedModelIDs = %q", got)
	}
	for _, id := range []string{"model-batch-a", "model-batch-c"} {
		if _, err := database.Model(ctx, id); err == nil {
			t.Fatalf("DeleteModels() left %s visible", id)
		}
		var tombstones int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_tombstones WHERE object_type='model' AND object_id=?`, id).Scan(&tombstones); err != nil {
			t.Fatal(err)
		}
		if tombstones != 1 {
			t.Fatalf("DeleteModels() wrote %d tombstones for %s, want 1", tombstones, id)
		}
	}
	if _, err := database.Model(ctx, "model-batch-b"); err != nil {
		t.Fatalf("DeleteModels() touched an unrequested model: %v", err)
	}
}

// A shared env group must survive until the last model in the batch is gone;
// only unreferenced groups lose their per-env secrets. Provider credential refs
// are shared and must never be returned for deletion.
func TestDeleteModelsCleansUnreferencedEnvGroupsAndSecrets(t *testing.T) {
	database, ctx := newModelBatchTestStore(t)
	createBatchEnvGroup(t, database, ctx, "env-batch-1", map[string]string{
		"TOKEN": "env/env-batch-1/TOKEN", "PROVIDER": "provider/batch/credential",
	})
	createBatchEnvGroup(t, database, ctx, "env-batch-2", map[string]string{"TOKEN": "env/env-batch-2/TOKEN"})
	createBatchModel(t, database, ctx, "model-env-1", "env-batch-1")
	createBatchModel(t, database, ctx, "model-env-2a", "env-batch-2")
	createBatchModel(t, database, ctx, "model-env-2b", "env-batch-2")

	cleanup, err := database.DeleteModels(ctx, []string{"model-env-1", "model-env-2a"})
	if err != nil {
		t.Fatal(err)
	}
	if !envGroupDeleted(t, database, ctx, "env-batch-1") {
		t.Fatal("unreferenced env group survived the batch delete")
	}
	if got := strings.Join(cleanup.DeletedEnvGroupIDs, ","); got != "env-batch-1" {
		t.Fatalf("DeletedEnvGroupIDs = %q", got)
	}
	if got := strings.Join(cleanup.SecretRefs, ","); got != "env/env-batch-1/TOKEN" {
		t.Fatalf("SecretRefs = %q, want only the per-env secret", got)
	}
	// The shared group still has model-env-2b, so it and its secret must stay.
	if envGroupDeleted(t, database, ctx, "env-batch-2") {
		t.Fatal("shared env group was removed too early")
	}

	cleanup, err = database.DeleteModels(ctx, []string{"model-env-2b"})
	if err != nil {
		t.Fatal(err)
	}
	if !envGroupDeleted(t, database, ctx, "env-batch-2") {
		t.Fatal("env group survived after its last model was deleted")
	}
	if got := strings.Join(cleanup.SecretRefs, ","); got != "env/env-batch-2/TOKEN" {
		t.Fatalf("SecretRefs after last model = %q", got)
	}
	var tombstones int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_tombstones WHERE object_type='env_group' AND object_id='env-batch-1'`).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if tombstones != 1 {
		t.Fatalf("env group tombstone count = %d, want 1", tombstones)
	}
}

// A group referenced by a historical runtime snapshot keeps its row and secrets
// even when its last model is deleted, matching EnvGroupInUse.
func TestDeleteModelsKeepsEnvGroupUsedByRuntimeSnapshot(t *testing.T) {
	database, ctx := newModelBatchTestStore(t)
	now := time.Now().UTC()
	project := domain.Project{ID: "project-batch-usage", Name: "Batch Usage", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-batch-usage", ProjectID: project.ID, Name: "ws", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	createBatchEnvGroup(t, database, ctx, "env-batch-usage", map[string]string{"TOKEN": "env/env-batch-usage/TOKEN"})
	createBatchModel(t, database, ctx, "model-batch-usage", "env-batch-usage")
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "snapshot-batch-usage", WorkspaceID: workspace.ID, Backend: "codex",
		ModelID: "model-batch-usage", WireModel: "wire-model-batch-usage", EnvGroupID: "env-batch-usage",
		EnvGroupRevision: 1, PermissionsJSON: "{}", CreatedAt: now,
	}
	if err := database.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateTask(ctx, domain.Task{
		ID: "task-batch-usage", ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Historical", OriginalRequest: "test", CurrentGoal: "test", Status: domain.TaskDraft,
		RuntimeConfigSnapshotID: snapshot.ID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	cleanup, err := database.DeleteModels(ctx, []string{"model-batch-usage"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cleanup.DeletedEnvGroupIDs) != 0 || len(cleanup.SecretRefs) != 0 {
		t.Fatalf("in-use env group was cleaned: %#v", cleanup)
	}
	if envGroupDeleted(t, database, ctx, "env-batch-usage") {
		t.Fatal("env group used by a runtime snapshot was removed")
	}
}
