package workspace

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

func TestWorkspaceUnknownSSHHostKeyClassification(t *testing.T) {
	t.Parallel()
	if !IsUnknownSSHHostKey(fmt.Errorf("SSH known_hosts is missing; verify the host once")) {
		t.Fatal("missing known_hosts did not trigger explicit trust flow")
	}
	if IsUnknownSSHHostKey(fmt.Errorf("knownhosts: key mismatch")) {
		t.Fatal("changed host key was classified as unknown")
	}
}

func TestWindowsRemotePayloadPreservesMetadataAndRawStdin(t *testing.T) {
	t.Parallel()
	payload, err := windowsRemotePayload(Command{
		Executable: "codex.exe", Args: []string{"exec", "--json", "-"}, Dir: `C:\workspace`,
		Env: map[string]string{"OPENAI_API_KEY": "top-secret"}, Stdin: "中文\x00prompt",
	})
	if err != nil {
		t.Fatal(err)
	}
	newline := strings.IndexByte(payload, '\n')
	if newline != 8 {
		t.Fatalf("invalid Windows SSH frame header: %q", payload)
	}
	length, err := strconv.ParseInt(payload[:newline], 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	metadataEnd := newline + 1 + int(length)
	var metadata windowsCommandMetadata
	if err := json.Unmarshal([]byte(payload[newline+1:metadataEnd]), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Executable != "codex.exe" || metadata.Directory != `C:\workspace` || metadata.Env["OPENAI_API_KEY"] != "top-secret" || metadata.Arguments != `exec --json -` {
		t.Fatalf("unexpected Windows metadata: %#v", metadata)
	}
	if payload[metadataEnd:] != "中文\x00prompt" {
		t.Fatalf("raw Windows stdin changed: %q", payload[metadataEnd:])
	}
	commandLine := windowsBootstrapCommand()
	if len(commandLine) >= 8000 {
		t.Fatalf("Windows SSH bootstrap exceeds cmd.exe command-line limit: %d", len(commandLine))
	}
	for _, secret := range []string{"top-secret", `C:\workspace`, "中文", "prompt"} {
		if strings.Contains(commandLine, secret) {
			t.Fatalf("Windows SSH exec arguments leaked dynamic data %q", secret)
		}
	}
}

func TestWindowsSSHBootstrapPreservesRawStdin(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell integration test")
	}
	raw := "中文\x00without-final-newline"
	payload, err := windowsRemotePayload(Command{
		Executable: "powershell.exe",
		Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
			`[Console]::OpenStandardInput().CopyTo([Console]::OpenStandardOutput())`},
		Stdin: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(windowsBootstrapCommand())
	command := exec.Command(parts[0], parts[1:]...)
	command.Stdin = strings.NewReader(payload)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != raw {
		t.Fatalf("bootstrap changed raw stdin: %q", output)
	}
}

func TestSSHRunnerAutoPrefersConfiguredPassword(t *testing.T) {
	hostSigner, _ := newSSHKeyPair(t)
	clientSigner, clientPrivateKey := newSSHKeyPair(t)
	listener, authUsed, serverErrors := startWorkspaceSSHServer(t, hostSigner, "secret", clientSigner.PublicKey())
	knownHostsPath := writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())
	privateKeyPath := writePrivateKey(t, clientPrivateKey)

	result, err := (&SSHRunner{
		Host: "127.0.0.1", User: "root", Port: listener.Addr().(*net.TCPAddr).Port,
		Auth: "auto", Password: "secret", Platform: "linux",
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

	result, err := (&SSHRunner{
		Host: "127.0.0.1", User: "root", Port: listener.Addr().(*net.TCPAddr).Port,
		Auth: "key", Platform: "linux", KnownHostsPaths: []string{knownHostsPath}, PrivateKeyPaths: []string{privateKeyPath},
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

func TestSSHRunnerDetectsWindowsBeforeRunningPowerShellCommand(t *testing.T) {
	hostSigner, _ := newSSHKeyPair(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	config := &ssh.ServerConfig{PasswordCallback: func(metadata ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if metadata.User() == "root" && string(password) == "secret" {
			return nil, nil
		}
		return nil, fmt.Errorf("invalid password")
	}}
	config.AddHostKey(hostSigner)
	serverErrors := make(chan error, 1)
	go func() {
		for index := 0; index < 3; index++ {
			if err := serveMockSSHExec(listener, config, func(command, stdin string) (string, error) {
				if index == 0 {
					if !strings.Contains(command, "sh -lc") || stdin != "" {
						return "", fmt.Errorf("POSIX platform probe invalid: command=%s stdin=%q", command, stdin)
					}
					return "", nil
				}
				if index == 1 {
					if !strings.Contains(command, "powershell.exe") || stdin != "" {
						return "", fmt.Errorf("Windows platform probe invalid: command=%s stdin=%q", command, stdin)
					}
					return "AHA2_WINDOWS:AMD64", nil
				}
				if !strings.Contains(command, "powershell.exe") || !strings.HasPrefix(stdin, "000000") {
					return "", fmt.Errorf("Windows command frame missing: command=%s stdin=%q", command, stdin)
				}
				return "runner ok\n", nil
			}); err != nil {
				serverErrors <- err
				return
			}
		}
		serverErrors <- nil
	}()
	knownHostsPath := writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())
	runner := &SSHRunner{
		Host: "127.0.0.1", User: "root", Port: listener.Addr().(*net.TCPAddr).Port,
		Auth: "password", Password: "secret", KnownHostsPaths: []string{knownHostsPath},
	}
	result, err := runner.Run(context.Background(), Command{Executable: "codex.exe", Args: []string{"--version"}, Timeout: 5 * time.Second}, nil)
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Stdout, "runner ok") {
		t.Fatalf("Windows SSH command result=%#v err=%v", result, err)
	}
	if runner.DetectedPlatform() != "windows/amd64" {
		t.Fatalf("detected platform=%q", runner.DetectedPlatform())
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
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

func serveMockSSHExec(listener net.Listener, config *ssh.ServerConfig, handle func(command, stdin string) (string, error)) error {
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
		var payload struct{ Command string }
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
			return err
		}
		if err := request.Reply(true, nil); err != nil {
			return err
		}
		data, err := io.ReadAll(channel)
		if err != nil {
			return err
		}
		stdout, err := handle(payload.Command, string(data))
		if err != nil {
			return err
		}
		if _, err := channel.Write([]byte(stdout)); err != nil {
			return err
		}
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
		return channel.Close()
	}
	return fmt.Errorf("SSH exec request was not received")
}
