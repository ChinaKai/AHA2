package workspace

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// This exercises the reverse forward against a real OpenSSH server rather than a
// Go implementation of one. The two differ where it matters here: OpenSSH does
// its own tcpip-forward negotiation and port assignment, and older versions had a
// port-0 bug the Go client works around. Testing only against x/crypto's server
// would leave that behaviour unverified.
//
// Opt-in because it needs a live sshd; see AHA2_REAL_SSHD_* below.
func TestReverseForwardAgainstRealOpenSSH(t *testing.T) {
	port := os.Getenv("AHA2_REAL_SSHD_PORT")
	if port == "" {
		t.Skip("AHA2_REAL_SSHD_PORT not set")
	}
	user := os.Getenv("AHA2_REAL_SSHD_USER")
	privateKey := os.Getenv("AHA2_REAL_SSHD_KEY")
	knownHosts := os.Getenv("AHA2_REAL_SSHD_KNOWN_HOSTS")
	if user == "" || privateKey == "" || knownHosts == "" {
		t.Skip("AHA2_REAL_SSHD_USER/KEY/KNOWN_HOSTS not set")
	}

	// Stand-in for AHA, reachable only from this host.
	ahaListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ahaListener.Close()
	// A real AHA2 health payload, because the runner now verifies the forward by
	// having the workspace fetch this. Any other body is correctly rejected as
	// "not AHA2", which would make this test report a transport failure that is
	// really just an unrealistic fixture.
	aha := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, aha2HealthPayload)
	})}
	go func() { _ = aha.Serve(ahaListener) }()
	defer aha.Close()

	portNumber := 0
	for _, character := range port {
		if character < '0' || character > '9' {
			continue
		}
		portNumber = portNumber*10 + int(character-'0')
	}

	runner := &SSHRunner{
		Host: "127.0.0.1", User: user, Port: portNumber, Platform: "linux",
		Auth: "key", KnownHostsPaths: []string{knownHosts}, PrivateKeyPaths: []string{privateKey},
	}

	// The command uses the injected URL and nothing else, so a failure here means
	// the forward did not deliver a usable endpoint.
	result, err := runner.Run(context.Background(), Command{
		Executable: "sh",
		Args:       []string{"-c", `curl -sS --max-time 10 "$AHA2_AGENT_API_URL/healthz" || { echo "FORWARD_FAILED:$AHA2_AGENT_API_URL" >&2; exit 3; }`},
		// A deliberately dead value: the runner must replace it.
		Env:            map[string]string{"AHA2_AGENT_API_URL": "http://127.0.0.1:1"},
		Timeout:        30 * time.Second,
		ReverseForward: &ReverseForward{Target: ahaListener.Addr().String(), EnvName: "AHA2_AGENT_API_URL"},
	}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, `"service":"aha2"`) {
		t.Fatalf("real sshd forward failed: exit=%d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if strings.Contains(result.Stderr, "FORWARD_FAILED") {
		t.Fatalf("command fell back to the unreachable configured address: %q", result.Stderr)
	}
}
