package workspace

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
)

func TestRemoteScriptKeepsSecretsOutOfSSHArguments(t *testing.T) {
	t.Parallel()
	script, err := remoteScript(Command{
		Executable: "codex",
		Args:       []string{"exec", "--json", "-"},
		Dir:        "/workspace",
		Env:        map[string]string{"OPENAI_API_KEY": "top-secret"},
		Stdin:      "prompt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "top-secret") {
		t.Fatal("secret was not delivered through stdin script")
	}
	if !strings.Contains(script, "$HOME/.local/bin") || !strings.Contains(script, "$HOME\"/.nvm/versions/node/*/bin") {
		t.Fatalf("user backend paths missing: %s", script)
	}
	runner := SSHRunner{Host: "localhost", User: "dev", Port: 22}
	if runner.Host+" "+runner.User == script {
		t.Fatal("unexpected script comparison")
	}
}

func TestRemoteScriptRejectsInvalidEnvName(t *testing.T) {
	t.Parallel()
	if _, err := remoteScript(Command{Executable: "echo", Env: map[string]string{"BAD-NAME": "x"}}); err == nil {
		t.Fatal("invalid environment name accepted")
	}
}

func TestSSHRunnerAutoPrefersConfiguredPassword(t *testing.T) {
	hostSigner, _ := newSSHKeyPair(t)
	clientSigner, clientPrivateKey := newSSHKeyPair(t)
	listener, authUsed, serverErrors := startWorkspaceSSHServer(t, hostSigner, "secret", clientSigner.PublicKey())
	knownHostsPath := writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())
	privateKeyPath := writePrivateKey(t, clientPrivateKey)

	result, err := (SSHRunner{
		Host: "127.0.0.1", User: "root", Port: listener.Addr().(*net.TCPAddr).Port,
		Auth: "auto", Password: "secret",
		KnownHostsPaths: []string{knownHostsPath}, PrivateKeyPaths: []string{privateKeyPath},
	}).Run(context.Background(), Command{
		Executable: "printf", Args: []string{"runner-ok"}, Timeout: 5 * time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "runner ok") {
		t.Fatalf("unexpected SSH result: %#v", result)
	}
	if method := <-authUsed; method != "password" {
		t.Fatalf("auto auth used %s before configured password", method)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestSSHRunnerKeyAuthenticationAndKnownHosts(t *testing.T) {
	hostSigner, _ := newSSHKeyPair(t)
	clientSigner, clientPrivateKey := newSSHKeyPair(t)
	listener, authUsed, serverErrors := startWorkspaceSSHServer(t, hostSigner, "", clientSigner.PublicKey())
	knownHostsPath := writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())
	privateKeyPath := writePrivateKey(t, clientPrivateKey)

	result, err := (SSHRunner{
		Host: "127.0.0.1", User: "root", Port: listener.Addr().(*net.TCPAddr).Port,
		Auth: "key", KnownHostsPaths: []string{knownHostsPath}, PrivateKeyPaths: []string{privateKeyPath},
	}).Run(context.Background(), Command{Executable: "true", Timeout: 5 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected SSH result: %#v", result)
	}
	if method := <-authUsed; method != "key" {
		t.Fatalf("key auth used %s", method)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}

	if _, err := workspaceSSHHostKeyCallback(filepath.Join(t.TempDir(), "missing")); err == nil ||
		!strings.Contains(err.Error(), "known_hosts") {
		t.Fatalf("missing known_hosts accepted: %v", err)
	}
}

func newSSHKeyPair(t *testing.T) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer, privateKey
}

func writeKnownHosts(t *testing.T, endpoint string, publicKey ssh.PublicKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(knownhosts.Line([]string{endpoint}, publicKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writePrivateKey(t *testing.T, privateKey ed25519.PrivateKey) string {
	t.Helper()
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func startWorkspaceSSHServer(
	t *testing.T,
	hostSigner ssh.Signer,
	password string,
	authorizedKey ssh.PublicKey,
) (net.Listener, <-chan string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	authUsed := make(chan string, 4)
	serverErrors := make(chan error, 1)
	config := &ssh.ServerConfig{
		PasswordCallback: func(metadata ssh.ConnMetadata, candidate []byte) (*ssh.Permissions, error) {
			if password != "" && metadata.User() == "root" && string(candidate) == password {
				authUsed <- "password"
				return nil, nil
			}
			return nil, fmt.Errorf("invalid password")
		},
		PublicKeyCallback: func(metadata ssh.ConnMetadata, candidate ssh.PublicKey) (*ssh.Permissions, error) {
			if metadata.User() == "root" && authorizedKey != nil &&
				string(candidate.Marshal()) == string(authorizedKey.Marshal()) {
				authUsed <- "key"
				return nil, nil
			}
			return nil, fmt.Errorf("invalid public key")
		},
	}
	config.AddHostKey(hostSigner)
	go func() {
		serverErrors <- serveWorkspaceSSHConnection(listener, config)
	}()
	return listener, authUsed, serverErrors
}

func serveWorkspaceSSHConnection(listener net.Listener, config *ssh.ServerConfig) error {
	connection, err := listener.Accept()
	if err != nil {
		return err
	}
	defer connection.Close()
	server, channels, requests, err := ssh.NewServerConn(connection, config)
	if err != nil {
		return err
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	channelRequest, ok := <-channels
	if !ok {
		return fmt.Errorf("SSH session channel was not opened")
	}
	channel, channelRequests, err := channelRequest.Accept()
	if err != nil {
		return err
	}
	defer channel.Close()
	for request := range channelRequests {
		if request.Type != "exec" {
			_ = request.Reply(false, nil)
			continue
		}
		if err := request.Reply(true, nil); err != nil {
			return err
		}
		if _, err := io.ReadAll(channel); err != nil {
			return err
		}
		if _, err := channel.Write([]byte("runner ok\n")); err != nil {
			return err
		}
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
		return channel.Close()
	}
	return fmt.Errorf("SSH exec request was not received")
}
