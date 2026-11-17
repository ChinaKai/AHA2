package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HostManager owns the packaged browser-host process. AHA no longer asks the
// owner for a loopback URL: the manager picks a random loopback port and a
// short-lived token, starts the host from the install directory, restarts it
// after a crash and stops the child when AHA shuts down.
//
// It implements Host itself so browser.Service always has a stable adapter.
// Before the first successful launch (or after the manager gives up) every
// operation fails closed with a typed error instead of blocking AHA startup.
type HostManager struct {
	opts HostManagerOptions
	log  *slog.Logger

	mu       sync.RWMutex
	current  *HTTPHost
	proc     HostProcess
	spec     LaunchSpec
	failure  *Error
	starting bool
	stopping bool
	stopCh   chan struct{}
	runCtx   context.Context
	runStop  context.CancelFunc

	portAttempts  int
	executableDir string
	workDir       string
	// restarts counts restart attempts since the host last stayed healthy for
	// StableResetAfter. It survives across individual crashes so a crash loop
	// cannot reset its own budget.
	restarts int
}

// LaunchSpec describes one browser-host child launch. Token is never logged.
type LaunchSpec struct {
	Node    string
	Script  string
	Port    int
	Token   string
	DataDir string
	HostID  string
}

// HostProcess is the minimal child-process surface needed by HostManager. It is
// an interface so launch/restart/stop behavior can be tested without Node.
type HostProcess interface {
	Wait() error
	Kill() error
	PID() int
}

// SpawnFunc starts a browser-host child for spec.
type SpawnFunc func(ctx context.Context, spec LaunchSpec) (HostProcess, error)

// HostManagerOptions configures discovery and lifecycle. Zero values select
// production defaults; tests override the spawner, timeouts and discovery.
type HostManagerOptions struct {
	Script           string
	Node             string
	DataDir          string
	HostID           string
	Logger           *slog.Logger
	ExecutableDir    string
	WorkDir          string
	ReadyTimeout     time.Duration
	StopGrace        time.Duration
	RestartDelay     time.Duration
	StableResetAfter time.Duration
	MaxRestarts      int
	PortAttempts     int
	Spawn            SpawnFunc
	ReservePort      func() (int, error)
}

const (
	defaultReadyTimeout     = 15 * time.Second
	defaultStopGrace        = 5 * time.Second
	defaultRestartDelay     = 500 * time.Millisecond
	defaultStableResetAfter = 60 * time.Second
	defaultMaxRestarts      = 5
	defaultPortAttempts     = 3
	hostTokenBytes          = 32
	// hostProbeTimeout bounds a single /v1/health call. Business requests use
	// DefaultHostTimeout so a real navigation (host-side deadline 30s) is never
	// cut off by a short probe timeout.
	hostProbeTimeout = 5 * time.Second
)

// NewHostManager returns a manager in the not-yet-started state.
func NewHostManager(options HostManagerOptions) *HostManager {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.ReadyTimeout <= 0 {
		options.ReadyTimeout = defaultReadyTimeout
	}
	if options.StopGrace <= 0 {
		options.StopGrace = defaultStopGrace
	}
	if options.RestartDelay <= 0 {
		options.RestartDelay = defaultRestartDelay
	}
	if options.StableResetAfter <= 0 {
		options.StableResetAfter = defaultStableResetAfter
	}
	if options.MaxRestarts <= 0 {
		options.MaxRestarts = defaultMaxRestarts
	}
	if options.PortAttempts <= 0 {
		options.PortAttempts = defaultPortAttempts
	}
	manager := &HostManager{
		opts:          options,
		log:           options.Logger,
		portAttempts:  options.PortAttempts,
		executableDir: options.ExecutableDir,
		workDir:       options.WorkDir,
	}
	if manager.executableDir == "" {
		if executable, err := os.Executable(); err == nil {
			manager.executableDir = filepath.Dir(executable)
		}
	}
	if manager.workDir == "" {
		manager.workDir, _ = os.Getwd()
	}
	return manager
}

func (m *HostManager) setFailure(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = nil
	m.failure = managerError(err)
}

// managerError keeps every manager-generated failure a typed, static,
// path-free error. Anything else becomes browser_host_start_failed.
func managerError(err error) *Error {
	var typed *Error
	if errors.As(err, &typed) {
		return typed
	}
	return &Error{Code: "browser_host_start_failed", Status: http.StatusServiceUnavailable,
		Message: hostErrorMessage("browser_host_start_failed")}
}

func (m *HostManager) available() (*HTTPHost, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current != nil {
		return m.current, nil
	}
	if m.failure != nil {
		return nil, m.failure
	}
	if m.starting {
		return nil, &Error{Code: "browser_host_starting", Status: http.StatusServiceUnavailable,
			Message: hostErrorMessage("browser_host_starting")}
	}
	return nil, &Error{Code: "browser_host_not_ready", Status: http.StatusServiceUnavailable,
		Message: hostErrorMessage("browser_host_not_ready")}
}

// Start discovers the host and launches it once. A failure is terminal for this
// AHA run (fail closed); it never blocks the caller beyond ReadyTimeout.
func (m *HostManager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.starting || m.current != nil || m.stopping {
		m.mu.Unlock()
		return nil
	}
	m.starting = true
	m.failure = nil
	m.stopCh = make(chan struct{})
	m.runCtx, m.runStop = context.WithCancel(ctx)
	m.mu.Unlock()

	script, node, err := m.prepare()
	if err != nil {
		m.mu.Lock()
		m.starting = false
		m.mu.Unlock()
		m.setFailure(err)
		return err
	}
	proc, host, spec, err := m.launch(ctx, script, node)
	if err != nil {
		m.mu.Lock()
		m.starting = false
		m.mu.Unlock()
		m.setFailure(err)
		return err
	}
	m.mu.Lock()
	m.starting = false
	// Stop may have run while the child was starting. It saw no process to
	// kill, so the manager still owns this one.
	if m.stopping {
		m.mu.Unlock()
		_ = proc.Kill()
		_ = proc.Wait()
		return nil
	}
	m.proc = proc
	m.current = host
	m.spec = spec
	m.failure = nil
	m.mu.Unlock()
	m.log.Info("browser host started", "port", spec.Port)
	go m.watch(proc)
	return nil
}

// prepare resolves Node, the packaged script and the profile root.
func (m *HostManager) prepare() (string, string, error) {
	node, err := discoverNode(m.opts.Node, m.executableDir)
	if err != nil {
		return "", "", &Error{Code: "browser_host_node_missing", Status: http.StatusServiceUnavailable,
			Message: hostErrorMessage("browser_host_node_missing")}
	}
	// Injected spawners in tests use stand-in executables; only real launches
	// pay for the version probe. An old Node fails at import time with an
	// opaque syntax error, so check the major version up front.
	if m.opts.Spawn == nil {
		major, versionErr := nodeMajorVersion(node)
		if versionErr != nil || major < 20 {
			m.log.Warn("browser host requires Node.js 20+", "major", major, "error", versionErr)
			return "", "", &Error{Code: "browser_host_node_missing", Status: http.StatusServiceUnavailable,
				Message: hostErrorMessage("browser_host_node_missing")}
		}
	}
	script, err := discoverHostScript(m.opts.Script, m.executableDir, m.workDir)
	if err != nil {
		return "", "", &Error{Code: "browser_host_script_missing", Status: http.StatusServiceUnavailable,
			Message: hostErrorMessage("browser_host_script_missing")}
	}
	if strings.TrimSpace(m.opts.DataDir) == "" {
		return "", "", &Error{Code: "browser_host_start_failed", Status: http.StatusServiceUnavailable,
			Message: hostErrorMessage("browser_host_start_failed")}
	}
	return script, node, nil
}

func (m *HostManager) launch(ctx context.Context, script, node string) (HostProcess, *HTTPHost, LaunchSpec, error) {
	var lastErr error
	for attempt := 0; attempt < m.portAttempts; attempt++ {
		port, err := m.reservePort()
		if err != nil {
			lastErr = err
			continue
		}
		token, err := newHostToken()
		if err != nil {
			return nil, nil, LaunchSpec{}, err
		}
		spec := LaunchSpec{
			Node: node, Script: script, Port: port, Token: token,
			DataDir: m.opts.DataDir, HostID: m.opts.HostID,
		}
		proc, err := m.spawn(ctx, spec)
		if err != nil {
			lastErr = fmt.Errorf("spawn browser host: %w", err)
			continue
		}
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
		// Readiness uses a short-lived probe client; every business call must
		// stay above the host's 30s navigation deadline or AHA would report a
		// timeout while the host has already committed the navigation.
		probe := NewHTTPHostWithTimeout(baseURL, token, hostProbeTimeout)
		if err := m.waitReady(ctx, probe, proc); err != nil {
			lastErr = err
			_ = proc.Kill()
			_ = proc.Wait()
			continue
		}
		host := NewHTTPHostWithTimeout(baseURL, token, DefaultHostTimeout)
		return proc, host, spec, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no loopback port could be reserved")
	}
	m.log.Warn("browser host launch failed", "error", sanitizeManagerLog(lastErr.Error(), m.opts.DataDir, script))
	return nil, nil, LaunchSpec{}, &Error{Code: "browser_host_start_failed",
		Status: http.StatusServiceUnavailable, Message: hostErrorMessage("browser_host_start_failed")}
}

func (m *HostManager) reservePort() (int, error) {
	if m.opts.ReservePort != nil {
		return m.opts.ReservePort()
	}
	return reserveLoopbackPort()
}

func (m *HostManager) spawn(ctx context.Context, spec LaunchSpec) (HostProcess, error) {
	if m.opts.Spawn != nil {
		return m.opts.Spawn(ctx, spec)
	}
	return m.spawnProcess(ctx, spec)
}

func (m *HostManager) waitReady(ctx context.Context, host *HTTPHost, proc HostProcess) error {
	// A child that exits during startup (missing dependency, bind failure,
	// rejected token) must fail the attempt immediately instead of burning the
	// whole readiness timeout against a port nobody serves.
	exited := make(chan struct{})
	go func() {
		_ = proc.Wait()
		close(exited)
	}()
	deadline := time.Now().Add(m.opts.ReadyTimeout)
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := host.Health(probeCtx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("browser host did not become ready: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			return errors.New("browser host exited during startup")
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// watch waits for one child and starts the restart loop after an unexpected
// exit. Stopping AHA must never be interpreted as a crash.
func (m *HostManager) watch(proc HostProcess) {
	err := proc.Wait()
	m.mu.Lock()
	if m.proc != proc {
		m.mu.Unlock()
		return
	}
	m.proc = nil
	m.current = nil
	stopping := m.stopping
	script := m.spec.Script
	attempt := 0
	if !stopping {
		// The crash consumes one unit of the restart budget. It is only reset
		// after the replacement host has stayed up for StableResetAfter, so a
		// ready-then-crash loop cannot restart forever.
		m.restarts++
		attempt = m.restarts
	}
	m.mu.Unlock()
	if stopping {
		return
	}
	if attempt > m.opts.MaxRestarts {
		m.setFailure(&Error{Code: "browser_host_start_failed", Status: http.StatusServiceUnavailable,
			Message: hostErrorMessage("browser_host_start_failed")})
		m.log.Error("browser host crash budget exhausted; browser features fail closed",
			"attempts", attempt, "limit", m.opts.MaxRestarts)
		return
	}
	m.log.Warn("browser host exited; restarting", "attempt", attempt, "limit", m.opts.MaxRestarts,
		"error", sanitizeManagerLog(fmt.Sprint(err), m.opts.DataDir, script))
	go m.restart(attempt)
}

// restart launches a replacement host. budget is the number of restart
// attempts already consumed since the last stable period; failed launches
// consume further units, so MaxRestarts bounds the whole unstable run.
func (m *HostManager) restart(budget int) {
	failures := 0
	m.mu.RLock()
	runCtx := m.runCtx
	m.mu.RUnlock()
	if runCtx == nil {
		runCtx = context.Background()
	}
	for {
		attempt := budget + failures
		delay := m.opts.RestartDelay << min(attempt-1, 4)
		if !m.sleep(delay) {
			return
		}
		script, node, err := m.prepare()
		if err == nil {
			var proc HostProcess
			var host *HTTPHost
			var spec LaunchSpec
			proc, host, spec, err = m.launch(runCtx, script, node)
			if err == nil {
				m.mu.Lock()
				if m.stopping {
					m.mu.Unlock()
					_ = proc.Kill()
					return
				}
				m.proc = proc
				m.current = host
				m.spec = spec
				m.failure = nil
				m.mu.Unlock()
				m.log.Info("browser host restarted", "port", spec.Port, "attempt", attempt)
				go m.watch(proc)
				go m.markStable(proc)
				return
			}
		}
		failures++
		m.mu.Lock()
		m.restarts = budget + failures
		used := m.restarts
		m.mu.Unlock()
		m.log.Warn("browser host restart failed",
			"attempt", used, "error", sanitizeManagerLog(fmt.Sprint(err), m.opts.DataDir, script))
		if used > m.opts.MaxRestarts {
			m.setFailure(err)
			m.log.Error("browser host restart limit reached; browser features fail closed",
				"attempts", used, "limit", m.opts.MaxRestarts)
			return
		}
	}
}

// markStable clears the restart budget only when the same child is still
// running after StableResetAfter. A replacement that dies sooner keeps the
// budget it inherited.
func (m *HostManager) markStable(proc HostProcess) {
	m.mu.RLock()
	stopCh := m.stopCh
	runCtx := m.runCtx
	m.mu.RUnlock()
	if stopCh == nil {
		return
	}
	timer := time.NewTimer(m.opts.StableResetAfter)
	defer timer.Stop()
	if runCtx == nil {
		runCtx = context.Background()
	}
	select {
	case <-stopCh:
		return
	case <-runCtx.Done():
		return
	case <-timer.C:
	}
	m.mu.Lock()
	if m.proc == proc && !m.stopping {
		m.restarts = 0
	}
	m.mu.Unlock()
}

func (m *HostManager) sleep(delay time.Duration) bool {
	m.mu.RLock()
	stopCh := m.stopCh
	stopping := m.stopping
	runCtx := m.runCtx
	m.mu.RUnlock()
	if stopping || stopCh == nil {
		return false
	}
	if runCtx == nil {
		runCtx = context.Background()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-stopCh:
		return false
	case <-timer.C:
		return true
	case <-runCtx.Done():
		return false
	}
}

// Stop asks the host to close its browser sessions, then kills the child if it
// does not exit within StopGrace. It is safe to call more than once.
func (m *HostManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if m.stopping {
		m.mu.Unlock()
		return nil
	}
	m.stopping = true
	if m.runStop != nil {
		m.runStop()
	}
	if m.stopCh != nil {
		close(m.stopCh)
	}
	host, proc := m.current, m.proc
	m.current = nil
	m.proc = nil
	m.mu.Unlock()

	if host != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_ = host.Shutdown(shutdownCtx)
		cancel()
	}
	if proc == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(m.opts.StopGrace):
		_ = proc.Kill()
		<-done
		return nil
	case <-ctx.Done():
		_ = proc.Kill()
		return ctx.Err()
	}
}

// discoverNode prefers an explicit override, then the Node runtime shipped with
// the Windows installer, then PATH and the usual Windows install locations.
// The packaged runtime is what makes a clean machine work without a manual
// Node/npm install; AHA2_NODE_PATH remains the troubleshooting override.
func discoverNode(explicit, executableDir string) (string, error) {
	return discoverNodeWith(explicit, executableDir, defaultCommonNodeCandidates())
}

// discoverNodeWith enumerates every fallback even when an earlier candidate is
// present in the list but missing on disk. The packaged runtime path is always
// tried first, but a missing file must not suppress the PATH or Windows
// install-location candidates.
func discoverNodeWith(explicit, executableDir string, common []string) (string, error) {
	// An explicit setting is authoritative: silently falling back after the
	// owner configured a broken path would hide the misconfiguration.
	configured := []string{strings.TrimSpace(explicit), strings.TrimSpace(os.Getenv("AHA2_NODE_PATH"))}
	var configuredCandidates []string
	for _, candidate := range configured {
		if candidate != "" {
			configuredCandidates = append(configuredCandidates, candidate)
		}
	}
	if len(configuredCandidates) > 0 {
		for _, candidate := range configuredCandidates {
			if usableNode(candidate) {
				return candidate, nil
			}
		}
		return "", errors.New("configured node executable not found")
	}
	var candidates []string
	if executableDir != "" {
		candidates = append(candidates, filepath.Join(executableDir, "node", "node.exe"))
	}
	if path, err := exec.LookPath("node"); err == nil {
		candidates = append(candidates, path)
	}
	if path, err := exec.LookPath("node.exe"); err == nil {
		candidates = append(candidates, path)
	}
	candidates = append(candidates, common...)
	for _, candidate := range candidates {
		if usableNode(candidate) {
			return candidate, nil
		}
	}
	return "", errors.New("node executable not found")
}

func usableNode(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// defaultCommonNodeCandidates lists the usual Windows per-machine and per-user
// Node install locations. Other platforms have no extra fallback because PATH
// and the packaged runtime cover them.
func defaultCommonNodeCandidates() []string {
	if os.PathSeparator != '\\' {
		return nil
	}
	return windowsNodeCandidates(
		os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA"))
}

func windowsNodeCandidates(programFiles, programFilesX86, localAppData string) []string {
	var candidates []string
	for _, base := range []string{programFiles, programFilesX86, localAppData} {
		if base == "" {
			continue
		}
		candidates = append(candidates, filepath.Join(base, "Programs", "nodejs", "node.exe"))
		candidates = append(candidates, filepath.Join(base, "nodejs", "node.exe"))
	}
	return candidates
}

// discoverHostScript checks the explicit override, the packaged install layout
// and the development checkout in that order.
func discoverHostScript(explicit, executableDir, workDir string) (string, error) {
	candidates := []string{strings.TrimSpace(explicit), strings.TrimSpace(os.Getenv("AHA2_BROWSER_HOST_SCRIPT"))}
	if executableDir != "" {
		candidates = append(candidates, filepath.Join(executableDir, "browser-host", "host.mjs"))
	}
	if workDir != "" {
		candidates = append(candidates, filepath.Join(workDir, "browser-host", "host.mjs"))
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("browser-host/host.mjs not found")
}

// nodeMajorVersion runs `node --version` with a short deadline. The host's
// dependency tree needs Node 20+; an older interpreter would fail later with a
// syntax error that is much harder to diagnose from the resources page.
func nodeMajorVersion(node string) (int, error) {
	// A freshly installed 84 MB node.exe can take a while on a cold disk or
	// under antivirus scanning; a false "Node missing" would disable the host.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, node, "--version").Output()
	if err != nil {
		return 0, err
	}
	text := strings.TrimSpace(string(output))
	text = strings.TrimPrefix(text, "v")
	if index := strings.IndexByte(text, '.'); index > 0 {
		text = text[:index]
	}
	major, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("unrecognized Node.js version %q", strings.TrimSpace(string(output)))
	}
	return major, nil
}

func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.Port <= 0 {
		return 0, errors.New("could not allocate a loopback port")
	}
	// The listener is closed before the child binds. A concurrent process can
	// still win the race; launch retries with a fresh port in that case.
	return address.Port, nil
}

// newHostToken returns a fresh 256-bit token. Tokens are regenerated for every
// launch (including restarts) and never written to disk or logs.
func newHostToken() (string, error) {
	buffer := make([]byte, hostTokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func sanitizeManagerLog(text, dataDir, script string) string {
	replacements := [][2]string{
		{dataDir, "[profile-root]"},
		{filepath.Dir(script), "[browser-host]"},
	}
	for _, pair := range replacements {
		if pair[0] != "" && pair[0] != "." {
			text = strings.ReplaceAll(text, pair[0], pair[1])
		}
	}
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 500 {
		text = text[:500]
	}
	return text
}

// processLogWriter copies child stdout/stderr into AHA's log one line at a
// time, stripping install paths. Browser-host itself never logs the token.
type processLogWriter struct {
	log      *slog.Logger
	level    slog.Level
	dataDir  string
	script   string
	mu       sync.Mutex
	leftover []byte
}

func (w *processLogWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.leftover = append(w.leftover, data...)
	for {
		index := strings.IndexByte(string(w.leftover), '\n')
		if index < 0 {
			if len(w.leftover) > 8*1024 {
				w.leftover = w.leftover[len(w.leftover)-8*1024:]
			}
			return len(data), nil
		}
		line := strings.TrimSpace(string(w.leftover[:index]))
		w.leftover = w.leftover[index+1:]
		if line != "" {
			w.log.Log(context.Background(), w.level, "browser host: "+sanitizeManagerLog(line, w.dataDir, w.script))
		}
	}
}

func (m *HostManager) spawnProcess(_ context.Context, spec LaunchSpec) (HostProcess, error) {
	arguments := []string{
		spec.Script,
		"--port", strconv.Itoa(spec.Port),
		"--parent-pid", strconv.Itoa(os.Getpid()),
	}
	command := exec.Command(spec.Node, arguments...)
	command.Dir = filepath.Dir(spec.Script)
	// The profile root travels through the environment, not argv, so the real
	// user-data directory does not show up in the host's command line.
	command.Env = append(os.Environ(),
		"AHA2_BROWSER_HOST_TOKEN="+spec.Token,
		"AHA2_BROWSER_HOST_ID="+spec.HostID,
		"AHA2_BROWSER_PROFILE_ROOT="+spec.DataDir,
	)
	command.Stdout = &processLogWriter{log: m.log, level: slog.LevelInfo,
		dataDir: m.opts.DataDir, script: spec.Script}
	command.Stderr = &processLogWriter{log: m.log, level: slog.LevelWarn,
		dataDir: m.opts.DataDir, script: spec.Script}
	configureHostProcess(command)
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &execHostProcess{command: command}, nil
}

type execHostProcess struct {
	command *exec.Cmd
	once    sync.Once
	err     error
}

func (p *execHostProcess) Wait() error {
	p.once.Do(func() { p.err = p.command.Wait() })
	return p.err
}

func (p *execHostProcess) Kill() error { return killHostProcess(p.command) }

func (p *execHostProcess) PID() int {
	if p.command.Process == nil {
		return 0
	}
	return p.command.Process.Pid
}

// Host adapter methods delegate to the currently running HTTP host. They all
// fail closed with the manager's last typed failure when no child is running.

func (m *HostManager) Health(ctx context.Context) (Health, error) {
	host, err := m.available()
	if err != nil {
		return Health{}, err
	}
	return host.Health(ctx)
}

func (m *HostManager) Open(ctx context.Context, profileID, browserKind string) (HostSession, error) {
	host, err := m.available()
	if err != nil {
		return HostSession{}, err
	}
	return host.Open(ctx, profileID, browserKind)
}

func (m *HostManager) Session(ctx context.Context, profileID string) (HostSession, error) {
	host, err := m.available()
	if err != nil {
		return HostSession{}, err
	}
	return host.Session(ctx, profileID)
}

func (m *HostManager) Close(ctx context.Context, profileID string) error {
	host, err := m.available()
	if err != nil {
		return err
	}
	return host.Close(ctx, profileID)
}

func (m *HostManager) OpenTab(ctx context.Context, profileID, sessionID, targetURL string) (HostTab, error) {
	host, err := m.available()
	if err != nil {
		return HostTab{}, err
	}
	return host.OpenTab(ctx, profileID, sessionID, targetURL)
}

func (m *HostManager) Navigate(ctx context.Context, profileID, sessionID, tabID, targetURL string) (HostTab, error) {
	host, err := m.available()
	if err != nil {
		return HostTab{}, err
	}
	return host.Navigate(ctx, profileID, sessionID, tabID, targetURL)
}

func (m *HostManager) Snapshot(ctx context.Context, profileID, sessionID, tabID string) (HostSnapshot, error) {
	host, err := m.available()
	if err != nil {
		return HostSnapshot{}, err
	}
	return host.Snapshot(ctx, profileID, sessionID, tabID)
}

func (m *HostManager) Frame(ctx context.Context, profileID, sessionID, tabID string) (HostFrame, error) {
	host, err := m.available()
	if err != nil {
		return HostFrame{}, err
	}
	return host.Frame(ctx, profileID, sessionID, tabID)
}

func (m *HostManager) CloseTab(ctx context.Context, profileID, sessionID, tabID string) error {
	host, err := m.available()
	if err != nil {
		return err
	}
	return host.CloseTab(ctx, profileID, sessionID, tabID)
}
