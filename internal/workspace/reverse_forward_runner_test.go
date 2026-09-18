package workspace

import (
	"context"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// This drives the real SSHRunner.Run path with a ReverseForward set, which is the
// only way to know the runner wires the tunnel into a command's environment. The
// property under test is the one the design promises: a command running "inside
// the workspace" can reach an AHA-side address it could not otherwise dial.
func TestSSHRunnerReverseForwardReachesAHATargetFromCommand(t *testing.T) {
	// Stand-in for AHA. Deliberately bound to a port the "workspace" would have to
	// reach through the tunnel.
	ahaListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ahaListener.Close()
	// The payload is what a real AHA2 returns, because the runner verifies the
	// forward by asking the workspace to fetch this endpoint. A toy body would be
	// correctly rejected as "not AHA2", which is not what this test is about.
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
		Host: "127.0.0.1", User: "dev", Platform: "linux",
		Port:            listener.Addr().(*net.TCPAddr).Port,
		KnownHostsPaths: []string{knownHostsPath},
		PrivateKeyPaths: []string{privateKeyPath},
	}

	// The command asserts the forwarded URL works from its own side. It uses the
	// value the runner injects, so a wrong port or a missing tunnel fails here
	// rather than silently in a live task.
	script := `url="$AHA2_AGENT_API_URL/healthz"
body=$(curl -sS --max-time 10 "$url") || { echo "CURL_FAILED:$url" >&2; exit 3; }
echo "BODY=$body"`
	forward := &ReverseForward{Target: ahaListener.Addr().String(), EnvName: "AHA2_AGENT_API_URL"}
	result, err := runner.Run(context.Background(), Command{
		Executable:     "sh",
		Args:           []string{"-c", script},
		Env:            map[string]string{"AHA2_AGENT_API_URL": "http://127.0.0.1:1"},
		Timeout:        20 * time.Second,
		ReverseForward: forward,
	}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, `"service":"aha2"`) {
		t.Fatalf("forwarded command exit=%d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	// The runner must overwrite a pre-existing value: the whole point is that the
	// configured address is replaced by one that actually works from inside.
	if strings.Contains(result.Stderr, "CURL_FAILED") {
		t.Fatalf("command used the unreachable configured address: %q", result.Stderr)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("ssh server: %v", err)
	}
}

// startForwardCapableSSHServer accepts one SSH connection, grants tcpip-forward
// requests, and pipes resulting channels back to the accepted local connection,
// behaving like a real sshd -R.
func startForwardCapableSSHServer(t *testing.T, hostSigner ssh.Signer) (net.Listener, chan error) {
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
				if request.Type != "tcpip-forward" {
					if request.WantReply {
						_ = request.Reply(false, nil)
					}
					continue
				}
				var payload struct {
					Addr  string
					Rport uint32
				}
				if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
					_ = request.Reply(false, nil)
					continue
				}
				forwardListener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					_ = request.Reply(false, nil)
					continue
				}
				port := uint32(forwardListener.Addr().(*net.TCPAddr).Port)
				var reply []byte
				if payload.Rport == 0 {
					reply = ssh.Marshal(struct{ Port uint32 }{port})
				}
				_ = request.Reply(true, reply)

				go func() {
					for {
						incoming, err := forwardListener.Accept()
						if err != nil {
							return
						}
						go func() {
							defer incoming.Close()
							origin := incoming.RemoteAddr().(*net.TCPAddr)
							channel, channelRequests, err := server.OpenChannel("forwarded-tcpip", ssh.Marshal(struct {
								Addr       string
								Port       uint32
								OriginAddr string
								OriginPort uint32
							}{payload.Addr, port, origin.IP.String(), uint32(origin.Port)}))
							if err != nil {
								return
							}
							go ssh.DiscardRequests(channelRequests)
							var pump sync.WaitGroup
							pump.Add(2)
							go func() { defer pump.Done(); _, _ = io.Copy(channel, incoming); channel.CloseWrite() }()
							go func() { defer pump.Done(); _, _ = io.Copy(incoming, channel) }()
							pump.Wait()
						}()
					}
				}()
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

// serveTestSession answers an exec request by running the received command with a
// real shell, streaming stdin/stdout/stderr over the channel. This is what a real
// sshd does for a non-interactive exec, and it is what lets the command actually
// attempt the forwarded connection rather than merely being described.
func serveTestSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	var execPayload struct{ Command string }
	for request := range requests {
		if request.Type != "exec" {
			if request.WantReply {
				_ = request.Reply(false, nil)
			}
			continue
		}
		if err := ssh.Unmarshal(request.Payload, &execPayload); err != nil {
			_ = request.Reply(false, nil)
			return
		}
		_ = request.Reply(true, nil)
		break
	}
	process := exec.Command("sh", "-c", execPayload.Command)
	process.Stdin = channel
	process.Stdout = channel
	process.Stderr = channel.Stderr()
	err := process.Run()
	status := uint32(0)
	if exitErr, ok := err.(*exec.ExitError); ok {
		status = uint32(exitErr.ExitCode())
	} else if err != nil {
		status = 1
	}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
	_ = channel.Close()
}
