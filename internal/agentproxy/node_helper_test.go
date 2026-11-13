package agentproxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests run the embedded Node helper as a real process and drive it with
// the Go client, so framing, flow control, half-close and process lifetime are
// checked across the two implementations rather than against a mock.

func requireNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	return node
}

func writeNodeHelper(t *testing.T) string {
	t.Helper()
	script, _ := NodeHelper()
	path := filepath.Join(t.TempDir(), "agent-proxy-node-helper.js")
	if err := os.WriteFile(path, script, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func startAgentProxyTestTarget(t *testing.T, handler http.Handler) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return listener.Addr().String()
}

// syncBuffer collects a child's stderr while the test may read it: os/exec
// copies stderr from its own goroutine until the process exits.
type syncBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (buffer *syncBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.data.Write(value)
}

func (buffer *syncBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.data.String()
}

func startNodeSession(t *testing.T, target string) (*Session, *syncBuffer) {
	t.Helper()
	node := requireNode(t)
	command := exec.Command(node, writeNodeHelper(t), "--stdio", "--protocol", "1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &syncBuffer{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	session, err := StartSession(Stdio{
		Stdin:  stdin,
		Stdout: stdout,
		Wait:   command.Wait,
		Close: func() error {
			// A helper that already exited on stdin EOF is the normal case, not
			// a close failure.
			if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}
			return nil
		},
	}, target, "http", 15*time.Second)
	if err != nil {
		_ = command.Process.Kill()
		t.Fatalf("start node helper: %v (stderr: %s)", err, stderr.String())
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, stderr
}

func TestNodeHelperCarriesHTTP(t *testing.T) {
	target := startAgentProxyTestTarget(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		_, _ = fmt.Fprintf(writer, `{"service":"aha2","path":%q,"body":%q}`, request.URL.Path, string(body))
	}))
	session, stderr := startNodeSession(t, target)

	response, err := (&http.Client{Timeout: 10 * time.Second}).Post(session.URL+"/healthz?x=1", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("POST through node helper: %v (stderr: %s)", err, stderr.String())
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), `"path":"/healthz"`) || !strings.Contains(string(body), `"body":"hello"`) {
		t.Fatalf("body = %q", body)
	}
}

func TestNodeHelperServesConcurrentStreams(t *testing.T) {
	target := startAgentProxyTestTarget(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(writer, request.URL.Query().Get("id"))
	}))
	session, stderr := startNodeSession(t, target)

	client := &http.Client{Timeout: 20 * time.Second}
	const parallel = 8
	var wait sync.WaitGroup
	errors := make([]error, parallel)
	wait.Add(parallel)
	for index := 0; index < parallel; index++ {
		go func(index int) {
			defer wait.Done()
			response, err := client.Get(fmt.Sprintf("%s/echo?id=%d", session.URL, index))
			if err != nil {
				errors[index] = err
				return
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				errors[index] = err
				return
			}
			if want := fmt.Sprintf("%d", index); string(body) != want {
				errors[index] = fmt.Errorf("response %d = %q", index, body)
			}
		}(index)
	}
	wait.Wait()
	for index, err := range errors {
		if err != nil {
			t.Fatalf("stream %d: %v (stderr: %s)", index, err, stderr.String())
		}
	}
}

// TestNodeHelperHandlesMoreThanOneWindow exercises the credit accounting in both
// directions: a request body and a response body each larger than the 256 KiB
// initial window cannot complete unless WINDOW frames are produced correctly.
func TestNodeHelperHandlesMoreThanOneWindow(t *testing.T) {
	requestSize := 3*InitialWindow + 1234
	responseSize := 3*InitialWindow + 4321
	target := startAgentProxyTestTarget(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		if len(body) != requestSize {
			t.Errorf("request body = %d bytes, want %d", len(body), requestSize)
		}
		_, _ = writer.Write(bytes.Repeat([]byte("r"), responseSize))
	}))
	session, stderr := startNodeSession(t, target)

	response, err := (&http.Client{Timeout: 30 * time.Second}).Post(
		session.URL+"/bulk", "application/octet-stream", bytes.NewReader(bytes.Repeat([]byte("q"), requestSize)))
	if err != nil {
		t.Fatalf("POST through node helper: %v (session: %v, stderr: %s)", err, session.Err(), stderr.String())
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != responseSize {
		t.Fatalf("response body = %d bytes, want %d", len(body), responseSize)
	}
}

// TestNodeHelperRejectsProtocolMismatch pins the handshake contract: a helper
// asked for a version it does not speak must fail loudly, not guess.
func TestNodeHelperRejectsProtocolMismatch(t *testing.T) {
	node := requireNode(t)
	command := exec.Command(node, writeNodeHelper(t), "--stdio", "--protocol", "2")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		t.Fatal("protocol 2 was accepted")
	}
	if !strings.Contains(stderr.String(), "unsupported protocol 2") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestNodeHelperMalformedFrameTearsDownChannel covers the "bad frame is fatal"
// rule: there is no resynchronisation point, so the helper exits and the client
// observes a dead tunnel instead of a half-open stream.
func TestNodeHelperMalformedFrameTearsDownChannel(t *testing.T) {
	target := startAgentProxyTestTarget(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "ok")
	}))
	session, stderr := startNodeSession(t, target)

	// Unknown frame type 9, stream 1, no payload.
	if _, err := session.stdio.Stdin.Write([]byte{9, 0, 0, 0, 1, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a malformed frame did not tear the channel down")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(stderr.String(), "unknown frame type 9") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(stderr.String(), "unknown frame type 9") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestNodeHelperExitsOnStdinEOF pins normal shutdown: the SSH session closing
// its side is what stops the helper, with no orphan process left behind.
func TestNodeHelperExitsOnStdinEOF(t *testing.T) {
	target := startAgentProxyTestTarget(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "ok")
	}))
	session, stderr := startNodeSession(t, target)
	if err := session.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("helper did not exit after stdin EOF (stderr: %s)", stderr.String())
	}
}
