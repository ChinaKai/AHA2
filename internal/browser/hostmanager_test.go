package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeChild struct {
	spec   LaunchSpec
	server *http.Server
	exit   chan struct{}
	once   sync.Once
	err    error
	killed bool
}

func (p *fakeChild) Exit(err error) {
	p.once.Do(func() {
		p.err = err
		_ = p.server.Close()
		close(p.exit)
	})
}

func (p *fakeChild) Wait() error {
	<-p.exit
	return p.err
}

func (p *fakeChild) Kill() error {
	p.killed = true
	p.Exit(errors.New("killed"))
	return nil
}

func (p *fakeChild) PID() int { return 0 }

type launchRecorder struct {
	mu            sync.Mutex
	specs         []LaunchSpec
	children      []*fakeChild
	spawnedAt     []time.Time
	failFor       int
	rejectToken   bool
	healthDelay   time.Duration
	navigateDelay time.Duration
}

func (r *launchRecorder) spawn(_ context.Context, spec LaunchSpec) (HostProcess, error) {
	r.mu.Lock()
	attempt := len(r.specs)
	r.specs = append(r.specs, spec)
	r.spawnedAt = append(r.spawnedAt, time.Now())
	fail := attempt < r.failFor
	r.mu.Unlock()
	if fail {
		return nil, fmt.Errorf("simulated spawn failure")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", spec.Port))
	if err != nil {
		return nil, err
	}
	child := &fakeChild{spec: spec, exit: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(writer http.ResponseWriter, request *http.Request) {
		expected := spec.Token
		if r.rejectToken {
			expected = "not-the-issued-token"
		}
		if request.Header.Get("Authorization") != "Bearer "+expected {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.healthDelay > 0 {
			select {
			case <-request.Context().Done():
				return
			case <-time.After(r.healthDelay):
			}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"host_id": "local", "version": "fake", "platform": "test", "edge_available": true,
		})
	})
	mux.HandleFunc("/v1/profiles/", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/navigate") {
			if r.navigateDelay > 0 {
				select {
				case <-request.Context().Done():
					return
				case <-time.After(r.navigateDelay):
				}
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"tab": map[string]string{
				"id": "tab_1", "url": "https://example.com/final", "title": "Final",
			}})
			return
		}
		writer.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/v1/shutdown", func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"ok":true}`))
		go child.Exit(nil)
	})
	child.server = &http.Server{Handler: mux}
	go func() { _ = child.server.Serve(listener) }()
	r.mu.Lock()
	r.children = append(r.children, child)
	r.mu.Unlock()
	return child, nil
}

func (r *launchRecorder) snapshot() ([]LaunchSpec, []*fakeChild) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]LaunchSpec(nil), r.specs...), append([]*fakeChild(nil), r.children...)
}

func (r *launchRecorder) spawnTimes() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.spawnedAt...)
}

func managerFixture(t *testing.T, recorder *launchRecorder, tweak func(*HostManagerOptions)) (*HostManager, string) {
	t.Helper()
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	script := filepath.Join(dir, "browser-host", "host.mjs")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("// fake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	options := HostManagerOptions{
		Script: script, Node: node, DataDir: filepath.Join(dir, "profiles"), HostID: "local",
		ReadyTimeout: 2 * time.Second, StopGrace: 300 * time.Millisecond,
		RestartDelay: 10 * time.Millisecond, StableResetAfter: time.Hour,
		MaxRestarts: 3, PortAttempts: 3,
		Logger: discardLogger(), Spawn: recorder.spawn,
	}
	if tweak != nil {
		tweak(&options)
	}
	return NewHostManager(options), script
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before the deadline")
}

func TestHostManagerStartsAndStopsTheChild(t *testing.T) {
	recorder := &launchRecorder{}
	manager, _ := managerFixture(t, recorder, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	health, err := manager.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.HostID != "local" || !health.EdgeAvailable {
		t.Fatalf("health=%#v", health)
	}
	specs, children := recorder.snapshot()
	if len(specs) != 1 || len(children) != 1 {
		t.Fatalf("spawned %d/%d, want one child", len(specs), len(children))
	}
	spec := specs[0]
	if spec.Port <= 0 || spec.Port > 65535 {
		t.Fatalf("port=%d", spec.Port)
	}
	if len(spec.Token) != 64 || strings.ToLower(spec.Token) != spec.Token {
		t.Fatalf("token is not a 256-bit hex string: %q", spec.Token)
	}
	if spec.DataDir == "" || spec.HostID != "local" {
		t.Fatalf("spec=%#v", spec)
	}

	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !children[0].killed {
		select {
		case <-children[0].exit:
		default:
			t.Fatalf("child is still running after Stop: %#v", children[0])
		}
	}
	if _, err := manager.Health(ctx); Code(err) != "browser_host_not_ready" {
		t.Fatalf("health after stop: %v", err)
	}
	// A clean stop must not be mistaken for a crash.
	time.Sleep(60 * time.Millisecond)
	if specs, _ := recorder.snapshot(); len(specs) != 1 {
		t.Fatalf("host was restarted after stop: %d launches", len(specs))
	}
}

func TestHostManagerRestartsWithFreshToken(t *testing.T) {
	recorder := &launchRecorder{}
	manager, _ := managerFixture(t, recorder, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	firstSpecs, firstChildren := recorder.snapshot()
	firstChildren[0].Exit(errors.New("crash"))
	waitFor(t, 3*time.Second, func() bool {
		specs, _ := recorder.snapshot()
		if len(specs) < 2 {
			return false
		}
		_, err := manager.Health(ctx)
		return err == nil
	})
	specs, _ := recorder.snapshot()
	if specs[1].Token == firstSpecs[0].Token {
		t.Fatal("restart reused the previous host token")
	}
	if specs[1].Script != firstSpecs[0].Script || specs[1].Node != firstSpecs[0].Node {
		t.Fatal("restart did not reuse the discovered host")
	}
	_ = manager.Stop(ctx)
}

func TestHostManagerRetriesPortAllocationAndFailsClosed(t *testing.T) {
	recorder := &launchRecorder{failFor: 2}
	manager, _ := managerFixture(t, recorder, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start after transient failures: %v", err)
	}
	if specs, _ := recorder.snapshot(); len(specs) != 3 {
		t.Fatalf("launch attempts=%d, want 3", len(specs))
	}
	_ = manager.Stop(ctx)

	failing := &launchRecorder{failFor: 100}
	closedManager, _ := managerFixture(t, failing, nil)
	if err := closedManager.Start(ctx); Code(err) != "browser_host_start_failed" {
		t.Fatalf("start error=%v, want browser_host_start_failed", err)
	}
	if _, err := closedManager.Health(ctx); Code(err) != "browser_host_start_failed" {
		t.Fatalf("health after failed start=%v", err)
	}
	if specs, _ := failing.snapshot(); len(specs) != 3 {
		t.Fatalf("failed launches=%d, want PortAttempts", len(specs))
	}
}

// exitedProcess models a host that dies before it ever listens. Waiting must
// report the exit instead of blocking readiness against a dead port.
type exitedProcess struct{}

func (exitedProcess) Wait() error { return errors.New("exited before listening") }
func (exitedProcess) Kill() error { return nil }
func (exitedProcess) PID() int    { return 0 }

func TestHostManagerFailsFastWhenTheChildExitsDuringStartup(t *testing.T) {
	manager, _ := managerFixture(t, &launchRecorder{}, func(options *HostManagerOptions) {
		options.ReadyTimeout = 5 * time.Second
		options.Spawn = func(context.Context, LaunchSpec) (HostProcess, error) {
			return exitedProcess{}, nil
		}
	})
	started := time.Now()
	if err := manager.Start(context.Background()); Code(err) != "browser_host_start_failed" {
		t.Fatalf("start error=%v, want browser_host_start_failed", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("a dead child burned the readiness timeout: %v", elapsed)
	}
}

func TestHostManagerStopDuringStartupKillsTheLateChild(t *testing.T) {
	recorder := &launchRecorder{}
	entered := make(chan struct{})
	release := make(chan struct{})
	manager, _ := managerFixture(t, recorder, func(options *HostManagerOptions) {
		options.ReadyTimeout = 5 * time.Second
		options.Spawn = func(ctx context.Context, spec LaunchSpec) (HostProcess, error) {
			close(entered)
			<-release
			return recorder.spawn(ctx, spec)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.Start(ctx) }()
	<-entered
	// Stop sees no process yet; the child appears afterwards and must still be
	// killed by the manager instead of outliving AHA.
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("start: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start did not return after stop")
	}
	_, children := recorder.snapshot()
	if len(children) != 1 {
		t.Fatalf("children=%d, want 1", len(children))
	}
	waitFor(t, time.Second, func() bool {
		select {
		case <-children[0].exit:
			return true
		default:
			return false
		}
	})
	if _, err := manager.Health(ctx); Code(err) != "browser_host_not_ready" {
		t.Fatalf("health after stop-during-start: %v", err)
	}
}

func TestHostManagerReportsMissingNodeAndScript(t *testing.T) {
	recorder := &launchRecorder{}
	manager, _ := managerFixture(t, recorder, func(options *HostManagerOptions) {
		options.Node = filepath.Join(t.TempDir(), "missing-node")
	})
	if err := manager.Start(context.Background()); Code(err) != "browser_host_node_missing" {
		t.Fatalf("missing node: %v", err)
	}
	if specs, _ := recorder.snapshot(); len(specs) != 0 {
		t.Fatal("manager spawned without a Node executable")
	}

	missingScript, _ := managerFixture(t, recorder, func(options *HostManagerOptions) {
		options.Script = filepath.Join(t.TempDir(), "missing-host.mjs")
	})
	if err := missingScript.Start(context.Background()); Code(err) != "browser_host_script_missing" {
		t.Fatalf("missing script: %v", err)
	}
}

func TestHostTokenIsRandomPerCall(t *testing.T) {
	first, err := newHostToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newHostToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) != 64 {
		t.Fatalf("tokens are not unique 256-bit hex: %q %q", first, second)
	}
}

func TestNodeMajorVersionReadsTheInterpreterVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the version probe fixture is a POSIX shell script")
	}
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nprintf 'v22.13.1\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	major, err := nodeMajorVersion(node)
	if err != nil || major != 22 {
		t.Fatalf("major=%d err=%v", major, err)
	}
	old := filepath.Join(dir, "node-old")
	if err := os.WriteFile(old, []byte("#!/bin/sh\nprintf 'v18.19.0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if major, err := nodeMajorVersion(old); err != nil || major != 18 {
		t.Fatalf("old major=%d err=%v", major, err)
	}
}

// TestHostManagerLaunchesRealNodeHost exercises the production spawner: argv,
// environment, readiness probe, graceful shutdown and the parent-pid watchdog
// wiring. Playwright is replaced by a stub that never launches a browser, so it
// runs on the Linux builder without Edge.
func TestHostManagerLaunchesRealNodeHost(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	source := filepath.Join("..", "..", "browser-host")
	if _, err := os.Stat(filepath.Join(source, "host.mjs")); err != nil {
		t.Skip("browser-host sources are not present")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "browser-host", "host.mjs")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"host.mjs", "server.mjs", "tabs.mjs", "lock.mjs", "package.json"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "browser-host", name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	playwright := filepath.Join(dir, "browser-host", "node_modules", "playwright")
	if err := os.MkdirAll(playwright, 0o755); err != nil {
		t.Fatal(err)
	}
	// The stub records the environment Playwright would hand to Edge, then
	// fails the launch. The probe must show no host token: the host consumes it
	// and removes it from the environment before importing Playwright.
	if err := os.WriteFile(filepath.Join(playwright, "index.mjs"), []byte(`import {writeFileSync} from "node:fs";
export const chromium = {launchPersistentContext: async () => {
  const probe = process.env.AHA2_TEST_ENV_PROBE;
  if (probe) writeFileSync(probe, JSON.stringify({token: process.env.AHA2_BROWSER_HOST_TOKEN || ""}));
  throw new Error("no browser");
}};
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(playwright, "package.json"),
		[]byte(`{"name":"playwright","type":"module","main":"index.mjs"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(t.TempDir(), "env-probe.json")
	t.Setenv("AHA2_TEST_ENV_PROBE", probe)
	// The stub never launches a browser, but the host first has to believe Edge
	// exists; any existing file is enough for that check.
	t.Setenv("AHA2_EDGE_PATH", node)
	manager := NewHostManager(HostManagerOptions{
		Script: script, Node: node, DataDir: filepath.Join(dir, "profiles"), HostID: "local",
		Logger: discardLogger(), ReadyTimeout: 15 * time.Second, StopGrace: 2 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start real host: %v", err)
	}
	// AHA2_EDGE_PATH points at a stand-in file for the environment probe, so
	// this host reports Edge as available; the health payload itself is covered
	// by the fake-host and safety tests.
	health, err := manager.Health(ctx)
	if err != nil || health.HostID != "local" {
		t.Fatalf("health=%#v err=%v", health, err)
	}
	if _, err := manager.Open(ctx, "browser_profile_1", "edge"); err == nil {
		t.Fatal("stub browser launch unexpectedly succeeded")
	}
	data, err := os.ReadFile(probe)
	if err != nil {
		t.Fatalf("Playwright stub did not run: %v", err)
	}
	var environment struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &environment); err != nil {
		t.Fatal(err)
	}
	if environment.Token != "" {
		t.Fatalf("host token leaked into the browser environment: %q", environment.Token)
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("stop real host: %v", err)
	}
	if _, err := manager.Health(ctx); err == nil {
		t.Fatal("host still reports healthy after Stop")
	}
}
