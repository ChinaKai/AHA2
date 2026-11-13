package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ChinaKai/AHA2/internal/agentproxy"
	"github.com/ChinaKai/AHA2/internal/domain"
)

// These tests drive the SSH stdio transport against a mock SSH server that
// executes commands locally. That is the same shape a real workspace has: the
// exec request runs a shell, and the session channel carries stdin/stdout.
//
// The normal helper is the embedded Node script, so most tests need a real node
// and skip where it is not installed. The explicit-override path keeps using a
// wrapper around this test binary, which runs the Go reference helper in a child
// process — framing, READY parsing and process lifetime are then exercised for
// both runtimes.

// TestAgentProxyHelperProcess is not a test: it is the helper child process the
// wrapper script starts. It exits before the testing framework prints anything,
// because stdout at that point is the tunnel.
func TestAgentProxyHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_AGENT_PROXY_HELPER") != "1" {
		return
	}
	protocol := agentproxy.Protocol
	args := os.Args
	for index, value := range args {
		if value == "--" {
			args = args[index+1:]
			break
		}
	}
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--protocol" {
			if parsed, err := strconv.Atoi(args[index+1]); err == nil {
				protocol = parsed
			}
		}
	}
	if err := agentproxy.RunHelper(os.Stdin, os.Stdout, protocol); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func requireNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	return node
}

func writeAgentProxyWrapper(t *testing.T, directory, marker string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	// The explicit override is an aha2-style helper: `version` identifies it and
	// the hidden `agent-proxy` subcommand enters the tunnel. This is the exact
	// contract Command builds, so a regression to a bare `--stdio` invocation
	// fails the end-to-end test instead of only the probe.
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  version) echo 'aha2 test " + marker + "'; exit 0 ;;\n" +
		"  agent-proxy) shift; GO_WANT_AGENT_PROXY_HELPER=1 exec '" + executable + "' -test.run=TestAgentProxyHelperProcess -- agent-proxy \"$@\" ;;\n" +
		"esac\n" +
		"exit 2\n"
	path := filepath.Join(directory, "aha2-test-helper")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// startExecSSHServer serves sessions whose exec request runs locally. It is the
// smallest thing that behaves like the workspace sshd for this transport.
func startExecSSHServer(t *testing.T) (string, int) {
	t.Helper()
	hostSigner, _ := newSSHKeyPair(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(hostSigner)

	var connection sync.WaitGroup
	t.Cleanup(connection.Wait)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connection.Add(1)
			go func() {
				defer connection.Done()
				serveExecSSHConnection(conn, config)
			}()
		}
	}()

	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	knownHosts := knownhosts.Line([]string{listener.Addr().String()}, hostSigner.PublicKey()) + "\n"
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(knownHosts), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	address := listener.Addr().(*net.TCPAddr)
	return address.IP.String(), address.Port
}

func serveExecSSHConnection(conn net.Conn, config *ssh.ServerConfig) {
	defer conn.Close()
	server, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			return
		}
		go func() {
			defer channel.Close()
			for request := range channelRequests {
				if request.Type != "exec" {
					_ = request.Reply(false, nil)
					continue
				}
				var payload struct{ Command string }
				if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
					_ = request.Reply(false, nil)
					return
				}
				if err := request.Reply(true, nil); err != nil {
					return
				}
				runExecOnChannel(channel, payload.Command)
				return
			}
		}()
	}
}

func runExecOnChannel(channel ssh.Channel, command string) {
	process := exec.Command("sh", "-c", command)
	// Stdin is pumped manually: assigning the channel to Cmd.Stdin would make
	// Cmd.Run wait for its stdin copier to hit EOF, and the client keeps stdin
	// open for the whole session (that is how the tunnel stays alive).
	stdin, err := process.StdinPipe()
	if err != nil {
		return
	}
	go func() {
		_, _ = io.Copy(stdin, channel)
		_ = stdin.Close()
	}()
	process.Stdout = channel
	process.Stderr = channel.Stderr()
	err = process.Run()
	status := 0
	var exitErr *exec.ExitError
	if err != nil {
		status = 127
		if errors.As(err, &exitErr) {
			status = exitErr.ExitCode()
		}
	}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
}

func agentProxyTestWorkspace(host string, port int) domain.Workspace {
	return domain.Workspace{
		ID: "workspace-test", Name: "test", Transport: "ssh",
		SSHHost: host, SSHUser: "tester", SSHPort: port, SSHAuth: "password", SSHPassword: "secret",
		Platform: "linux/" + runtime.GOARCH,
	}
}

// TestAgentProxyHelperCommand pins the two launch contracts. The explicit
// override is an aha2-style helper entered through `agent-proxy`; the embedded
// script is run by the detected interpreter with the flags directly.
func TestAgentProxyHelperCommand(t *testing.T) {
	node := AgentProxyHelper{Path: "/srv/helper.js", Runtime: agentproxy.NodeHelperRuntime, Interpreter: "/usr/local/bin/node"}
	if got, want := node.Command(agentproxy.Protocol), `'/usr/local/bin/node' '/srv/helper.js' --stdio --protocol 1`; got != want {
		t.Fatalf("node command = %q, want %q", got, want)
	}
	exec := AgentProxyHelper{Path: "/usr/local/bin/aha2", Runtime: agentProxyRuntimeExec}
	if got, want := exec.Command(agentproxy.Protocol), `'/usr/local/bin/aha2' agent-proxy --stdio --protocol 1`; got != want {
		t.Fatalf("exec command = %q, want %q", got, want)
	}
}

func readTestManifest(t *testing.T, path string) agentProxyManifest {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest agentProxyManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func testSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func agentProxyShareDir(workDir string) string {
	return filepath.Join(workDir, ".aha2-context", "share", "agent-proxy")
}

// TestAgentProxyNodeHelperMaterializesAndReuses pins the new artifact: the
// manifest records runtime=node, the uploaded file is the embedded script with
// the digest the manifest promises, and a second ensure does not rewrite it.
func TestAgentProxyNodeHelperMaterializesAndReuses(t *testing.T) {
	requireNode(t)
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, "")

	helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-1", "1.0.0", "")
	if err != nil {
		t.Fatalf("ensure helper: %v", err)
	}
	if helper.Runtime != agentproxy.NodeHelperRuntime {
		t.Fatalf("runtime = %q, want node", helper.Runtime)
	}
	if !strings.HasSuffix(helper.Path, ".js") {
		t.Fatalf("helper path = %s, want a .js script", helper.Path)
	}
	if filepath.Dir(helper.Path) != agentProxyShareDir(workDir) {
		t.Fatalf("helper path = %s, want under %s", helper.Path, agentProxyShareDir(workDir))
	}
	if helper.Note != "" {
		t.Fatalf("shared directory reported a fallback: %s", helper.Note)
	}
	info, err := os.Stat(helper.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("helper mode = %v, want 0700", info.Mode().Perm())
	}
	script, digest := agentproxy.NodeHelper()
	content, err := os.ReadFile(helper.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, script) {
		t.Fatal("uploaded helper is not the embedded script")
	}
	if got := testSHA256(content); got != digest {
		t.Fatalf("uploaded digest = %s, want %s", got, digest)
	}
	manifest := readTestManifest(t, filepath.Join(agentProxyShareDir(workDir), agentProxyManifestName))
	if manifest.Protocol != agentproxy.Protocol || manifest.Version != "1.0.0" || manifest.Runtime != agentproxy.NodeHelperRuntime {
		t.Fatalf("manifest = %#v", manifest)
	}
	if manifest.SHA256 != digest {
		t.Fatalf("manifest sha = %s, want %s", manifest.SHA256, digest)
	}

	// Reuse: the second ensure must not upload again, so the file's mtime and
	// the manifest stay untouched.
	before := info.ModTime()
	again, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-2", "1.0.0", "")
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if again.Path != helper.Path {
		t.Fatalf("reuse picked %s, want %s", again.Path, helper.Path)
	}
	after, err := os.Stat(helper.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before) {
		t.Fatalf("helper was rewritten on reuse: %s -> %s", before, after.ModTime())
	}

	// A new version uploads a new file and prunes anything older than the one
	// generation kept for already running processes.
	third, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-3", "2.0.0", "")
	if err != nil {
		t.Fatalf("upgrade ensure: %v", err)
	}
	if third.Path == helper.Path {
		t.Fatal("upgrade reused the old script")
	}
	entries, err := os.ReadDir(agentProxyShareDir(workDir))
	if err != nil {
		t.Fatal(err)
	}
	var scripts []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), agentProxyScriptPrefix) {
			scripts = append(scripts, entry.Name())
		}
	}
	// Current plus the generation it replaced: an old helper that is still
	// running keeps its inode, and anything older than that is dropped.
	if len(scripts) != 2 {
		t.Fatalf("share directory kept %v, want the current and previous helper", scripts)
	}
	if manifest := readTestManifest(t, filepath.Join(agentProxyShareDir(workDir), agentProxyManifestName)); manifest.Version != "2.0.0" {
		t.Fatalf("manifest version = %s, want 2.0.0", manifest.Version)
	}
}

func TestAgentProxyNodeHelperConcurrentEnsure(t *testing.T) {
	requireNode(t)
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, "")

	const parallel = 4
	paths := make([]string, parallel)
	errs := make([]error, parallel)
	var wait sync.WaitGroup
	wait.Add(parallel)
	for index := 0; index < parallel; index++ {
		go func(index int) {
			defer wait.Done()
			helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-"+strconv.Itoa(index), "1.0.0", "")
			paths[index], errs[index] = helper.Path, err
		}(index)
	}
	wait.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("ensure %d: %v", index, err)
		}
		if paths[index] != paths[0] {
			t.Fatalf("concurrent ensures disagreed: %s vs %s", paths[index], paths[0])
		}
	}
	manifest := readTestManifest(t, filepath.Join(filepath.Dir(paths[0]), agentProxyManifestName))
	if manifest.SHA256 == "" {
		t.Fatal("manifest lost its digest")
	}
	// The manifest is written last: every reader must find a file that matches
	// the digest it promises.
	content, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := testSHA256(content); got != manifest.SHA256 {
		t.Fatalf("manifest digest %s does not match file %s", manifest.SHA256, got)
	}
}

func TestAgentProxyEndToEndCarriesHTTP(t *testing.T) {
	requireNode(t)
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, "")

	target := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, `{"service":"aha2","path":"`+request.URL.Path+`"}`)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = target.Serve(listener) }()
	defer target.Close()

	helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-1", "1.0.0", "")
	if err != nil {
		t.Fatalf("ensure helper: %v", err)
	}
	proxy, err := StartAgentAPIProxy(context.Background(), workspace, helper, listener.Addr().String(), "http")
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Close()
	if proxy.Runtime != agentproxy.NodeHelperRuntime {
		t.Fatalf("proxy runtime = %q, want node", proxy.Runtime)
	}
	if !strings.HasPrefix(proxy.URL, "http://127.0.0.1:") {
		t.Fatalf("proxy URL = %q", proxy.URL)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(proxy.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"service":"aha2"`) || !strings.Contains(string(body), "/healthz") {
		t.Fatalf("body = %q", body)
	}

	// Closing the tunnel must actually end the remote process: the next request
	// is refused rather than silently served by a leftover helper.
	if err := proxy.Close(); err != nil {
		t.Fatalf("close proxy: %v", err)
	}
	if _, err := client.Get(proxy.URL + "/healthz"); err == nil {
		t.Fatal("a closed tunnel still answered")
	}
}

func TestAgentProxyStartFailsWithoutHelper(t *testing.T) {
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	start := time.Now()
	helper := AgentProxyHelper{Path: "/nonexistent/aha2-agent-proxy", Runtime: agentProxyRuntimeExec}
	_, err := StartAgentAPIProxy(context.Background(), workspace, helper, "127.0.0.1:1", "http")
	if err == nil {
		t.Fatal("starting a missing helper succeeded")
	}
	if !strings.Contains(err.Error(), "Agent API stdio 代理启动失败") {
		t.Fatalf("error does not explain the fallback: %v", err)
	}
	// Retries are bounded, but they must not take the Turn's whole timeout.
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("failure took %s", elapsed)
	}
}

func TestAgentProxyCleanupKeepsSharedDirectory(t *testing.T) {
	requireNode(t)
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, "")

	helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-1", "1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(workDir, ".aha2-context", "task-1", "scratch", "main")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RemoveTaskScratch(context.Background(), workspace, workDir, "task-1"); err != nil {
		t.Fatalf("remove scratch: %v", err)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("scratch survived cleanup: %v", err)
	}
	if _, err := os.Stat(helper.Path); err != nil {
		t.Fatalf("task cleanup removed the shared helper: %v", err)
	}
}

// TestAgentProxyExplicitOverrideUsesExecRuntime covers the retained
// AHA2_AGENT_PROXY_HELPER path: the supplied file is uploaded, recorded as
// runtime=exec, and run directly without any node probe.
func TestAgentProxyExplicitOverrideUsesExecRuntime(t *testing.T) {
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, writeAgentProxyWrapper(t, t.TempDir(), "v1"))

	helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-1", "1.0.0", "")
	if err != nil {
		t.Fatalf("explicit helper was rejected: %v", err)
	}
	if helper.Runtime != agentProxyRuntimeExec {
		t.Fatalf("runtime = %q, want exec", helper.Runtime)
	}
	if strings.HasSuffix(helper.Path, ".js") {
		t.Fatalf("explicit helper was uploaded as a script: %s", helper.Path)
	}
	manifest := readTestManifest(t, filepath.Join(filepath.Dir(helper.Path), agentProxyManifestName))
	if manifest.Runtime != agentProxyRuntimeExec || manifest.Protocol != agentproxy.Protocol {
		t.Fatalf("manifest = %#v", manifest)
	}

	target := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, `{"service":"aha2"}`)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = target.Serve(listener) }()
	defer target.Close()

	proxy, err := StartAgentAPIProxy(context.Background(), workspace, helper, listener.Addr().String(), "http")
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Close()
	response, err := (&http.Client{Timeout: 10 * time.Second}).Get(proxy.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET through override: %v", err)
	}
	response.Body.Close()
}

// TestAgentProxyExplicitOverrideRequiresAha2Entry pins the documented override
// contract: a helper that only understands `--stdio` is rejected by the
// `version` probe, instead of passing preparation and failing at startup.
func TestAgentProxyExplicitOverrideRequiresAha2Entry(t *testing.T) {
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	path := filepath.Join(t.TempDir(), "stdio-only-helper")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n[ \"$1\" = \"--stdio\" ] && exec cat >/dev/null\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(agentProxyHelperOverride, path)

	_, err := EnsureAgentProxyHelper(context.Background(), workspace, t.TempDir(), "task-1", "1.0.0", "")
	if err == nil {
		t.Fatal("a bare --stdio helper was accepted as an aha2-style override")
	}
	if !strings.Contains(err.Error(), "helper 无法执行") {
		t.Fatalf("error = %v, want the failed version probe", err)
	}
}

// TestAgentProxyManifestReadRejectsOversizedFile calls the reader directly with
// a manifest that is valid JSON but larger than the limit.
//
// The end-to-end ensure path cannot pin this: an unreadable manifest makes it
// upload over the file, so "the limit fired" and "the limit is gone" both end in
// a successful ensure. Only asking the reader itself distinguishes them.
func TestAgentProxyManifestReadRejectsOversizedFile(t *testing.T) {
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	directory := t.TempDir()
	runner := RunnerFor(workspace)

	// Control: a normal manifest reads back, so a failure below is about size.
	small := `{"protocol":1,"version":"1.0.0","runtime":"node","sha256":"abc","updated_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(directory, agentProxyManifestName), []byte(small), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := readAgentProxyManifest(context.Background(), runner, directory)
	if err != nil || parsed == nil || parsed.Version != "1.0.0" {
		t.Fatalf("small manifest read = %#v err=%v", parsed, err)
	}

	oversized := `{"protocol":1,"version":"1.0.0","runtime":"node","sha256":"` +
		strings.Repeat("x", agentProxyManifestLimit) + `","updated_at":"2026-01-01T00:00:00Z"}`
	if len(oversized) <= agentProxyManifestLimit {
		t.Fatalf("fixture is only %d bytes", len(oversized))
	}
	if !json.Valid([]byte(oversized)) {
		t.Fatal("fixture is not valid JSON")
	}
	if err := os.WriteFile(filepath.Join(directory, agentProxyManifestName), []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAgentProxyManifest(context.Background(), runner, directory); err == nil {
		t.Fatal("an oversized manifest was accepted")
	} else if !strings.Contains(err.Error(), "manifest too large") {
		t.Fatalf("err = %v, want a size rejection", err)
	}
}

// TestAgentProxyEnsureOverwritesACorruptManifest covers recovery, not the size
// limit: whatever the manifest contains, ensure must still leave a usable helper
// behind.
func TestAgentProxyEnsureOverwritesACorruptManifest(t *testing.T) {
	requireNode(t)
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, "")

	shareDir := agentProxyShareDir(workDir)
	if err := os.MkdirAll(shareDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oversized := make([]byte, agentProxyManifestLimit+1)
	for index := range oversized {
		oversized[index] = 'x'
	}
	if err := os.WriteFile(filepath.Join(shareDir, agentProxyManifestName), oversized, 0o600); err != nil {
		t.Fatal(err)
	}

	helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-1", "1.0.0", "")
	if err != nil {
		t.Fatalf("ensure over an oversized manifest: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(shareDir, agentProxyManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if len(content) > agentProxyManifestLimit {
		t.Fatalf("manifest was not replaced: %d bytes", len(content))
	}
	if manifest := readTestManifest(t, filepath.Join(shareDir, agentProxyManifestName)); manifest.Version != "1.0.0" {
		t.Fatalf("manifest version = %q", manifest.Version)
	}
	if filepath.Dir(helper.Path) != shareDir {
		t.Fatalf("helper landed outside the shared directory: %s", helper.Path)
	}
}

// TestAgentProxyFallbackDirectoryIsCleanedWithTaskScratch pins the fallback's
// lifetime: it lives inside the Task scratch, so closing the Task removes it
// instead of leaving one helper copy per Task behind.
func TestAgentProxyFallbackDirectoryIsCleanedWithTaskScratch(t *testing.T) {
	requireNode(t)
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, "")

	// A regular file where the shared directory's parent must be makes the
	// shared candidate fail deterministically, even for a privileged user.
	contextRoot := filepath.Join(workDir, ".aha2-context")
	if err := os.MkdirAll(contextRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	shareSentinel := filepath.Join(contextRoot, "share")
	if err := os.WriteFile(shareSentinel, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-1", "1.0.0", "")
	if err != nil {
		t.Fatalf("ensure with an unusable shared directory: %v", err)
	}
	fallbackDir := filepath.Join(contextRoot, "task-1", "scratch", "agent-proxy")
	if filepath.Dir(helper.Path) != fallbackDir {
		t.Fatalf("helper path = %s, want %s", helper.Path, fallbackDir)
	}
	if !strings.Contains(helper.Note, "scratch") {
		t.Fatalf("note does not name the fallback: %q", helper.Note)
	}

	if err := RemoveTaskScratch(context.Background(), workspace, workDir, "task-1"); err != nil {
		t.Fatalf("remove scratch: %v", err)
	}
	if _, err := os.Stat(fallbackDir); !os.IsNotExist(err) {
		t.Fatalf("fallback helper survived Task cleanup: %v", err)
	}
	if _, err := os.Stat(shareSentinel); err != nil {
		t.Fatalf("Task cleanup touched the shared path: %v", err)
	}
}

// runAgentProxyNodeProbe runs the real detection script with an exact PATH, so
// the shebang/PATH precedence is tested without depending on the test machine.
func runAgentProxyNodeProbe(t *testing.T, path, backend string) (string, string, int) {
	t.Helper()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not available")
	}
	command := exec.Command(shell, "-c", agentProxyNodeProbeScript, "aha2-agent-proxy-node", backend)
	command.Env = []string{"PATH=" + path}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run probe: %v", err)
	}
	return stdout.String(), stderr.String(), exitErr.ExitCode()
}

func writeExecutable(t *testing.T, directory, name string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// nodeProbePath builds the exact PATH for a probe run: the tools the script
// itself calls plus, optionally, a node binary.
func nodeProbePath(t *testing.T, nodePath string) string {
	t.Helper()
	directory := t.TempDir()
	for _, tool := range []string{"head", "basename"} {
		resolved, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not available", tool)
		}
		if err := os.Symlink(resolved, filepath.Join(directory, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if nodePath != "" {
		if err := os.Symlink(nodePath, filepath.Join(directory, "node")); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

// TestAgentProxyNodeProbePrefersBackendShebang is the hard guarantee: a Node
// backend script is already running in the workspace, so its interpreter wins
// over whatever `node` happens to be on PATH.
func TestAgentProxyNodeProbePrefersBackendShebang(t *testing.T) {
	shebangDir := t.TempDir()
	shebangNode := writeExecutable(t, shebangDir, "node")
	pathNode := writeExecutable(t, t.TempDir(), "node")
	pathDir := nodeProbePath(t, pathNode)
	backend := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(backend, []byte("#!"+shebangNode+"\nconsole.log('backend')\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runAgentProxyNodeProbe(t, pathDir, backend)
	if code != 0 {
		t.Fatalf("probe exit = %d, stderr = %q", code, stderr)
	}
	if strings.TrimSpace(stdout) != shebangNode {
		t.Fatalf("interpreter = %q, want the backend shebang %q (PATH node was %q)", stdout, shebangNode, pathNode)
	}
}

// TestAgentProxyNodeProbeFallsBackToPath covers a backend that is not a Node
// script: the workspace's own `node` is used.
func TestAgentProxyNodeProbeFallsBackToPath(t *testing.T) {
	pathNode := writeExecutable(t, t.TempDir(), "node")
	pathDir := nodeProbePath(t, pathNode)
	backend := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(backend, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runAgentProxyNodeProbe(t, pathDir, backend)
	if code != 0 {
		t.Fatalf("probe exit = %d, stderr = %q", code, stderr)
	}
	if want := filepath.Join(pathDir, "node"); strings.TrimSpace(stdout) != want {
		t.Fatalf("interpreter = %q, want PATH node %q", stdout, want)
	}
}

// TestAgentProxyNodeProbeReportsWhyNodeIsMissing keeps the node layer's failure
// from being silent: the caller logs this text before falling back.
func TestAgentProxyNodeProbeReportsWhyNodeIsMissing(t *testing.T) {
	_, stderr, code := runAgentProxyNodeProbe(t, nodeProbePath(t, ""), "/usr/local/bin/codex")
	if code == 0 {
		t.Fatal("probe succeeded without node")
	}
	if !strings.Contains(stderr, "没有 node") || !strings.Contains(stderr, "/usr/local/bin/codex") {
		t.Fatalf("stderr = %q, want the missing-node reason naming the backend", stderr)
	}
}

// TestProbeAgentAPIOverStdioMaterializesAndCloses covers the detection contract:
// a missing helper is uploaded, the proxy carries a real /healthz request, and
// the tunnel is closed afterwards so no helper process is left on the workspace.
func TestProbeAgentAPIOverStdioMaterializesAndCloses(t *testing.T) {
	requireNode(t)
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, "")

	target := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			http.NotFound(writer, request)
			return
		}
		_, _ = io.WriteString(writer, `{"ok":true,"service":"aha2"}`)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = target.Serve(listener) }()
	defer target.Close()

	if _, err := os.Stat(agentProxyShareDir(workDir)); !os.IsNotExist(err) {
		t.Fatalf("the share directory existed before detection: %v", err)
	}
	result, err := ProbeAgentAPIOverStdio(context.Background(), workspace, "http://"+listener.Addr().String(), workDir, "", "1.0.0", "")
	if err != nil {
		t.Fatalf("stdio detection failed: %v", err)
	}
	if result.Runtime != agentproxy.NodeHelperRuntime {
		t.Fatalf("runtime = %q, want node", result.Runtime)
	}
	if !strings.HasPrefix(result.URL, "http://127.0.0.1:") {
		t.Fatalf("proxy URL = %q", result.URL)
	}
	if filepath.Dir(result.Helper) != agentProxyShareDir(workDir) {
		t.Fatalf("helper = %s, want it materialised in %s", result.Helper, agentProxyShareDir(workDir))
	}
	if _, err := os.Stat(result.Helper); err != nil {
		t.Fatalf("the helper was not materialised: %v", err)
	}
	// Closing the SSH session is what stops the remote helper. A request to the
	// former proxy port must be refused, not served by an orphan.
	client := &http.Client{Timeout: 2 * time.Second}
	if response, err := client.Get(result.URL + "/healthz"); err == nil {
		response.Body.Close()
		t.Fatal("the proxy still answered after detection returned")
	}
}

// TestProbeAgentAPIOverStdioClosesProxyOnHealthFailure makes the cleanup
// unconditional: a helper that starts but does not serve a real AHA2 health
// response must still be stopped.
func TestProbeAgentAPIOverStdioClosesProxyOnHealthFailure(t *testing.T) {
	requireNode(t)
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, "")

	target := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"ok":false,"service":"not-aha2"}`)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = target.Serve(listener) }()
	defer target.Close()

	result, err := ProbeAgentAPIOverStdio(context.Background(), workspace, "http://"+listener.Addr().String(), workDir, "", "1.0.0", "")
	if err == nil || !strings.Contains(err.Error(), "healthz") {
		t.Fatalf("err = %v, want a health probe failure", err)
	}
	if result.URL == "" {
		t.Fatalf("the proxy URL was not reported for diagnostics: %#v", result)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	if response, err := client.Get(result.URL + "/healthz"); err == nil {
		response.Body.Close()
		t.Fatal("the proxy still answered after a failed detection")
	}
}
