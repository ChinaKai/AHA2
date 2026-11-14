package knowledgevault

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// readerFixture exports the standard seed database, rebuilds the index and
// returns a reader opened exactly like the HTTP server does (read-only index).
func readerFixture(t *testing.T) (Config, *Reader, context.Context) {
	t.Helper()
	config, index, ctx := exportSeedVault(t)
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
	return config, reader, ctx
}

func walkTree(nodes []TreeNode, visit func(TreeNode)) {
	for _, node := range nodes {
		visit(node)
		walkTree(node.Children, visit)
	}
}

func TestReaderTreeKeepsInboxButHidesReservedAreas(t *testing.T) {
	_, reader, ctx := readerFixture(t)
	result, err := reader.Tree(ctx, "project:project-demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.IndexReady || result.Count == 0 {
		t.Fatalf("tree = %+v", result)
	}
	paths := []string{}
	inbox := 0
	walkTree(result.Nodes, func(node TreeNode) {
		paths = append(paths, node.Path)
		if strings.Contains(node.Path, "/inbox/") {
			inbox++
		}
		if strings.HasPrefix(node.Path, "skills/") || strings.Contains(node.Path, "/skills/") {
			t.Errorf("reserved path leaked into the knowledge tree: %s", node.Path)
		}
	})
	if inbox == 0 {
		t.Fatalf("inbox documents are missing from the real directory tree: %v", paths)
	}
	if !containsPath(paths, "projects/demo-project/navigation-group/module-a.md") {
		t.Fatalf("folder child missing from tree: %v", paths)
	}
	var group *TreeNode
	walkTree(result.Nodes, func(node TreeNode) {
		if node.Path == "projects/demo-project/navigation-group" || node.Path == "projects/demo-project/navigation-group/index.md" {
			copy := node
			group = &copy
		}
	})
	if group == nil || len(group.Children) == 0 {
		t.Fatalf("folder did not keep its children: %+v", group)
	}
	global, err := reader.Tree(ctx, "global", "")
	if err != nil {
		t.Fatal(err)
	}
	walkTree(global.Nodes, func(node TreeNode) {
		if node.Scope != "global" {
			t.Errorf("global tree leaked %s scope %s", node.Path, node.Scope)
		}
	})
	if _, err := reader.Tree(ctx, "missing:lib", ""); err == nil {
		t.Fatal("an unknown library must be rejected")
	}
}

func directChildByPath(node TreeNode, target string) *TreeNode {
	for index := range node.Children {
		if node.Children[index].Path == target {
			return &node.Children[index]
		}
	}
	return nil
}

func findTreeNodeByPath(nodes []TreeNode, target string) *TreeNode {
	for index := range nodes {
		if nodes[index].Path == target {
			return &nodes[index]
		}
		if found := findTreeNodeByPath(nodes[index].Children, target); found != nil {
			return found
		}
	}
	return nil
}

// TestReaderTreeFollowsVaultDirectories guards the real-vault layout: exported
// directory nodes keep is_index=false after a rebuild, so the tree must nest by
// path instead of trusting the database parent/index flags.
func TestReaderTreeFollowsVaultDirectories(t *testing.T) {
	_, reader, ctx := readerFixture(t)
	global, err := reader.Tree(ctx, "global", "")
	if err != nil {
		t.Fatal(err)
	}
	root := findTreeNodeByPath(global.Nodes, "global/index.md")
	if root == nil {
		t.Fatalf("global root index missing from tree: %+v", global.Nodes)
	}
	if !root.IsIndex || !root.System {
		t.Fatalf("directory node was not promoted to a folder: %+v", root)
	}
	for _, relative := range []string{"global/general/index.md", "global/agent-lessons/index.md"} {
		node := directChildByPath(*root, relative)
		if node == nil {
			t.Fatalf("%s is not a child of the global root", relative)
		}
		if !node.IsIndex || !node.System {
			t.Fatalf("is_index=0 directory node was not promoted: %+v", node)
		}
	}
	lessons := findTreeNodeByPath(global.Nodes, "global/agent-lessons/index.md")
	if lessons == nil {
		t.Fatal("agent-lessons folder missing")
	}
	for _, relative := range []string{
		"global/agent-lessons/behavior-lessons/index.md",
		"global/agent-lessons/technical-diagnostics/index.md",
	} {
		if findTreeNodeByPath(lessons.Children, relative) == nil {
			t.Fatalf("%s is not nested under agent-lessons", relative)
		}
	}
	verify := findTreeNodeByPath(global.Nodes, "global/agent-lessons/behavior-lessons/verify-first.md")
	if verify == nil {
		t.Fatal("behavior lesson document missing")
	}
	if directChildByPath(*root, "global/agent-lessons/behavior-lessons/verify-first.md") != nil {
		t.Fatal("nested behavior lesson was flattened onto the global root")
	}
	counted := 0
	walkTree(global.Nodes, func(TreeNode) { counted++ })
	if counted != global.Count {
		t.Fatalf("tree exposes %d nodes but reports count %d", counted, global.Count)
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func childIDs(node TreeNode) []string {
	ids := make([]string, 0, len(node.Children))
	for _, child := range node.Children {
		ids = append(ids, child.ID)
	}
	return ids
}

func TestBuildReaderTreeUsesPathsAndPromotesIndexDocuments(t *testing.T) {
	// Every index.md carries is_index=false and every document keeps a flat
	// database parent: only the vault path may decide the hierarchy.
	entries := []IndexEntry{
		{ID: "root", Path: "global/index.md", Type: TypeNavigation, Title: "Global", Scope: "global"},
		{ID: "general", Path: "global/general/index.md", Type: TypeNavigation, Title: "General", Scope: "global", ParentID: "root"},
		{ID: "general-doc", Path: "global/general/deploy.md", Type: TypePractice, Title: "Deploy", Scope: "global", ParentID: "root"},
		{ID: "lessons", Path: "global/agent-lessons/index.md", Type: TypeNavigation, Title: "Agent Lessons", Scope: "global", ParentID: "root"},
		{ID: "behavior", Path: "global/agent-lessons/behavior-lessons/index.md", Type: TypeNavigation, Title: "Behavior Lessons", Scope: "global", ParentID: "lessons"},
		{ID: "behavior-doc", Path: "global/agent-lessons/behavior-lessons/verify-first.md", Type: TypePractice, Title: "Verify First", Scope: "global", ParentID: "root"},
		{ID: "technical", Path: "global/agent-lessons/technical-diagnostics/index.md", Type: TypeNavigation, Title: "Technical Diagnostics", Scope: "global", ParentID: "lessons"},
		{ID: "technical-doc", Path: "global/agent-lessons/technical-diagnostics/serial-login.md", Type: TypeDiagnostic, Title: "Serial Login", Scope: "global", ParentID: "root"},
		{ID: "loose-doc", Path: "global/loose/topic.md", Type: TypeReference, Title: "Loose Topic", Scope: "global", ParentID: "root"},
		{ID: "root-doc", Path: "global/overview.md", Type: TypeReference, Title: "Overview", Scope: "global", ParentID: "root"},
	}
	nodes := buildReaderTree(entries)
	if len(nodes) != 1 || nodes[0].ID != "root" {
		t.Fatalf("tree roots = %+v", nodes)
	}
	root := nodes[0]
	if !root.IsIndex || !root.System || root.Title != "Global" || root.Path != "global/index.md" {
		t.Fatalf("root node = %+v", root)
	}
	if got, want := childIDs(root), []string{"lessons", "general", "loose-doc", "root-doc"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root children = %v, want %v", got, want)
	}
	if node := findTreeNodeByPath(root.Children, "global/loose"); node != nil {
		t.Fatalf("directory without index.md became a node: %+v", node)
	}
	lessons := findTreeNodeByPath(nodes, "global/agent-lessons/index.md")
	if lessons == nil {
		t.Fatal("agent-lessons folder missing")
	}
	if got, want := childIDs(*lessons), []string{"behavior", "technical"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("agent-lessons children = %v, want %v", got, want)
	}
	for _, id := range []string{"general", "lessons", "behavior", "technical"} {
		node := findTreeNodeByPath(nodes, nodePath(t, entries, id))
		if node == nil || !node.IsIndex || !node.System {
			t.Fatalf("folder %s was not promoted to an index node: %+v", id, node)
		}
	}
	if got, want := childIDs(*findTreeNodeByPath(nodes, "global/general/index.md")), []string{"general-doc"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("general children = %v, want %v", got, want)
	}
	if got, want := childIDs(*findTreeNodeByPath(nodes, "global/agent-lessons/behavior-lessons/index.md")), []string{"behavior-doc"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("behavior children = %v, want %v", got, want)
	}
	counted := 0
	walkTree(nodes, func(TreeNode) { counted++ })
	if counted != len(entries) {
		t.Fatalf("tree exposes %d nodes, want %d", counted, len(entries))
	}
}

func nodePath(t *testing.T, entries []IndexEntry, id string) string {
	t.Helper()
	for _, entry := range entries {
		if entry.ID == id {
			return entry.Path
		}
	}
	t.Fatalf("entry %s missing", id)
	return ""
}

func TestReaderLibrariesExcludeInboxFromCounts(t *testing.T) {
	_, reader, ctx := readerFixture(t)
	libraries := reader.Libraries(ctx)
	if !libraries.Enabled || !libraries.IndexReady || len(libraries.Libraries) == 0 {
		t.Fatalf("libraries = %+v", libraries)
	}
	entries, err := reader.index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	nonInbox := 0
	for _, entry := range entries {
		if !isInboxPath(entry.Path) && !isReservedVaultPath(entry.Path) {
			nonInbox++
		}
	}
	total := 0
	for _, library := range libraries.Libraries {
		total += library.Count
	}
	if total != nonInbox {
		t.Fatalf("library counts = %d, non-inbox entries = %d", total, nonInbox)
	}
}

func TestReaderSearchAppliesExactTypeStatusAndTagFilters(t *testing.T) {
	config, reader, ctx := readerFixture(t)
	// The standard seed has no tags, so add one indexed file with two tags and
	// a stale status to exercise exact filters through the read API.
	tagged := filepath.Join(config.VaultPath, "global", "general", "tagged.md")
	raw := `---
id: knowledge_global_tagged_reader
type: practice
status: stale
title: 标签文档
tags: [alpha, beta]
---
用于验证标签筛选。
`
	if err := os.WriteFile(tagged, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	_ = index.Close()

	results, err := reader.Search(ctx, DocumentSearchOptions{Tag: "alpha", Library: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Results) != 1 || results.Results[0].Title != "标签文档" {
		t.Fatalf("tag search = %+v", results.Results)
	}
	if results.Results[0].Status != "stale" {
		t.Fatalf("status = %q", results.Results[0].Status)
	}
	for _, partial := range []string{"alp", "alp%", "alpha_"} {
		miss, err := reader.Search(ctx, DocumentSearchOptions{Tag: partial})
		if err != nil {
			t.Fatal(err)
		}
		if len(miss.Results) != 0 {
			t.Fatalf("tag search %q was not exact: %+v", partial, miss.Results)
		}
	}
	byType, err := reader.Search(ctx, DocumentSearchOptions{Type: "reference", Library: "project:project-demo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range byType.Results {
		if item.Type != "reference" {
			t.Fatalf("type filter leaked %+v", item)
		}
	}
	if len(byType.Results) == 0 {
		t.Fatal("expected reference documents in the demo project")
	}
	byStatus, err := reader.Search(ctx, DocumentSearchOptions{Status: "stale", Library: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byStatus.Results) != 1 {
		t.Fatalf("status filter = %+v", byStatus.Results)
	}
}

func TestReaderRecentUsesFileMTimeAndKeepsInboxReadable(t *testing.T) {
	config, reader, ctx := readerFixture(t)
	target := filepath.Join(config.VaultPath, "projects", "demo-project", "navigation-group", "module-a.md")
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatal(err)
	}
	recent, err := reader.Recent(ctx, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent.Items) == 0 || recent.Items[0].Path != "projects/demo-project/navigation-group/module-a.md" {
		t.Fatalf("recent did not order by mtime: %+v", recent.Items)
	}
	if strings.Contains(recent.Items[0].Title, "inbox") {
		t.Fatalf("unexpected first item: %+v", recent.Items[0])
	}

	inbox, err := reader.Inbox(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if inbox.Count == 0 || len(inbox.Items) == 0 {
		t.Fatalf("inbox = %+v", inbox)
	}
	for _, item := range inbox.Items {
		if !isInboxPath(item.Path) {
			t.Fatalf("non-inbox item in inbox: %+v", item)
		}
	}
	doc, err := reader.Document(ctx, inbox.Items[0].ID, inbox.Items[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Path, "/inbox/") {
		t.Fatalf("inbox document is not readable: %+v", doc)
	}
}

func TestReaderDocumentResolvesIDAndPathAndReturnsOutlineAndLinks(t *testing.T) {
	_, reader, ctx := readerFixture(t)
	entries, err := reader.index.Entries(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var target IndexEntry
	for _, entry := range entries {
		if strings.HasSuffix(entry.Path, "/shared-deploy.md") {
			target = entry
			break
		}
	}
	if target.ID == "" {
		t.Fatal("seed serial-login entry missing")
	}
	byID, err := reader.Document(ctx, target.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	byPath, err := reader.Document(ctx, "", target.Path)
	if err != nil {
		t.Fatal(err)
	}
	if byID.ID != byPath.ID || byID.AbsolutePath != byPath.AbsolutePath {
		t.Fatalf("id/path disagree: %+v vs %+v", byID, byPath)
	}
	if byID.ObsidianFile != strings.TrimSuffix(target.Path, ".md") {
		t.Fatalf("obsidian file = %q", byID.ObsidianFile)
	}
	if byID.ObsidianVault != reader.Manifest().ObsidianVault {
		t.Fatalf("obsidian vault = %q", byID.ObsidianVault)
	}
	if len(byID.Backlinks) == 0 {
		t.Fatalf("backlinks = %+v", byID.Backlinks)
	}
	if _, err := reader.Document(ctx, "missing-id", ""); err != ErrDocumentNotFound {
		t.Fatalf("missing document error = %v", err)
	}
}

func TestReaderDocumentBuildsOutlineFromHeadings(t *testing.T) {
	config, reader, ctx := readerFixture(t)
	outlinePath := filepath.Join(config.VaultPath, "global", "general", "outline.md")
	raw := "---\nid: knowledge_global_outline_reader\ntype: reference\nstatus: verified\ntitle: 大纲文档\n---\n\n# 一级\n\n## 二级\n\n正文。\n"
	if err := os.WriteFile(outlinePath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	_ = index.Close()
	doc, err := reader.Document(ctx, "", "global/general/outline.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Outline) != 2 || doc.Outline[0].Text != "一级" || doc.Outline[1].Level != 2 {
		t.Fatalf("outline = %+v", doc.Outline)
	}
	if doc.Outline[0].Slug == "" {
		t.Fatalf("outline slug = %+v", doc.Outline[0])
	}
}

func TestReaderOutlineSkipsFencedCode(t *testing.T) {
	config, reader, ctx := readerFixture(t)
	outlinePath := filepath.Join(config.VaultPath, "global", "general", "fenced-outline.md")
	raw := "---\nid: knowledge_global_fenced_outline\ntype: reference\nstatus: verified\ntitle: 围栏文档\n---\n\n" +
		"# 文档\n\n" +
		"```bash\n# 注释\n## 假标题\n```\n\n" +
		"## 真标题\n\n" +
		"~~~shell\n# 另一个注释\n~~~\n\n" +
		"### 末尾标题\n"
	if err := os.WriteFile(outlinePath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	_ = index.Close()
	doc, err := reader.Document(ctx, "", "global/general/fenced-outline.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Outline) != 3 {
		t.Fatalf("outline = %+v", doc.Outline)
	}
	want := []string{"文档", "真标题", "末尾标题"}
	for index, item := range doc.Outline {
		if item.Text != want[index] {
			t.Fatalf("outline[%d] = %+v, want %q", index, item, want[index])
		}
	}
}

func TestReaderIndexIsReadOnly(t *testing.T) {
	_, reader, _ := readerFixture(t)
	if _, err := reader.index.db.Exec(`INSERT INTO entries (id) VALUES ('should-fail')`); err == nil {
		t.Fatal("the reader index accepted a write")
	}
}

func TestReaderSkillsAreListedFromVaultFolders(t *testing.T) {
	config, _, _ := readerFixture(t)
	directory := filepath.Join(config.VaultPath, "skills", "demo-skill")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: Demo Skill\ndescription: Read-only demo\n---\n\n# Demo\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "extra.md"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReader(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	skills, err := reader.Skills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills.Skills) == 0 {
		t.Fatal("skill package was not listed")
	}
	var found bool
	for _, skill := range skills.Skills {
		if skill.Slug == "demo-skill" {
			found = true
			if skill.Name != "Demo Skill" || skill.Description != "Read-only demo" || skill.FileCount != 2 {
				t.Fatalf("skill = %+v", skill)
			}
			if !strings.HasSuffix(skill.OpenPath, filepath.Join("skills", "demo-skill", "SKILL.md")) {
				t.Fatalf("open path = %q", skill.OpenPath)
			}
		}
	}
	if !found {
		t.Fatalf("demo-skill not found: %+v", skills.Skills)
	}
}

func TestOpenReaderWithoutVaultOrIndexIsAnEmptyStateNotAnError(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	reader, err := OpenReader(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	libraries := reader.Libraries(context.Background())
	if !libraries.Enabled || libraries.VaultReady || libraries.IndexReady || libraries.Warning != "vault_missing" {
		t.Fatalf("libraries = %+v", libraries)
	}
	tree, err := reader.Tree(context.Background(), "global", "")
	if err != nil {
		t.Fatal(err)
	}
	if tree.IndexReady || tree.Count != 0 || len(tree.Nodes) != 0 {
		t.Fatalf("tree = %+v", tree)
	}
	if _, err := reader.Document(context.Background(), "x", ""); err != ErrReaderNotReady {
		t.Fatalf("document error = %v", err)
	}
}

// benchmarkReaderFixture builds a 1000-document vault so the two performance
// targets from the Phase 2 brief can be measured with `go test -bench`.
func benchmarkReaderFixture(b *testing.B) *Reader {
	b.Helper()
	ctx := context.Background()
	config := DefaultConfig(b.TempDir())
	if _, err := Bootstrap(config, InitOptions{VaultName: "Perf Vault", Commit: false}); err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 1000; index++ {
		relative := fmt.Sprintf("global/topic-%02d/doc-%04d.md", index%25, index)
		absolute := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			b.Fatal(err)
		}
		title := fmt.Sprintf("文档 %04d", index)
		body := "普通内容。\n"
		if index%2 == 0 {
			body = "构建 发布 验证 内容。\n"
		}
		raw := fmt.Sprintf("---\nid: knowledge_perf_%04d\ntype: practice\nstatus: verified\ntitle: %s\ntags: [perf, group-%02d]\n---\n# %s\n\n%s", index, title, index%25, title, body)
		if err := os.WriteFile(absolute, []byte(raw), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	index, err := OpenIndex(ctx, config.IndexPath)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		b.Fatal(err)
	}
	if err := index.Close(); err != nil {
		b.Fatal(err)
	}
	reader, err := OpenReader(ctx, config)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = reader.Close() })
	return reader
}

func BenchmarkReaderTree1000Documents(b *testing.B) {
	reader := benchmarkReaderFixture(b)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		result, err := reader.Tree(ctx, "global", "")
		if err != nil {
			b.Fatal(err)
		}
		if result.Count < 1000 {
			b.Fatalf("tree count = %d", result.Count)
		}
	}
}

func BenchmarkReaderSearchLIKE1000Documents(b *testing.B) {
	reader := benchmarkReaderFixture(b)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		result, err := reader.Search(ctx, DocumentSearchOptions{Query: "构建", Library: "global", Limit: 40})
		if err != nil {
			b.Fatal(err)
		}
		if len(result.Results) == 0 {
			b.Fatal("search returned no results")
		}
	}
}
