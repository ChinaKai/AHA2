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
