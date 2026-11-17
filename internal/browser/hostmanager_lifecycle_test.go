package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// crashChild terminates the newest fake host and waits until the manager has
// either spawned a replacement or given up.
func crashChild(t *testing.T, recorder *launchRecorder, manager *HostManager) bool {
	t.Helper()
	specs, children := recorder.snapshot()
	if len(children) == 0 {
		t.Fatal("no fake child to crash")
	}
	before := len(specs)
	children[len(children)-1].Exit(errors.New("crash"))
	waitFor(t, 3*time.Second, func() bool {
		specs, _ := recorder.snapshot()
		if len(specs) > before {
			return true
		}
		_, err := manager.Health(context.Background())
		return Code(err) == "browser_host_start_failed"
	})
	specs, _ = recorder.snapshot()
	return len(specs) > before
}

// TestHostManagerCrashLoopHonorsRestartBudget repeatedly waits for a ready
// host and crashes it. MaxRestarts bounds the whole unstable run, not just one
// restart() invocation, and the delays must grow exponentially.
func TestHostManagerCrashLoopHonorsRestartBudget(t *testing.T) {
	recorder := &launchRecorder{}
	manager, _ := managerFixture(t, recorder, func(options *HostManagerOptions) {
		options.MaxRestarts = 3
		options.RestartDelay = 50 * time.Millisecond
		options.StableResetAfter = time.Hour
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	for crashes := 0; crashes < 10; crashes++ {
		var lastErr error
		waitFor(t, 3*time.Second, func() bool {
			_, lastErr = manager.Health(ctx)
			return lastErr == nil || Code(lastErr) == "browser_host_start_failed"
		})
		if Code(lastErr) == "browser_host_start_failed" {
			break
		}
		if !crashChild(t, recorder, manager) {
			break
		}
	}
	specs, _ := recorder.snapshot()
	if len(specs) != manager.opts.MaxRestarts+1 {
		t.Fatalf("launches=%d, want initial + %d restarts", len(specs), manager.opts.MaxRestarts)
	}
	times := recorder.spawnTimes()
	if len(times) != len(specs) {
		t.Fatalf("spawn timestamps=%d, launches=%d", len(times), len(specs))
	}
	expected := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond}
	for index, want := range expected {
		gap := times[index+1].Sub(times[index])
		if gap < want*7/10 {
			t.Fatalf("restart %d waited %v, want at least about %v", index+1, gap, want)
		}
	}
	if _, err := manager.Health(ctx); Code(err) != "browser_host_start_failed" {
		t.Fatalf("health after exhausted crash budget=%v", err)
	}
	_ = manager.Stop(ctx)
}

// TestHostManagerRestartBudgetResetsAfterStablePeriod proves the inverse: once
// the replacement has stayed up for StableResetAfter, a later crash gets a
// fresh budget instead of inheriting the previous count.
func TestHostManagerRestartBudgetResetsAfterStablePeriod(t *testing.T) {
	recorder := &launchRecorder{}
	manager, _ := managerFixture(t, recorder, func(options *HostManagerOptions) {
		options.MaxRestarts = 1
		options.RestartDelay = 10 * time.Millisecond
		options.StableResetAfter = 40 * time.Millisecond
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !crashChild(t, recorder, manager) {
		t.Fatal("first crash did not get its allowed restart")
	}
	waitFor(t, 3*time.Second, func() bool {
		_, err := manager.Health(ctx)
		return err == nil
	})
	// Let markStable clear the budget before the next crash.
	time.Sleep(120 * time.Millisecond)
	children := func() []*fakeChild {
		_, items := recorder.snapshot()
		return items
	}()
	children[len(children)-1].Exit(errors.New("crash"))
	waitFor(t, 3*time.Second, func() bool {
		specs, _ := recorder.snapshot()
		if len(specs) < 3 {
			return false
		}
		_, err := manager.Health(ctx)
		return err == nil
	})
	if specs, _ := recorder.snapshot(); len(specs) != 3 {
		t.Fatalf("launches=%d, want initial + 2 restarts", len(specs))
	}
	_ = manager.Stop(ctx)
}

// TestHostManagerBusinessCallsSurviveSlowNavigation is the regression for the
// 5s business-client timeout: a 5.5s navigation must not be reported as a
// browser_host_timeout while the host may already have committed it.
func TestHostManagerBusinessCallsSurviveSlowNavigation(t *testing.T) {
	recorder := &launchRecorder{navigateDelay: 5500 * time.Millisecond}
	manager, _ := managerFixture(t, recorder, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	started := time.Now()
	tab, err := manager.Navigate(ctx, "browser_profile_1", "session_1", "tab_1", "https://example.com/next", "fence-1")
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("slow navigation failed after %v: %v", elapsed, err)
	}
	if elapsed < 5500*time.Millisecond {
		t.Fatalf("navigation returned after %v, before the fake host answered", elapsed)
	}
	if tab.URL != "https://example.com/final" {
		t.Fatalf("tab=%#v", tab)
	}
	_ = manager.Stop(ctx)
}

// TestHostManagerReadinessProbeStaysShort keeps the probe bounded: a host that
// answers health only after 6s must fail startup near ReadyTimeout, not block
// for the 60s business timeout (or silently succeed once health finally lands).
func TestHostManagerReadinessProbeStaysShort(t *testing.T) {
	recorder := &launchRecorder{healthDelay: 6 * time.Second}
	manager, _ := managerFixture(t, recorder, func(options *HostManagerOptions) {
		options.ReadyTimeout = 500 * time.Millisecond
		options.PortAttempts = 1
	})
	started := time.Now()
	err := manager.Start(context.Background())
	if Code(err) != "browser_host_start_failed" {
		t.Fatalf("start error=%v, want browser_host_start_failed", err)
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("readiness probe waited %v; it must stay on the short probe timeout", elapsed)
	}
}

// TestDiscoverNodeFallsBackToCommonPaths models a Windows machine where the
// packaged runtime is missing, PATH has no Node, but %ProgramFiles%\nodejs
// exists. The packaged-path candidate being absent must not suppress the
// common install-location candidates.
func TestDiscoverNodeFallsBackToCommonPaths(t *testing.T) {
	executableDir := t.TempDir() // no node/node.exe under the install dir
	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)
	t.Setenv("AHA2_NODE_PATH", "")
	common := filepath.Join(t.TempDir(), "node.exe")
	if err := os.WriteFile(common, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	node, err := discoverNodeWith("", executableDir, []string{common})
	if err != nil || node != common {
		t.Fatalf("fallback runtime=%q err=%v, want %q", node, err, common)
	}
}

func TestDiscoverNodeExplicitOverrideFailsClosed(t *testing.T) {
	executableDir := t.TempDir()
	bundled := filepath.Join(executableDir, "node", "node.exe")
	if err := os.MkdirAll(filepath.Dir(bundled), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundled, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	common := filepath.Join(t.TempDir(), "node.exe")
	if err := os.WriteFile(common, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing-node")
	t.Setenv("AHA2_NODE_PATH", "")
	if _, err := discoverNodeWith(missing, executableDir, []string{common}); err == nil {
		t.Fatal("invalid explicit override fell back to a working runtime")
	}
	t.Setenv("AHA2_NODE_PATH", missing)
	if _, err := discoverNode("", executableDir); err == nil {
		t.Fatal("invalid AHA2_NODE_PATH fell back to a working runtime")
	}
}

func TestWindowsNodeCandidatesCoverInstallLocations(t *testing.T) {
	programFiles := "/Program Files"
	programFilesX86 := "/Program Files (x86)"
	localAppData := "/Users/owner/AppData/Local"
	got := windowsNodeCandidates(programFiles, programFilesX86, localAppData)
	want := []string{
		filepath.Join(programFiles, "Programs", "nodejs", "node.exe"),
		filepath.Join(programFiles, "nodejs", "node.exe"),
		filepath.Join(programFilesX86, "Programs", "nodejs", "node.exe"),
		filepath.Join(programFilesX86, "nodejs", "node.exe"),
		filepath.Join(localAppData, "Programs", "nodejs", "node.exe"),
		filepath.Join(localAppData, "nodejs", "node.exe"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("windows candidates=%v, want %v", got, want)
	}
}

func TestDiscoverNodePrefersPackagedRuntime(t *testing.T) {
	executableDir := t.TempDir()
	packaged := filepath.Join(executableDir, "node", "node.exe")
	if err := os.MkdirAll(filepath.Dir(packaged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packaged, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AHA2_NODE_PATH", "")
	node, err := discoverNode("", executableDir)
	if err != nil || node != packaged {
		t.Fatalf("packaged runtime=%q err=%v, want %q", node, err, packaged)
	}

	override := filepath.Join(t.TempDir(), "node-override")
	if err := os.WriteFile(override, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	node, err = discoverNode(override, executableDir)
	if err != nil || node != override {
		t.Fatalf("explicit runtime=%q err=%v, want %q", node, err, override)
	}
	t.Setenv("AHA2_NODE_PATH", override)
	node, err = discoverNode("", executableDir)
	if err != nil || node != override {
		t.Fatalf("AHA2_NODE_PATH runtime=%q err=%v, want %q", node, err, override)
	}
}
