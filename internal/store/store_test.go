package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestStorePersistsProject(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "aha2.db")
	ctx := context.Background()
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	item := domain.Project{ID: "project-1", Name: "AHA2", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, item); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.Project(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "AHA2" {
		t.Fatalf("unexpected project: %#v", stored)
	}
}
