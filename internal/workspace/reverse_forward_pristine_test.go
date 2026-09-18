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

// TestReverseForwardCarriesAHAIntoAPristineRemoteWorkspace is the closest thing
// to the live remote case that can run without the Owner's Claude credentials.
//
// It reproduces a freshly provisioned remote SSH workspace exactly as AHA sees it
// at Turn start: the probe against the workspace's own loopback found nothing
// (127.0.0.1 inside that machine is that machine, not AHA), the recorded address
// is therefore an error state, and the task directory has never had an AHA
// backend run in it. The runner must still hand the command a working AHA
// endpoint, obtained over the connection it opened itself.
//
// What this does not cover: whether the Owner's Claude credentials exist on the
// host. That is an account boundary, not a transport property, and it is the only
// remaining gate on running a full Task there.
func TestReverseForwardCarriesAHAIntoAPristineRemoteWorkspace(t *testing.T) {
	// Stand-in for AHA, bound where only this side can reach it.
	ahaListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ahaListener.Close()
	// A real AHA2 health payload: the runner verifies the forward by having the
	// workspace fetch this, so a body AHA2 would never return proves nothing.
	aha := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, aha2HealthPayload)
	})}
	go func() { _ = aha.Serve(ahaListener) }()
	defer aha.Close()

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

	// The command reproduces the real probe: reach the Agent API at the address
	// the workspace would have derived for itself. That address is what failed on
	// the real host, and the tunnel must replace it.
	script := `url="$AHA2_AGENT_API_URL/healthz"
echo "USING=$AHA2_AGENT_API_URL"
body=$(curl -sS --max-time 10 "$url") || { echo "UNREACHABLE:$url" >&2; exit 3; }
echo "BODY=$body"`

	forward := &ReverseForward{Target: ahaListener.Addr().String(), EnvName: "AHA2_AGENT_API_URL"}
	result, err := runner.Run(context.Background(), Command{
		Executable: "sh",
		Args:       []string{"-c", script},
		// Exactly what an unforwarded remote workspace is handed today: AHA's own
		// loopback, which from inside the workspace resolves to the workspace.
		Env:            map[string]string{"AHA2_AGENT_API_URL": "http://127.0.0.1:8766"},
		Timeout:        20 * time.Second,
		ReverseForward: forward,
	}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", result.ExitCode, result.Stderr)
	}
	if strings.Contains(result.Stderr, "UNREACHABLE") {
		t.Fatalf("command fell back to the unreachable address: %q", result.Stderr)
	}
	// The endpoint must have been replaced, not merely reachable: a matching
	// 127.0.0.1:8766 would mean the runner forwarded a dead address.
	if strings.Contains(result.Stdout, "USING=http://127.0.0.1:8766") {
		t.Fatalf("the runner left the unreachable configured address in place: %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "127.0.0.1:") || !strings.Contains(result.Stdout, `"service":"aha2"`) {
		t.Fatalf("forwarded stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("ssh server: %v", err)
	}
}
