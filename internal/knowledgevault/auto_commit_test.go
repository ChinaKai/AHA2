package knowledgevault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func headSHA(t *testing.T, directory string) string {
	t.Helper()
	return testGitCommand(t, directory, "rev-parse", "HEAD")
}

func TestAutoCommitSelectsTextAndBlocksUnsafeChanges(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/tracked.md", "# Tracked\n")
	if _, err := git.Commit(ctx, "tracked"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/tracked.md", "# Tracked edited\n")
	writeGitTestFile(t, config.VaultPath, "global/general/new.md", "# New\n")
	writeGitTestFile(t, config.VaultPath, "assets/logo.png", "not really a png")
	writeGitTestFile(t, config.VaultPath, ".env", "TOKEN=value\n")
	writeGitTestFile(t, config.VaultPath, "notes/private.md", "# note\n\n-----BEGIN RSA PRIVATE KEY-----\nsecret\n")
	writeGitTestFile(t, config.VaultPath, "big.md", strings.Repeat("a", maxAutoCommitFileBytes+1))

	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Clean || scan.Unmerged {
		t.Fatalf("unexpected scan: %+v", scan)
	}
	if scan.Pending != 6 {
		t.Fatalf("pending = %d, want 6: %+v", scan.Pending, scan)
	}
	eligible := map[string]bool{}
	for _, path := range scan.Eligible {
		eligible[path] = true
	}
	if !eligible["global/general/tracked.md"] || !eligible["global/general/new.md"] {
		t.Fatalf("text changes missing from eligible: %+v", scan.Eligible)
	}
	if len(eligible) != 2 {
		t.Fatalf("eligible = %+v, want only the two text changes", scan.Eligible)
	}
	reasons := map[string]string{}
	for _, blocked := range scan.Blocked {
		reasons[blocked.Path] = blocked.Reason
	}
	for path, want := range map[string]string{
		"assets/logo.png": "binary", ".env": "secret",
		"notes/private.md": "secret", "big.md": "too_large",
	} {
		if reasons[path] != want {
			t.Errorf("blocked[%s] = %q, want %q (all: %+v)", path, reasons[path], want, scan.Blocked)
		}
	}

	result, err := git.AutoCommit(ctx, "external", scan.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed || len(result.Files) != 2 {
		t.Fatalf("unexpected auto commit: %+v", result)
	}
	if message := testGitCommand(t, config.VaultPath, "log", "-1", "--format=%s"); !strings.Contains(message, "auto-commit 2 file(s) (external change)") {
		t.Fatalf("unexpected commit subject: %q", message)
	}
	committed := testGitCommand(t, config.VaultPath, "show", "--name-only", "--format=", "HEAD")
	for _, path := range []string{"global/general/tracked.md", "global/general/new.md"} {
		if !strings.Contains(committed, path) {
			t.Fatalf("commit is missing %s:\n%s", path, committed)
		}
	}
	for _, path := range []string{"assets/logo.png", ".env", "notes/private.md", "big.md"} {
		if strings.Contains(committed, path) {
			t.Fatalf("commit unexpectedly contains blocked path %s:\n%s", path, committed)
		}
		if _, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(path))); err != nil {
			t.Fatalf("blocked file %s was removed: %v", path, err)
		}
	}
	after, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Clean || len(after.Eligible) != 0 || after.Pending != 4 {
		t.Fatalf("blocked files should remain pending-only: %+v", after)
	}
}

// TestAutoCommitGateShapeOfReportedPreflightFiles recreates the path, size,
// line-ending, frontmatter and body shape of the two files reported in the
// Windows vault. Neither path nor size nor LF/UTF-8/no-NUL content trips a
// gate, which leaves the content secret scan as the only rule that can block
// such a file. The second half proves that a real secret-looking line does.
func TestAutoCommitGateShapeOfReportedPreflightFiles(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	const devicePath = "global/agent-lessons/technical-diagnostics/serial-hardware-board/device-report-visibility-log-branch-and-queue.md"
	const inboxPath = "projects/aha2/inbox/远程部署 wrapper 被 AHA 重启打断：deploy-aha2-remote-last.json 会停在旧版本，以 worker 结果 JSON 为准.md"
	writeGitTestFile(t, config.VaultPath, devicePath, "# seed\n")
	if _, err := git.Commit(ctx, "seed"); err != nil {
		t.Fatal(err)
	}

	deviceDoc := padDocToSize("---\nstatus: stale\ntype: diagnostic\n---\n\n# 设备上报是否可见：先核实日志分支与发送队列\n\n判断设备有没有上报某个属性时，不能只看日志里是否出现 payload；日志分支本身可能屏蔽 data。\n", 1803)
	longLine := strings.Repeat("远程部署结果以 worker 为准。", 12) + "\n"
	if len([]byte(longLine)) != 457 {
		t.Fatalf("long line = %d bytes, want 457", len([]byte(longLine)))
	}
	inboxDoc := padDocToSize("---\ntype: diagnostic\nstatus: draft\n---\n\n# 远程部署 wrapper 被 AHA 重启打断\n\ndeploy-aha2-remote-last.json 会停在旧版本。\n"+longLine, 2117)
	writeGitTestFile(t, config.VaultPath, devicePath, deviceDoc)
	writeGitTestFile(t, config.VaultPath, inboxPath, inboxDoc)

	for path, size := range map[string]int{devicePath: 1803, inboxPath: 2117} {
		content, err := os.ReadFile(filepath.Join(config.VaultPath, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		if len(content) != size {
			t.Fatalf("%s = %d bytes, want %d", path, len(content), size)
		}
		if strings.ContainsRune(string(content), '\r') || strings.ContainsRune(string(content), 0) {
			t.Fatalf("%s is not LF/plain text", path)
		}
		if !utf8.Valid(content) {
			t.Fatalf("%s is not valid UTF-8", path)
		}
		if reason := autoCommitPathExclusion(path); reason != "" {
			t.Fatalf("%s is blocked by path rule %q", path, reason)
		}
		if isAutoCommitBinaryPath(path) {
			t.Fatalf("%s is classified binary by extension", path)
		}
		if looksBinary(content) || containsSecretMaterial(content) {
			t.Fatalf("%s unexpectedly trips a content rule", path)
		}
	}

	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Pending != 2 || len(scan.Blocked) != 0 || len(scan.Eligible) != 2 {
		t.Fatalf("reported-shape scan = %+v, want both files eligible", scan)
	}

	// A token assignment is the content-level rule that can block an otherwise
	// valid markdown file. The gate stays; the status now names this reason.
	secretDoc := inboxDoc + "\ntoken = 0123456789abcdef\n"
	writeGitTestFile(t, config.VaultPath, inboxPath, secretDoc)
	blockedScan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if blockedScan.BlockedTotal != 1 || len(blockedScan.Blocked) != 1 {
		t.Fatalf("secret scan = %+v, want one blocked file", blockedScan)
	}
	if entry := blockedScan.Blocked[0]; entry.Path != inboxPath || entry.Reason != "secret" {
		t.Fatalf("blocked entry = %+v, want %s (secret)", entry, inboxPath)
	}
	for _, path := range blockedScan.Eligible {
		if path == inboxPath {
			t.Fatalf("secret document stayed eligible: %+v", blockedScan.Eligible)
		}
	}
}

func padDocToSize(base string, size int) string {
	if len(base) >= size {
		return base
	}
	var builder strings.Builder
	builder.WriteString(base)
	filler := "补充说明：这是一行用于复现文件形状的普通正文。\n"
	for builder.Len()+len(filler) <= size {
		builder.WriteString(filler)
	}
	if remaining := size - builder.Len(); remaining > 0 {
		if remaining == 1 {
			builder.WriteByte('\n')
		} else {
			builder.WriteString(strings.Repeat("x", remaining-1))
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func TestNormalizeBlockedChangesSanitizesAndDeduplicates(t *testing.T) {
	normalized, total := normalizeBlockedChanges([]BlockedChange{
		{Path: "bad\nname\r\t.md", Reason: "Secret!"},
		{Path: "bad\nname\r\t.md", Reason: "binary"},
		{Path: "ok.md", Reason: "not regular"},
	}, 2)
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if len(normalized) != 2 {
		t.Fatalf("normalized = %+v, want two deduplicated entries", normalized)
	}
	if normalized[0].Path != "badname.md" || normalized[0].Reason != "secret" {
		t.Fatalf("first entry = %+v", normalized[0])
	}
	if normalized[1].Path != "ok.md" || normalized[1].Reason != "notregular" {
		t.Fatalf("second entry = %+v", normalized[1])
	}
}

func TestAutoCommitCommitsDeleteAndRename(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/old.md", "# Old\n")
	writeGitTestFile(t, config.VaultPath, "global/general/rename-me.md", "# Rename\n")
	if _, err := git.Commit(ctx, "seed"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(config.VaultPath, "global/general/old.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(
		filepath.Join(config.VaultPath, "global/general/rename-me.md"),
		filepath.Join(config.VaultPath, "global/general/renamed.md"),
	); err != nil {
		t.Fatal(err)
	}
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := git.AutoCommit(ctx, "external", scan.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed {
		t.Fatalf("delete/rename was not committed: %+v", result)
	}
	if dirty := testGitCommand(t, config.VaultPath, "status", "--porcelain"); dirty != "" {
		t.Fatalf("worktree not clean after delete/rename commit:\n%s", dirty)
	}
	tree := testGitCommand(t, config.VaultPath, "ls-tree", "-r", "--name-only", "HEAD")
	if strings.Contains(tree, "old.md") || strings.Contains(tree, "rename-me.md") || !strings.Contains(tree, "renamed.md") {
		t.Fatalf("unexpected tree after rename:\n%s", tree)
	}
}

func TestAutoCommitCapsBatchAtTwentyFiles(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxAutoCommitFiles+1; index++ {
		writeGitTestFile(t, config.VaultPath, "inbox/"+string(rune('a'+index))+".md", "# File\n")
	}
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Eligible) != maxAutoCommitFiles || scan.Pending != maxAutoCommitFiles+1 {
		t.Fatalf("batch scan = %+v", scan)
	}
	first, err := git.AutoCommit(ctx, "external", scan.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Committed || len(first.Files) != maxAutoCommitFiles || first.Pending != 1 {
		t.Fatalf("first batch = %+v", first)
	}
	secondScan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if secondScan.Clean || len(secondScan.Eligible) != 1 {
		t.Fatalf("leftover batch = %+v", secondScan)
	}
	second, err := git.AutoCommit(ctx, "external", secondScan.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Committed || len(second.Files) != 1 {
		t.Fatalf("second batch = %+v", second)
	}
	if dirty := testGitCommand(t, config.VaultPath, "status", "--porcelain"); dirty != "" {
		t.Fatalf("worktree not clean after both batches:\n%s", dirty)
	}
}

func TestAutoCommitRefusesUnmergedWorktree(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/conflict.md", "# Base\n")
	if _, err := git.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	testGitCommand(t, config.VaultPath, "checkout", "-b", "side")
	writeGitTestFile(t, config.VaultPath, "global/general/conflict.md", "# Side\n")
	testGitCommand(t, config.VaultPath, "commit", "-am", "side")
	testGitCommand(t, config.VaultPath, "checkout", "main")
	writeGitTestFile(t, config.VaultPath, "global/general/conflict.md", "# Main\n")
	testGitCommand(t, config.VaultPath, "commit", "-am", "main")
	merge := exec.Command("git", "-C", config.VaultPath, "merge", "side")
	merge.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := merge.Run(); err == nil {
		t.Fatal("merge unexpectedly succeeded")
	}
	headBefore := headSHA(t, config.VaultPath)
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !scan.Unmerged {
		t.Fatalf("scan did not report unmerged entries: %+v", scan)
	}
	if _, err := git.AutoCommit(ctx, "external", scan.Fingerprint); !errors.Is(err, ErrGitUnmerged) {
		t.Fatalf("auto commit = %v, want ErrGitUnmerged", err)
	}
	if head := headSHA(t, config.VaultPath); head != headBefore {
		t.Fatalf("refused auto commit changed HEAD: %s -> %s", headBefore, head)
	}
	if content, _ := os.ReadFile(filepath.Join(config.VaultPath, "global/general/conflict.md")); !strings.Contains(string(content), "<<<<<<<") {
		t.Fatalf("conflict markers were removed: %q", content)
	}
}

func TestAutoCommitRejectsStaleFingerprint(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/stale.md", "# One\n")
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/stale.md", "# Two\n")
	headBefore := headSHA(t, config.VaultPath)
	if _, err := git.AutoCommit(ctx, "external", scan.Fingerprint); !errors.Is(err, ErrAutoCommitStale) {
		t.Fatalf("auto commit = %v, want ErrAutoCommitStale", err)
	}
	if head := headSHA(t, config.VaultPath); head != headBefore {
		t.Fatalf("stale auto commit changed HEAD: %s -> %s", headBefore, head)
	}
}

func TestAutoCommitCommitsAlreadyStagedRename(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/staged.md", "# Staged rename\n")
	if _, err := git.Commit(ctx, "seed"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(
		filepath.Join(config.VaultPath, "global/general/staged.md"),
		filepath.Join(config.VaultPath, "global/general/staged-renamed.md"),
	); err != nil {
		t.Fatal(err)
	}
	// The owner already staged the rename; auto-commit must still record the
	// deletion side instead of leaving it behind.
	testGitCommand(t, config.VaultPath, "add", "-A")
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := git.AutoCommit(ctx, "external", scan.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed {
		t.Fatalf("staged rename was not committed: %+v", result)
	}
	if dirty := testGitCommand(t, config.VaultPath, "status", "--porcelain"); dirty != "" {
		t.Fatalf("worktree not clean after staged rename:\n%s", dirty)
	}
	tree := testGitCommand(t, config.VaultPath, "ls-tree", "-r", "--name-only", "HEAD")
	if strings.Contains(tree, "staged.md") || !strings.Contains(tree, "staged-renamed.md") {
		t.Fatalf("unexpected tree:\n%s", tree)
	}
}

func TestAutoCommitFindsSecretBeyondTheFirstSixtyFourKiB(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("# harmless filler line\n", 4000) // ~88 KiB
	body += "password = \"supersecretvalue\"\n"
	writeGitTestFile(t, config.VaultPath, "notes/late-secret.md", body)
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Eligible) != 0 || scan.BlockedTotal != 1 || len(scan.Blocked) != 1 {
		t.Fatalf("late secret not blocked: %+v", scan)
	}
	if scan.Blocked[0].Reason != "secret" {
		t.Fatalf("late secret reason = %q", scan.Blocked[0].Reason)
	}
}

func TestAutoCommitBlocksAnthropicStyleKeys(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "notes/provider.md", "key = \"sk-ant-api03-abcdefghijklmnopqrstuvwxyz012345\"\n")
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scan.BlockedTotal != 1 || scan.Blocked[0].Reason != "secret" {
		t.Fatalf("Anthropic-style key was not blocked: %+v", scan)
	}
}

func TestAutoCommitAllowsEnvTemplatesAndPublicKeys(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, ".env.example", "EXAMPLE=1\n")
	writeGitTestFile(t, config.VaultPath, "keys/id_rsa.pub", "ssh-rsa AAAA example\n")
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scan.BlockedTotal != 0 || len(scan.Eligible) != 2 {
		t.Fatalf("templates/public keys were misclassified: %+v", scan)
	}
	result, err := git.AutoCommit(ctx, "external", scan.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed || len(result.Files) != 2 {
		t.Fatalf("templates/public keys were not committed: %+v", result)
	}
}

func TestAutoCommitDoesNotReadHugeUntrackedFiles(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.VaultPath, "huge.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(2 << 30); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("scanning a sparse 2GiB file took %s", elapsed)
	}
	if scan.BlockedTotal != 1 || scan.Blocked[0].Reason != "too_large" {
		t.Fatalf("huge file classification = %+v", scan)
	}
	if len(scan.Eligible) != 0 {
		t.Fatalf("huge file became eligible: %+v", scan.Eligible)
	}
}

func TestAutoCommitRejectsControlCharacterPaths(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(config.VaultPath, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.VaultPath, "notes", "bad\nname.md"), []byte("# Bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Eligible) != 0 || scan.BlockedTotal != 1 || scan.Blocked[0].Reason != "invalid_path" {
		t.Fatalf("control-character path was not blocked: %+v", scan)
	}
	if cleaned := sanitizeCommitPath("a\nb\tc.md"); cleaned != "abc.md" {
		t.Fatalf("commit path was not sanitized: %q", cleaned)
	}
}

func TestAutoCommitReportsBlockedTotalBeyondTheReportCap(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxAutoCommitBlockedReport+10; index++ {
		writeGitTestFile(t, config.VaultPath, fmt.Sprintf(".env.secret%d", index), "VALUE=1\n")
	}
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scan.BlockedTotal != maxAutoCommitBlockedReport+10 {
		t.Fatalf("blocked total = %d, want %d", scan.BlockedTotal, maxAutoCommitBlockedReport+10)
	}
	if len(scan.Blocked) != maxAutoCommitBlockedReport {
		t.Fatalf("blocked report length = %d, want cap %d", len(scan.Blocked), maxAutoCommitBlockedReport)
	}
}

func TestAutoCommitBlocksBareTokenAndOpenAIProjectKeys(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	if _, err := git.Init(ctx); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "notes/bare-token.md", "token = \"abcdefgh12345678\"\n")
	writeGitTestFile(t, config.VaultPath, "notes/project-key.md", "key sk-proj-abcdefghijklmnopqrstuvwxyz012345\n")
	writeGitTestFile(t, config.VaultPath, "notes/token-prose.md", "调用前需要先获取 token，再放入请求头。\n")
	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blocked := map[string]string{}
	for _, item := range scan.Blocked {
		blocked[item.Path] = item.Reason
	}
	if blocked["notes/bare-token.md"] != "secret" || blocked["notes/project-key.md"] != "secret" {
		t.Fatalf("bare token / sk-proj key not blocked: %+v", scan.Blocked)
	}
	if scan.BlockedTotal != 2 {
		t.Fatalf("blocked total = %d, want 2", scan.BlockedTotal)
	}
	if len(scan.Eligible) != 1 || scan.Eligible[0] != "notes/token-prose.md" {
		t.Fatalf("normal token prose was not eligible: %+v", scan.Eligible)
	}
}
