package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestProductLineAndProjectDeletesPersistTombstones(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()

	directProject := domain.Project{ID: "product-line-direct-project", Name: "Direct", CreatedAt: now, UpdatedAt: now}
	directLine := domain.ProductLine{ID: "product-line-direct", ProjectID: directProject.ID, Name: "Direct line", BranchPattern: "release/*", Default: true, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, directProject); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateProductLine(ctx, directLine); err != nil {
		t.Fatal(err)
	}
	directLine.Name = "Direct line updated"
	directLine.UpdatedAt = now.Add(time.Second)
	if err := database.UpdateProductLine(ctx, directLine); err != nil {
		t.Fatal(err)
	}
	if got, err := database.ProductLine(ctx, directLine.ID); err != nil || got.Name != directLine.Name || !got.Default {
		t.Fatalf("updated product line=%#v err=%v", got, err)
	}
	if err := database.DeleteProductLine(ctx, directLine.ID); err != nil {
		t.Fatal(err)
	}
	directTombstone, err := database.SyncTombstone(ctx, "product_line", directLine.ID)
	if err != nil || directTombstone.Version != timeString(directLine.UpdatedAt) || directTombstone.SyncKey == "" {
		t.Fatalf("direct product line tombstone=%#v err=%v", directTombstone, err)
	}

	cascadeProject := domain.Project{ID: "product-line-cascade-project", Name: "Cascade", CreatedAt: now, UpdatedAt: now.Add(2 * time.Second)}
	cascadeLine := domain.ProductLine{ID: "product-line-cascade", ProjectID: cascadeProject.ID, Name: "Cascade line", Default: true, CreatedAt: now, UpdatedAt: now.Add(3 * time.Second)}
	if err := database.CreateProject(ctx, cascadeProject); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateProductLine(ctx, cascadeLine); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteProject(ctx, cascadeProject.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Project(ctx, cascadeProject.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted project remains: %v", err)
	}
	if _, err := database.ProductLine(ctx, cascadeLine.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cascaded product line remains: %v", err)
	}
	projectTombstone, err := database.SyncTombstone(ctx, "project", cascadeProject.ID)
	if err != nil || projectTombstone.Version != timeString(cascadeProject.UpdatedAt) || projectTombstone.SyncKey == "" {
		t.Fatalf("project tombstone=%#v err=%v", projectTombstone, err)
	}
	lineTombstone, err := database.SyncTombstone(ctx, "product_line", cascadeLine.ID)
	if err != nil || lineTombstone.Version != timeString(cascadeLine.UpdatedAt) || !strings.HasPrefix(lineTombstone.SyncKey, projectTombstone.SyncKey) {
		t.Fatalf("cascaded product line tombstone=%#v err=%v", lineTombstone, err)
	}
}
