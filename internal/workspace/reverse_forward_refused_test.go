package workspace

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestReverseForwardRefusedFallsBackToAWorkingAddress covers a server that
// declines the forwarding request outright, as an sshd with AllowTcpForwarding no
// does.
//
// The fallback is deliberate rather than a failure: a workspace that can already
// reach AHA by address needs no tunnel, and a server that refuses forwarding may
// well be one of those. What matters is that the address handed to the command is
// one that actually answers — the previous behaviour handed over the configured
// value without checking, which for a remote workspace is its own loopback.
func TestReverseForwardRefusedFallsBackToAWorkingAddress(t *testing.T) {
	hostSigner, clientPrivateKey := newSSHKeyPair(t)
	listener, serverErr := startForwardRefusingSSHServer(t, hostSigner)
	knownHostsPath := writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())
	privateKeyPath := writePrivateKey(t, clientPrivateKey)
	runner := &SSHRunner{
		Host: "127.0.0.1", User: "root", Platform: "linux",
		Port:            listener.Addr().(*net.TCPAddr).Port,
		KnownHostsPaths: []string{knownHostsPath},
		PrivateKeyPaths: []string{privateKeyPath},
	}

	// A reachable address, so the outcome does not depend on whatever happens to
	// be listening on this host.
	configured, stop := newHealthServer(t)
	defer stop()

	result, err := runner.Run(context.Background(), Command{
		Executable: "sh",
		Args: []string{"-c", `url="$AHA2_AGENT_API_URL/healthz"
body=$(curl --noproxy '*' -fsS --connect-timeout 3 --max-time 6 "$url") || { echo "UNUSABLE:$AHA2_AGENT_API_URL" >&2; exit 3; }
echo "BODY=$body"`},
		Env:     map[string]string{"AHA2_AGENT_API_URL": configured},
		Timeout: 30 * time.Second,
		// The gate only asks for a forward when the resolved address is loopback,
		// so a target is always set here even though it will never be used.
		ReverseForward: &ReverseForward{Target: "127.0.0.1:8766", EnvName: "AHA2_AGENT_API_URL"},
	}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// The command must complete and actually reach the address it was given. This
	// is the real assertion: returning the configured value is only correct if
	// that value answers, and a test that merely echoed the variable would pass
	// even when the address is dead.
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, `"service":"aha2"`) {
		t.Fatalf("command could not reach the address it was given: exit=%d stdout=%q stderr=%q",
			result.ExitCode, result.Stdout, result.Stderr)
	}
	if strings.Contains(result.Stderr, "UNUSABLE") {
		t.Fatalf("an unusable address was handed to the command: %q", result.Stderr)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("ssh server: %v", err)
	}
}

// startForwardRefusingSSHServer behaves like an sshd with AllowTcpForwarding no:
// it declines every tcpip-forward request but still runs commands.
func startForwardRefusingSSHServer(t *testing.T, hostSigner ssh.Signer) (net.Listener, chan error) {
	t.Helper()
	config := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
		return nil, nil
	}}
	config.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	serverErr := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer connection.Close()
		server, channels, requests, err := ssh.NewServerConn(connection, config)
		if err != nil {
			serverErr <- err
			return
		}
		defer server.Close()
		go func() {
			for request := range requests {
				// Decline every forward, exactly as a restrictive sshd does.
				_ = request.Reply(false, nil)
			}
		}()
		for newChannel := range channels {
			if newChannel.ChannelType() != "session" {
				newChannel.Reject(ssh.UnknownChannelType, "expected session")
				continue
			}
			channel, channelRequests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go serveTestSession(channel, channelRequests)
		}
		serverErr <- nil
	}()
	return listener, serverErr
}
