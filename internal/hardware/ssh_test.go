package hardware

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestSSHKnownHostsCallback(t *testing.T) {
	t.Parallel()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(knownhosts.Line([]string{"example.test"}, hostKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	callback, err := sshHostKeyCallback(path)
	if err != nil {
		t.Fatal(err)
	}
	address, _ := net.ResolveTCPAddr("tcp", "192.0.2.10:22")
	if err := callback("example.test:22", address, hostKey); err != nil {
		t.Fatalf("known host rejected: %v", err)
	}
	otherPublic, _, _ := ed25519.GenerateKey(rand.Reader)
	otherKey, _ := ssh.NewPublicKey(otherPublic)
	if err := callback("example.test:22", address, otherKey); err == nil {
		t.Fatal("changed host key accepted")
	}
}

func TestSSHRequiresKnownHostsAndAuthentication(t *testing.T) {
	t.Parallel()
	if _, err := sshHostKeyCallback(filepath.Join(t.TempDir(), "missing")); err == nil ||
		!strings.Contains(err.Error(), "known_hosts") {
		t.Fatalf("unexpected known_hosts error: %v", err)
	}
	methods, err := sshAuthMethods("password", domain.HardwareSSHAuthPassword)
	if err != nil || len(methods) < 2 {
		t.Fatalf("password auth methods: %d %v", len(methods), err)
	}
	if _, err := sshAuthMethods("", domain.HardwareSSHAuthPassword); err == nil {
		t.Fatal("password mode accepted without a password")
	}
	if _, err := sshAuthMethods("", domain.HardwareSSHAuthKey); err == nil {
		t.Fatal("key mode accepted without a private key")
	}
}

func TestOpenSSHTerminalInteractiveShell(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	authUsed := make(chan string, 1)
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(metadata ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if metadata.User() == "root" && string(password) == "secret" {
				authUsed <- "password"
				return nil, nil
			}
			return nil, fmt.Errorf("invalid credentials")
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) {
			authUsed <- "key"
			return nil, nil
		},
	}
	serverConfig.AddHostKey(hostSigner)
	serverErrors := make(chan error, 1)
	go serveSSHTestConnection(listener, serverConfig, serverErrors)

	sshDir := filepath.Join(t.TempDir(), ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	knownHost, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(sshDir, "known_hosts"),
		[]byte(knownhosts.Line([]string{listener.Addr().String()}, knownHost)+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(sshDir, "id_ed25519")
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	terminal, err := openSSHTerminalWithPaths(
		ctx, listener.Addr().String(), "root", "secret", domain.HardwareSSHAuthAuto,
		[]string{filepath.Join(sshDir, "known_hosts")}, []string{privatePath},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if method := <-authUsed; method != "password" {
		t.Fatalf("auto auth used %s before configured password", method)
	}
	buffer := make([]byte, 128)
	count, err := terminal.Read(buffer)
	if err != nil || !strings.Contains(string(buffer[:count]), "ssh ready") {
		t.Fatalf("SSH ready output: %q %v", buffer[:count], err)
	}
	if _, err := terminal.Write([]byte("ping\r\n")); err != nil {
		t.Fatal(err)
	}
	count, err = terminal.Read(buffer)
	if err != nil || !strings.Contains(string(buffer[:count]), "echo:ping") {
		t.Fatalf("SSH echo output: %q %v", buffer[:count], err)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func serveSSHTestConnection(listener net.Listener, config *ssh.ServerConfig, result chan<- error) {
	connection, err := listener.Accept()
	if err != nil {
		result <- err
		return
	}
	defer connection.Close()
	server, channels, requests, err := ssh.NewServerConn(connection, config)
	if err != nil {
		result <- err
		return
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	for request := range channels {
		if request.ChannelType() != "session" {
			_ = request.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		channel, channelRequests, err := request.Accept()
		if err != nil {
			result <- err
			return
		}
		for channelRequest := range channelRequests {
			switch channelRequest.Type {
			case "pty-req":
				_ = channelRequest.Reply(true, nil)
			case "shell":
				_ = channelRequest.Reply(true, nil)
				_, _ = channel.Write([]byte("ssh ready\r\n"))
				buffer := make([]byte, 128)
				count, readErr := channel.Read(buffer)
				if readErr != nil && readErr != io.EOF {
					result <- readErr
					return
				}
				_, _ = channel.Write(append([]byte("echo:"), buffer[:count]...))
				_ = channel.Close()
				result <- nil
				return
			default:
				_ = channelRequest.Reply(false, nil)
			}
		}
	}
	result <- nil
}
