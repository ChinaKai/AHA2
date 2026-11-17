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
	if !strings.Contains(status.LastError, "有 1 个改动无法自动提交") {
		t.Fatalf("legacy last_error lost: %q", status.LastError)
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

func TestSyncerTagWarningSurvivesSuccessfulSync(t *testing.T) {
	ctx := context.Background()
	config, manager := newAutoSyncTestManager(t)
	git := manager.Git()
	remote := newBareRemote(t)
	if _, err := git.SetRemote(ctx, remote, "main"); err != nil {
		t.Fatal(err)
	}
	// Pre-policy fixture: CRLF baseline without .gitattributes.
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
	// A broken ref makes `git tag` fail while `rev-parse --verify` also fails,
	// so Renormalize records the backup-tag warning and still commits.
	brokenRef := filepath.Join(config.VaultPath, ".git", "refs", "tags", lineEndingBackupTag)
	if err := os.WriteFile(brokenRef, []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	syncer, _, _ := newTestSyncer(t, manager, nil)
	syncer.RunOnce(ctx)

	status := syncer.Status()
	if !strings.Contains(status.Warning, "备份 tag 创建失败") {
		t.Fatalf("tag warning missing from status: %+v", status)
	}
	if status.LastError != "" {
		t.Fatalf("successful sync did not clear last_error: %+v", status)
	}
	if status.Paused {
		t.Fatalf("tag warning paused the syncer: %+v", status)
	}
	state, err := os.ReadFile(filepath.Join(config.VaultPath, ".aha", lineEndingStateFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), "备份 tag 创建失败") {
		t.Fatalf("line-ending state lost the warning: %s", state)
	}
	if remoteHead, local := testGitCommand(t, remote, "rev-parse", "refs/heads/main"), headSHA(t, config.VaultPath); remoteHead != local {
		t.Fatalf("preflight sync did not push: remote=%s local=%s", remoteHead, local)
	}
}
