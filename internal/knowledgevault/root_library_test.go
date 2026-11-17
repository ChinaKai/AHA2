package knowledgevault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newRootLibraryVault(t *testing.T) (Config, *Reader) {
	t.Helper()
	ctx := context.Background()
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Root Libraries", Commit: false}); err != nil {
		t.Fatal(err)
	}
	write := func(relative, content string) {
		target := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notes/doc.md", "---\nid: knowledge_notes_doc\ntitle: Root note\nscope: root\ntype: reference\nstatus: verified\n---\nRoot note body.\n")
	write("notes/sub/nested.md", "---\nid: knowledge_notes_nested\ntitle: Nested note\nscope: root\ntype: practice\nstatus: verified\n---\nNested root body.\n")
	write("projects/alpha/index.md", "---\nid: knowledge_root_project_alpha\ntitle: Alpha\nscope: project\nproject_id: project_alpha\ntype: navigation\nstatus: verified\nis_index: true\n---\nAlpha.\n")
	write("projects/alpha/project-note.md", "---\nid: knowledge_project_note\ntitle: Project note\nscope: project\nproject_id: project_alpha\ntype: practice\nstatus: verified\n---\nProject body.\n")
	write("global/general/shared.md", "---\nid: knowledge_global_shared\ntitle: Shared\nscope: global\ntype: reference\nstatus: verified\n---\nGlobal body.\n")
	if err := os.MkdirAll(filepath.Join(config.VaultPath, "notes", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReader(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return config, reader
}

func TestRootLibraryIndexPagesLibrariesTreeAndSearch(t *testing.T) {
	ctx := context.Background()
	config, reader := newRootLibraryVault(t)

	for _, relative := range []string{"notes/index.md", "notes/sub/index.md", "notes/empty/index.md"} {
		if info, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(relative))); err != nil || info.IsDir() {
			t.Fatalf("%s was not generated: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "index.md")); !os.IsNotExist(err) {
		t.Fatalf("a vault root index.md was generated: %v", err)
	}

	entries, err := reader.Entries(ctx, "root", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Path == "notes/index.md" {
			t.Fatalf("root navigation page leaked into the index: %#v", entry)
		}
		if entry.ProjectID != "" {
			t.Fatalf("root entry has a project id: %#v", entry)
		}
	}

	libraries := reader.Libraries(ctx)
	var rootLibrary *Library
	for index := range libraries.Libraries {
		if libraries.Libraries[index].ID == "root:notes" {
			rootLibrary = &libraries.Libraries[index]
		}
	}
	if rootLibrary == nil || rootLibrary.Kind != "root" || rootLibrary.ProjectID != "" || rootLibrary.Count == 0 {
		t.Fatalf("root library = %#v list=%#v", rootLibrary, libraries.Libraries)
	}
	if rootLibrary.RootID != vaultNavigationID("notes/index.md") {
		t.Fatalf("root library root id = %q", rootLibrary.RootID)
	}

	tree, err := reader.Tree(ctx, "root:notes", "")
	if err != nil {
		t.Fatal(err)
	}
	var notesFolder *TreeNode
	for index := range tree.Nodes {
		if tree.Nodes[index].Path == "notes/index.md" {
			notesFolder = &tree.Nodes[index]
		}
	}
	if notesFolder == nil || notesFolder.ID != vaultNavigationID("notes/index.md") || !notesFolder.IsIndex {
		t.Fatalf("root library folder = %#v nodes=%#v", notesFolder, tree.Nodes)
	}
	childPaths := map[string]bool{}
	for _, child := range notesFolder.Children {
		childPaths[child.Path] = true
	}
	for _, want := range []string{"notes/doc.md", "notes/sub/index.md", "notes/empty/index.md"} {
		if !childPaths[want] {
			t.Fatalf("tree missing %s: %#v", want, notesFolder.Children)
		}
	}
	all, err := reader.Tree(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	foundRoot := false
	for _, node := range all.Nodes {
		foundRoot = foundRoot || node.Path == "notes/index.md"
	}
	if !foundRoot {
		t.Fatalf("all-libraries tree does not contain the root library: %#v", all.Nodes)
	}

	rootSearch, err := reader.Search(ctx, DocumentSearchOptions{Query: "Root note body", Library: "root:notes"})
	if err != nil || len(rootSearch.Results) != 1 || rootSearch.Results[0].Path != "notes/doc.md" {
		t.Fatalf("root search = %#v err=%v", rootSearch, err)
	}
	otherRoot, err := reader.Search(ctx, DocumentSearchOptions{Query: "Project body", Library: "root:notes"})
	if err != nil || len(otherRoot.Results) != 0 {
		t.Fatalf("root search leaked project documents: %#v err=%v", otherRoot, err)
	}
	globalSearch, err := reader.Search(ctx, DocumentSearchOptions{Query: "body"})
	if err != nil || len(globalSearch.Results) < 3 {
		t.Fatalf("global search = %#v err=%v", globalSearch, err)
	}
	navSearch, err := reader.Search(ctx, DocumentSearchOptions{Query: "Notes", Library: "root:notes"})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range navSearch.Results {
		if result.Path == "notes/index.md" {
			t.Fatalf("root navigation page was searchable: %#v", result)
		}
	}

	doc, err := reader.Document(ctx, vaultNavigationID("notes/index.md"), "")
	if err != nil || doc.LibraryID != "root:notes" || !strings.Contains(doc.Body, "notes/doc") {
		t.Fatalf("root navigation document = %#v err=%v", doc, err)
	}
}

func TestRootLibraryAutoIndexFingerprintTracksEmptyDirectories(t *testing.T) {
	ctx := context.Background()
	config, _ := newRootLibraryVault(t)
	before, err := autoIndexFingerprint(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(config.VaultPath, "notes", "fresh-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := autoIndexFingerprint(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("creating an empty root-library directory did not change the auto-index fingerprint")
	}
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "notes", "fresh-empty", "index.md")); err != nil {
		t.Fatalf("new empty directory did not receive an index page: %v", err)
	}
}

func TestRootLibraryDeletedDirectoryIsCleanedFromIndex(t *testing.T) {
	ctx := context.Background()
	config, reader := newRootLibraryVault(t)
	if err := os.RemoveAll(filepath.Join(config.VaultPath, "notes")); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenReader(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	entries, err := reopened.Entries(ctx, "root", "")
	if err != nil || len(entries) != 0 {
		t.Fatalf("deleted root library entries = %#v err=%v", entries, err)
	}
	for _, library := range reopened.Libraries(ctx).Libraries {
		if library.ID == "root:notes" {
			t.Fatalf("deleted root library still listed: %#v", library)
		}
	}
	_ = reader.Close()
}
