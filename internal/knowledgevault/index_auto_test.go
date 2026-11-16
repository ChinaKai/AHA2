package knowledgevault

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func vaultPathExists(t *testing.T, vaultPath, relative string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(vaultPath, filepath.FromSlash(relative)))
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("stat %s: %v", relative, err)
	return false
}

func removeVaultTestFile(t *testing.T, vaultPath, relative string) {
	t.Helper()
	if err := os.Remove(filepath.Join(vaultPath, filepath.FromSlash(relative))); err != nil {
		t.Fatalf("remove %s: %v", relative, err)
	}
}

func assertIndexFrontmatter(t *testing.T, raw, scope, projectID string) {
	t.Helper()
	document := ParseDocument(raw)
	if document.Warning != "" {
		t.Fatalf("created index page has invalid frontmatter: %s", document.Warning)
	}
	if document.Frontmatter.IsIndex == nil || !*document.Frontmatter.IsIndex {
		t.Fatalf("created index page is missing is_index: true:\n%s", raw)
	}
	if document.Frontmatter.Scope != scope {
		t.Fatalf("scope = %q, want %q:\n%s", document.Frontmatter.Scope, scope, raw)
	}
	if document.Frontmatter.ProjectID != projectID {
		t.Fatalf("project_id = %q, want %q:\n%s", document.Frontmatter.ProjectID, projectID, raw)
	}
	if document.Frontmatter.Type != TypeNavigation {
		t.Fatalf("type = %q, want %q:\n%s", document.Frontmatter.Type, TypeNavigation, raw)
	}
	if document.Frontmatter.Status != "verified" {
		t.Fatalf("status = %q, want verified:\n%s", document.Frontmatter.Status, raw)
	}
}

func TestRebuildCreatesMissingDirectoryIndexAndFillsList(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	groupRelative := "projects/demo-project/navigation-group/index.md"
	removeVaultTestFile(t, config.VaultPath, groupRelative)

	report, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	raw := readExportedFile(t, config.VaultPath, groupRelative)
	assertIndexFrontmatter(t, raw, "project", "project-demo")
	if strings.Count(raw, indexBlockStart) != 1 || strings.Count(raw, indexBlockEnd) != 1 {
		t.Fatalf("created page does not carry exactly one generated block:\n%s", raw)
	}
	if !strings.Contains(raw, "navigation-group/module-a|模块 A") || !strings.Contains(raw, "navigation-group/module-b|模块 B 决策") {
		t.Fatalf("generated list is missing the directory's documents:\n%s", raw)
	}

	entries, err := index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	group := findIndexEntry(t, entries, groupRelative)
	if !group.IsIndex || group.Scope != "project" || group.ProjectID != "project-demo" {
		t.Fatalf("created page projection = %+v", group)
	}
	if !strings.Contains(group.Body, indexBlockStart) || !strings.Contains(group.Body, "module-a") {
		t.Fatalf("projection dropped the generated list from the directory page:\n%s", group.Body)
	}
	for _, warning := range report.Warnings {
		if strings.Contains(warning, groupRelative) {
			t.Fatalf("unexpected warning for the created page: %s", warning)
		}
	}
}

func TestRebuildRecoversProjectBindingFromDocumentsWhenRootIndexIsMissing(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	rootRelative := "projects/demo-project/index.md"
	removeVaultTestFile(t, config.VaultPath, rootRelative)

	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	raw := readExportedFile(t, config.VaultPath, rootRelative)
	assertIndexFrontmatter(t, raw, "project", "project-demo")

	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects["project-demo"] != "demo-project" {
		t.Fatalf("manifest lost the recovered project binding: %#v", manifest.Projects)
	}

	entries, err := index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	root := findIndexEntry(t, entries, rootRelative)
	if !root.IsIndex || root.Scope != "project" || root.ProjectID != "project-demo" {
		t.Fatalf("recreated project root = %+v", root)
	}
	module := findIndexEntry(t, entries, "projects/demo-project/navigation-group/module-a.md")
	if module.Scope != "project" || module.ProjectID != "project-demo" {
		t.Fatalf("project document lost its scope: %+v", module)
	}
	if !strings.Contains(readExportedFile(t, config.VaultPath, rootRelative), "navigation-group/index|导航组") {
		t.Fatalf("project root does not list its nested directory:\n%s", readExportedFile(t, config.VaultPath, rootRelative))
	}
}

func TestRebuildSkipsReservedDirectoriesWhenCreatingIndexes(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	excluded := []string{
		"projects/demo-project/inbox/loose.md",
		"projects/demo-project/skills/tool/SKILL.md",
		"projects/demo-project/.hidden/note.md",
		"projects/demo-project/archive/note.md",
		"projects/demo-project/orphans/note.md",
		"global/inbox/loose.md",
		"global/skills/demo/SKILL.md",
		"global/.cache/note.md",
		"orphans/legacy/note.md",
	}
	for _, relative := range excluded {
		writeVaultTestFile(t, config.VaultPath, relative, "# 保留目录\n\n不应生成目录页。\n")
	}
	writeVaultTestFile(t, config.VaultPath, "projects/demo-project/topics/note.md", "# 主题\n\n应生成目录页。\n")
	writeVaultTestFile(t, config.VaultPath, "archive/aha1-archive/topic/note.md", "# 归档主题\n\n应生成目录页。\n")
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}

	for _, directory := range []string{
		"projects/demo-project/inbox", "projects/demo-project/skills", "projects/demo-project/.hidden",
		"projects/demo-project/archive", "projects/demo-project/orphans",
		"global/inbox", "global/skills", "global/.cache",
		"archive/aha1-archive/inbox", "orphans/legacy",
	} {
		if vaultPathExists(t, config.VaultPath, directory+"/index.md") {
			t.Fatalf("%s received an automatic index page", directory)
		}
	}
	if !vaultPathExists(t, config.VaultPath, "projects/demo-project/topics/index.md") {
		t.Fatal("ordinary project directory did not receive an index page")
	}
	assertIndexFrontmatter(t, readExportedFile(t, config.VaultPath, "projects/demo-project/topics/index.md"), "project", "project-demo")
	if !strings.Contains(readExportedFile(t, config.VaultPath, "projects/demo-project/topics/index.md"), "topics/note|主题") {
		t.Fatalf("created directory page does not list its document:\n%s", readExportedFile(t, config.VaultPath, "projects/demo-project/topics/index.md"))
	}
	if !vaultPathExists(t, config.VaultPath, "archive/aha1-archive/topic/index.md") {
		t.Fatal("archive directory did not receive an index page")
	}
	assertIndexFrontmatter(t, readExportedFile(t, config.VaultPath, "archive/aha1-archive/topic/index.md"), "archive", "project-archive")
	if !strings.Contains(readExportedFile(t, config.VaultPath, "archive/aha1-archive/topic/index.md"), "topic/note|归档主题") {
		t.Fatalf("created archive page does not list its document:\n%s", readExportedFile(t, config.VaultPath, "archive/aha1-archive/topic/index.md"))
	}
}

func TestRebuildIndexBackfillIsIdempotent(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	groupRelative := "projects/demo-project/navigation-group/index.md"
	removeVaultTestFile(t, config.VaultPath, groupRelative)

	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	first := readExportedFile(t, config.VaultPath, groupRelative)
	entries, err := index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	firstBody := findIndexEntry(t, entries, groupRelative).Body
	if !strings.Contains(firstBody, "module-a") {
		t.Fatalf("first rebuild projection is missing the generated list:\n%s", firstBody)
	}

	pagesBefore := exportedIndexPages(t, config.VaultPath)
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	second := readExportedFile(t, config.VaultPath, groupRelative)
	if second != first {
		t.Fatalf("second rebuild rewrote the created page:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if strings.Count(second, indexBlockStart) != 1 {
		t.Fatalf("second rebuild duplicated the generated block:\n%s", second)
	}
	if pagesAfter := exportedIndexPages(t, config.VaultPath); !bytes.Equal(pagesBefore, pagesAfter) {
		t.Fatal("second rebuild changed index pages on disk")
	}
	entries, err = index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if secondBody := findIndexEntry(t, entries, groupRelative).Body; secondBody != firstBody {
		t.Fatalf("projection body changed across rebuilds:\n--- first ---\n%s\n--- second ---\n%s", firstBody, secondBody)
	}
}

func TestRebuildStripsGeneratedIndexOnlyFromOrdinaryDocuments(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	relative := "global/general/with-stale-block.md"
	writeVaultTestFile(t, config.VaultPath, relative, "# 正常文档\n\n正文。\n\n"+indexBlockStart+"\n## 文档\n\n- [[stale|旧内容]]\n"+indexBlockEnd+"\n")
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	entries, err := index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ordinary := findIndexEntry(t, entries, relative)
	if strings.Contains(ordinary.Body, indexBlockStart) || strings.Contains(ordinary.Body, "旧内容") {
		t.Fatalf("ordinary document kept a generated block:\n%s", ordinary.Body)
	}
	directory := findIndexEntry(t, entries, "global/general/index.md")
	if !strings.Contains(directory.Body, indexBlockStart) {
		t.Fatalf("directory page lost its generated block:\n%s", directory.Body)
	}
}

func TestRebuildBackfillsOldVaultWithoutIndexPages(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	indexPaths := []string{}
	err := filepath.WalkDir(config.VaultPath, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == "index.md" {
			relative, relErr := filepath.Rel(config.VaultPath, filePath)
			if relErr != nil {
				return relErr
			}
			indexPaths = append(indexPaths, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range indexPaths {
		removeVaultTestFile(t, config.VaultPath, relative)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}

	created := []string{
		"global/index.md", "global/general/index.md",
		"projects/demo-project/index.md",
		"projects/demo-project/navigation-group/index.md",
		"archive/aha1-archive/index.md",
		"archive/index.md",
	}
	for _, relative := range created {
		if !vaultPathExists(t, config.VaultPath, relative) {
			t.Fatalf("%s was not backfilled", relative)
		}
	}
	if raw := readExportedFile(t, config.VaultPath, "global/index.md"); !strings.Contains(raw, "global/general/index|General") {
		t.Fatalf("global root does not list its subdirectory:\n%s", raw)
	}
	assertIndexFrontmatter(t, readExportedFile(t, config.VaultPath, "global/index.md"), "global", "")
	assertIndexFrontmatter(t, readExportedFile(t, config.VaultPath, "archive/aha1-archive/index.md"), "archive", "project-archive")
	for _, relative := range []string{
		"projects/demo-project/inbox/index.md",
		"archive/aha1-archive/inbox/index.md",
		"orphans/index.md",
	} {
		if vaultPathExists(t, config.VaultPath, relative) {
			t.Fatalf("%s should not be backfilled", relative)
		}
	}

	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects["project-demo"] != "demo-project" {
		t.Fatalf("manifest binding was not recovered: %#v", manifest.Projects)
	}
	entries, err := index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"projects/demo-project/index.md", "projects/demo-project/navigation-group/index.md"} {
		entry := findIndexEntry(t, entries, relative)
		if !entry.IsIndex || entry.Scope != "project" || entry.ProjectID != "project-demo" {
			t.Fatalf("backfilled %s projection = %+v", relative, entry)
		}
	}
	module := findIndexEntry(t, entries, "projects/demo-project/navigation-group/module-a.md")
	if module.Scope != "project" || module.ProjectID != "project-demo" {
		t.Fatalf("project document scope was lost: %+v", module)
	}
}

func TestRebuildKeepsHandWrittenPageWhileBackfillingOthers(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	handWrittenRelative := "projects/demo-project/manual/index.md"
	handWritten := "---\ntitle: 手写目录\ntype: navigation\nis_index: true\n---\n\n手写导语，不要覆盖。\n"
	writeVaultTestFile(t, config.VaultPath, handWrittenRelative, handWritten)
	writeVaultTestFile(t, config.VaultPath, "projects/demo-project/manual/child.md", "# 子文档\n\n正文。\n")
	removeVaultTestFile(t, config.VaultPath, "projects/demo-project/navigation-group/index.md")

	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if got := readExportedFile(t, config.VaultPath, handWrittenRelative); got != handWritten {
		t.Fatalf("hand-written index page was overwritten:\n%s", got)
	}
	if !vaultPathExists(t, config.VaultPath, "projects/demo-project/navigation-group/index.md") {
		t.Fatal("missing page was not created next to the hand-written one")
	}
}

func TestReaderDocumentAndTreeExposeGeneratedDirectoryList(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	groupRelative := "projects/demo-project/navigation-group/index.md"
	removeVaultTestFile(t, config.VaultPath, groupRelative)
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReader(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	document, err := reader.Document(ctx, "", groupRelative)
	if err != nil {
		t.Fatal(err)
	}
	if !document.IsIndex {
		t.Fatalf("directory page is not marked as an index: %+v", document)
	}
	if !strings.Contains(document.Body, indexBlockStart) || !strings.Contains(document.Body, "module-a|模块 A") {
		t.Fatalf("Reader.Document returned a directory page without the generated list:\n%s", document.Body)
	}

	ordinary, err := reader.Document(ctx, "", "projects/demo-project/navigation-group/module-a.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ordinary.Body, indexBlockStart) {
		t.Fatalf("ordinary document leaked a generated block:\n%s", ordinary.Body)
	}

	tree, err := reader.Tree(ctx, "project:project-demo", "")
	if err != nil {
		t.Fatal(err)
	}
	node := findTreeNodeByPath(tree.Nodes, groupRelative)
	if node == nil || !node.IsIndex {
		t.Fatalf("directory page is missing from the reader tree: %+v", tree.Nodes)
	}
}

func TestRebuildCreatesGlobalRootIndexFromDirectDocuments(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	if err := os.RemoveAll(filepath.Join(config.VaultPath, "global", "general")); err != nil {
		t.Fatal(err)
	}
	removeVaultTestFile(t, config.VaultPath, "global/index.md")
	writeVaultTestFile(t, config.VaultPath, "global/direct.md", "# 直接文档\n\n位于 global 根目录。\n")
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	raw := readExportedFile(t, config.VaultPath, "global/index.md")
	assertIndexFrontmatter(t, raw, "global", "")
	if !strings.Contains(raw, "global/direct|直接文档") {
		t.Fatalf("global root page does not list its direct document:\n%s", raw)
	}
}

func TestCreateVaultIndexPageNeverOverwritesSymlink(t *testing.T) {
	vaultPath := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(vaultPath, "global")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "index.md")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if err := createVaultIndexPage(vaultPath, "global/index.md", Frontmatter{Title: "全局知识", Scope: "global", Type: TypeNavigation, Status: "verified", IsIndex: boolPointer(true)}); err != nil {
		t.Fatalf("createVaultIndexPage returned %v", err)
	}
	if content, err := os.ReadFile(outside); err == nil && string(content) != "outside" {
		t.Fatalf("symlink target was overwritten: %s", content)
	}
	info, err := os.Lstat(filepath.Join(target, "index.md"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("existing symlink was replaced: %v %+v", err, info)
	}
}
