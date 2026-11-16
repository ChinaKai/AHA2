package knowledgevault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeVaultTestFile(t *testing.T, vaultPath, relative, content string) {
	t.Helper()
	target := filepath.Join(vaultPath, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRebuildDerivesManifestLibrariesAndSkills(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	writeVaultTestFile(t, config.VaultPath, "projects/manual-project/index.md", `---
id: knowledge_root_project_manual
title: 手工项目
project_id: project-manual
type: navigation
status: verified
is_index: true
---
手工项目入口。
`)
	writeVaultTestFile(t, config.VaultPath, "global/skills/demo/SKILL.md", "---\nname: Demo\n---\n\n# Demo\n")
	writeVaultTestFile(t, config.VaultPath, "projects/manual-project/skills/tool/SKILL.md", "---\nname: Tool\n---\n\n# Tool\n")
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects["project-demo"] != "demo-project" || manifest.Projects["project-manual"] != "manual-project" {
		t.Fatalf("projects = %#v", manifest.Projects)
	}
	if manifest.ProjectNames["project-manual"] != "手工项目" {
		t.Fatalf("project names = %#v", manifest.ProjectNames)
	}
	if manifest.Archives["project-archive"] != "aha1-archive" {
		t.Fatalf("archives = %#v", manifest.Archives)
	}
	if manifest.Skills["demo"] != (SkillLocation{Scope: "global"}) {
		t.Fatalf("global skill = %#v", manifest.Skills["demo"])
	}
	if manifest.Skills["tool"] != (SkillLocation{Scope: "project", ProjectID: "project-manual"}) {
		t.Fatalf("project skill = %#v", manifest.Skills["tool"])
	}

	before, err := os.ReadFile(filepath.Join(config.VaultPath, manifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(config.VaultPath, manifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("manifest is not idempotent:\n%s\n---\n%s", before, after)
	}
}

func TestRebuildCreatesAndMaintainsProjectsRootIndex(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	rootIndex := filepath.Join(config.VaultPath, "projects", "index.md")
	raw, err := os.ReadFile(rootIndex)
	if err != nil {
		t.Fatalf("projects/index.md was not created: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, indexBlockStart) || !strings.Contains(text, "[[projects/demo-project/index|Demo Project]]") {
		t.Fatalf("projects root index does not list the project entry:\n%s", text)
	}
	if strings.Contains(text, "projects/index|") {
		t.Fatalf("projects root index must not link itself:\n%s", text)
	}
	// The container page is navigation, not a project document.
	entries, err := index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Path == projectsRootIndexPath {
			t.Fatalf("projects root index leaked into the indexed documents: %+v", entry)
		}
	}

	// A second rebuild is byte-stable.
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(rootIndex)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, again) {
		t.Fatalf("projects root index is not idempotent:\n%s\n---\n%s", raw, again)
	}

	// Hand-written text outside the generated block survives, and the block is
	// refreshed when the project set changes.
	writeVaultTestFile(t, config.VaultPath, "projects/index.md", "---\ntitle: 项目导航\n---\n\n手写导语，不要覆盖。\n\n"+indexBlockStart+"\n"+indexBlockEnd+"\n")
	writeVaultTestFile(t, config.VaultPath, "projects/second-project/index.md", "---\nproject_id: project-second\ntitle: Second Project\ntype: navigation\nis_index: true\n---\n")
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	handWritten, err := os.ReadFile(rootIndex)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(handWritten), "手写导语，不要覆盖。") {
		t.Fatalf("hand-written intro was overwritten:\n%s", handWritten)
	}
	if !strings.Contains(string(handWritten), "[[projects/second-project/index|Second Project]]") {
		t.Fatalf("generated block was not refreshed for a new project:\n%s", handWritten)
	}
}

func TestRebuildCreatesAndMaintainsArchiveRootIndex(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(config.VaultPath, filepath.FromSlash(archiveRootIndexPath))
	raw, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatalf("archive/index.md was not created: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, indexBlockStart) || !strings.Contains(text, "[[archive/aha1-archive/index|AHA1 Archive]]") {
		t.Fatalf("archive root index does not list the archive entry:\n%s", text)
	}
	if strings.Contains(text, "[[archive/index|") {
		t.Fatalf("archive root index must not link itself:\n%s", text)
	}
	entries, err := index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if isContainerVaultIndexPath(config.VaultPath, entry.Path) {
			t.Fatalf("container index page leaked into the indexed documents: %+v", entry)
		}
	}

	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, again) {
		t.Fatalf("archive root index is not idempotent:\n%s\n---\n%s", raw, again)
	}

	// Hand-written text outside the generated block survives, and the block is
	// refreshed when the archive set changes.
	writeVaultTestFile(t, config.VaultPath, archiveRootIndexPath, "---\ntitle: 归档导航\n---\n\n手写归档导语，不要覆盖。\n\n"+indexBlockStart+"\n"+indexBlockEnd+"\n")
	writeVaultTestFile(t, config.VaultPath, "archive/second-archive/index.md", "---\nproject_id: project-second-archive\ntitle: Second Archive\ntype: navigation\nis_index: true\n---\n")
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	handWritten, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(handWritten), "手写归档导语，不要覆盖。") || !strings.Contains(string(handWritten), "title: 归档导航") {
		t.Fatalf("hand-written archive frontmatter/intro was overwritten:\n%s", handWritten)
	}
	if !strings.Contains(string(handWritten), "[[archive/second-archive/index|Second Archive]]") {
		t.Fatalf("generated archive block was not refreshed:\n%s", handWritten)
	}
}

func TestRebuildCreatesIndexPagesForEmptyDirectories(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(config.VaultPath, "projects", "demo-project", "empty-group", "nested-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(config.VaultPath, "archive", "aha1-archive", "empty-archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Archives["project-flat-new"] = "flat-new"
	manifest.ArchiveNames["project-flat-new"] = "Flat New"
	if err := writeManifest(config.VaultPath, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(config.VaultPath, "archive", "flat-new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	for relative, want := range map[string][2]string{
		"projects/demo-project/empty-group/index.md":              {"project", "project-demo"},
		"projects/demo-project/empty-group/nested-empty/index.md": {"project", "project-demo"},
		"archive/aha1-archive/empty-archive/index.md":             {"archive", "project-archive"},
		"archive/flat-new/index.md":                               {"archive", "project-flat-new"},
	} {
		raw, readErr := os.ReadFile(filepath.Join(config.VaultPath, filepath.FromSlash(relative)))
		if readErr != nil {
			t.Fatalf("%s was not created for an empty directory: %v", relative, readErr)
		}
		assertIndexFrontmatter(t, string(raw), want[0], want[1])
	}
	if raw := readExportedFile(t, config.VaultPath, "projects/demo-project/empty-group/index.md"); !strings.Contains(raw, "[[projects/demo-project/empty-group/nested-empty/index|Nested Empty]]") {
		t.Fatalf("empty directory page does not list its empty child:\n%s", raw)
	}
	if raw := readExportedFile(t, config.VaultPath, "projects/demo-project/index.md"); !strings.Contains(raw, "[[projects/demo-project/empty-group/index|Empty Group]]") {
		t.Fatalf("project root does not list the new empty directory:\n%s", raw)
	}
	if raw := readExportedFile(t, config.VaultPath, archiveRootIndexPath); !strings.Contains(raw, "[[archive/flat-new/index|Flat New]]") {
		t.Fatalf("archive root does not list the new empty archive:\n%s", raw)
	}
}

func TestRebuildCreatesIndexInNonProjectFolderUnderProjects(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	writeVaultTestFile(t, config.VaultPath, "projects/loose-folder/child/note.md", "# 随手目录\n\n没有 project 映射。\n")
	if err := os.MkdirAll(filepath.Join(config.VaultPath, "projects", "loose-empty", "empty-child"), 0o755); err != nil {
		t.Fatal(err)
	}
	report, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"projects/loose-folder/index.md",
		"projects/loose-folder/child/index.md",
		"projects/loose-empty/index.md",
		"projects/loose-empty/empty-child/index.md",
	} {
		raw, readErr := os.ReadFile(filepath.Join(config.VaultPath, filepath.FromSlash(relative)))
		if readErr != nil {
			t.Fatalf("%s was not created for a non-project folder: %v", relative, readErr)
		}
		assertIndexFrontmatter(t, string(raw), "project", "")
	}
	if raw := readExportedFile(t, config.VaultPath, "projects/index.md"); !strings.Contains(raw, "[[projects/loose-folder/index|Loose Folder]]") || !strings.Contains(raw, "[[projects/loose-empty/index|Loose Empty]]") {
		t.Fatalf("projects root does not list the non-project folders:\n%s", raw)
	}
	warned := map[string]bool{}
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "no project id") {
			for _, root := range []string{"projects/loose-folder", "projects/loose-empty"} {
				if strings.Contains(warning, root) {
					warned[root] = true
				}
			}
		}
	}
	for _, root := range []string{"projects/loose-folder", "projects/loose-empty"} {
		if !warned[root] {
			t.Fatalf("unattributable folder %s was not reported: %v", root, report.Warnings)
		}
	}
}

func TestRebuildMaintainsNestedArchiveContainerIndex(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	writeVaultTestFile(t, config.VaultPath, "archive/projects/old-lib/index.md", "---\nproject_id: project-old\ntitle: Old Lib\ntype: navigation\nis_index: true\n---\n")
	writeVaultTestFile(t, config.VaultPath, "archive/projects/old-lib/note.md", "# 旧库\r\n\r\n正文。\r\n")
	report, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if raw := readExportedFile(t, config.VaultPath, archiveProjectsIndexPath); !strings.Contains(raw, "[[archive/projects/old-lib/index|Old Lib]]") {
		t.Fatalf("archive/projects container does not list the nested archive:\n%s", raw)
	}
	if raw := readExportedFile(t, config.VaultPath, archiveRootIndexPath); !strings.Contains(raw, "[[archive/projects/old-lib/index|Old Lib]]") {
		t.Fatalf("archive root does not list the nested archive:\n%s", raw)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Archives["project-old"] != "projects/old-lib" {
		t.Fatalf("nested archive binding = %#v", manifest.Archives)
	}
	for projectID, slug := range manifest.Archives {
		if slug == "projects" {
			t.Fatalf("container page was bound as archive library (%s)", projectID)
		}
	}
	entries, err := index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if isContainerVaultIndexPath(config.VaultPath, entry.Path) {
			t.Fatalf("container page leaked into the index: %+v", entry)
		}
	}
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "archive/projects") && strings.Contains(warning, "unbound") {
			t.Fatalf("container page was reported unbound: %v", report.Warnings)
		}
	}
}

func TestMaintainProjectsRootIndexSkipsMissingProjectsDirectory(t *testing.T) {
	vaultPath := t.TempDir()
	warnings := maintainProjectsRootIndex(vaultPath, Manifest{})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if _, err := os.Stat(filepath.Join(vaultPath, "projects", "index.md")); !os.IsNotExist(err) {
		t.Fatalf("projects/index.md was created without a projects directory: %v", err)
	}
}

func TestRebuildNeverStoresKnowledgeHomeAsProjectName(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	// The database names are authoritative on export; a rebuild must not
	// overwrite them with the generated root index title (知识首页).
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ProjectNames["project-demo"] != "Demo Project" || manifest.ArchiveNames["project-archive"] != "AHA1 Archive" {
		t.Fatalf("rebuild replaced exported project names: %#v / %#v", manifest.ProjectNames, manifest.ArchiveNames)
	}

	// Simulate an install written by the old generation logic, where every
	// project_name was the knowledge-home page title. A rebuild repairs the
	// cache to the stable slug instead of writing 知识首页 again.
	manifest.ProjectNames["project-demo"] = "知识首页"
	manifest.ArchiveNames["project-archive"] = "全局知识首页"
	if err := writeManifest(config.VaultPath, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	repaired, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.ProjectNames["project-demo"] != "demo-project" || repaired.ArchiveNames["project-archive"] != "aha1-archive" {
		t.Fatalf("repaired project names = %#v, want the vault slugs", repaired.ProjectNames)
	}
	for id, name := range repaired.ProjectNames {
		if name == "知识首页" || name == "全局知识首页" {
			t.Fatalf("project %s still carries the knowledge-home title", id)
		}
	}
	for id, name := range repaired.ArchiveNames {
		if name == "知识首页" || name == "全局知识首页" {
			t.Fatalf("archive %s still carries the knowledge-home title", id)
		}
	}

	before, err := os.ReadFile(filepath.Join(config.VaultPath, manifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(config.VaultPath, manifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("repaired manifest is not idempotent:\n%s\n---\n%s", before, after)
	}
}

func TestResolveLibraryNameRejectsKnowledgeHomeTitles(t *testing.T) {
	cases := []struct {
		name     string
		existing string
		title    string
		slug     string
		id       string
		want     string
	}{
		{"exported name wins over the generated index title", "AHA2", "知识首页", "aha2", "project-6ff866", "AHA2"},
		{"generated index title is replaced by the slug", "知识首页", "知识首页", "aha2", "project-6ff866", "aha2"},
		{"global home title is replaced by the slug", "全局知识首页", "知识首页", "bu2-cam1", "project-5ac7", "bu2-cam1"},
		{"hand-authored project title is kept", "", "手工项目", "manual-project", "project-manual", "手工项目"},
		{"missing everything falls back to the project id", "", "", "", "project-x", "project-x"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := resolveLibraryName(testCase.existing, testCase.title, testCase.slug, testCase.id); got != testCase.want {
				t.Fatalf("resolveLibraryName(%q, %q, %q, %q) = %q, want %q",
					testCase.existing, testCase.title, testCase.slug, testCase.id, got, testCase.want)
			}
		})
	}
}

func TestRebuildMarksUnboundProjectWithoutDroppingFiles(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	writeVaultTestFile(t, config.VaultPath, "projects/unbound/index.md", "---\ntitle: 未绑定\nis_index: true\n---\n\n未绑定目录。\n")
	report, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range manifest.Projects {
		if slug == "unbound" {
			t.Fatalf("unbound directory was mapped: %#v", manifest.Projects)
		}
	}
	found := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "projects/unbound: unbound") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing unbound warning: %v", report.Warnings)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "projects", "unbound", "index.md")); err != nil {
		t.Fatalf("unbound file was removed: %v", err)
	}
}

func TestRebuildMaintainsExistingIndexPages(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	groupRelative := "projects/demo-project/navigation-group/index.md"
	original := readExportedFile(t, config.VaultPath, groupRelative)
	marker := strings.Index(original, indexBlockStart)
	if marker < 0 {
		t.Fatalf("seed index has no generated block: %s", original)
	}
	handWritten := "手工简介：先读入口。\n\n"
	originalHead := original[:marker]
	withIntro := originalHead + handWritten + original[marker:]
	writeVaultTestFile(t, config.VaultPath, groupRelative, withIntro)

	writeVaultTestFile(t, config.VaultPath, "projects/demo-project/navigation-group/topic/index.md", `---
title: 主题目录
type: navigation
is_index: true
---
主题入口。

`+indexBlockStart+`
`+indexBlockEnd+`
`)
	writeVaultTestFile(t, config.VaultPath, "projects/demo-project/navigation-group/topic/leaf.md", "# 主题叶子\n\n叶子正文。\n")
	writeVaultTestFile(t, config.VaultPath, "projects/demo-project/navigation-group/added.md", "# 新增文档\n\n新增正文。\n")

	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	group := readExportedFile(t, config.VaultPath, groupRelative)
	if !strings.HasPrefix(group, originalHead+handWritten) {
		t.Fatalf("frontmatter or hand-written intro was lost:\n%s", group)
	}
	topicLine := "- [[projects/demo-project/navigation-group/topic/index|主题目录]]"
	addedLine := "- [[projects/demo-project/navigation-group/added|新增文档]]"
	if !strings.Contains(group, topicLine) || !strings.Contains(group, addedLine) {
		t.Fatalf("new children missing from index:\n%s", group)
	}
	if strings.Index(group, topicLine) > strings.Index(group, addedLine) {
		t.Fatalf("subdirectory was not listed before documents:\n%s", group)
	}
	if strings.Contains(group, "topic/leaf") {
		t.Fatalf("nested leaf leaked into the parent index:\n%s", group)
	}
	topic := readExportedFile(t, config.VaultPath, "projects/demo-project/navigation-group/topic/index.md")
	if !strings.Contains(topic, "- [[projects/demo-project/navigation-group/topic/leaf|主题叶子]]") {
		t.Fatalf("nested index was not maintained:\n%s", topic)
	}

	// Repeated rebuilds must be byte-stable.
	before := exportedIndexPages(t, config.VaultPath)
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if after := exportedIndexPages(t, config.VaultPath); !bytes.Equal(before, after) {
		t.Fatalf("index pages changed on a repeated rebuild")
	}

	// Move the document to the nested topic; the old page must drop it.
	leafPath := filepath.Join(config.VaultPath, "projects", "demo-project", "navigation-group", "added.md")
	if err := os.Rename(leafPath, filepath.Join(config.VaultPath, "projects", "demo-project", "navigation-group", "topic", "added.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	group = readExportedFile(t, config.VaultPath, groupRelative)
	if strings.Contains(group, "added|新增文档") {
		t.Fatalf("moved document remained in the parent index:\n%s", group)
	}
	topic = readExportedFile(t, config.VaultPath, "projects/demo-project/navigation-group/topic/index.md")
	if !strings.Contains(topic, "added|新增文档") {
		t.Fatalf("moved document missing from the nested index:\n%s", topic)
	}

	// Delete it again; the nested page must update as well.
	if err := os.Remove(filepath.Join(config.VaultPath, "projects", "demo-project", "navigation-group", "topic", "added.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	topic = readExportedFile(t, config.VaultPath, "projects/demo-project/navigation-group/topic/index.md")
	if strings.Contains(topic, "added|新增文档") {
		t.Fatalf("deleted document remained in the nested index:\n%s", topic)
	}
}

func TestRebuildLeavesIndexPagesWithoutMarkersAlone(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	relative := "projects/demo-project/no-marker/index.md"
	body := "---\ntitle: 无 marker\ntype: navigation\nis_index: true\n---\n\n手写内容。\n"
	writeVaultTestFile(t, config.VaultPath, relative, body)
	writeVaultTestFile(t, config.VaultPath, "projects/demo-project/no-marker/child.md", "# 子文档\n")
	report, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := readExportedFile(t, config.VaultPath, relative); got != body {
		t.Fatalf("marker-less index was overwritten:\n%s", got)
	}
	found := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, relative+": generated index markers are missing") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing marker warning: %v", report.Warnings)
	}
}

func TestRebuildHandlesEmptyMalformedAndNestedIndexes(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	emptyRelative := "projects/demo-project/empty/index.md"
	writeVaultTestFile(t, config.VaultPath, emptyRelative, `---
title: 空目录
is_index: true
---
`+indexBlockStart+"\nstale\n"+indexBlockEnd+"\n")
	badRelative := "projects/demo-project/broken/index.md"
	writeVaultTestFile(t, config.VaultPath, badRelative, `---
id: [broken
---
# 坏索引
`+indexBlockStart+"\n"+indexBlockEnd+"\n")
	writeVaultTestFile(t, config.VaultPath, "projects/demo-project/broken/child.md", "# 子文档\n")
	report, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatalf("rebuild rejected malformed index: %v", err)
	}
	if !strings.Contains(readExportedFile(t, config.VaultPath, emptyRelative), "_暂无文档。_") {
		t.Fatalf("empty index was not maintained: %s", readExportedFile(t, config.VaultPath, emptyRelative))
	}
	if !strings.Contains(readExportedFile(t, config.VaultPath, badRelative), "broken/child|子文档") {
		t.Fatalf("malformed index was not maintained: %s", readExportedFile(t, config.VaultPath, badRelative))
	}
	found := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "invalid frontmatter") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing malformed frontmatter warning: %v", report.Warnings)
	}
}

func exportedIndexPages(t *testing.T, vaultPath string) []byte {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(vaultPath, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "index.md" {
			return nil
		}
		paths = append(paths, filePath)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	buffer := &bytes.Buffer{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		relative, _ := filepath.Rel(vaultPath, path)
		buffer.WriteString(filepath.ToSlash(relative))
		buffer.WriteByte('\n')
		buffer.Write(data)
		buffer.WriteByte('\n')
	}
	return buffer.Bytes()
}

func TestRebuildCreatesMissingManifest(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	manifestPath := filepath.Join(config.VaultPath, manifestFileName)
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != ManifestVersion || manifest.Projects["project-demo"] != "demo-project" {
		t.Fatalf("generated manifest = %#v", manifest)
	}
}

func TestReadGuideMissingVaultReportsNotFoundNotEscape(t *testing.T) {
	_, err := ReadGuide(t.TempDir())
	if !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("ReadGuide on a vault without a guide = %v, want ErrGuideNotFound", err)
	}
	if strings.Contains(err.Error(), "escapes") {
		t.Fatalf("a missing guide was reported as a path escape: %v", err)
	}
}

func TestEnsureGuideBackfillsOnceAndKeepsHandWrittenGuide(t *testing.T) {
	vaultPath := t.TempDir()
	created, err := EnsureGuide(vaultPath, time.Now())
	if err != nil || !created {
		t.Fatalf("EnsureGuide on an empty vault = (%v, %v), want created", created, err)
	}
	guidePath := filepath.Join(vaultPath, filepath.FromSlash(GuideRelativePath))
	canonical, err := os.ReadFile(guidePath)
	if err != nil {
		t.Fatal(err)
	}
	created, err = EnsureGuide(vaultPath, time.Now())
	if err != nil || created {
		t.Fatalf("second EnsureGuide = (%v, %v), want an idempotent no-op", created, err)
	}
	if after, err := os.ReadFile(guidePath); err != nil || !bytes.Equal(after, canonical) {
		t.Fatalf("second EnsureGuide changed the guide: err=%v", err)
	}

	custom := "---\ntitle: 手写指南\n---\n\n自定义正文。\n"
	if err := os.WriteFile(guidePath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err = EnsureGuide(vaultPath, time.Now())
	if err != nil || created {
		t.Fatalf("EnsureGuide over a hand-written guide = (%v, %v), want a no-op", created, err)
	}
	if after, err := os.ReadFile(guidePath); err != nil || string(after) != custom {
		t.Fatalf("EnsureGuide overwrote the hand-written guide: %q err=%v", after, err)
	}
}

func TestRepairGuideEndpointsRewritesOnlyRetiredEndpoints(t *testing.T) {
	vaultPath := t.TempDir()
	target := filepath.Join(vaultPath, filepath.FromSlash(GuideRelativePath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	old := "---\ntitle: 手写指南\n---\n\n手写导语必须保留。\n\n" +
		"- `POST /api/v1/agent/knowledge/candidates`：提交候选，每批 1–20 条。\n" +
		"- `POST /api/v1/agent/knowledge/{id}/feedback`：提交 `stale`、`wrong` 或 `helped`。\n" +
		"- API：POST /api/v1/agent/knowledge/candidates（1–20 条，标题 ≤300、正文 ≤20000 字符）、POST /api/v1/agent/knowledge/{id}/feedback。\n"
	if err := os.WriteFile(target, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := RepairGuideEndpoints(vaultPath)
	if err != nil || !changed {
		t.Fatalf("RepairGuideEndpoints = (%v, %v), want changed", changed, err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	repaired := string(got)
	for _, retired := range []string{
		"/api/v1/agent/knowledge/candidates", "/api/v1/agent/knowledge/{id}/feedback", "提交候选，每批",
	} {
		if strings.Contains(repaired, retired) {
			t.Fatalf("repaired guide still contains %q:\n%s", retired, repaired)
		}
	}
	for _, want := range []string{
		"手写导语必须保留。", "/api/v1/agent/knowledge/entries", "/api/v1/agent/knowledge/feedback",
		"mode=create|revise", "`entry_id` + `kind`",
	} {
		if !strings.Contains(repaired, want) {
			t.Fatalf("repaired guide is missing %q:\n%s", want, repaired)
		}
	}

	changed, err = RepairGuideEndpoints(vaultPath)
	if err != nil || changed {
		t.Fatalf("second RepairGuideEndpoints = (%v, %v), want an idempotent no-op", changed, err)
	}
	if after, err := os.ReadFile(target); err != nil || string(after) != repaired {
		t.Fatalf("second RepairGuideEndpoints changed the guide: err=%v", err)
	}

	if changed, err := RepairGuideEndpoints(t.TempDir()); err != nil || changed {
		t.Fatalf("RepairGuideEndpoints on a vault without a guide = (%v, %v), want a no-op", changed, err)
	}
}

func TestReadGuideAndBootstrapDoesNotOverwrite(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Guide Vault"}); err != nil {
		t.Fatal(err)
	}
	guide, err := ReadGuide(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if guide.Path != GuideRelativePath || guide.Title != GuideTitle || !strings.Contains(guide.Prompt, "先搜索再写") {
		t.Fatalf("guide = %#v", guide)
	}
	for _, expected := range []string{"global/", "projects/", "navigation", "decision", "practice", "diagnostic", "reference", "architecture", "inbox", "stale", "wrong", "helped", "禁止", "/api/v1/agent/knowledge/guide", "/api/v1/agent/knowledge/entries", "/api/v1/agent/knowledge/feedback"} {
		if !strings.Contains(guide.Content, expected) {
			t.Fatalf("guide is missing %q", expected)
		}
	}
	for _, retired := range []string{"/api/v1/agent/knowledge/candidates", "/api/v1/agent/knowledge/{id}/feedback"} {
		if strings.Contains(guide.Content, retired) || strings.Contains(guide.Prompt, retired) {
			t.Fatalf("default guide still teaches the retired endpoint %q", retired)
		}
	}
	custom := "---\ntitle: 自定义指南\n---\n\n自定义正文。\n"
	target := filepath.Join(config.VaultPath, filepath.FromSlash(GuideRelativePath))
	if err := os.WriteFile(target, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(config, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != custom {
		t.Fatalf("bootstrap overwrote the guide: %s", got)
	}
}

func TestReaderSkillFilesAndGuide(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	writeVaultTestFile(t, config.VaultPath, "global/skills/demo/SKILL.md", "---\nname: Demo\n---\n\n# Demo\n")
	writeVaultTestFile(t, config.VaultPath, "global/skills/demo/scripts/run.sh", "#!/bin/sh\necho demo\n")
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReader(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	files, err := reader.SkillFiles("global/skills/demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "SKILL.md" || files[1].Path != "scripts/run.sh" {
		t.Fatalf("skill files = %#v", files)
	}
	if _, err := reader.SkillFiles("global/skills/missing"); err == nil {
		t.Fatal("missing skill package was accepted")
	}
	guide, err := reader.Guide()
	if err != nil || guide.Path != GuideRelativePath {
		t.Fatalf("reader guide = %#v err=%v", guide, err)
	}
}
