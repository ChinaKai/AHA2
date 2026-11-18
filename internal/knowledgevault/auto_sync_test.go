package knowledgevault

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type testAutoSyncSettings struct {
	mu      sync.Mutex
	enabled bool
}

func (s *testAutoSyncSettings) AutoSyncEnabled(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled, nil
}

func (s *testAutoSyncSettings) SetAutoSyncEnabled(_ context.Context, enabled bool) error {
	s.mu.Lock()
	s.enabled = enabled
	s.mu.Unlock()
	return nil
}

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func newTestClock() *testClock {
	return &testClock{at: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) Advance(delta time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(delta)
	c.mu.Unlock()
}

func newAutoSyncTestManager(t *testing.T) (Config, *Manager) {
	t.Helper()
	config := DefaultConfig(t.TempDir())
	config.AllowLocalRemote = true
	normalized, err := config.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(normalized, InitOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Git().Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return normalized, manager
}

func newTestSyncer(t *testing.T, manager *Manager, emit func(SyncEvent)) (*Syncer, *testAutoSyncSettings, *testClock) {
	t.Helper()
	settings := &testAutoSyncSettings{enabled: true}
	clock := newTestClock()
	syncer := NewSyncer(manager, settings, SyncerConfig{
		Now: clock.Now, QuietPeriod: 30 * time.Second, WriteDebounce: time.Second,
		RemoteInterval: time.Minute, RetryBase: 10 * time.Millisecond, RetryMax: time.Second,
		MaxFailures: 2, Emit: emit,
	})
	if err := syncer.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	return syncer, settings, clock
}

func markPreflightDone(syncer *Syncer) {
	syncer.mu.Lock()
	syncer.state.preflightVersion = AutoSyncPreflightVersion
	syncer.mu.Unlock()
}

func TestSyncerPreflightReportsCRLFBlobsWithoutRewriting(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	// Reproduce an upgraded vault: it predates the policy file and its blobs
	// still carry CRLF.
	if err := os.Remove(filepath.Join(config.VaultPath, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit(ctx, "drop policy for upgrade fixture"); err != nil {
		t.Fatal(err)
	}
	testGitCommand(t, config.VaultPath, "config", "core.autocrlf", "false")
	writeGitTestFile(t, config.VaultPath, "global/general/crlf.md", "# CRLF\r\n\r\nbody\r\n")
	testGitCommand(t, config.VaultPath, "add", "-A")
	testGitCommand(t, config.VaultPath, "commit", "-m", "crlf baseline")
	writeGitTestFile(t, config.VaultPath, "global/general/edit.md", "# Edited externally\n")
	blobBefore := gitOutputBytes(t, config.VaultPath, "show", "HEAD:global/general/crlf.md")

	var events []SyncEvent
	syncer, _, _ := newTestSyncer(t, manager, func(event SyncEvent) { events = append(events, event) })
	syncer.RunOnce(ctx)

	status := syncer.Status()
	if !status.Paused || status.PreflightStage != PreflightStageLineEndings {
		t.Fatalf("CRLF vault did not pause at the line-ending stage: %+v", status)
	}
	if !strings.Contains(status.LastError, "1 个索引文件仍含 CRLF") {
		t.Fatalf("pause did not report the CRLF count: %q", status.LastError)
	}
	// The repair hint must be actionable even without an existing policy file,
	// and must not imply that "重新检查" resumes sync.
	for _, required := range []string{"* text=auto eol=lf", "git add --renormalize .", "切换一次自动同步开关", "“重新检查”只刷新统计"} {
		if !strings.Contains(status.LastError, required) {
			t.Fatalf("pause hint missing %q: %q", required, status.LastError)
		}
	}
	// No silent bulk rewrite: the policy file stays absent, the CRLF blob is
	// byte-identical, and the eligible edit is not committed either because the
	// preflight stops before the commit loop.
	if _, err := os.Stat(filepath.Join(config.VaultPath, ".gitattributes")); !os.IsNotExist(err) {
		t.Fatalf("policy file was created over CRLF blobs: %v", err)
	}
	blobAfter := gitOutputBytes(t, config.VaultPath, "show", "HEAD:global/general/crlf.md")
	if string(blobAfter) != string(blobBefore) {
		t.Fatalf("preflight rewrote the CRLF blob: %q -> %q", blobBefore, blobAfter)
	}
	if message := testGitCommand(t, config.VaultPath, "log", "-1", "--format=%s"); strings.Contains(message, "Normalize") {
		t.Fatalf("preflight created a normalization commit: %q", message)
	}
	if len(events) != 1 || events[0].Kind != SyncEventPaused {
		t.Fatalf("preflight pause events = %+v", events)
	}
	if !strings.Contains(events[0].Detail, "CRLF") {
		t.Fatalf("pause event did not report the CRLF count: %q", events[0].Detail)
	}
}

// A Windows-edited note can contain both CRLF and LF. git reports it as
// i/mixed, which must be treated exactly like i/crlf: pause before the commit
// loop instead of silently rewriting the blob to LF.
func TestSyncerPreflightPausesOnMixedBlobWithoutRewriting(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	// Commit the mixed blob without the LF policy, otherwise `git add` would
	// normalize it before the preflight ever sees it.
	if err := os.Remove(filepath.Join(config.VaultPath, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit(ctx, "drop policy for mixed fixture"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/mixed.md", "# Mixed\r\n\r\nbody\n")
	testGitCommand(t, config.VaultPath, "add", "-A")
	testGitCommand(t, config.VaultPath, "commit", "-m", "mixed baseline")
	blobBefore := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD:global/general/mixed.md")
	headBefore := headSHA(t, config.VaultPath)

	syncer, _, _ := newTestSyncer(t, manager, nil)
	syncer.RunOnce(ctx)

	status := syncer.Status()
	if !status.Paused || status.PreflightStage != PreflightStageLineEndings {
		t.Fatalf("mixed-line-ending vault did not pause at the line-ending stage: %+v", status)
	}
	if !strings.Contains(status.LastError, "混合行尾 1") {
		t.Fatalf("pause did not report the mixed count: %q", status.LastError)
	}
	if head := headSHA(t, config.VaultPath); head != headBefore {
		t.Fatalf("mixed-ending preflight committed: %s -> %s", headBefore, head)
	}
	if blobAfter := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD:global/general/mixed.md"); blobAfter != blobBefore {
		t.Fatalf("mixed blob was rewritten: %s -> %s", blobBefore, blobAfter)
	}
	if message := testGitCommand(t, config.VaultPath, "log", "-1", "--format=%s"); strings.Contains(message, "Normalize") {
		t.Fatalf("preflight created a normalization commit: %q", message)
	}
}

// A `-text` rule marks a file as binary. git never reports it as modified and
// renormalize never stages it, so it is safe to leave out of the migration
// counts; the quiet-period auto-commit must not touch its blob either.
func TestSyncerPreflightAllowsBinaryTextPolicy(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	path := commitExplicitCRLFFixture(t, config.VaultPath, "*.raw -text\n", "global/general/report.raw", "a\r\nb\r\n")
	headBefore := headSHA(t, config.VaultPath)
	blobBefore := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD:"+path)

	syncer, _, clock := newTestSyncer(t, manager, nil)
	syncer.RunOnce(ctx)

	status := syncer.Status()
	if status.Paused {
		t.Fatalf("-text policy paused the preflight: stage=%q error=%q", status.PreflightStage, status.LastError)
	}
	if !status.PreflightDone || status.PreflightStage != "" {
		t.Fatalf("preflight did not finish cleanly: %+v", status)
	}
	if head := headSHA(t, config.VaultPath); head != headBefore {
		t.Fatalf("-text policy preflight committed: %s -> %s", headBefore, head)
	}
	// Several quiet-period cycles must not stage the CRLF blob either.
	for cycle := 0; cycle < 4; cycle++ {
		clock.Advance(31 * time.Second)
		syncer.RunOnce(ctx)
	}
	if head := headSHA(t, config.VaultPath); head != headBefore {
		t.Fatalf("quiet-period auto-commit touched the -text blob: %s -> %s", headBefore, head)
	}
	if blobAfter := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD:"+path); blobAfter != blobBefore {
		t.Fatalf("-text blob was rewritten: %s -> %s", blobBefore, blobAfter)
	}
	if content, err := os.ReadFile(filepath.Join(config.VaultPath, ".gitattributes")); err != nil {
		t.Fatal(err)
	} else if string(content) != "*.raw -text\n" {
		t.Fatalf("owner .gitattributes was rewritten:\n%s", content)
	}
}

// `eol=crlf` is NOT a safe exclusion: git reports the CRLF blob as modified and
// the next ordinary add (including the quiet-period auto-commit) rewrites the
// index blob to LF. The preflight must pause and leave the blob untouched.
func TestSyncerPreflightPausesOnExplicitCRLFPolicy(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	path := commitExplicitCRLFFixture(t, config.VaultPath, "*.crlf text eol=crlf\n", "global/general/report.crlf", "a\r\nb\r\n")
	headBefore := headSHA(t, config.VaultPath)
	blobBefore := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD:"+path)

	syncer, _, clock := newTestSyncer(t, manager, nil)
	syncer.RunOnce(ctx)

	status := syncer.Status()
	if !status.Paused || status.PreflightStage != PreflightStageLineEndings {
		t.Fatalf("eol=crlf CRLF blob did not pause at the line-ending stage: %+v", status)
	}
	if head := headSHA(t, config.VaultPath); head != headBefore {
		t.Fatalf("eol=crlf preflight committed: %s -> %s", headBefore, head)
	}
	// The quiet period must not rewrite the blob while the syncer is paused.
	for cycle := 0; cycle < 4; cycle++ {
		clock.Advance(31 * time.Second)
		syncer.RunOnce(ctx)
	}
	if head := headSHA(t, config.VaultPath); head != headBefore {
		t.Fatalf("paused eol=crlf vault still committed: %s -> %s", headBefore, head)
	}
	if blobAfter := testGitCommand(t, config.VaultPath, "rev-parse", "HEAD:"+path); blobAfter != blobBefore {
		t.Fatalf("eol=crlf blob was rewritten: %s -> %s", blobBefore, blobAfter)
	}
	if message := testGitCommand(t, config.VaultPath, "log", "-1", "--format=%s"); strings.Contains(message, "Normalize") {
		t.Fatalf("preflight created a normalization commit: %q", message)
	}

	// After the owner follows the hint (renormalize + commit) the blob becomes
	// i/lf w/crlf, the gate releases and the preflight finishes.
	testGitCommand(t, config.VaultPath, "add", "--renormalize", ".")
	testGitCommand(t, config.VaultPath, "commit", "-m", "owner renormalized line endings")
	if eol := testGitCommand(t, config.VaultPath, "ls-files", "--eol", "--", path); !strings.Contains(eol, "i/lf") || !strings.Contains(eol, "w/crlf") {
		t.Fatalf("manual renormalize did not produce i/lf w/crlf: %q", eol)
	}
	syncer.ResetPause()
	syncer.RunOnce(ctx)
	released := syncer.Status()
	if released.Paused || !released.PreflightDone {
		t.Fatalf("preflight stayed paused after the owner renormalized: %+v", released)
	}
}

// commitExplicitCRLFFixture builds a repo where path is an i/crlf blob whose
// attr column matches attrs. The policy file is committed separately without
// staging the CRLF file, otherwise `git add -A` would normalize it first.
func commitExplicitCRLFFixture(t *testing.T, vaultPath, attrs, path, body string) string {
	t.Helper()
	writeGitTestFile(t, vaultPath, ".gitattributes", "# owner policy pending\n")
	testGitCommand(t, vaultPath, "add", "-A")
	testGitCommand(t, vaultPath, "commit", "-m", "placeholder policy")
	writeGitTestFile(t, vaultPath, path, body)
	testGitCommand(t, vaultPath, "add", "-A")
	testGitCommand(t, vaultPath, "commit", "-m", "crlf baseline")
	writeGitTestFile(t, vaultPath, ".gitattributes", attrs)
	testGitCommand(t, vaultPath, "add", "--", ".gitattributes")
	testGitCommand(t, vaultPath, "commit", "-m", "owner line-ending policy", "--", ".gitattributes")
	eol := testGitCommand(t, vaultPath, "ls-files", "--eol", "--", path)
	if !strings.Contains(eol, "i/crlf") || !strings.Contains(eol, "attr/") {
		t.Fatalf("fixture is not an explicit CRLF-attr blob: %q", eol)
	}
	return path
}

func TestSyncerDetectsExternalChangeAfterQuietPeriod(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	syncer, _, clock := newTestSyncer(t, manager, nil)
	markPreflightDone(syncer)
	commitBefore := headSHA(t, config.VaultPath)

	writeGitTestFile(t, config.VaultPath, "global/general/external.md", "# External\n")
	syncer.RunOnce(ctx)
	if headSHA(t, config.VaultPath) != commitBefore {
		t.Fatal("external change was committed before the quiet period")
	}
	clock.Advance(29 * time.Second)
	syncer.RunOnce(ctx)
	if headSHA(t, config.VaultPath) != commitBefore {
		t.Fatal("external change was committed before 30 seconds of quiet")
	}
	clock.Advance(2 * time.Second)
	syncer.RunOnce(ctx)
	if headSHA(t, config.VaultPath) == commitBefore {
		t.Fatal("external change was not committed after the quiet period")
	}
	if message := testGitCommand(t, config.VaultPath, "log", "-1", "--format=%s"); !strings.Contains(message, "(external change)") {
		t.Fatalf("unexpected commit subject: %q", message)
	}
	if status := syncer.Status(); status.Pending != 0 || status.LastCommit == "" || status.Paused {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestSyncerCommitsAHAWriteAfterDebounce(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	syncer, _, clock := newTestSyncer(t, manager, nil)
	markPreflightDone(syncer)

	writeGitTestFile(t, config.VaultPath, "global/general/aha-write.md", "# AHA write\n")
	syncer.NotifyWrite()
	syncer.RunOnce(ctx)
	if message := testGitCommand(t, config.VaultPath, "log", "-1", "--format=%s"); strings.Contains(message, "AHA2 write") {
		t.Fatalf("AHA write ignored the debounce: %q", message)
	}
	clock.Advance(2 * time.Second)
	syncer.RunOnce(ctx)
	if message := testGitCommand(t, config.VaultPath, "log", "-1", "--format=%s"); !strings.Contains(message, "(AHA2 write)") {
		t.Fatalf("AHA write was not committed after debounce: %q", message)
	}
}

func TestSyncerDisabledPerformsNoGitOperations(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	syncer, _, _ := newTestSyncer(t, manager, nil)
	if err := syncer.SetEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	commitBefore := headSHA(t, config.VaultPath)
	writeGitTestFile(t, config.VaultPath, "global/general/while-off.md", "# Off\n")
	syncer.NotifyWrite()
	syncer.RunOnce(ctx)
	if head := headSHA(t, config.VaultPath); head != commitBefore {
		t.Fatalf("disabled syncer committed: %s -> %s", commitBefore, head)
	}
	if status := syncer.Status(); status.Enabled {
		t.Fatalf("status still enabled: %+v", status)
	}
}

func TestSyncerDivergedRemoteEmitsConflictOnceAndKeepsLocal(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	remote := newBareRemote(t)
	if _, err := git.SetRemote(ctx, remote, "main"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/base.md", "# Base\n")
	if _, err := git.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Push(ctx); err != nil {
		t.Fatal(err)
	}
	clone := cloneRemote(t, remote, "other-writer")
	writeGitTestFile(t, clone, "global/general/remote.md", "# Remote\n")
	testGitCommand(t, clone, "add", "-A")
	testGitCommand(t, clone, "commit", "-m", "remote side")
	testGitCommand(t, clone, "push", "origin", "main")
	writeGitTestFile(t, config.VaultPath, "global/general/local.md", "# Local\n")
	if _, err := git.Commit(ctx, "local side"); err != nil {
		t.Fatal(err)
	}
	localHead := headSHA(t, config.VaultPath)

	var events []SyncEvent
	syncer, _, _ := newTestSyncer(t, manager, func(event SyncEvent) { events = append(events, event) })
	markPreflightDone(syncer)
	syncer.RunOnce(ctx)

	if len(events) != 1 || events[0].Kind != SyncEventConflict {
		t.Fatalf("events = %+v, want one conflict", events)
	}
	if !strings.Contains(events[0].Detail, "本地") || !strings.Contains(events[0].Detail, "远端") {
		t.Fatalf("conflict detail is not actionable: %q", events[0].Detail)
	}
	status := syncer.Status()
	if !status.Paused || !status.Conflict || status.PauseReason != "conflict" {
		t.Fatalf("unexpected conflict status: %+v", status)
	}
	if head := headSHA(t, config.VaultPath); head != localHead {
		t.Fatalf("conflict handling moved local HEAD: %s -> %s", localHead, head)
	}
	// A real second pull attempt with the same pair must not emit again.
	syncer.ResetPause()
	syncer.RunOnce(ctx)
	if len(events) != 1 {
		t.Fatalf("same conflict pair emitted another event: %+v", events)
	}
	if status := syncer.Status(); !status.Paused || !status.Conflict || status.PauseReason != "conflict" {
		t.Fatalf("unchanged divergence did not pause again after reset: %+v", status)
	}
	if head := headSHA(t, config.VaultPath); head != localHead {
		t.Fatalf("repeated conflict handling moved local HEAD: %s -> %s", localHead, head)
	}
	// A genuinely new remote commit creates a new conflict pair and one more
	// event; local history is still untouched.
	writeGitTestFile(t, clone, "global/general/remote-two.md", "# Remote two\n")
	testGitCommand(t, clone, "add", "-A")
	testGitCommand(t, clone, "commit", "-m", "remote side two")
	testGitCommand(t, clone, "push", "origin", "main")
	syncer.ResetPause()
	syncer.RunOnce(ctx)
	if len(events) != 2 || events[1].Kind != SyncEventConflict {
		t.Fatalf("new conflict pair did not emit exactly once: %+v", events)
	}
	if head := headSHA(t, config.VaultPath); head != localHead {
		t.Fatalf("new conflict handling moved local HEAD: %s -> %s", localHead, head)
	}
}

func TestSyncerPushFailureBacksOffAndPauses(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	if _, err := git.SetRemote(ctx, "ssh://git@127.0.0.1:1/repo.git", "main"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/offline.md", "# Offline\n")
	if _, err := git.Commit(ctx, "offline"); err != nil {
		t.Fatal(err)
	}
	syncer, _, clock := newTestSyncer(t, manager, nil)
	markPreflightDone(syncer)

	syncer.RunOnce(ctx)
	first := syncer.Status()
	if first.Failures != 1 || first.Paused {
		t.Fatalf("first failure status: %+v", first)
	}
	clock.Advance(20 * time.Millisecond)
	syncer.RunOnce(ctx)
	second := syncer.Status()
	if !second.Paused || second.Failures != 2 || second.PauseReason == "" {
		t.Fatalf("second failure did not pause: %+v", second)
	}
	clock.Advance(time.Minute)
	syncer.RunOnce(ctx)
	if third := syncer.Status(); third.Failures != 2 {
		t.Fatalf("paused syncer kept retrying: %+v", third)
	}
}

func TestSyncerPreflightFailurePausesAndPreservesFiles(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	// Start from the pre-policy upgrade fixture so a failed preflight can be
	// checked to leave no half-written .gitattributes behind.
	if err := os.Remove(filepath.Join(config.VaultPath, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit(ctx, "drop policy for upgrade fixture"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "assets/logo.png", "binary-looking")
	commitBefore := headSHA(t, config.VaultPath)
	var events []SyncEvent
	syncer, _, _ := newTestSyncer(t, manager, func(event SyncEvent) { events = append(events, event) })
	syncer.RunOnce(ctx)

	status := syncer.Status()
	if !status.Paused || status.PauseReason != "preflight_failed" {
		t.Fatalf("unexpected preflight failure status: %+v", status)
	}
	if len(events) != 1 || events[0].Kind != SyncEventPaused {
		t.Fatalf("preflight failure events = %+v", events)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "assets/logo.png")); err != nil {
		t.Fatalf("blocked file was discarded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, ".gitattributes")); !os.IsNotExist(err) {
		t.Fatalf("preflight wrote the policy after failing: %v", err)
	}
	if head := headSHA(t, config.VaultPath); head != commitBefore {
		t.Fatalf("failed preflight committed: %s -> %s", commitBefore, head)
	}
}

func TestSyncerPreflightFailureExposesBlockedPathReasonAndPersists(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	if err := os.Remove(filepath.Join(config.VaultPath, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit(ctx, "drop policy for upgrade fixture"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/eligible.md", "# Eligible\n")
	// The broad front-door pattern treats a bare token assignment as secret
	// material. The file itself stays in the worktree; only path + rule are
	// reported.
	writeGitTestFile(t, config.VaultPath, "global/general/token-note.md", "# Note\n\ntoken = abcdefgh12345678\n")

	syncer, settings, _ := newTestSyncer(t, manager, nil)
	syncer.RunOnce(ctx)

	status := syncer.Status()
	if !status.Paused || status.PauseReason != "preflight_failed" {
		t.Fatalf("unexpected preflight failure status: %+v", status)
	}
	// This pause happens before the LF policy is touched. The stage must say so
	// instead of letting the settings panel blame the line-ending migration.
	if status.PreflightStage != PreflightStagePendingChanges {
		t.Fatalf("preflight stage = %q, want %q", status.PreflightStage, PreflightStagePendingChanges)
	}
	if status.Blocked != 1 || len(status.BlockedFiles) != 1 {
		t.Fatalf("blocked files not reported: %+v", status)
	}
	blocked := status.BlockedFiles[0]
	if blocked.Path != "global/general/token-note.md" || blocked.Reason != "secret" {
		t.Fatalf("blocked entry = %+v, want the token note with reason secret", blocked)
	}
	// The stable aggregate sentence stays as the prefix; the detail names the
	// exact path and rule so the owner can act without reading the vault.
	if !strings.Contains(status.LastError, "有 1 个改动无法自动提交（二进制/密钥/超大文件）") {
		t.Fatalf("aggregate prefix lost: %q", status.LastError)
	}
	if !strings.Contains(status.LastError, "token-note.md") || !strings.Contains(status.LastError, "secret") {
		t.Fatalf("last error does not name path/rule: %q", status.LastError)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, "global/general/token-note.md")); err != nil {
		t.Fatalf("blocked file was discarded: %v", err)
	}
	if dirty := testGitCommand(t, config.VaultPath, "status", "--porcelain", "--", "global/general/eligible.md"); dirty != "" {
		t.Fatalf("eligible text change was not committed before the pause:\n%s", dirty)
	}

	// The state file must be actionable after a restart, not just in memory.
	payload, err := os.ReadFile(filepath.Join(config.VaultPath, stateDirectory, AutoSyncStateFile))
	if err != nil {
		t.Fatal(err)
	}
	var saved autoSyncStateFile
	if err := json.Unmarshal(payload, &saved); err != nil {
		t.Fatalf("auto-sync state is not JSON: %v\n%s", err, payload)
	}
	if saved.PauseReason != "preflight_failed" || saved.BlockedTotal != 1 || len(saved.Blocked) != 1 {
		t.Fatalf("persisted blocked state = %+v", saved)
	}
	if saved.PreflightStage != PreflightStagePendingChanges {
		t.Fatalf("persisted preflight stage = %q", saved.PreflightStage)
	}
	if !saved.PendingKnown || saved.Pending != 1 {
		t.Fatalf("persisted pending = %+v, want a known count of 1", saved)
	}
	if saved.Blocked[0].Path != "global/general/token-note.md" || saved.Blocked[0].Reason != "secret" {
		t.Fatalf("persisted blocked entry = %+v", saved.Blocked[0])
	}

	reloaded := NewSyncer(manager, settings, SyncerConfig{Now: newTestClock().Now})
	restored := reloaded.Status()
	if !restored.Paused || restored.PauseReason != "preflight_failed" {
		t.Fatalf("reloaded pause state = %+v", restored)
	}
	if restored.Blocked != 1 || len(restored.BlockedFiles) != 1 || restored.BlockedFiles[0].Reason != "secret" {
		t.Fatalf("reloaded blocked state = %+v", restored)
	}
	if !restored.PendingKnown || restored.Pending != 1 {
		t.Fatalf("reloaded pending = %+v, want a known count of 1", restored)
	}
	if restored.PreflightStage != PreflightStagePendingChanges {
		t.Fatalf("reloaded preflight stage = %q", restored.PreflightStage)
	}
}

func TestSyncerLoadsLegacyStateWithoutBlockedFields(t *testing.T) {
	config, manager := newAutoSyncTestManager(t)
	statePath := filepath.Join(config.VaultPath, stateDirectory, AutoSyncStateFile)
	legacy := []byte("{\n  \"preflight_version\": 0,\n  \"pause_reason\": \"preflight_failed\",\n  \"last_error\": \"有 1 个改动无法自动提交（二进制/密钥/超大文件）；preflight 已暂停并保留现场\"\n}\n")
	if err := os.WriteFile(statePath, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	syncer := NewSyncer(manager, nil, SyncerConfig{Now: newTestClock().Now})
	status := syncer.Status()
	if !status.Paused || status.PauseReason != "preflight_failed" {
		t.Fatalf("legacy state did not load: %+v", status)
	}
	if status.Blocked != 0 || len(status.BlockedFiles) != 0 || status.Pending != 0 {
		t.Fatalf("legacy state invented blocked fields: %+v", status)
	}
	if status.PendingKnown {
		t.Fatalf("legacy state without a pending field reported a known zero: %+v", status)
	}
	if status.PreflightStage != "" {
		t.Fatalf("legacy state without blocked files invented a preflight stage: %+v", status)
	}
	if !strings.Contains(status.LastError, "有 1 个改动无法自动提交") {
		t.Fatalf("legacy last_error lost: %q", status.LastError)
	}
}

// A paused loop returns before scanning, so the persisted pause-time count is
// the only thing Status() can report. An upgraded state file has no pending
// field at all; rendering that as 0 told the owner there was nothing to
// commit while git status listed real changes. StatusLive fills the panel from
// one read-only scan and never commits or rewrites the state file.
func TestSyncerStatusLiveCountsPendingWhilePaused(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	statePath := filepath.Join(config.VaultPath, stateDirectory, AutoSyncStateFile)
	legacy := []byte("{\n  \"preflight_version\": 0,\n  \"pause_reason\": \"preflight_failed\",\n  \"last_error\": \"有 1 个改动无法自动提交（二进制/密钥/超大文件）；preflight 已暂停并保留现场\"\n}\n")
	if err := os.WriteFile(statePath, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	// Changes that appeared after the pause: two ordinary documents and one
	// credential-looking note that the commit gate still refuses.
	writeGitTestFile(t, config.VaultPath, "global/general/one.md", "# One\n\nbody\n")
	writeGitTestFile(t, config.VaultPath, "global/general/two.md", "# Two\n\nbody\n")
	writeGitTestFile(t, config.VaultPath, "global/general/token-note.md", "# Note\n\ntoken = abcdefgh12345678\n")

	syncer := NewSyncer(manager, nil, SyncerConfig{Now: newTestClock().Now})
	paused := syncer.Status()
	if !paused.Paused || paused.Pending != 0 || paused.PendingKnown {
		t.Fatalf("paused snapshot = %+v, want an unknown zero", paused)
	}
	headBefore := headSHA(t, config.VaultPath)
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	live := syncer.StatusLive(ctx)
	if live.Pending != 3 || !live.PendingKnown {
		t.Fatalf("live pending = %+v, want 3 known changes", live)
	}
	if live.Blocked != 1 || len(live.BlockedFiles) != 1 {
		t.Fatalf("live blocked = %+v, want the token note", live)
	}
	if blocked := live.BlockedFiles[0]; blocked.Path != "global/general/token-note.md" || blocked.Reason != "secret" {
		t.Fatalf("live blocked entry = %+v", blocked)
	}
	// Read-only: no commit, no state rewrite, and Status() keeps the pause-time
	// snapshot for anything that is not the settings render.
	if head := headSHA(t, config.VaultPath); head != headBefore {
		t.Fatalf("StatusLive committed: %s -> %s", headBefore, head)
	}
	stateAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(stateAfter) != string(stateBefore) {
		t.Fatalf("StatusLive rewrote the state file:\n%s\n---\n%s", stateBefore, stateAfter)
	}
	if after := syncer.Status(); after.Pending != 0 || after.PendingKnown {
		t.Fatalf("Status changed after a live render: %+v", after)
	}
}

// A Windows host usually has core.autocrlf=true in the global git config. An
// upgraded vault without the policy file must still finish the LF preflight
// instead of pausing on phantom line-ending changes.
func TestSyncerPreflightLFVaultIgnoresGlobalAutocrlf(t *testing.T) {
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[core]\n\tautocrlf = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	// Upgrade fixture: no policy file and no repo-local autocrlf pin, like the
	// Windows vault that paused.
	if err := os.Remove(filepath.Join(config.VaultPath, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	testGitCommand(t, config.VaultPath, "config", "--local", "--unset", "core.autocrlf")
	writeGitTestFile(t, config.VaultPath, "global/general/one.md", "# One\n\nbody\n")
	writeGitTestFile(t, config.VaultPath, "global/general/two.md", "# Two\n\nbody\n")
	if _, err := git.Commit(ctx, "drop policy for upgrade fixture"); err != nil {
		t.Fatal(err)
	}
	// Tracked LF edits: git prints "LF will be replaced by CRLF" for each
	// modified file while diffing under autocrlf=true.
	writeGitTestFile(t, config.VaultPath, "global/general/one.md", "# One\n\nLF body\n")
	writeGitTestFile(t, config.VaultPath, "global/general/two.md", "# Two\n\nLF body\n")

	syncer, _, _ := newTestSyncer(t, manager, nil)
	syncer.RunOnce(ctx)

	status := syncer.Status()
	if status.Paused {
		t.Fatalf("autocrlf=true paused an LF vault: reason=%q error=%q", status.PauseReason, status.LastError)
	}
	if !status.PreflightDone {
		t.Fatalf("preflight did not finish under autocrlf=true: %+v", status)
	}
	if configured := testGitCommand(t, config.VaultPath, "config", "--local", "--get", "core.autocrlf"); configured != "false" {
		t.Fatalf("repo-local core.autocrlf = %q, want false", configured)
	}
	attributes, err := os.ReadFile(filepath.Join(config.VaultPath, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(attributes), "* text=auto eol=lf") {
		t.Fatalf(".gitattributes lost the LF policy:\n%s", attributes)
	}
	for _, path := range []string{"global/general/one.md", "global/general/two.md"} {
		blob := gitOutputBytes(t, config.VaultPath, "show", "HEAD:"+path)
		if strings.Contains(string(blob), "\r\n") {
			t.Fatalf("%s committed CRLF line endings", path)
		}
	}
	// The only line-ending commit allowed here installs the policy; an all-LF
	// vault must not be reported as renormalized.
	if message := testGitCommand(t, config.VaultPath, "log", "-1", "--format=%s"); strings.Contains(message, "Normalize") {
		t.Fatalf("all-LF vault was reported as a line-ending migration: %q", message)
	}
	if status.PreflightStage != "" {
		t.Fatalf("successful preflight kept a failure stage: %+v", status)
	}
}

// When the LF policy itself cannot be written, the pause must be attributed to
// the line-ending step. That is the only case where "行尾迁移预检失败" is true.
func TestSyncerPreflightLineEndingFailureReportsStage(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	// Upgrade fixture: no policy file. A directory at that path makes the
	// policy write fail without touching any document content.
	if err := os.Remove(filepath.Join(config.VaultPath, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit(ctx, "drop policy for upgrade fixture"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(config.VaultPath, ".gitattributes"), 0o755); err != nil {
		t.Fatal(err)
	}

	syncer, _, _ := newTestSyncer(t, manager, nil)
	syncer.RunOnce(ctx)

	status := syncer.Status()
	if !status.Paused || status.PauseReason != "preflight_failed" {
		t.Fatalf("unexpected pause status: %+v", status)
	}
	if status.PreflightStage != PreflightStageLineEndings {
		t.Fatalf("preflight stage = %q, want %q", status.PreflightStage, PreflightStageLineEndings)
	}
	if status.PendingKnown {
		t.Fatalf("line-ending failure skipped the scan but reported a known pending count: %+v", status)
	}
}

func TestSyncerSingleFlightCommitsOneBatch(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	syncer, _, clock := newTestSyncer(t, manager, nil)
	markPreflightDone(syncer)
	for index := 0; index < 5; index++ {
		writeGitTestFile(t, config.VaultPath, "inbox/"+string(rune('a'+index))+".md", "# File\n")
	}
	commitBefore := headSHA(t, config.VaultPath)
	syncer.RunOnce(ctx) // establish fingerprint
	clock.Advance(31 * time.Second)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			syncer.RunOnce(ctx)
		}()
	}
	wait.Wait()
	if count := testGitCommand(t, config.VaultPath, "rev-list", "--count", commitBefore+"..HEAD"); count != "1" {
		t.Fatalf("concurrent cycles made %s commits, want 1", count)
	}
	if dirty := testGitCommand(t, config.VaultPath, "status", "--porcelain"); dirty != "" {
		t.Fatalf("worktree dirty after single-flight commit:\n%s", dirty)
	}
}

func TestSyncerPushesLocalCommitsWithBlockedFiles(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	remote := newBareRemote(t)
	if _, err := git.SetRemote(ctx, remote, "main"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/base.md", "# Base\n")
	if _, err := git.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Push(ctx); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/local.md", "# Local\n")
	if _, err := git.Commit(ctx, "local commit"); err != nil {
		t.Fatal(err)
	}
	localHead := headSHA(t, config.VaultPath)
	writeGitTestFile(t, config.VaultPath, ".env", "TOKEN=secret\n")
	syncer, _, _ := newTestSyncer(t, manager, nil)
	markPreflightDone(syncer)
	syncer.RunOnce(ctx)

	if remoteHead := testGitCommand(t, remote, "rev-parse", "refs/heads/main"); remoteHead != localHead {
		t.Fatalf("blocked file prevented pushing local commits: remote=%s local=%s", remoteHead, localHead)
	}
	status := syncer.Status()
	if status.Paused || status.Blocked != 1 || status.LastSyncAt == "" {
		t.Fatalf("unexpected status after blocked-file push: %+v", status)
	}

	// A remote file colliding with the untracked blocked file makes pull fail
	// safely; the syncer must record it, stay unpaused, and still push.
	clone := cloneRemote(t, remote, "remote-writer")
	writeGitTestFile(t, clone, ".env", "REMOTE=1\n")
	testGitCommand(t, clone, "add", "-f", ".env")
	testGitCommand(t, clone, "commit", "-m", "remote env")
	testGitCommand(t, clone, "push", "origin", "main")
	syncer.RunOnce(ctx)
	status = syncer.Status()
	if status.Paused || status.Failures != 0 {
		t.Fatalf("safe pull refusal paused the syncer: %+v", status)
	}
	if !strings.Contains(status.LastError, "拉取被本地文件阻止") {
		t.Fatalf("safe pull refusal was not reported: %+v", status)
	}
	if head := headSHA(t, config.VaultPath); head != localHead {
		t.Fatalf("safe pull refusal moved local HEAD: %s -> %s", localHead, head)
	}
	if _, err := os.Stat(filepath.Join(config.VaultPath, ".env")); err != nil {
		t.Fatalf("pull overwrote the blocked local file: %v", err)
	}
}

func TestBlockedLogSummarySanitizesControlCharacters(t *testing.T) {
	summary := blockedLogSummary([]BlockedChange{{Path: "bad\nname\r\t.md", Reason: "invalid_path"}})
	if strings.ContainsAny(summary, "\n\r\t") {
		t.Fatalf("blocked log summary kept control characters: %q", summary)
	}
	if !strings.Contains(summary, "badname.md(invalid_path)") {
		t.Fatalf("blocked log summary lost the path: %q", summary)
	}
}

func TestSyncerPushRejectionWithAncestorLocalDoesNotConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("git hooks need an executable bit on Windows")
	}
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	remote := newBareRemote(t)
	if _, err := git.SetRemote(ctx, remote, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Push(ctx); err != nil {
		t.Fatal(err)
	}
	clone := cloneRemote(t, remote, "hook-writer")
	// The pre-push hook advances the remote after the sync's pull succeeded
	// and before its push lands, reproducing the rejection race.
	hookPath := filepath.Join(config.VaultPath, ".git", "hooks", "pre-push")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := fmt.Sprintf("#!/bin/sh\nset -e\ncd %q\nprintf 'advanced\\n' > remote-advance.md\ngit add -A\ngit -c user.name=Hook -c user.email=hook@example.com commit -q -m advance\ngit push -q origin main\necho 'rejected by pre-push hook' >&2\nexit 1\n", clone)
	if err := os.WriteFile(hookPath, []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	localHead := headSHA(t, config.VaultPath)
	var events []SyncEvent
	syncer, _, _ := newTestSyncer(t, manager, func(event SyncEvent) { events = append(events, event) })
	markPreflightDone(syncer)
	syncer.RunOnce(ctx)

	if len(events) != 0 {
		t.Fatalf("ancestor push rejection emitted a conflict event: %+v", events)
	}
	status := syncer.Status()
	if status.Paused || status.Conflict || status.PauseReason != "" {
		t.Fatalf("ancestor push rejection paused the syncer: %+v", status)
	}
	if !strings.Contains(status.LastError, "远端已前进") {
		t.Fatalf("ancestor push rejection was not reported: %+v", status)
	}
	if head := headSHA(t, config.VaultPath); head != localHead {
		t.Fatalf("safe rejection moved local HEAD: %s -> %s", localHead, head)
	}
	if remoteHead, local := testGitCommand(t, remote, "rev-parse", "refs/heads/main"), headSHA(t, config.VaultPath); remoteHead == local {
		t.Fatal("hook did not advance the remote before the rejected push")
	}
}

func TestRenormalizeMissingBackupTagIsWarning(t *testing.T) {
	ctx := context.Background()
	config, git := newGitTestVault(t)
	testGitCommand(t, config.VaultPath, "init", "-b", "main")
	testGitCommand(t, config.VaultPath, "config", "core.autocrlf", "false")
	writeGitTestFile(t, config.VaultPath, "global/general/crlf.md", "# CRLF\r\n\r\nbody\r\n")
	testGitCommand(t, config.VaultPath, "add", "-A")
	testGitCommand(t, config.VaultPath, "commit", "-m", "crlf baseline")
	// The auto-sync preflight no longer renormalizes a vault with CRLF blobs
	// (it reports the count instead), so the explicit migration entry point is
	// covered directly here. The existing policy file is never rewritten.
	writeGitTestFile(t, config.VaultPath, ".gitattributes", GitAttributesContent())
	// A broken ref makes `git tag` fail while `rev-parse --verify` also fails,
	// so Renormalize records the backup-tag warning and still commits.
	brokenRef := filepath.Join(config.VaultPath, ".git", "refs", "tags", lineEndingBackupTag)
	if err := os.WriteFile(brokenRef, []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := git.Renormalize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Warning, "备份 tag 创建失败") {
		t.Fatalf("tag warning missing from renormalize result: %+v", result)
	}
	if !result.Committed {
		t.Fatalf("tag warning aborted the migration: %+v", result)
	}
	state, err := os.ReadFile(filepath.Join(config.VaultPath, ".aha", lineEndingStateFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), "备份 tag 创建失败") {
		t.Fatalf("line-ending state lost the warning: %s", state)
	}
}

func TestSyncerCallsRecoveredOnlyAfterSuccessfulSync(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	remote := newBareRemote(t)
	if _, err := git.SetRemote(ctx, remote, "main"); err != nil {
		t.Fatal(err)
	}
	writeGitTestFile(t, config.VaultPath, "global/general/base.md", "# Base\n")
	if _, err := git.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Push(ctx); err != nil {
		t.Fatal(err)
	}
	// A second writer advances the remote while the local repository also
	// commits, so the pair genuinely diverges.
	clone := cloneRemote(t, remote, "other-writer")
	writeGitTestFile(t, clone, "global/general/remote.md", "# Remote\n")
	testGitCommand(t, clone, "add", "-A")
	testGitCommand(t, clone, "commit", "-m", "remote side")
	testGitCommand(t, clone, "push", "origin", "main")
	writeGitTestFile(t, config.VaultPath, "global/general/local.md", "# Local\n")
	if _, err := git.Commit(ctx, "local side"); err != nil {
		t.Fatal(err)
	}

	recovered := 0
	settings := &testAutoSyncSettings{enabled: true}
	clock := newTestClock()
	syncer := NewSyncer(manager, settings, SyncerConfig{
		Now: clock.Now, QuietPeriod: 30 * time.Second, WriteDebounce: time.Second,
		RemoteInterval: time.Minute, RetryBase: 10 * time.Millisecond, RetryMax: time.Second,
		MaxFailures: 2, Recovered: func() { recovered++ },
	})
	if err := syncer.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	markPreflightDone(syncer)
	syncer.RunOnce(ctx)
	if status := syncer.Status(); !status.Paused || !status.Conflict {
		t.Fatalf("expected a conflict pause, status=%+v", status)
	}
	if recovered != 0 {
		t.Fatalf("conflict cycle reported recovery %d times", recovered)
	}

	// The owner resolves the divergence outside AHA2, then the next cycle
	// succeeds and the host is told the notification is stale.
	testGitCommand(t, config.VaultPath, "fetch", "origin", "main")
	testGitCommand(t, config.VaultPath, "merge", "--no-edit", "origin/main")
	syncer.ResetPause()
	clock.Advance(time.Minute)
	syncer.RunOnce(ctx)
	if status := syncer.Status(); status.Paused || status.Conflict {
		t.Fatalf("sync did not recover: %+v", status)
	}
	if recovered == 0 {
		t.Fatal("successful sync did not close stale notifications")
	}
}
