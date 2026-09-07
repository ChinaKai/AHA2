package hardware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type sshTerminal struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	output  *io.PipeReader
	writer  *io.PipeWriter
	once    sync.Once
}

type SSHHostKeyInfo struct {
	Endpoint    string `json:"endpoint"`
	Algorithm   string `json:"algorithm"`
	Fingerprint string `json:"fingerprint"`
}

var knownHostsWriteMu sync.Mutex

var errSSHHostKeyCaptured = errors.New("SSH host key captured")

// ProbeSSHHostKey completes only the SSH key exchange. It never offers a user
// password or private key, so callers can display the server identity before
// deciding whether to trust it.
func ProbeSSHHostKey(ctx context.Context, endpoint string) (SSHHostKeyInfo, error) {
	info, _, err := probeSSHHostKey(ctx, endpoint)
	return info, err
}

func defaultKnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve SSH home: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

// TrustSSHHostKey probes the host again, verifies the exact fingerprint shown
// to the Owner, and appends it to the same known_hosts file used by terminals.
func TrustSSHHostKey(ctx context.Context, endpoint, expectedFingerprint string) (SSHHostKeyInfo, error) {
	path, err := defaultKnownHostsPath()
	if err != nil {
		return SSHHostKeyInfo{}, err
	}
	return trustSSHHostKeyAtPath(ctx, endpoint, expectedFingerprint, path)
}

func trustSSHHostKeyAtPath(ctx context.Context, endpoint, expectedFingerprint, path string) (SSHHostKeyInfo, error) {
	info, key, err := probeSSHHostKey(ctx, endpoint)
	if err != nil {
		return SSHHostKeyInfo{}, err
	}
	if expectedFingerprint == "" || expectedFingerprint != info.Fingerprint {
		return SSHHostKeyInfo{}, errors.New("SSH host key fingerprint changed; review the new fingerprint before trusting")
	}
	if err := appendTrustedSSHHostKey(path, endpoint, key); err != nil {
		return SSHHostKeyInfo{}, err
	}
	return info, nil
}

func probeSSHHostKey(ctx context.Context, endpoint string) (SSHHostKeyInfo, ssh.PublicKey, error) {
	endpoint = strings.TrimSpace(endpoint)
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		return SSHHostKeyInfo{}, nil, fmt.Errorf("invalid SSH endpoint: %w", err)
	}
	var captured ssh.PublicKey
	config := &ssh.ClientConfig{User: "aha2-host-key-probe", HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		captured = key
		return errSSHHostKeyCaptured
	}, Timeout: 8 * time.Second, HostKeyAlgorithms: []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}}
	connection, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return SSHHostKeyInfo{}, nil, err
	}
	defer connection.Close()
	_, _, _, _ = ssh.NewClientConn(connection, endpoint, config)
	if captured == nil {
		return SSHHostKeyInfo{}, nil, errors.New("SSH server did not present a host key")
	}
	return SSHHostKeyInfo{Endpoint: endpoint, Algorithm: captured.Type(), Fingerprint: ssh.FingerprintSHA256(captured)}, captured, nil
}

func appendTrustedSSHHostKey(path, endpoint string, key ssh.PublicKey) error {
	knownHostsWriteMu.Lock()
	defer knownHostsWriteMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create SSH directory: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		callback, callbackErr := knownhosts.New(path)
		if callbackErr != nil {
			return fmt.Errorf("load SSH known_hosts: %w", callbackErr)
		}
		address, _ := net.ResolveTCPAddr("tcp", endpoint)
		if verifyErr := callback(endpoint, address, key); verifyErr == nil {
			return nil
		} else {
			var keyErr *knownhosts.KeyError
			if !errors.As(verifyErr, &keyErr) || len(keyErr.Want) > 0 {
				return errors.New("SSH host key changed; remove the old key only after independent verification")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open SSH known_hosts: %w", err)
	}
	defer file.Close()
	if _, err := fmt.Fprintln(file, knownhosts.Line([]string{endpoint}, key)); err != nil {
		return fmt.Errorf("write SSH known_hosts: %w", err)
	}
	return file.Sync()
}

func IsUnknownSSHHostKey(err error) bool {
	if err == nil {
		return false
	}
	var keyErr *knownhosts.KeyError
	return (errors.As(err, &keyErr) && len(keyErr.Want) == 0) || strings.Contains(err.Error(), "knownhosts: key is unknown")
}

func openSSHTerminal(ctx context.Context, endpoint, username, password, authMode string) (io.ReadWriteCloser, error) {
	if username == "" {
		return nil, fmt.Errorf("SSH username is required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve SSH home: %w", err)
	}
	return openSSHTerminalWithPaths(
		ctx, endpoint, username, password, authMode,
		[]string{filepath.Join(home, ".ssh", "known_hosts")},
		[]string{
			filepath.Join(home, ".ssh", "id_ed25519"),
			filepath.Join(home, ".ssh", "id_ecdsa"),
			filepath.Join(home, ".ssh", "id_rsa"),
		},
	)
}

func openSSHTerminalWithPaths(
	ctx context.Context,
	endpoint, username, password, authMode string,
	knownHostsPaths, privateKeyPaths []string,
) (io.ReadWriteCloser, error) {
	callback, err := sshHostKeyCallback(knownHostsPaths...)
	if err != nil {
		return nil, err
	}
	authMethods, err := sshAuthMethods(password, authMode, privateKeyPaths...)
	if err != nil {
		return nil, err
	}
	config := &ssh.ClientConfig{
		User: username, Auth: authMethods, HostKeyCallback: callback, Timeout: 8 * time.Second,
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoED25519,
			ssh.KeyAlgoECDSA256,
			ssh.KeyAlgoECDSA384,
			ssh.KeyAlgoECDSA521,
			ssh.KeyAlgoRSASHA512,
			ssh.KeyAlgoRSASHA256,
		},
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, err
	}
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, endpoint, config)
	if err != nil {
		connection.Close()
		return nil, err
	}
	client := ssh.NewClient(clientConnection, channels, requests)
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, err
	}
	output, writer := io.Pipe()
	session.Stdout = writer
	session.Stderr = writer
	modes := ssh.TerminalModes{
		ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 115200, ssh.TTY_OP_OSPEED: 115200,
	}
	if err := session.RequestPty("xterm-256color", 28, 100, modes); err != nil {
		writer.Close()
		output.Close()
		session.Close()
		client.Close()
		return nil, fmt.Errorf("request SSH terminal: %w", err)
	}
	if err := session.Shell(); err != nil {
		writer.Close()
		output.Close()
		session.Close()
		client.Close()
		return nil, fmt.Errorf("start SSH shell: %w", err)
	}
	terminal := &sshTerminal{
		client: client, session: session, stdin: stdin, output: output, writer: writer,
	}
	go func() {
		waitErr := session.Wait()
		if waitErr != nil && !errors.Is(waitErr, io.EOF) {
			_ = writer.CloseWithError(waitErr)
		} else {
			_ = writer.Close()
		}
		_ = client.Close()
	}()
	return terminal, nil
}

func (terminal *sshTerminal) Read(buffer []byte) (int, error) {
	return terminal.output.Read(buffer)
}

func (terminal *sshTerminal) Write(data []byte) (int, error) {
	return terminal.stdin.Write(data)
}

func (terminal *sshTerminal) Resize(cols, rows int) error {
	return terminal.session.WindowChange(rows, cols)
}

func (terminal *sshTerminal) Close() error {
	var result error
	terminal.once.Do(func() {
		_ = terminal.stdin.Close()
		if err := terminal.session.Close(); err != nil && !errors.Is(err, io.EOF) {
			result = err
		}
		_ = terminal.client.Close()
		_ = terminal.writer.Close()
		_ = terminal.output.Close()
	})
	return result
}

func sshAuthMethods(password, authMode string, privateKeyPaths ...string) ([]ssh.AuthMethod, error) {
	authMode = normalizeSSHAuth(authMode)
	passwordMethods := func() []ssh.AuthMethod {
		if password == "" {
			return nil
		}
		return []ssh.AuthMethod{
			ssh.Password(password),
			ssh.KeyboardInteractive(func(_ string, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for index := range answers {
					answers[index] = password
				}
				return answers, nil
			}),
		}
	}
	var keyMethods []ssh.AuthMethod
	for _, path := range privateKeyPaths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		signer, parseErr := ssh.ParsePrivateKey(data)
		if parseErr == nil {
			keyMethods = append(keyMethods, ssh.PublicKeys(signer))
		}
	}
	var methods []ssh.AuthMethod
	switch authMode {
	case domain.HardwareSSHAuthPassword:
		methods = passwordMethods()
		if len(methods) == 0 {
			return nil, fmt.Errorf("SSH password authentication requires a configured password")
		}
	case domain.HardwareSSHAuthKey:
		methods = keyMethods
		if len(methods) == 0 {
			return nil, fmt.Errorf("SSH key authentication requires an unencrypted private key in ~/.ssh")
		}
	default:
		methods = append(methods, passwordMethods()...)
		methods = append(methods, keyMethods...)
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("SSH requires a configured password or an unencrypted private key in ~/.ssh")
	}
	return methods, nil
}

func sshHostKeyCallback(paths ...string) (ssh.HostKeyCallback, error) {
	var existing []string
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			existing = append(existing, path)
		}
	}
	if len(existing) == 0 {
		return nil, fmt.Errorf("SSH known_hosts is missing; verify the host once with the system ssh client")
	}
	callback, err := knownhosts.New(existing...)
	if err != nil {
		return nil, fmt.Errorf("load SSH known_hosts: %w", err)
	}
	return callback, nil
}
