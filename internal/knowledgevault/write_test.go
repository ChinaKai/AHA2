package knowledgevault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newWriteTestManager(t *testing.T) (*Manager, Config, context.Context) {
	t.Helper()
	config, index, ctx := exportSeedVault(t)
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	manager.SetRuntimeWrite(true)
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	return manager, config, ctx
}

const (
	writeTestSummary = "这是一段用于写入测试的摘要，长度足够通过最小校验。"
	writeTestBody    = "这是知识库写入测试的正文内容，包含足够的字符以通过最小长度校验。"
)

func writeTestCreate(title string) WriteRequest {
	return WriteRequest{
		Mode: WriteModeCreate, Scope: WriteScopeProject, ProjectID: "project-demo",
		Type: "practice", Title: title, Summary: writeTestSummary, Body: writeTestBody,
		Tags: []string{"Write-Test"}, SearchQuery: "写入测试",
		SourceTask: "task-write", SourceTurn: "turn-write", Writer: "main",
	}
}

func writeTestCreateThemed(title, theme string) WriteRequest {
	request := writeTestCreate(title)
	request.Summary = "这是关于" + theme + "的测试摘要，内容独立用于区分相似度检查。"
	request.Body = "这是关于" + theme + "的测试正文，包含独立主题信息以避免触发重复检测。"
	return request
}

func writeTestRevise(entryID string, baseRevision int, title string) WriteRequest {
	return WriteRequest{
		Mode: WriteModeRevise, Scope: WriteScopeProject, ProjectID: "project-demo",
		EntryID: entryID, BaseRevision: baseRevision,
		Type: "practice", Title: title, Summary: writeTestSummary,
		Body:       "这是修订后的正文内容，同样需要满足最小长度要求才能写入文件。",
		Tags:       []string{"write-test"},
		SourceTask: "task-write", SourceTurn: "turn-write", Writer: "main",
	}
}

func writeTestEntry(t *testing.T, manager *Manager, ctx context.Context, id string) IndexEntry {
	t.Helper()
	var found IndexEntry
	err := manager.WithReader(func(reader *Reader) error {
		entries, err := reader.Entries(ctx, "project", "project-demo")
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.ID == id {
				found = entry
				return nil
			}
		}
		return errors.New("entry not indexed")
	})
	if err != nil {
		t.Fatalf("read entry %s: %v", id, err)
	}
	return found
}

func writeTestCountEntries(t *testing.T, manager *Manager, ctx context.Context) int {
	t.Helper()
	count := 0
	if err := manager.WithReader(func(reader *Reader) error {
		entries, err := reader.Entries(ctx, "", "")
		count = len(entries)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func writeTestFile(t *testing.T, config Config, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(config.VaultPath, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}

func TestWriteKnowledgeCreatesProjectInboxDocument(t *testing.T) {
	manager, config, ctx := newWriteTestManager(t)
	result, err := manager.WriteKnowledge(ctx, writeTestCreate("写入测试条目一"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != WriteModeCreate || result.Revision != 1 || result.Status != StatusVerified {
		t.Fatalf("result = %+v", result)
	}
	if !strings.HasPrefix(result.Path, "projects/demo-project/inbox/") {
		t.Fatalf("path = %s, want project inbox", result.Path)
	}
	if filepath.Base(result.Path) == "index.md" {
		t.Fatalf("server allocated a reserved index.md path: %s", result.Path)
	}
	if result.ContentHash != hashBytes([]byte(writeTestFile(t, config, result.Path))) {
		t.Fatal("content hash does not match the file on disk")
	}
	document := ParseDocument(writeTestFile(t, config, result.Path))
	if document.Frontmatter.Summary != writeTestSummary || document.Frontmatter.Revision != 1 {
		t.Fatalf("frontmatter = %+v", document.Frontmatter)
	}
	if document.Frontmatter.SourceTask != "task-write" || document.Frontmatter.ProjectID != "project-demo" {
		t.Fatalf("audit frontmatter = %+v", document.Frontmatter)
	}

	entry := writeTestEntry(t, manager, ctx, result.ID)
	if entry.Path != result.Path || entry.Revision != 1 || entry.Summary != writeTestSummary || entry.ContentHash != result.ContentHash {
		t.Fatalf("indexed entry = %+v", entry)
	}

	// The document must be visible through the same read path the Web API uses.
	err = manager.WithReader(func(reader *Reader) error {
		doc, err := reader.Document(ctx, result.ID, "")
		if err != nil {
			return err
		}
		if doc.Title != "写入测试条目一" || strings.TrimSpace(doc.Body) != writeTestBody {
			t.Fatalf("web document = %+v", doc)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWriteKnowledgeUsesExistingTopicGroupAndRejectsUnknownGroup(t *testing.T) {
	manager, _, ctx := newWriteTestManager(t)
	request := writeTestCreate("分组写入测试")
	request.Group = "navigation-group"
	result, err := manager.WriteKnowledge(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Path, "projects/demo-project/navigation-group/") {
		t.Fatalf("path = %s, want navigation-group", result.Path)
	}

	request = writeTestCreate("不存在分组写入")
	request.Group = "missing-group"
	_, err = manager.WriteKnowledge(ctx, request)
	if err == nil || errorCode(err) != WriteErrorValidation {
		t.Fatalf("unknown group error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(manager.config.VaultPath, "projects", "demo-project", "missing-group")); !os.IsNotExist(statErr) {
		t.Fatalf("agent created a topic group directory: %v", statErr)
	}
}

func TestWriteKnowledgeValidationTable(t *testing.T) {
	manager, _, ctx := newWriteTestManager(t)
	base := writeTestCountEntries(t, manager, ctx)
	cases := []struct {
		name   string
		mutate func(*WriteRequest)
		code   string
	}{
		{"wrong type", func(r *WriteRequest) { r.Type = "general" }, WriteErrorValidation},
		{"missing type", func(r *WriteRequest) { r.Type = "" }, WriteErrorValidation},
		{"short title", func(r *WriteRequest) { r.Title = "ab" }, WriteErrorValidation},
		{"multiline title", func(r *WriteRequest) { r.Title = "第一行\n第二行" }, WriteErrorValidation},
		{"short summary", func(r *WriteRequest) { r.Summary = "太短" }, WriteErrorValidation},
		{"long body", func(r *WriteRequest) { r.Body = strings.Repeat("a", 20001) }, WriteErrorValidation},
		{"too many tags", func(r *WriteRequest) {
			r.Tags = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}
		}, WriteErrorValidation},
		{"bad confidence", func(r *WriteRequest) { value := 1.5; r.Confidence = &value }, WriteErrorValidation},
		{"no search evidence", func(r *WriteRequest) { r.SearchQuery = ""; r.DuplicateAck = false }, WriteErrorValidation},
		{"group traversal", func(r *WriteRequest) { r.Group = "../escape" }, WriteErrorValidation},
		{"secret in body", func(r *WriteRequest) { r.Body = writeTestBody + " password = supersecret123" }, WriteErrorForbiddenContent},
		{"secret in summary", func(r *WriteRequest) { r.Summary = "这段摘要里包含 password=supersecret123 这样的内容" }, WriteErrorForbiddenContent},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := writeTestCreate("校验失败测试")
			testCase.mutate(&request)
			_, err := manager.WriteKnowledge(ctx, request)
			if err == nil || errorCode(err) != testCase.code {
				t.Fatalf("error = %v, want code %s", err, testCase.code)
			}
		})
	}
	if after := writeTestCountEntries(t, manager, ctx); after != base {
		t.Fatalf("index entry count changed from %d to %d after rejected writes", base, after)
	}
}

func TestWriteKnowledgeGlobalScopeRequiresPermission(t *testing.T) {
	manager, config, ctx := newWriteTestManager(t)
	request := writeTestCreate("全局收件箱测试")
	request.Scope = WriteScopeGlobal
	request.ProjectID = ""
	_, err := manager.WriteKnowledge(ctx, request)
	if err == nil || errorCode(err) != WriteErrorForbiddenScope {
		t.Fatalf("global without permission error = %v", err)
	}

	request.AllowGlobal = true
	result, err := manager.WriteKnowledge(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Path, "global/inbox/") {
		t.Fatalf("global path = %s", result.Path)
	}
	if _, statErr := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(result.Path))); statErr != nil {
		t.Fatal(statErr)
	}
}

func TestWriteKnowledgeRejectsClientSelectedPaths(t *testing.T) {
	cases := []string{
		"../../etc/passwd",
		"/etc/passwd",
		"projects/demo-project/inbox/index.md",
		"projects/demo-project/skills/evil.md",
		"projects/demo-project/archive/old.md",
		"global/index.md",
		"archive/demo-project/old.md",
		"orphans/loose.md",
		".trash/gone.md",
		`projects\demo-project\inbox\evil.md`,
	}
	for _, relative := range cases {
		if err := validateVaultWritePath(relative, WriteScopeProject, "demo-project"); err == nil {
			t.Fatalf("validateVaultWritePath(%q) accepted a forbidden path", relative)
		}
	}
	if err := validateVaultWritePath("projects/demo-project/inbox/ok.md", WriteScopeProject, "demo-project"); err != nil {
		t.Fatalf("valid path rejected: %v", err)
	}
	if err := validateVaultWritePath("projects/other-project/inbox/ok.md", WriteScopeProject, "demo-project"); errorCode(err) != WriteErrorForbiddenScope {
		t.Fatalf("cross-project path error = %v", err)
	}
}

func TestWriteKnowledgeDoesNotFollowSymlinks(t *testing.T) {
	manager, config, ctx := newWriteTestManager(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	// SafeSegment keeps spaces, so this is the exact name allocateDocumentPath
	// would pick first.
	link := filepath.Join(config.VaultPath, "projects", "demo-project", "inbox", "符号链接写入测试.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	result, err := manager.WriteKnowledge(ctx, writeTestCreate("符号链接写入测试"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(result.Path) == "符号链接写入测试.md" {
		t.Fatalf("write reused the symlinked path: %s", result.Path)
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "outside" {
		t.Fatalf("write escaped through a symlink: %q", data)
	}

	direct := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(direct, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeVaultFileIfChanged(config.VaultPath, link, []byte("overwrite"), 0o644); err == nil {
		t.Fatal("writeVaultFileIfChanged overwrote a symlink")
	}
}

func TestWriteKnowledgeRejectsHighSimilarityDuplicate(t *testing.T) {
	manager, _, ctx := newWriteTestManager(t)
	first, err := manager.WriteKnowledge(ctx, writeTestCreate("重复知识检测"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.WriteKnowledge(ctx, writeTestCreate("重复知识检测"))
	var writeErr *WriteError
	if !errors.As(err, &writeErr) || writeErr.Code != WriteErrorDuplicate {
		t.Fatalf("duplicate error = %v", err)
	}
	if len(writeErr.Candidates) == 0 || writeErr.Candidates[0].ID != first.ID {
		t.Fatalf("duplicate candidates = %+v", writeErr.Candidates)
	}
	if writeErr.Candidates[0].Similarity < 0.8 {
		t.Fatalf("similarity = %f, want >= 0.8", writeErr.Candidates[0].Similarity)
	}

	request := writeTestCreate("重复知识检测")
	request.DuplicateAck = true
	second, err := manager.WriteKnowledge(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.Path == first.Path {
		t.Fatalf("duplicate_ack reused the existing entry: %+v", second)
	}
}

func TestWriteKnowledgeReviseBumpsRevisionAndDetectsConflicts(t *testing.T) {
	manager, config, ctx := newWriteTestManager(t)
	created, err := manager.WriteKnowledge(ctx, writeTestCreate("修订测试原始标题"))
	if err != nil {
		t.Fatal(err)
	}
	revised, err := manager.WriteKnowledge(ctx, writeTestRevise(created.ID, 1, "修订测试新标题"))
	if err != nil {
		t.Fatal(err)
	}
	if revised.Revision != 2 || revised.Path != created.Path || revised.Status != StatusVerified {
		t.Fatalf("revised = %+v", revised)
	}
	document := ParseDocument(writeTestFile(t, config, revised.Path))
	if document.Frontmatter.Title != "修订测试新标题" || document.Frontmatter.Revision != 2 {
		t.Fatalf("revised frontmatter = %+v", document.Frontmatter)
	}
	entry := writeTestEntry(t, manager, ctx, created.ID)
	if entry.Revision != 2 || entry.Title != "修订测试新标题" {
		t.Fatalf("revised index entry = %+v", entry)
	}

	state, err := manager.loadAgentStateLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Entries) != 2 {
		t.Fatalf("audit records = %d, want 2", len(state.Entries))
	}
	record := state.Entries[len(state.Entries)-1]
	if record.OldRevision != 1 || record.DiffSummary == "" || record.Writer != "main" || record.PreviousHash == "" {
		t.Fatalf("revise audit record = %+v", record)
	}

	// Stale base revision and stale base hash both conflict instead of
	// overwriting a newer revision.
	if _, err := manager.WriteKnowledge(ctx, writeTestRevise(created.ID, 1, "过期修订")); errorCode(err) != WriteErrorRevisionConflict {
		t.Fatalf("stale base revision error = %v", err)
	}
	staleHash := writeTestRevise(created.ID, 2, "错误哈希修订")
	staleHash.BaseHash = strings.Repeat("0", 64)
	if _, err := manager.WriteKnowledge(ctx, staleHash); errorCode(err) != WriteErrorRevisionConflict {
		t.Fatalf("stale base hash error = %v", err)
	}
	if entry := writeTestEntry(t, manager, ctx, created.ID); entry.Revision != 2 || entry.Title != "修订测试新标题" {
		t.Fatalf("conflicting writes changed the entry: %+v", entry)
	}
}

func TestWriteKnowledgeConcurrentReviseAllowsOneWriter(t *testing.T) {
	manager, _, ctx := newWriteTestManager(t)
	created, err := manager.WriteKnowledge(ctx, writeTestCreate("并发修订测试"))
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make([]error, 2)
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			title := "并发修订结果一"
			if index == 1 {
				title = "并发修订结果二"
			}
			_, results[index] = manager.WriteKnowledge(ctx, writeTestRevise(created.ID, 1, title))
		}(index)
	}
	wait.Wait()
	successes, conflicts := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errorCode(err) == WriteErrorRevisionConflict:
			conflicts++
		default:
			t.Fatalf("unexpected concurrent revise error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflict=%d, want 1/1", successes, conflicts)
	}
	if entry := writeTestEntry(t, manager, ctx, created.ID); entry.Revision != 2 {
		t.Fatalf("revision = %d, want 2", entry.Revision)
	}
}

func TestWriteKnowledgeIdempotencyKeyReplaysOnce(t *testing.T) {
	manager, _, ctx := newWriteTestManager(t)
	request := writeTestCreate("幂等写入测试")
	request.IdempotencyKey = "idem-1"
	first, err := manager.WriteKnowledge(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.WriteKnowledge(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !second.IdempotentReplay || second.ID != first.ID || second.Path != first.Path {
		t.Fatalf("replay = %+v", second)
	}
}

func TestWriteKnowledgePerTurnBudget(t *testing.T) {
	manager, _, ctx := newWriteTestManager(t)
	manager.setWriteBudgetForTest(1)
	first := writeTestCreateThemed("预算测试第一", "部署预算与回滚策略")
	if _, err := manager.WriteKnowledge(ctx, first); err != nil {
		t.Fatal(err)
	}
	_, err := manager.WriteKnowledge(ctx, writeTestCreateThemed("预算测试第二", "缓存失效与超时重试"))
	if errorCode(err) != WriteErrorBudget {
		t.Fatalf("second write error = %v, want budget", err)
	}
	nextTurn := writeTestCreateThemed("预算测试下一轮", "索引重建与灰度读取")
	nextTurn.SourceTurn = "turn-write-2"
	if _, err := manager.WriteKnowledge(ctx, nextTurn); err != nil {
		t.Fatalf("next Turn write: %v", err)
	}
}

func TestWriteKnowledgeAtomicFailureLeavesNoHalfFile(t *testing.T) {
	manager, config, ctx := newWriteTestManager(t)
	manager.writeHook = func() error { return errors.New("injected failure") }
	_, err := manager.WriteKnowledge(ctx, writeTestCreate("原子失败新建"))
	if err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("create error = %v", err)
	}
	createdPath := filepath.Join(config.VaultPath, "projects", "demo-project", "inbox", "原子失败新建.md")
	if _, statErr := os.Stat(createdPath); !os.IsNotExist(statErr) {
		t.Fatalf("failed create left a file: %v", statErr)
	}

	manager.writeHook = nil
	created, err := manager.WriteKnowledge(ctx, writeTestCreate("原子失败修订"))
	if err != nil {
		t.Fatal(err)
	}
	before := writeTestFile(t, config, created.Path)
	manager.writeHook = func() error { return errors.New("injected failure") }
	_, err = manager.WriteKnowledge(ctx, writeTestRevise(created.ID, 1, "原子失败修订新"))
	if err == nil {
		t.Fatal("failed revise reported success")
	}
	if after := writeTestFile(t, config, created.Path); after != before {
		t.Fatal("failed revise did not restore the previous file")
	}
	if entry := writeTestEntry(t, manager, ctx, created.ID); entry.Revision != 1 {
		t.Fatalf("failed revise changed the index: %+v", entry)
	}
}

func TestWriteKnowledgeFeedbackChangesStatusOnly(t *testing.T) {
	manager, config, ctx := newWriteTestManager(t)
	created, err := manager.WriteKnowledge(ctx, writeTestCreate("反馈状态测试"))
	if err != nil {
		t.Fatal(err)
	}
	before := ParseDocument(writeTestFile(t, config, created.Path))

	helped, err := manager.KnowledgeFeedback(ctx, FeedbackRequest{
		EntryID: created.ID, Kind: FeedbackHelped, ProjectID: "project-demo",
		SourceTask: "task-write", SourceTurn: "turn-write", Writer: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if helped.Status != StatusVerified || helped.Queued || helped.Revision != 1 {
		t.Fatalf("helped = %+v", helped)
	}

	stale, err := manager.KnowledgeFeedback(ctx, FeedbackRequest{
		EntryID: created.ID, Kind: FeedbackStale, ProjectID: "project-demo",
		SourceTask: "task-write", SourceTurn: "turn-write", Writer: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Status != StatusStale || !stale.Queued || stale.Revision != 1 {
		t.Fatalf("stale = %+v", stale)
	}
	after := ParseDocument(writeTestFile(t, config, created.Path))
	if after.Frontmatter.Status != StatusStale {
		t.Fatalf("status = %q, want stale", after.Frontmatter.Status)
	}
	if after.Frontmatter.Revision != before.Frontmatter.Revision {
		t.Fatal("feedback bumped the revision")
	}
	if strings.TrimSpace(after.Body) != strings.TrimSpace(before.Body) {
		t.Fatal("feedback changed the body")
	}
	entry := writeTestEntry(t, manager, ctx, created.ID)
	if entry.Status != StatusStale || entry.Revision != 1 {
		t.Fatalf("indexed feedback entry = %+v", entry)
	}

	state, err := manager.loadAgentStateLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Feedback) != 2 || len(state.Pending) != 1 || state.Pending[0].Kind != FeedbackStale {
		t.Fatalf("feedback state = %+v", state)
	}

	// Feedback never counts against the write budget.
	manager.setWriteBudgetForTest(1)
	if _, err := manager.KnowledgeFeedback(ctx, FeedbackRequest{
		EntryID: created.ID, Kind: FeedbackWrong, ProjectID: "project-demo",
	}); err != nil {
		t.Fatalf("feedback hit the entry write budget: %v", err)
	}
}

func TestWriteKnowledgeFeedbackRejectsCrossProjectAndUnknownEntries(t *testing.T) {
	manager, _, ctx := newWriteTestManager(t)
	created, err := manager.WriteKnowledge(ctx, writeTestCreate("跨项目反馈测试"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.KnowledgeFeedback(ctx, FeedbackRequest{
		EntryID: created.ID, Kind: FeedbackStale, ProjectID: "project-other",
	}); errorCode(err) != WriteErrorForbiddenScope {
		t.Fatalf("cross-project feedback error = %v", err)
	}
	if _, err := manager.KnowledgeFeedback(ctx, FeedbackRequest{
		EntryID: "knowledge_missing", Kind: FeedbackStale, ProjectID: "project-demo",
	}); errorCode(err) != WriteErrorNotFound {
		t.Fatalf("missing entry feedback error = %v", err)
	}
}

func TestWriteKnowledgeRuntimeGateDefaultsClosed(t *testing.T) {
	manager, _, ctx := newWriteTestManager(t)
	manager.SetRuntimeWrite(false)
	_, err := manager.WriteKnowledge(ctx, writeTestCreate("写入开关默认关闭"))
	if errorCode(err) != WriteErrorDisabled {
		t.Fatalf("write with gate off = %v", err)
	}
	if _, err := manager.KnowledgeFeedback(ctx, FeedbackRequest{
		EntryID: "knowledge_any", Kind: FeedbackHelped, ProjectID: "project-demo",
	}); errorCode(err) != WriteErrorDisabled {
		t.Fatalf("feedback with gate off = %v", err)
	}
}
