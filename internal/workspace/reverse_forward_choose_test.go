package workspace

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// These tests pin how the runner chooses which Agent API address to hand the
// command. The choice cannot be made by inspection from AHA: whether a given
// address answers depends on the workspace's own network position, and an
// established forward that carries no traffic is indistinguishable from a working
// one until something tries it. Getting it wrong does not fail the Turn — the Turn
// runs to its full duration with every Agent API call timing out, which reads as
// the Agent hanging.

// TestReverseForwardVerifiesTheTunnelBeforeTrustingIt covers the case this
// verification exists for: the forward is established, but the target behind it
// does not answer.
//
// A forward that is accepted can still be useless — AHA's own listener may be
// down, or something else may be on the port. Handing that URL to the command
// produces a Turn that hangs rather than one that fails, so the runner must check
// and fall back instead.
func TestReverseForwardVerifiesTheTunnelBeforeTrustingIt(t *testing.T) {
	// A dead target for the forward: nothing is listening, so every tunnelled
	// connection fails even though the SSH forward itself is granted.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	dead.Close()

	// A configured address that genuinely answers, as a reachable workspace has.
	// It is deliberately a different address from the dead tunnel, so the
	// assertions can tell which one was chosen.
	configured, stopReachable := newHealthServer(t)
	defer stopReachable()

	hostSigner, clientPrivateKey := newSSHKeyPair(t)
	listener, serverErr := startForwardCapableSSHServer(t, hostSigner)
	knownHostsPath := writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())
	privateKeyPath := writePrivateKey(t, clientPrivateKey)
	runner := &SSHRunner{
		Host: "127.0.0.1", User: "root", Platform: "linux",
		Port:            listener.Addr().(*net.TCPAddr).Port,
		KnownHostsPaths: []string{knownHostsPath},
		PrivateKeyPaths: []string{privateKeyPath},
	}

	result, err := runner.Run(context.Background(), Command{
		Executable: "sh",
		Args:       []string{"-c", `echo "SEEN=$AHA2_AGENT_API_URL"`},
		Env:        map[string]string{"AHA2_AGENT_API_URL": configured},
		Timeout:    30 * time.Second,
		// The tunnel points at a dead target, so it cannot serve the probe.
		ReverseForward: &ReverseForward{Target: deadAddr, EnvName: "AHA2_AGENT_API_URL"},
	}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", result.ExitCode, result.Stderr)
	}
	// It must have fallen back to the address that actually works, not the tunnel.
	if !strings.Contains(result.Stdout, "SEEN="+configured) {
		t.Fatalf("stdout=%q, want the reachable configured address %s", result.Stdout, configured)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("ssh server: %v", err)
	}
}

// TestReverseForwardFailsLoudlyWhenNothingAnswers covers the remote workspace
// whose configured address is loopback, which is the shape the tunnel exists for.
//
// When the tunnel cannot serve and the configured address cannot either, there is
// no address that works. Running anyway would burn the whole Turn on failing API
// calls; stopping with a reason is the only outcome that can be acted on. The
// error must name the cause, because the operator's next step depends on it.
func TestReverseForwardFailsLoudlyWhenNothingAnswers(t *testing.T) {
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	dead.Close()

	hostSigner, clientPrivateKey := newSSHKeyPair(t)
	listener, serverErr := startForwardCapableSSHServer(t, hostSigner)
	knownHostsPath := writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())
	privateKeyPath := writePrivateKey(t, clientPrivateKey)
	runner := &SSHRunner{
		Host: "127.0.0.1", User: "root", Platform: "linux",
		Port:            listener.Addr().(*net.TCPAddr).Port,
		KnownHostsPaths: []string{knownHostsPath},
		PrivateKeyPaths: []string{privateKeyPath},
	}

	// Exactly what a remote workspace gets: AHA's loopback, which from inside the
	// workspace is the workspace's own loopback and therefore never AHA.
	const configured = "http://127.0.0.1:1"
	_, err = runner.Run(context.Background(), Command{
		Executable:     "sh",
		Args:           []string{"-c", `echo "SHOULD_NOT_RUN"`},
		Env:            map[string]string{"AHA2_AGENT_API_URL": configured},
		Timeout:        30 * time.Second,
		ReverseForward: &ReverseForward{Target: deadAddr, EnvName: "AHA2_AGENT_API_URL"},
	}, nil)
	if err == nil {
		t.Fatal("a Turn with no reachable Agent API address must stop, not run")
	}
	message := err.Error()
	if !strings.Contains(message, "Agent API 不可达") {
		t.Fatalf("error should say what failed: %v", err)
	}
	// The operator needs the reason to know what to fix, and the host to know where.
	if !strings.Contains(message, "转发") {
		t.Fatalf("error should mention the forwarding failure: %v", err)
	}
	if !strings.Contains(message, runner.Host) {
		t.Fatalf("error should name the host to investigate: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("ssh server: %v", err)
	}
}

// newHealthServer starts a stand-in for AHA that answers like the real thing, and
// returns its base URL plus a stop function.
func newHealthServer(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, aha2HealthPayload)
	})}
	go func() { _ = server.Serve(listener) }()
	stop := func() { listener.Close(); _ = server.Close() }
	t.Cleanup(stop)
	return "http://" + listener.Addr().String(), stop
}
