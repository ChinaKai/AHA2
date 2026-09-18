package workspace

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// This is the end-to-end proof of the design: a connection opened at the
// workspace-side port that the SSH server allocated arrives at the AHA-side
// target, over a real SSH connection with a real tcpip-forward handshake.
//
// Without this, the feature would only be "we call ListenTCP"; with it, the
// property that matters is observed.
func TestSSHReverseForwardReachesTargetThroughRealHandshake(t *testing.T) {
	// Stand-in for AHA: answers on the host loopback.
	ahaListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ahaListener.Close()
	aha := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"service":"aha2"}`)
	})}
	go func() { _ = aha.Serve(ahaListener) }()
	defer aha.Close()

	// A real SSH server that grants forwarding requests.
	hostKey, _ := newSSHKeyPair(t)
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(hostKey)
	sshListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sshListener.Close()

	var grantMu sync.Mutex
	grantedForward := false
	go func() {
		for {
			conn, err := sshListener.Accept()
			if err != nil {
				return
			}
			go serveForwardingSSHServer(t, conn, serverConfig, &grantMu, &grantedForward, ahaListener.Addr().String())
		}
	}()

	// Client side mirrors what SSHRunner does.
	client, err := ssh.Dial("tcp", sshListener.Addr().String(), &ssh.ClientConfig{
		User: "test", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	listener, err := client.ListenTCP(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("request remote forward: %v", err)
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.Port == 0 {
		t.Fatalf("server did not assign a forward port: %#v", listener.Addr())
	}
	go serveReverseForward(listener, ahaListener.Addr().String())

	grantMu.Lock()
	sawGrant := grantedForward
	grantMu.Unlock()
	if !sawGrant {
		t.Fatal("the SSH server never received a tcpip-forward request")
	}

	// The workspace-side endpoint the backend would be told to use.
	endpoint := "http://" + net.JoinHostPort("127.0.0.1", itoa(address.Port))
	response, err := (&http.Client{Timeout: 5 * time.Second}).Get(endpoint + "/healthz")
	if err != nil {
		t.Fatalf("GET through the forward: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "aha2") {
		t.Fatalf("forwarded response = %d %q", response.StatusCode, body)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

// serveForwardingSSHServer accepts local forwarding requests and proxies the
// resulting channels to target, which is what a real sshd does for -R.
func serveForwardingSSHServer(t *testing.T, conn net.Conn, config *ssh.ServerConfig, mu *sync.Mutex, granted *bool, target string) {
	serverConn, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer serverConn.Close()

	// Collect the listeners the client asked for, keyed by the address string the
	// client will use in its forwarded channel names.
	forwarded := map[string]net.Listener{}
	var forwardMu sync.Mutex
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
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				_ = request.Reply(false, nil)
				continue
			}
			port := uint32(listener.Addr().(*net.TCPAddr).Port)
			key := net.JoinHostPort(payload.Addr, itoa(int(port)))
			forwardMu.Lock()
			forwarded[key] = listener
			forwardMu.Unlock()

			t.Logf("server: granted forward %q -> local port %d", payload.Addr, port)
			mu.Lock()
			*granted = true
			mu.Unlock()

			// The reply carries the assigned port when the client asked for 0.
			var reply []byte
			if payload.Rport == 0 {
				reply = ssh.Marshal(struct{ Port uint32 }{port})
			}
			_ = request.Reply(true, reply)

			go func() {
				for {
					incoming, err := listener.Accept()
					if err != nil {
						return
					}
					t.Logf("server: accepted connection on forwarded port %d", port)
					go func() {
						defer incoming.Close()
						t.Logf("server: opening forwarded-tcpip channel")
						origin := incoming.RemoteAddr().(*net.TCPAddr)
						channel, requests, err := serverConn.OpenChannel("forwarded-tcpip", ssh.Marshal(struct {
							Addr       string
							Port       uint32
							OriginAddr string
							OriginPort uint32
						}{payload.Addr, port, origin.IP.String(), uint32(origin.Port)}))
						if err != nil {
							t.Logf("server: OpenChannel error: %v", err)
							return
						}
						go ssh.DiscardRequests(requests)
						// A real sshd pipes the accepted connection into the
						// forwarded channel and lets the client reach the target;
						// dialing the target here would bypass the code under test.
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
		newChannel.Reject(ssh.UnknownChannelType, "unused")
	}
	_ = context.Background()
}
