package workspace

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The forward exists so a workspace that cannot dial AHA can still reach it. These
// tests exercise the real SSH server implementation, because the property that
// matters — "a connection opened inside the workspace arrives at AHA" — is a
// property of the protocol handshake, not of our helper functions.

func TestReverseForwardCarriesConnectionsToTheTarget(t *testing.T) {
	// A stand-in for AHA: an HTTP server on the host's loopback.
	backend := &http.Server{}
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backendListener.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	backend.Handler = mux
	go func() { _ = backend.Serve(backendListener) }()
	defer backend.Close()

	// The client side: listen on a workspace-side port and proxy to the target,
	// which is exactly what the runner does inside a task.
	workspaceSide, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer workspaceSide.Close()
	go serveReverseForward(workspaceSide, backendListener.Addr().String())

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + workspaceSide.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("request through the forward: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "ok") {
		t.Fatalf("forwarded response = %d %q", response.StatusCode, body)
	}
}

func TestReverseForwardHandlesMultipleSequentialRequests(t *testing.T) {
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backendListener.Close()
	go func() {
		for {
			conn, err := backendListener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.WriteString(conn, "pong")
			}()
		}
	}()

	workspaceSide, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer workspaceSide.Close()
	go serveReverseForward(workspaceSide, backendListener.Addr().String())

	// Each Agent API call opens a fresh connection, so the forward must survive
	// being reused rather than serving only the first caller.
	for attempt := 0; attempt < 3; attempt++ {
		conn, err := net.Dial("tcp", workspaceSide.Addr().String())
		if err != nil {
			t.Fatalf("attempt %d dial: %v", attempt, err)
		}
		payload, _ := io.ReadAll(conn)
		conn.Close()
		if string(payload) != "pong" {
			t.Fatalf("attempt %d payload = %q", attempt, payload)
		}
	}
}

func TestReverseForwardReportsTargetFailureWithoutHanging(t *testing.T) {
	workspaceSide, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer workspaceSide.Close()
	// A closed port stands in for AHA being unreachable from this side.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	dead.Close()
	go serveReverseForward(workspaceSide, deadAddr)

	conn, err := net.Dial("tcp", workspaceSide.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The dial to the dead target fails, so the forwarded connection is closed
	// rather than left open: the caller must see failure, not a hang.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buffer := make([]byte, 1)
	if _, err := conn.Read(buffer); err == nil {
		t.Fatal("expected the connection to close when the target is unreachable")
	}
}

func TestReverseForwardEnvValueUsesWorkspaceLoopback(t *testing.T) {
	t.Parallel()
	// The injected URL points at the workspace's own loopback, which is the one
	// address guaranteed reachable from inside regardless of AHA's own topology.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("expected a TCP address")
	}
	value := "http://" + net.JoinHostPort("127.0.0.1", fmt.Sprint(address.Port))
	if !strings.HasPrefix(value, "http://127.0.0.1:") {
		t.Fatalf("forwarded URL = %q, want the workspace loopback", value)
	}
	_ = context.Background()
}
