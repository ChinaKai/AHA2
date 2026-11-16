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

func TestRemoveEmptyProjectSpaceRestoresWhenFilesAppearAfterRename(t *testing.T) {
	manager, config := newCleanupManager(t)
	ctx := context.Background()
	projectID := "project-rename-race-infra"
	if _, err := manager.ProvisionProjectSpace(ctx, projectID, "Rename Race Infra"); err != nil {
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
	lateRelative := "late-write.md"
	result, needsRebuild, err := removeEmptyProjectSpaceWithHooks(config, projectID, time.Now().UTC(), cleanupHooks{
		afterRename: func(trashPath string) {
			// Simulate a writer that added a file between the pre-rename check
			// and the rename: the rename carries the file into .trash.
			if writeErr := os.WriteFile(filepath.Join(trashPath, lateRelative), []byte("late"), 0o644); writeErr != nil {
				t.Errorf("write late file: %v", writeErr)
			}
		},
	})
	if !errors.Is(err, ErrProjectSpaceNotEmpty) {
		t.Fatalf("error = %v, want ErrProjectSpaceNotEmpty", err)
	}
	if result.Removed || needsRebuild {
		t.Fatalf("result = %#v needsRebuild = %v", result, needsRebuild)
	}
	if !cleanupContainsPath(result.BlockingPaths, lateRelative) {
		t.Fatalf("blocking paths = %#v", result.BlockingPaths)
	}
	// The directory is restored with the late file intact and the manifest
	// mapping is unchanged.
	if _, statErr := os.Stat(filepath.Join(spaceDir, lateRelative)); statErr != nil {
		t.Fatalf("late file was not restored with the directory: %v", statErr)
	}
	manifest, err = ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects[projectID] != slug {
		t.Fatalf("restored cleanup changed the mapping: %#v", manifest.Projects)
	}
	trash, err := os.ReadDir(filepath.Join(config.VaultPath, ".trash"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range trash {
		if strings.HasPrefix(entry.Name(), projectSpaceTrashPrefix) {
			t.Fatalf("failed cleanup left trash entry %s", entry.Name())
		}
	}
}

func TestRemoveEmptyProjectSpaceKeepsLateTrashContent(t *testing.T) {
	manager, config := newCleanupManager(t)
	ctx := context.Background()
	projectID := "project-remove-race-infra"
	if _, err := manager.ProvisionProjectSpace(ctx, projectID, "Remove Race Infra"); err != nil {
		t.Fatal(err)
	}
	result, needsRebuild, err := removeEmptyProjectSpaceWithHooks(config, projectID, time.Now().UTC(), cleanupHooks{
		beforeRemove: func(trashPath string) {
			// A file appears after the post-rename confirmation. It must be
			// kept, never deleted, even though the mapping is already gone.
			if writeErr := os.WriteFile(filepath.Join(trashPath, "late-write.md"), []byte("late"), 0o644); writeErr != nil {
				t.Errorf("write late file: %v", writeErr)
			}
		},
	})
	if err != nil {
		t.Fatalf("keeping late content must not fail the cleanup: %v", err)
	}
	if !result.Removed || !needsRebuild {
		t.Fatalf("result = %#v needsRebuild = %v", result, needsRebuild)
	}
	if result.Warning == "" || !cleanupContainsPath(result.BlockingPaths, "late-write.md") {
		t.Fatalf("result = %#v", result)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, mapped := manifest.Projects[projectID]; mapped {
		t.Fatalf("manifest still maps the project: %#v", manifest.Projects)
	}
	trash, err := os.ReadDir(filepath.Join(config.VaultPath, ".trash"))
	if err != nil {
		t.Fatal(err)
	}
	kept := 0
	for _, entry := range trash {
		if !strings.HasPrefix(entry.Name(), projectSpaceTrashPrefix) {
			continue
		}
		kept++
		if _, statErr := os.Stat(filepath.Join(config.VaultPath, ".trash", entry.Name(), "late-write.md")); statErr != nil {
			t.Fatalf("late file was deleted: %v", statErr)
		}
	}
	if kept != 1 {
		t.Fatalf("kept trash entries = %d, want 1", kept)
	}
}

func writeTrashScaffolding(t *testing.T, directory, projectID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	index := "---\ntitle: Scaffold\nslug: scaffold\nscope: project\nproject_id: " + projectID + "\ntype: index\nstatus: verified\n---\n# Scaffold\n"
	if err := os.WriteFile(filepath.Join(directory, "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".gitkeep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSweepProjectSpaceTrashRemovesOnlyConfirmedScaffolding(t *testing.T) {
	manager, config := newCleanupManager(t)
	trashDir := filepath.Join(config.VaultPath, ".trash")

	empty := filepath.Join(trashDir, "infra-space-empty-20260101T000000Z")
	writeTrashScaffolding(t, empty, "project-trash-empty")

	busy := filepath.Join(trashDir, "infra-space-busy-20260101T000000Z")
	writeTrashScaffolding(t, busy, "project-trash-busy")
	if err := os.MkdirAll(filepath.Join(busy, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(busy, "notes", "keep.md"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	unknown := filepath.Join(trashDir, "infra-space-unknown-20260101T000000Z")
	if err := os.MkdirAll(unknown, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unknown, "index.md"), []byte("---\ntitle: Unknown\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fileEntry := filepath.Join(trashDir, "infra-space-file")
	if err := os.WriteFile(fileEntry, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	unrelated := filepath.Join(trashDir, "manual")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelated, "note.md"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := manager.SweepProjectSpaceTrash()
	if err != nil {
		t.Fatal(err)
	}
	wantRemoved := ".trash/" + filepath.Base(empty)
	if len(result.Removed) != 1 || result.Removed[0] != wantRemoved {
		t.Fatalf("removed = %#v, want [%s]", result.Removed, wantRemoved)
	}
	for _, want := range []string{".trash/" + filepath.Base(busy), ".trash/" + filepath.Base(unknown), ".trash/infra-space-file"} {
		found := false
		for _, retained := range result.Retained {
			if retained == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("retained = %#v, missing %s", result.Retained, want)
		}
	}
	if len(result.Warnings) != 3 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
	if _, statErr := os.Stat(empty); !os.IsNotExist(statErr) {
		t.Fatalf("confirmed scaffolding was not removed: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(busy, "notes", "keep.md")); statErr != nil {
		t.Fatalf("non-empty trash content was touched: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(unrelated, "note.md")); statErr != nil {
		t.Fatalf("unrelated trash content was touched: %v", statErr)
	}

	// Idempotent: a second startup removes nothing and reports the same
	// retained entries again.
	again, err := SweepProjectSpaceTrash(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Removed) != 0 {
		t.Fatalf("second sweep removed %#v", again.Removed)
	}
	if len(again.Retained) != len(result.Retained) {
		t.Fatalf("second sweep retained = %#v", again.Retained)
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
