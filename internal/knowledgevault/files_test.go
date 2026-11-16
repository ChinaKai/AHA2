package knowledgevault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newFilesTestReader(t *testing.T) (*Reader, Config) {
	t.Helper()
	ctx := context.Background()
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Files"}); err != nil {
		t.Fatal(err)
	}
	write := func(relative, content string) {
		t.Helper()
		target := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "# User readme\n")
	write("archive/old/doc.md", "# Archived doc\n")
	write("skills/demo/SKILL.md", "# Demo skill\n")
	write("projects/demo/index.md", "---\ntitle: Demo\nscope: project\nproject_id: project-demo\ntype: navigation\nis_index: true\n---\n# Demo\n")
	write("projects/demo/raw.bin", string([]byte{0x00, 0x01, 0x02}))
	write(".obsidian/app.json", "{}")
	reader, err := OpenReader(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader, config
}

func TestFilesListsRealTreeAndHidesDotPaths(t *testing.T) {
	reader, config := newFilesTestReader(t)
	list, err := reader.Files("")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]VaultFileEntry{}
	for _, entry := range list.Entries {
		names[entry.Name] = entry
	}
	for _, expected := range []string{"global", "projects", "archive", "skills", "README.md"} {
		if _, ok := names[expected]; !ok {
			t.Fatalf("root listing missing %q: %#v", expected, list.Entries)
		}
	}
	for _, hidden := range []string{".aha-vault.json", ".obsidian", ".git", ".aha", ".trash"} {
		if _, ok := names[hidden]; ok {
			t.Fatalf("root listing exposed hidden path %q", hidden)
		}
	}
	if names["projects"].Kind != "folder" || names["README.md"].Kind != "markdown" {
		t.Fatalf("entry kinds = %#v", names)
	}
	// The listing is the real filesystem: an unindexed file is still present.
	if _, ok := names["README.md"]; !ok {
		t.Fatal("unindexed root user file missing from the directory")
	}
	sub, err := reader.Files("projects/demo")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Count != 2 {
		t.Fatalf("project listing = %#v", sub.Entries)
	}
	if _, err := reader.Files("../../etc"); err == nil {
		t.Fatal("directory traversal was accepted")
	}
	if _, err := reader.Files(".aha"); err == nil {
		t.Fatal("dot directory was readable")
	}
	// A symlink must not escape the vault.
	target := filepath.Join(config.VaultPath, "escape.md")
	if err := os.Symlink("/etc/passwd", target); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := reader.File("escape.md"); err == nil {
		t.Fatal("symlinked file was readable")
	}
}

func TestFileReadsDirectlyWithoutIndex(t *testing.T) {
	reader, _ := newFilesTestReader(t)
	file, err := reader.File("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != "markdown" || !strings.Contains(file.Content, "User readme") {
		t.Fatalf("file = %#v", file)
	}
	index, err := reader.File("projects/demo/index.md")
	if err != nil {
		t.Fatal(err)
	}
	if index.Title != "Demo" {
		t.Fatalf("markdown title = %q", index.Title)
	}
	if _, err := reader.File("projects/demo/raw.bin"); !errors.Is(err, ErrBinaryFile) {
		t.Fatalf("binary error = %v", err)
	}
}
