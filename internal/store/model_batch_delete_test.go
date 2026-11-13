package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

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

func TestDeleteModelsIsAtomicWhenOneModelIsGone(t *testing.T) {
	database, ctx := newModelBatchTestStore(t)
	if err := database.DeleteModel(ctx, "model-batch-b"); err != nil {
		t.Fatal(err)
	}
	err := database.DeleteModels(ctx, []string{"model-batch-a", "model-batch-b", "model-batch-c"})
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
	if err := database.DeleteModels(ctx, []string{"model-batch-a", "model-batch-c", "model-batch-a"}); err != nil {
		t.Fatal(err)
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
