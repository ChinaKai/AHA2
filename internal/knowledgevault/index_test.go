package knowledgevault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exportSeedVault(t *testing.T) (Config, *Index, context.Context) {
	t.Helper()
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	config := DefaultConfig(t.TempDir())
	if _, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	return config, index, ctx
}

func findIndexEntry(t *testing.T, entries []IndexEntry, relative string) IndexEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Path == relative {
			return entry
		}
	}
	t.Fatalf("indexed entry %s not found", relative)
	return IndexEntry{}
}

func TestRebuildProjectsVaultTree(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	report, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.Entries != 17 || report.Global != 9 || report.Projects != 6 || report.Archives != 2 {
		t.Fatalf("rebuild counts = %+v", report)
	}
	if report.Links != 1 || report.Tags != 0 {
		t.Fatalf("rebuild graph counts = %+v", report)
	}
	if report.Warnings != nil {
		t.Fatalf("unexpected warnings: %v", report.Warnings)
	}

	entries, err := index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 17 {
		t.Fatalf("entries = %d, want 17", len(entries))
	}
	globalRoot := findIndexEntry(t, entries, "global/index.md")
	if !globalRoot.IsIndex || globalRoot.Scope != "global" || globalRoot.Type != TypeNavigation {
		t.Fatalf("global root = %+v", globalRoot)
	}
	module := findIndexEntry(t, entries, "projects/demo-project/navigation-group/module-a.md")
	group := findIndexEntry(t, entries, "projects/demo-project/navigation-group/index.md")
	if module.ParentID != group.ID {
		t.Fatalf("module parent = %q, want %q", module.ParentID, group.ID)
	}
	if module.Slug != "module-a" || module.Type != TypeReference || module.Status != "verified" {
		t.Fatalf("module = %+v", module)
	}
	inbox := findIndexEntry(t, entries, "projects/demo-project/inbox/inbox-one.md")
	if inbox.ParentID != "knowledge_root_project_project-demo" {
		t.Fatalf("inbox parent = %q", inbox.ParentID)
	}
	archive := findIndexEntry(t, entries, "archive/aha1-archive/inbox/legacy-note.md")
	if archive.Scope != "archive" || !archive.Archive || archive.ProjectID != "project-archive" {
		t.Fatalf("archive entry = %+v", archive)
	}
	if archive.LegacyType != "general" || archive.Type != TypeReference {
		t.Fatalf("archive type mapping = %+v", archive)
	}

	results, err := index.Search(ctx, "部署前先验证", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != "global/general/shared-deploy.md" {
		t.Fatalf("search results = %+v", results)
	}
	scoped, err := index.Search(ctx, "根下叶子", SearchOptions{Scope: "project", ProjectID: "project-demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 2 {
		t.Fatalf("project search = %+v", scoped)
	}
	archived, err := index.Search(ctx, "AHA1 归档内容", SearchOptions{IncludeArchives: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 || archived[0].Scope != "archive" {
		t.Fatalf("archive search = %+v", archived)
	}
	hidden, err := index.Search(ctx, "AHA1 归档内容", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden) != 0 {
		t.Fatalf("archives leaked into default search: %+v", hidden)
	}

	status, err := index.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Entries != 17 || status.Global != 9 || status.Projects != 6 || status.Archives != 2 {
		t.Fatalf("index status = %+v", status)
	}
	if status.Links != 1 || status.RebuiltAt == "" {
		t.Fatalf("index status metadata = %+v", status)
	}
}

func TestRebuildIsRepeatable(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	first, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	firstEntries, err := index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if first.Entries != second.Entries || first.Links != second.Links || first.Global != second.Global {
		t.Fatalf("rebuild is not repeatable: %+v vs %+v", first, second)
	}
	secondEntries, err := index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(firstEntries) != len(secondEntries) {
		t.Fatalf("entry count changed: %d vs %d", len(firstEntries), len(secondEntries))
	}
	for index := range firstEntries {
		if firstEntries[index].ID != secondEntries[index].ID || firstEntries[index].Path != secondEntries[index].Path {
			t.Fatalf("entry %d changed: %+v vs %+v", index, firstEntries[index], secondEntries[index])
		}
	}
}

func TestRebuildAfterDeletingIndexDatabase(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	first, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(config.IndexPath + suffix)
	}
	if _, err := os.Stat(config.IndexPath); !os.IsNotExist(err) {
		t.Fatalf("index database still exists: %v", err)
	}

	reopened, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	second, err := reopened.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if second.Entries != first.Entries || second.Global != first.Global || second.Projects != first.Projects || second.Archives != first.Archives {
		t.Fatalf("rebuilt index differs: %+v vs %+v", first, second)
	}
	results, err := reopened.Search(ctx, "模块 A", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Path != "projects/demo-project/navigation-group/module-a.md" {
		t.Fatalf("search after rebuild = %+v", results)
	}
}

func TestRebuildKeepsMalformedFrontmatterFile(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	raw := "---\nid: [broken\n---\n\n# Broken note\n\nstill-searchable-content\n"
	brokenPath := filepath.Join(config.VaultPath, "projects", "demo-project", "inbox", "broken.md")
	if err := os.WriteFile(brokenPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.Entries != 18 {
		t.Fatalf("entries = %d, want 18", report.Entries)
	}
	foundWarning := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "invalid frontmatter") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("no frontmatter warning: %v", report.Warnings)
	}
	entries, err := index.Entries(ctx, "project", "project-demo")
	if err != nil {
		t.Fatal(err)
	}
	broken := findIndexEntry(t, entries, "projects/demo-project/inbox/broken.md")
	if broken.Body != raw {
		t.Fatalf("malformed file lost its raw text: %q", broken.Body)
	}
	if broken.Warning == "" || broken.Title != "Broken note" {
		t.Fatalf("malformed entry = %+v", broken)
	}
	if broken.ParentID != "knowledge_root_project_project-demo" {
		t.Fatalf("malformed entry parent = %q", broken.ParentID)
	}
	results, err := index.Search(ctx, "still-searchable-content", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Path != "projects/demo-project/inbox/broken.md" {
		t.Fatalf("malformed file is not searchable: %+v", results)
	}
}

func TestRebuildSkipsSymlinkEscape(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("# Outside secret\n\noutside-secret-content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(config.VaultPath, "projects", "demo-project", "inbox", "escape.md")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	report, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.Skipped == 0 {
		t.Fatal("symlink was not counted as skipped")
	}
	if report.Entries != 17 {
		t.Fatalf("entries = %d, want 17", report.Entries)
	}
	foundWarning := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "symbolic link skipped") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("no symlink warning: %v", report.Warnings)
	}
	results, err := index.Search(ctx, "outside-secret-content", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("outside file was indexed: %+v", results)
	}
}

func TestRebuildInfersFrontmatterAndStableIDs(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	raw := "# 自带头衔\n\n推断正文 inference-body\n"
	relative := "projects/demo-project/inbox/loose-note.md"
	if err := os.WriteFile(filepath.Join(config.VaultPath, filepath.FromSlash(relative)), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	entries, err := index.Entries(ctx, "project", "project-demo")
	if err != nil {
		t.Fatal(err)
	}
	inferred := findIndexEntry(t, entries, relative)
	if !strings.HasPrefix(inferred.ID, "vault_") {
		t.Fatalf("inferred id = %q", inferred.ID)
	}
	if inferred.Title != "自带头衔" || inferred.Slug != "loose-note" || inferred.Type != TypeReference || inferred.Status != "verified" {
		t.Fatalf("inferred entry = %+v", inferred)
	}
	firstID := inferred.ID

	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	entries, err = index.Entries(ctx, "project", "project-demo")
	if err != nil {
		t.Fatal(err)
	}
	again := findIndexEntry(t, entries, relative)
	if again.ID != firstID {
		t.Fatalf("file-inferred id is not stable: %q vs %q", firstID, again.ID)
	}
}
