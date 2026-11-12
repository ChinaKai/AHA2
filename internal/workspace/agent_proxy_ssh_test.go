package workspace

import (
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
// exec request runs a shell, and the session channel carries stdin/stdout. The
// helper in these tests is a wrapper script around this test binary, which runs
// the real agentproxy helper in a child process — so framing, READY parsing and
// process lifetime are all exercised rather than simulated.

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

func writeAgentProxyWrapper(t *testing.T, directory, marker string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
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

func TestAgentProxyHelperMaterializesAndReuses(t *testing.T) {
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	helperSource := writeAgentProxyWrapper(t, t.TempDir(), "v1")
	t.Setenv(agentProxyHelperOverride, helperSource)

	helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-1", "1.0.0")
	if err != nil {
		t.Fatalf("ensure helper: %v", err)
	}
	shareDir := filepath.Join(workDir, ".aha2-context", "share", "agent-proxy")
	if filepath.Dir(helper.Path) != shareDir {
		t.Fatalf("helper path = %s, want under %s", helper.Path, shareDir)
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
	manifest := readTestManifest(t, filepath.Join(shareDir, "manifest.json"))
	if manifest.Protocol != agentproxy.Protocol || manifest.Version != "1.0.0" || manifest.OS != "linux" {
		t.Fatalf("manifest = %#v", manifest)
	}

	// Reuse: the second ensure must not upload again, so the file's mtime and
	// the manifest stay untouched.
	before := info.ModTime()
	again, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-2", "1.0.0")
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
	upgraded := writeAgentProxyWrapper(t, t.TempDir(), "v2")
	t.Setenv(agentProxyHelperOverride, upgraded)
	third, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-3", "2.0.0")
	if err != nil {
		t.Fatalf("upgrade ensure: %v", err)
	}
	if third.Path == helper.Path {
		t.Fatal("upgrade reused the old binary")
	}
	entries, err := os.ReadDir(shareDir)
	if err != nil {
		t.Fatal(err)
	}
	var binaries []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), agentProxyBinaryPrefix) {
			binaries = append(binaries, entry.Name())
		}
	}
	// Current plus the generation it replaced: an old helper that is still
	// running keeps its inode, and anything older than that is dropped.
	if len(binaries) != 2 {
		t.Fatalf("share directory kept %v, want the current and previous helper", binaries)
	}
	foundCurrent := false
	for _, name := range binaries {
		if name == filepath.Base(third.Path) {
			foundCurrent = true
		}
	}
	if !foundCurrent {
		t.Fatalf("share directory does not contain %s: %v", filepath.Base(third.Path), binaries)
	}
	if manifest := readTestManifest(t, filepath.Join(shareDir, "manifest.json")); manifest.Version != "2.0.0" {
		t.Fatalf("manifest version = %s, want 2.0.0", manifest.Version)
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

func TestAgentProxyHelperConcurrentEnsure(t *testing.T) {
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, writeAgentProxyWrapper(t, t.TempDir(), "v1"))

	const parallel = 4
	paths := make([]string, parallel)
	errs := make([]error, parallel)
	var wait sync.WaitGroup
	wait.Add(parallel)
	for index := 0; index < parallel; index++ {
		go func(index int) {
			defer wait.Done()
			helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-"+strconv.Itoa(index), "1.0.0")
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
	manifest := readTestManifest(t, filepath.Join(filepath.Dir(paths[0]), "manifest.json"))
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
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, writeAgentProxyWrapper(t, t.TempDir(), "v1"))

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

	helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-1", "1.0.0")
	if err != nil {
		t.Fatalf("ensure helper: %v", err)
	}
	proxy, err := StartAgentAPIProxy(context.Background(), workspace, helper.Path, listener.Addr().String())
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Close()
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
	_, err := StartAgentAPIProxy(context.Background(), workspace, "/nonexistent/aha2-agent-proxy", "127.0.0.1:1")
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
	host, port := startExecSSHServer(t)
	workspace := agentProxyTestWorkspace(host, port)
	workDir := t.TempDir()
	t.Setenv(agentProxyHelperOverride, writeAgentProxyWrapper(t, t.TempDir(), "v1"))

	helper, err := EnsureAgentProxyHelper(context.Background(), workspace, workDir, "task-1", "1.0.0")
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
