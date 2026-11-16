package knowledgevault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newCleanupManager(t *testing.T) (*Manager, Config) {
	t.Helper()
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Cleanup"}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	return manager, config
}

func TestRemoveEmptyProjectSpaceCleansGeneratedScaffolding(t *testing.T) {
	ctx := context.Background()
	manager, config := newCleanupManager(t)
	projectID := "project-empty-infra"
	if _, err := manager.ProvisionProjectSpace(ctx, projectID, "Empty Infra"); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[projectID]
	if slug == "" {
		t.Fatalf("project was not provisioned: %#v", manifest.Projects)
	}
	spaceDir := filepath.Join(config.VaultPath, "projects", slug)
	if err := os.WriteFile(filepath.Join(spaceDir, ".gitkeep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(spaceDir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := manager.RemoveEmptyProjectSpace(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Removed || result.Slug != slug {
		t.Fatalf("cleanup result = %#v", result)
	}
	manifest, err = ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, mapped := manifest.Projects[projectID]; mapped {
		t.Fatalf("manifest still maps the project: %#v", manifest.Projects)
	}
	if _, name := manifest.ProjectNames[projectID]; name {
		t.Fatalf("manifest still has the project display name: %#v", manifest.ProjectNames)
	}
	if _, statErr := os.Stat(spaceDir); !os.IsNotExist(statErr) {
		t.Fatalf("project directory still exists: %v", statErr)
	}
	// The rebuild ran with the removal, so the index no longer carries even the
	// generated root entry.
	if err := manager.WithReader(func(reader *Reader) error {
		entries, entryErr := reader.Entries(ctx, "project", projectID)
		if entryErr != nil {
			return entryErr
		}
		if len(entries) != 0 {
			t.Fatalf("index still has project entries: %#v", entries)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Idempotent: nothing to do on the second call.
	again, err := manager.RemoveEmptyProjectSpace(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Removed {
		t.Fatalf("second cleanup removed something: %#v", again)
	}
}

func TestRemoveEmptyProjectSpaceRefusesKnowledgeFiles(t *testing.T) {
	ctx := context.Background()
	manager, config := newCleanupManager(t)
	projectID := "project-busy-infra"
	if _, err := manager.ProvisionProjectSpace(ctx, projectID, "Busy Infra"); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[projectID]
	note := "---\ntitle: Keep\nslug: keep\nscope: project\nproject_id: " + projectID + "\ntype: reference\nstatus: verified\n---\n# Keep\n\nmarker\n"
	notePath := filepath.Join(config.VaultPath, "projects", slug, "notes", "keep.md")
	if err := os.MkdirAll(filepath.Dir(notePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notePath, []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}

	result, err := manager.RemoveEmptyProjectSpace(ctx, projectID)
	if !errors.Is(err, ErrProjectSpaceNotEmpty) {
		t.Fatalf("error = %v, want ErrProjectSpaceNotEmpty", err)
	}
	if result.Removed || !cleanupContainsPath(result.BlockingPaths, "notes/keep.md") {
		t.Fatalf("result = %#v", result)
	}
	manifest, err = ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects[projectID] != slug {
		t.Fatalf("refused cleanup changed the mapping: %#v", manifest.Projects)
	}
	if _, err := os.Stat(notePath); err != nil {
		t.Fatalf("refused cleanup removed a knowledge file: %v", err)
	}
}

func cleanupContainsPath(paths []string, want string) bool {
	for _, item := range paths {
		if item == want {
			return true
		}
	}
	return false
}

func TestRemoveEmptyProjectSpaceRequiresAttributableRootIndex(t *testing.T) {
	ctx := context.Background()
	manager, config := newCleanupManager(t)
	projectID := "project-unconfirmed-infra"
	if _, err := manager.ProvisionProjectSpace(ctx, projectID, "Unconfirmed Infra"); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[projectID]
	spaceDir := filepath.Join(config.VaultPath, "projects", slug)
	if err := os.Remove(filepath.Join(spaceDir, "index.md")); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.RemoveEmptyProjectSpace(ctx, projectID); !errors.Is(err, ErrProjectSpaceUnconfirmed) {
		t.Fatalf("error = %v, want ErrProjectSpaceUnconfirmed", err)
	}
	if _, err := os.Stat(spaceDir); err != nil {
		t.Fatalf("unconfirmed space was deleted: %v", err)
	}
	manifest, err = ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects[projectID] != slug {
		t.Fatalf("unconfirmed cleanup changed the mapping: %#v", manifest.Projects)
	}
}

func TestRemoveEmptyProjectSpaceLeavesStateUntouchedWhenManifestIsBroken(t *testing.T) {
	ctx := context.Background()
	manager, config := newCleanupManager(t)
	projectID := "project-broken-manifest-infra"
	if _, err := manager.ProvisionProjectSpace(ctx, projectID, "Broken Manifest Infra"); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[projectID]
	spaceDir := filepath.Join(config.VaultPath, "projects", slug)
	manifestPath := filepath.Join(config.VaultPath, manifestFileName)
	if err := os.WriteFile(manifestPath, []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.RemoveEmptyProjectSpace(ctx, projectID); err == nil {
		t.Fatal("broken manifest did not fail the cleanup")
	}
	if _, err := os.Stat(spaceDir); err != nil {
		t.Fatalf("failed cleanup deleted the directory: %v", err)
	}
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("failed cleanup rewrote the manifest")
	}
}

func TestRemoveEmptyProjectSpaceRestoresDirectoryWhenManifestWriteFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based write failure is not portable to Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the directory permission this test relies on")
	}
	ctx := context.Background()
	manager, config := newCleanupManager(t)
	projectID := "project-rollback-infra"
	if _, err := manager.ProvisionProjectSpace(ctx, projectID, "Rollback Infra"); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[projectID]
	if slug == "" {
		t.Fatalf("project was not provisioned: %#v", manifest.Projects)
	}
	spaceDir := filepath.Join(config.VaultPath, "projects", slug)
	// Pre-create .trash so the only failing step is the manifest write.
	if err := os.MkdirAll(filepath.Join(config.VaultPath, ".trash"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(config.VaultPath, 0o755) })
	if err := os.Chmod(config.VaultPath, 0o555); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.RemoveEmptyProjectSpace(ctx, projectID); err == nil {
		t.Fatal("cleanup succeeded although the manifest could not be written")
	}
	if err := os.Chmod(config.VaultPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(spaceDir); statErr != nil {
		t.Fatalf("rollback did not restore the project directory: %v", statErr)
	}
	manifest, err = ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects[projectID] != slug {
		t.Fatalf("failed cleanup changed the mapping: %#v", manifest.Projects)
	}
	trash, err := os.ReadDir(filepath.Join(config.VaultPath, ".trash"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range trash {
		if strings.HasPrefix(entry.Name(), "infra-space-") {
			t.Fatalf("failed cleanup left trash entry %s", entry.Name())
		}
	}
}

func TestRemoveEmptyProjectSpaceDropsStaleMappingWithoutDirectory(t *testing.T) {
	ctx := context.Background()
	manager, config := newCleanupManager(t)
	projectID := "project-stale-mapping-infra"
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Projects[projectID] = "stale-slug"
	manifest.ProjectNames[projectID] = "Stale"
	manifest.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeManifest(config.VaultPath, manifest); err != nil {
		t.Fatal(err)
	}
	result, err := manager.RemoveEmptyProjectSpace(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Removed || result.Slug != "stale-slug" {
		t.Fatalf("result = %#v", result)
	}
	manifest, err = ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, mapped := manifest.Projects[projectID]; mapped {
		t.Fatalf("stale mapping survived: %#v", manifest.Projects)
	}
}
