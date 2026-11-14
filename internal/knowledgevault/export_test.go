package knowledgevault

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestExportWritesNavigableVault(t *testing.T) {
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	execTestSQL(t, databasePath, `UPDATE knowledge_entries SET status='candidate', source_task_id='task-42', source_turn_id='turn-7', confidence=0.42 WHERE scope='project' AND project_id='project-demo' AND slug='module-b'`)
	config := DefaultConfig(t.TempDir())

	report, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 16 || report.Global != 8 || report.Projects != 6 || report.Archives != 2 {
		t.Fatalf("unexpected counts: %+v", report)
	}
	if report.ProjectInbox != 2 || report.Groups != 5 {
		t.Fatalf("unexpected layout counts: %+v", report)
	}
	if report.Written == 0 {
		t.Fatal("expected files to be written")
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", report.Warnings)
	}

	for _, relative := range []string{
		"global/index.md",
		"global/general/index.md",
		"global/general/shared-deploy.md",
		"global/agent-lessons/index.md",
		"global/agent-lessons/technical-diagnostics/index.md",
		"global/agent-lessons/technical-diagnostics/serial-login.md",
		"global/agent-lessons/behavior-lessons/index.md",
		"global/agent-lessons/behavior-lessons/verify-first.md",
		"projects/demo-project/index.md",
		"projects/demo-project/navigation-group/index.md",
		"projects/demo-project/navigation-group/module-a.md",
		"projects/demo-project/navigation-group/module-b.md",
		"projects/demo-project/inbox/inbox-one.md",
		"projects/demo-project/inbox/inbox-two.md",
		"archive/aha1-archive/index.md",
		"archive/aha1-archive/inbox/legacy-note.md",
	} {
		document := ParseDocument(readExportedFile(t, config.VaultPath, relative))
		if document.Warning != "" {
			t.Errorf("%s: %s", relative, document.Warning)
			continue
		}
		if document.Frontmatter.ID == "" || document.Frontmatter.Slug == "" || document.Frontmatter.Type == "" || document.Frontmatter.Status == "" {
			t.Errorf("%s lost identity fields: %+v", relative, document.Frontmatter)
		}
	}

	groupID := ""
	{
		group := ParseDocument(readExportedFile(t, config.VaultPath, "projects/demo-project/navigation-group/index.md"))
		groupID = group.Frontmatter.ID
		if group.Frontmatter.Type != TypeNavigation || group.Frontmatter.Parent != "knowledge_root_project_project-demo" {
			t.Fatalf("group frontmatter = %+v", group.Frontmatter)
		}
		if group.Frontmatter.Scope != "project" || group.Frontmatter.ProjectID != "project-demo" {
			t.Fatalf("group project = %+v", group.Frontmatter)
		}
		if !strings.Contains(group.Body, "模块入口。") {
			t.Fatalf("group body lost: %q", group.Body)
		}
		if !strings.Contains(group.Body, "[[projects/demo-project/navigation-group/module-a|模块 A]]") {
			t.Fatalf("generated navigation block missing: %q", group.Body)
		}
		if !strings.Contains(group.Body, "[[projects/demo-project/navigation-group/module-b|模块 B 决策]]") {
			t.Fatalf("generated navigation block missing module-b: %q", group.Body)
		}
	}

	module := ParseDocument(readExportedFile(t, config.VaultPath, "projects/demo-project/navigation-group/module-a.md"))
	if module.Frontmatter.Parent != groupID {
		t.Fatalf("module parent = %q, want %q", module.Frontmatter.Parent, groupID)
	}
	if module.Frontmatter.Type != TypeReference || module.Frontmatter.Slug != "module-a" {
		t.Fatalf("module frontmatter = %+v", module.Frontmatter)
	}
	if !strings.Contains(module.Body, "模块 A 的说明。") {
		t.Fatalf("module body = %q", module.Body)
	}
	decision := ParseDocument(readExportedFile(t, config.VaultPath, "projects/demo-project/navigation-group/module-b.md"))
	if decision.Frontmatter.Status != "candidate" || decision.Frontmatter.SourceTask != "task-42" || decision.Frontmatter.SourceTurn != "turn-7" {
		t.Fatalf("module-b metadata = %+v", decision.Frontmatter)
	}
	if decision.Frontmatter.Confidence == nil || *decision.Frontmatter.Confidence != 0.42 {
		t.Fatalf("module-b confidence = %#v", decision.Frontmatter.Confidence)
	}

	inbox := ParseDocument(readExportedFile(t, config.VaultPath, "projects/demo-project/inbox/inbox-one.md"))
	if inbox.Frontmatter.Parent != "knowledge_root_project_project-demo" {
		t.Fatalf("inbox leaf parent = %q", inbox.Frontmatter.Parent)
	}
	if !strings.Contains(inbox.Body, "根下叶子一。") {
		t.Fatalf("inbox body = %q", inbox.Body)
	}

	archive := ParseDocument(readExportedFile(t, config.VaultPath, "archive/aha1-archive/inbox/legacy-note.md"))
	if archive.Frontmatter.Scope != "archive" || archive.Frontmatter.Type != TypeReference || archive.Frontmatter.LegacyType != "general" {
		t.Fatalf("archive frontmatter = %+v", archive.Frontmatter)
	}
	if archive.Frontmatter.ProjectID != "project-archive" {
		t.Fatalf("archive project = %q", archive.Frontmatter.ProjectID)
	}
	if !strings.Contains(archive.Body, "AHA1 归档内容。") {
		t.Fatalf("archive body = %q", archive.Body)
	}

	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects["project-demo"] != "demo-project" {
		t.Fatalf("project slug map = %#v", manifest.Projects)
	}
	if manifest.Archives["project-archive"] != "aha1-archive" {
		t.Fatalf("archive slug map = %#v", manifest.Archives)
	}
	if manifest.ProjectNames["project-demo"] != "Demo Project" {
		t.Fatalf("project name map = %#v", manifest.ProjectNames)
	}
	if manifest.Export == nil || manifest.Export.Total != 16 || manifest.Export.ProjectInbox != 2 {
		t.Fatalf("manifest export summary = %+v", manifest.Export)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "projects", "demo-project", "skills", ".gitkeep")); err != nil {
		t.Fatalf("skills placeholder missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, ".git")); err == nil {
		t.Fatal("export must not initialize git when Commit is false")
	}
}

func TestExportKeepsEmptyGlobalCategoriesAsFolders(t *testing.T) {
	database, databasePath := openTestStore(t)
	if err := database.EnsureGlobalKnowledgeCategories(context.Background()); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig(t.TempDir())
	report, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false})
	if err != nil {
		t.Fatal(err)
	}
	if report.Global != 5 || report.Total != 5 || report.Groups != 4 {
		t.Fatalf("global counts = %+v", report)
	}
	for _, relative := range []string{
		"global/index.md",
		"global/general/index.md",
		"global/agent-lessons/index.md",
		"global/agent-lessons/technical-diagnostics/index.md",
		"global/agent-lessons/behavior-lessons/index.md",
	} {
		document := ParseDocument(readExportedFile(t, config.VaultPath, relative))
		if document.Frontmatter.Type != TypeNavigation {
			t.Errorf("%s type = %q", relative, document.Frontmatter.Type)
		}
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "global", "inbox")); err != nil {
		t.Fatalf("global inbox missing: %v", err)
	}
}

func TestExportIsIdempotent(t *testing.T) {
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	config := DefaultConfig(t.TempDir())

	if _, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	firstHash := exportedTreeHash(t, config.VaultPath)
	manifestBefore, err := os.ReadFile(filepath.Join(config.VaultPath, manifestFileName))
	if err != nil {
		t.Fatal(err)
	}

	report, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false})
	if err != nil {
		t.Fatal(err)
	}
	if report.Written != 0 || report.Removed != 0 {
		t.Fatalf("second export was not a no-op: %+v", report)
	}
	if secondHash := exportedTreeHash(t, config.VaultPath); secondHash != firstHash {
		t.Fatal("second export changed vault files")
	}
	manifestAfter, err := os.ReadFile(filepath.Join(config.VaultPath, manifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifestBefore) != string(manifestAfter) {
		t.Fatalf("second export changed the manifest:\n%s\n---\n%s", manifestBefore, manifestAfter)
	}
}

func TestExportPrunesRemovedDocuments(t *testing.T) {
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	config := DefaultConfig(t.TempDir())
	if _, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(config.VaultPath, "projects", "demo-project", "inbox", "inbox-two.md")
	if _, err := os.Stat(leaf); err != nil {
		t.Fatal(err)
	}
	execTestSQL(t, databasePath, `DELETE FROM knowledge_entries WHERE scope='project' AND project_id='project-demo' AND slug='inbox-two'`)

	report, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false})
	if err != nil {
		t.Fatal(err)
	}
	if report.Removed != 1 {
		t.Fatalf("removed = %d, want 1", report.Removed)
	}
	if _, err := os.Stat(leaf); !os.IsNotExist(err) {
		t.Fatalf("removed export was not pruned: %v", err)
	}
}

func TestExportRefusesToWriteThroughSymlinks(t *testing.T) {
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	config := DefaultConfig(t.TempDir())
	if _, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.md")
	if err := os.WriteFile(victim, []byte("do not touch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(config.VaultPath, "projects", "demo-project", "inbox", "inbox-one.md")
	if err := os.Remove(leaf); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, leaf); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	if _, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false}); err == nil {
		t.Fatal("export overwrote a symlink that points outside the vault")
	}
	content, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "do not touch\n" {
		t.Fatalf("file outside the vault was modified: %q", content)
	}
}

func TestExportRefusesSymlinkedParentDirectory(t *testing.T) {
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	config := DefaultConfig(t.TempDir())
	if _, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	inbox := filepath.Join(config.VaultPath, "projects", "demo-project", "inbox")
	if err := os.RemoveAll(inbox); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, inbox); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	if _, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false}); err == nil {
		t.Fatal("export wrote through a symlinked directory outside the vault")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("files escaped the vault: %v", entries)
	}
}

func TestExportHandlesAcceptanceScale(t *testing.T) {
	database, databasePath := openTestStore(t)
	ctx := context.Background()
	if err := database.EnsureGlobalKnowledgeCategories(ctx); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 216; index++ {
		createTestKnowledge(t, database, testKnowledge{
			Scope: "global", ParentID: "knowledge_global_general", Slug: fmt.Sprintf("global-leaf-%03d", index),
			Type: "reference", Title: fmt.Sprintf("全局条目 %03d", index),
			Body: fmt.Sprintf("acceptance-marker global %03d", index),
		})
	}
	createTestProject(t, database, "project-scale", "Scale Project", "folder")
	root, err := database.EnsureKnowledgeRoot(ctx, "project", "project-scale")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 575; index++ {
		createTestKnowledge(t, database, testKnowledge{
			Scope: "project", ProjectID: "project-scale", ParentID: root.ID,
			Slug: fmt.Sprintf("leaf-%03d", index), Type: "practice",
			Title: fmt.Sprintf("收件箱条目 %03d", index),
			Body:  fmt.Sprintf("acceptance-marker project %03d", index),
		})
	}

	config := DefaultConfig(t.TempDir())
	report, err := Export(ctx, config, databasePath, ExportOptions{Commit: false})
	if err != nil {
		t.Fatal(err)
	}
	if report.Global != 221 {
		t.Fatalf("global = %d, want 221", report.Global)
	}
	if report.Projects != 576 {
		t.Fatalf("projects = %d, want 576", report.Projects)
	}
	if report.ProjectInbox != 575 {
		t.Fatalf("project inbox = %d, want 575", report.ProjectInbox)
	}
	if report.Total != 797 {
		t.Fatalf("total = %d, want 797", report.Total)
	}
	for _, relative := range []string{"projects/scale-project/inbox/leaf-000.md", "projects/scale-project/inbox/leaf-574.md"} {
		if _, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("missing %s: %v", relative, err)
		}
	}

	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	rebuild, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if rebuild.Entries != 797 || rebuild.Global != 221 || rebuild.Projects != 576 || rebuild.Archives != 0 {
		t.Fatalf("rebuild counts = %+v", rebuild)
	}
	if rebuild.Warnings != nil {
		t.Fatalf("unexpected rebuild warnings: %v", rebuild.Warnings)
	}
	results, err := index.Search(ctx, "acceptance-marker", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Fatalf("search results = %d, want 5", len(results))
	}
	scoped, err := index.Search(ctx, "acceptance-marker", SearchOptions{Scope: "project", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 10 {
		t.Fatalf("project search results = %d, want 10", len(scoped))
	}
	for _, item := range scoped {
		if item.Scope != "project" || item.ProjectID != "project-scale" {
			t.Fatalf("scope filter leaked: %+v", item)
		}
	}
}

func TestExportUsesStableProjectSlugForNonASCIIProject(t *testing.T) {
	database, databasePath := openTestStore(t)
	ctx := context.Background()
	if err := database.EnsureGlobalKnowledgeCategories(ctx); err != nil {
		t.Fatal(err)
	}
	createTestProject(t, database, "project-cn", "中文项目", "folder")
	root, err := database.EnsureKnowledgeRoot(ctx, "project", "project-cn")
	if err != nil {
		t.Fatal(err)
	}
	createTestKnowledge(t, database, testKnowledge{
		Scope: "project", ProjectID: "project-cn", ParentID: root.ID,
		Slug: "first-note", Type: "practice", Title: "第一条", Body: "正文。",
	})
	config := DefaultConfig(t.TempDir())
	if _, err := Export(ctx, config, databasePath, ExportOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Projects["project-cn"] != "project-cn" {
		t.Fatalf("project slug = %#v", manifest.Projects)
	}
	if manifest.ProjectNames["project-cn"] != "中文项目" {
		t.Fatalf("project name = %#v", manifest.ProjectNames)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "projects", "project-cn", "inbox", "first-note.md")); err != nil {
		t.Fatalf("project file missing: %v", err)
	}
	execTestSQL(t, databasePath, `UPDATE projects SET name='改名的项目' WHERE id='project-cn'`)
	if _, err := Export(ctx, config, databasePath, ExportOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	renamed, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Projects["project-cn"] != "project-cn" || renamed.ProjectNames["project-cn"] != "改名的项目" {
		t.Fatalf("project slug was not stable across rename: %#v %#v", renamed.Projects, renamed.ProjectNames)
	}
}

func TestExportNeutralizesUnsafeSlugs(t *testing.T) {
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	ctx := context.Background()
	root, err := database.Knowledge(ctx, "knowledge_root_project_project-demo")
	if err != nil {
		t.Fatal(err)
	}
	unsafe := []string{"../escape", "/etc/passwd", "CON", "a:b"}
	for index, slug := range unsafe {
		entry := createTestKnowledge(t, database, testKnowledge{
			Scope: "project", ProjectID: "project-demo", ParentID: root.ID,
			Slug: fmt.Sprintf("placeholder-%d", index), Type: "reference",
			Title: fmt.Sprintf("Unsafe %d", index), Body: "unsafe body",
		})
		execTestSQL(t, databasePath, `UPDATE knowledge_entries SET slug=? WHERE id=?`, slug, entry.ID)
	}
	config := DefaultConfig(t.TempDir())
	report, err := Export(ctx, config, databasePath, ExportOptions{Commit: false})
	if err != nil {
		t.Fatalf("export rejected unsafe slug instead of neutralizing it: %v", err)
	}
	if report.Total != 20 {
		t.Fatalf("total = %d, want 20", report.Total)
	}

	for _, relative := range []string{
		"projects/demo-project/inbox/-escape.md",
		"projects/demo-project/inbox/-etc-passwd.md",
		"projects/demo-project/inbox/_CON.md",
		"projects/demo-project/inbox/a-b.md",
	} {
		if _, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(relative))); err != nil {
			t.Errorf("expected neutralized path %s: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "..", "escape.md")); !os.IsNotExist(err) {
		t.Fatalf("vault escape file exists: %v", err)
	}
	err = filepath.WalkDir(config.VaultPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, relErr := filepath.Rel(config.VaultPath, path)
		if relErr != nil {
			return relErr
		}
		if relErr := ValidateRelativePath(filepath.ToSlash(relative)); relErr != nil {
			return fmt.Errorf("%s: %w", relative, relErr)
		}
		if !IsInsideRoot(config.VaultPath, path) {
			return fmt.Errorf("%s escapes the vault root", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExportResolvesCaseInsensitiveCollisions(t *testing.T) {
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	ctx := context.Background()
	createTestProject(t, database, "project-case", "Case Project", "folder")
	root, err := database.EnsureKnowledgeRoot(ctx, "project", "project-case")
	if err != nil {
		t.Fatal(err)
	}
	upper := createTestKnowledge(t, database, testKnowledge{
		Scope: "project", ProjectID: "project-case", ParentID: root.ID,
		Slug: "case-doc-upper", Type: "reference", Title: "Upper", Body: "upper",
	})
	lower := createTestKnowledge(t, database, testKnowledge{
		Scope: "project", ProjectID: "project-case", ParentID: root.ID,
		Slug: "case-doc-lower", Type: "reference", Title: "Lower", Body: "lower",
	})
	execTestSQL(t, databasePath, `UPDATE knowledge_entries SET slug='CaseDoc' WHERE id=?`, upper.ID)
	execTestSQL(t, databasePath, `UPDATE knowledge_entries SET slug='casedoc' WHERE id=?`, lower.ID)

	config := DefaultConfig(t.TempDir())
	report, err := Export(ctx, config, databasePath, ExportOptions{Commit: false})
	if err != nil {
		t.Fatal(err)
	}
	foundCollision := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "case-insensitive path collision") {
			foundCollision = true
		}
	}
	if !foundCollision {
		t.Fatalf("no collision warning: %v", report.Warnings)
	}
	directory := filepath.Join(config.VaultPath, "projects", "case-project", "inbox")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "CaseDoc.md") || !strings.Contains(joined, "casedoc-") {
		t.Fatalf("collision filenames = %v", names)
	}
}

func TestExportOrphansAreIsolatedAndReconciled(t *testing.T) {
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	now := "2026-11-14T10:00:00Z"
	execTestSQL(t, databasePath, `INSERT INTO knowledge_entries(id,scope,project_id,parent_id,slug,type,title,body,status,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"knowledge_orphan_one", "project", "project-deleted", "", "orphan-one", "practice", "孤儿条目一", "孤儿正文一", "verified", now, now)
	execTestSQL(t, databasePath, `INSERT INTO knowledge_entries(id,scope,project_id,parent_id,slug,type,title,body,status,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"knowledge_orphan_two", "project", "project-deleted", "", "orphan-two", "diagnostic", "孤儿条目二", "孤儿正文二", "verified", now, now)
	config := DefaultConfig(t.TempDir())

	report, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false})
	if err != nil {
		t.Fatal(err)
	}
	if report.SourceEntries != 18 || report.Total != 18 || report.ExportedEntries != 18 || report.AccountedEntries != 18 {
		t.Fatalf("reconciliation counts = %+v", report)
	}
	if report.Orphans != 2 || len(report.OrphanList) != 2 {
		t.Fatalf("orphan report = %+v", report)
	}
	if report.Global != 8 || report.Projects != 6 || report.Archives != 2 {
		t.Fatalf("category counts changed: %+v", report)
	}
	orphanWarnings := 0
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "orphan entry") {
			orphanWarnings++
		}
	}
	if orphanWarnings != 2 {
		t.Fatalf("orphan warnings = %d: %v", orphanWarnings, report.Warnings)
	}
	foundBodies := map[string]bool{}
	err = filepath.WalkDir(filepath.Join(config.VaultPath, "orphans"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, body := range []string{"孤儿正文一", "孤儿正文二"} {
			if strings.Contains(string(data), body) {
				foundBodies[body] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(foundBodies) != 2 {
		t.Fatalf("orphan bodies not preserved: %v", foundBodies)
	}

	ctx := context.Background()
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	rebuild, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if rebuild.Entries != 18 || rebuild.Orphans != 2 || rebuild.Global+rebuild.Projects+rebuild.Archives+rebuild.Orphans != rebuild.Entries {
		t.Fatalf("rebuild did not account for orphans: %+v", rebuild)
	}
}

func TestExportDryRunDoesNotWrite(t *testing.T) {
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	config := DefaultConfig(t.TempDir())

	report, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || report.SourceEntries != 16 || report.Total != 16 || report.AccountedEntries != 16 {
		t.Fatalf("dry-run reconciliation = %+v", report)
	}
	if report.Written != 0 || report.Unchanged != 0 || report.Removed != 0 || report.FilesPlanned == 0 {
		t.Fatalf("dry-run touched files: %+v", report)
	}
	if _, err := os.Stat(config.VaultPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run created vault path: %v", err)
	}
}

func TestExportDoesNotFollowPreplantedTemporarySymlink(t *testing.T) {
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	config := DefaultConfig(t.TempDir())
	if _, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	victimPath := filepath.Join(t.TempDir(), "victim.txt")
	victim := []byte("external victim must stay unchanged")
	if err := os.WriteFile(victimPath, victim, 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(config.VaultPath, "projects", "demo-project", "inbox", "inbox-one.md")
	temporary := target + ".tmp"
	if err := os.Symlink(victimPath, temporary); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	execTestSQL(t, databasePath, `UPDATE knowledge_entries SET body='内容已更新，触发重写。' WHERE slug='inbox-one'`)
	second, err := Export(context.Background(), config, databasePath, ExportOptions{Commit: false})
	if err != nil {
		t.Fatal(err)
	}
	if second.Written == 0 {
		t.Fatal("expected the changed document to be rewritten")
	}
	after, err := os.ReadFile(victimPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(victim) {
		t.Fatalf("external victim was overwritten: %q", after)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("exported document became a symlink")
	}
}
