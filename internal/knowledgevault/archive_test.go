package knowledgevault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newArchiveTestVault(t *testing.T) (Config, *Manager) {
	t.Helper()
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Archive Vault"}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return config, manager
}

func writeArchiveTestDocument(t *testing.T, config Config, relative, title, body string) {
	t.Helper()
	raw := "---\n" +
		"title: " + title + "\n" +
		"slug: test-doc\n" +
		"scope: project\n" +
		"project_id: p529alpha\n" +
		"type: reference\n" +
		"status: verified\n" +
		"---\n# " + title + "\n\n" + body + "\n"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(config.VaultPath, filepath.FromSlash(relative))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.VaultPath, filepath.FromSlash(relative)), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveProjectSpaceMovesFilesAndRebuildsArchiveIndex(t *testing.T) {
	ctx := context.Background()
	config, manager := newArchiveTestVault(t)
	stamp := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	if _, err := EnsureProjectSpace(config, "p529alpha", "Alpha Project", stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureProjectSpace(config, "p529other", "Other Project", stamp); err != nil {
		t.Fatal(err)
	}
	writeArchiveTestDocument(t, config, "projects/alpha-project/notes/first.md", "归档笔记", "archive-search-marker-529")
	if _, err := manager.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	// A hand-written manifest field must survive the archive rewrite.
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ObsidianVault = "hand-written-obsidian"
	if err := writeManifest(config.VaultPath, manifest); err != nil {
		t.Fatal(err)
	}

	archivedPath, err := ArchiveProjectSpace(config, "p529alpha", stamp)
	if err != nil {
		t.Fatal(err)
	}
	const wantPrefix = "archive/projects/alpha-project-p529alph-20261116T100000Z"
	if archivedPath != wantPrefix {
		t.Fatalf("archived path = %q, want %q", archivedPath, wantPrefix)
	}
	if _, err := manager.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "projects", "alpha-project")); !os.IsNotExist(err) {
		t.Fatalf("live project directory still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(archivedPath), "notes", "first.md")); err != nil {
		t.Fatalf("archived document missing: %v", err)
	}

	after, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := after.Projects["p529alpha"]; exists {
		t.Fatalf("archived project still in manifest.projects: %#v", after.Projects)
	}
	if _, exists := after.ProjectNames["p529alpha"]; exists {
		t.Fatalf("archived project still in manifest.project_names: %#v", after.ProjectNames)
	}
	if after.Archives["p529alpha"] != "projects/alpha-project-p529alph-20261116T100000Z" {
		t.Fatalf("manifest archive mapping = %#v", after.Archives)
	}
	if after.ArchiveNames["p529alpha"] != "Alpha Project" {
		t.Fatalf("manifest archive name = %#v", after.ArchiveNames)
	}
	if after.Projects["p529other"] != "other-project" || after.ProjectNames["p529other"] != "Other Project" {
		t.Fatalf("other project mapping was not preserved: %#v", after.Projects)
	}
	if after.VaultID != manifest.VaultID || after.VaultName != "Archive Vault" || after.ObsidianVault != "hand-written-obsidian" {
		t.Fatalf("hand-written manifest fields changed: %#v", after)
	}

	if err := manager.WithReader(func(reader *Reader) error {
		entries, err := reader.Entries(ctx, "project", "p529alpha")
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			t.Fatalf("archived project still has live entries: %#v", entries)
		}
		plain, err := reader.Search(ctx, DocumentSearchOptions{Query: "archive-search-marker-529", Limit: 10})
		if err != nil {
			return err
		}
		if len(plain.Results) != 0 {
			t.Fatalf("default search leaked archived knowledge: %#v", plain.Results)
		}
		archived, err := reader.Search(ctx, DocumentSearchOptions{Query: "archive-search-marker-529", Limit: 10, IncludeArchives: true})
		if err != nil {
			return err
		}
		if len(archived.Results) != 1 || !archived.Results[0].Archive || archived.Results[0].Scope != "archive" {
			t.Fatalf("archived knowledge is not searchable: %#v", archived.Results)
		}
		if archived.Results[0].ProjectID != "p529alpha" {
			t.Fatalf("archived entry project = %q", archived.Results[0].ProjectID)
		}
		libraryEntries, err := reader.Entries(ctx, "archive", "p529alpha")
		if err != nil {
			return err
		}
		paths := map[string]bool{}
		for _, entry := range libraryEntries {
			paths[entry.Path] = true
		}
		if !paths["archive/projects/alpha-project-p529alph-20261116T100000Z/index.md"] ||
			!paths["archive/projects/alpha-project-p529alph-20261116T100000Z/notes/first.md"] {
			t.Fatalf("archive library entries = %#v", paths)
		}
		library := reader.Libraries(ctx)
		found := false
		for _, item := range library.Archived {
			if item.ProjectID == "p529alpha" && item.Name == "Alpha Project" {
				found = true
			}
		}
		if !found {
			t.Fatalf("archived library not listed: %#v", library.Archived)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveProjectSpaceIsIdempotentWithoutSpace(t *testing.T) {
	ctx := context.Background()
	config, manager := newArchiveTestVault(t)
	stamp := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	for attempt := 0; attempt < 2; attempt++ {
		archivedPath, err := ArchiveProjectSpace(config, "p529missing", stamp)
		if err != nil || archivedPath != "" {
			t.Fatalf("attempt %d = %q err=%v", attempt, archivedPath, err)
		}
	}
	if archivedPath, err := manager.ArchiveProjectSpace(ctx, "p529missing"); err != nil || archivedPath != "" {
		t.Fatalf("manager no-op = %q err=%v", archivedPath, err)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "archive", "projects")); !os.IsNotExist(err) {
		t.Fatalf("idempotent call created an archive directory: %v", err)
	}
}

func TestArchiveProjectSpaceWithoutVaultIsNoOp(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	archivedPath, err := ArchiveProjectSpace(config, "p529missing", time.Now().UTC())
	if err != nil || archivedPath != "" {
		t.Fatalf("no-vault archive = %q err=%v", archivedPath, err)
	}
}

func TestArchiveProjectSpaceNeverOverwritesExistingArchive(t *testing.T) {
	config, _ := newArchiveTestVault(t)
	stamp := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	if _, err := EnsureProjectSpace(config, "p529alpha", "Alpha Project", stamp); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(config.VaultPath, "archive", "projects", "alpha-project-p529alph-20261116T100000Z")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "keep.md"), []byte("older archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	archivedPath, err := ArchiveProjectSpace(config, "p529alpha", stamp)
	if err != nil {
		t.Fatal(err)
	}
	if archivedPath != "archive/projects/alpha-project-p529alph-20261116T100000Z-2" {
		t.Fatalf("conflict suffix = %q", archivedPath)
	}
	raw, err := os.ReadFile(filepath.Join(existing, "keep.md"))
	if err != nil || string(raw) != "older archive" {
		t.Fatalf("existing archive was overwritten: %q err=%v", raw, err)
	}
}

func TestArchiveProjectSpaceFailureLeavesLiveSpaceIntact(t *testing.T) {
	config, _ := newArchiveTestVault(t)
	stamp := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	if _, err := EnsureProjectSpace(config, "p529alpha", "Alpha Project", stamp); err != nil {
		t.Fatal(err)
	}
	// A file where archive/projects should be makes the target directory
	// impossible to create, so the move fails before any file is touched.
	if err := os.MkdirAll(filepath.Join(config.VaultPath, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.VaultPath, "archive", "projects"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	archivedPath, err := ArchiveProjectSpace(config, "p529alpha", stamp)
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("archive error = %v (path %q)", err, archivedPath)
	}
	if _, statErr := os.Stat(filepath.Join(config.VaultPath, "projects", "alpha-project", "index.md")); statErr != nil {
		t.Fatalf("live space was changed by a failed archive: %v", statErr)
	}
	manifest, readErr := ReadManifest(config.VaultPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if manifest.Projects["p529alpha"] != "alpha-project" {
		t.Fatalf("manifest mapping was dropped on failure: %#v", manifest.Projects)
	}
}
