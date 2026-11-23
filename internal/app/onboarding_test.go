package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/store"
)

func TestOnboardingExistingDirectoryUsesHostPathWithoutCreatingFiles(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := NewService(db, nil, StubExecutor{})
	defer service.Shutdown(ctx)

	root := t.TempDir()
	workspace, err := service.CreateOnboardingWorkspace(ctx, "Existing directory", &root)
	if err != nil || workspace.RootPath != root || workspace.ProjectID == "" {
		t.Fatalf("existing directory workspace = %+v, %v", workspace, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("files after adding an existing directory: %v, %v", entries, err)
	}
	if err := os.WriteFile(filepath.Join(root, "unavailable"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "unavailable")
	if _, err := service.CreateOnboardingWorkspace(ctx, "Invalid directory", &file); err != ErrOnboardingDirectory {
		t.Fatalf("using a file as a directory = %v", err)
	}
}
