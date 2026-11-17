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
	mu       sync.Mutex
	specs    []LaunchSpec
	children []*fakeChild
	failFor  int
}

func (r *launchRecorder) spawn(_ context.Context, spec LaunchSpec) (HostProcess, error) {
	r.mu.Lock()
	attempt := len(r.specs)
	r.specs = append(r.specs, spec)
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
		if request.Header.Get("Authorization") != "Bearer "+spec.Token {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"host_id": "local", "version": "fake", "platform": "test", "edge_available": true,
		})
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
		RestartDelay: 10 * time.Millisecond, MaxRestarts: 3, PortAttempts: 3,
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
	if err := os.WriteFile(filepath.Join(playwright, "index.mjs"),
		[]byte("export const chromium = {launchPersistentContext: async () => { throw new Error('no browser'); }};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(playwright, "package.json"),
		[]byte(`{"name":"playwright","type":"module","main":"index.mjs"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewHostManager(HostManagerOptions{
		Script: script, Node: node, DataDir: filepath.Join(dir, "profiles"), HostID: "local",
		Logger: discardLogger(), ReadyTimeout: 15 * time.Second, StopGrace: 2 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start real host: %v", err)
	}
	health, err := manager.Health(ctx)
	if err != nil || health.HostID != "local" || health.EdgeAvailable {
		t.Fatalf("health=%#v err=%v", health, err)
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("stop real host: %v", err)
	}
	if _, err := manager.Health(ctx); err == nil {
		t.Fatal("host still reports healthy after Stop")
	}
}
