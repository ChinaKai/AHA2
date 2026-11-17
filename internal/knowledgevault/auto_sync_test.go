package knowledgevault

import (
	"context"
	"os"
	"path/filepath"
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

func TestSyncerPreflightCommitsExternalChangesThenNormalizesOnce(t *testing.T) {
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

	commitBefore := headSHA(t, config.VaultPath)
	var events []SyncEvent
	syncer, _, _ := newTestSyncer(t, manager, func(event SyncEvent) { events = append(events, event) })
	syncer.RunOnce(ctx)

	headAfter := headSHA(t, config.VaultPath)
	if headAfter == commitBefore {
		t.Fatal("preflight made no commit")
	}
	if count := testGitCommand(t, config.VaultPath, "rev-list", "--count", commitBefore+"..HEAD"); count != "2" {
		t.Fatalf("preflight commits = %s, want 2 (external change + rename normalize)", count)
	}
	if message := testGitCommand(t, config.VaultPath, "log", "-1", "--format=%s"); !strings.Contains(message, "Normalize knowledge vault line endings") {
		t.Fatalf("last preflight commit = %q", message)
	}
	blob := gitOutputBytes(t, config.VaultPath, "show", "HEAD:global/general/crlf.md")
	if strings.Contains(string(blob), "\r\n") {
		t.Fatalf("preflight did not normalize the CRLF blob: %q", blob)
	}
	if dirty := testGitCommand(t, config.VaultPath, "status", "--porcelain"); dirty != "" {
		t.Fatalf("worktree dirty after preflight:\n%s", dirty)
	}
	status := syncer.Status()
	if !status.PreflightDone || status.Paused {
		t.Fatalf("unexpected status after preflight: %+v", status)
	}
	if len(events) != 0 {
		t.Fatalf("clean preflight emitted events: %+v", events)
	}
	if _, err := git.ScanAutoCommit(ctx); err != nil {
		t.Fatal(err)
	}
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
