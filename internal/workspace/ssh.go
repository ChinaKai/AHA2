package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type SSHRunner struct {
	Host            string
	User            string
	Port            int
	Auth            string
	Password        string
	KnownHostsPaths []string
	PrivateKeyPaths []string
}

func (runner SSHRunner) Run(parent context.Context, command Command, onLine LineHandler) (Result, error) {
	if runner.Host == "" {
		return Result{}, fmt.Errorf("ssh host is required")
	}
	if runner.User == "" {
		return Result{}, fmt.Errorf("ssh user is required")
	}
	port := runner.Port
	if port == 0 {
		port = 22
	}
	script, err := remoteScript(command)
	if err != nil {
		return Result{}, err
	}
	ctx := parent
	cancel := func() {}
	if command.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, command.Timeout)
	}
	defer cancel()
	start := time.Now()
	client, err := runner.connect(ctx, net.JoinHostPort(runner.Host, strconv.Itoa(port)))
	if err != nil {
		return Result{}, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return Result{}, err
	}
	defer session.Close()
	session.Stdin = strings.NewReader(script)
	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	stderrPipe, err := session.StderrPipe()
	if err != nil {
		return Result{}, err
	}
	var stdout, stderr bytes.Buffer
	var wait sync.WaitGroup
	wait.Add(2)
	go scanOutput(stdoutPipe, &stdout, onLine, &wait)
	go scanOutput(stderrPipe, &stderr, nil, &wait)
	done := make(chan error, 1)
	go func() { done <- session.Run("sh -s") }()
	var runErr error
	select {
	case runErr = <-done:
	case <-ctx.Done():
		_ = client.Close()
		runErr = <-done
	}
	wait.Wait()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if runErr == nil {
		return result, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitStatus()
		return result, nil
	}
	return result, runErr
}

func (runner SSHRunner) connect(ctx context.Context, endpoint string) (*ssh.Client, error) {
	knownHosts, privateKeys := runner.KnownHostsPaths, runner.PrivateKeyPaths
	if len(knownHosts) == 0 || len(privateKeys) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve SSH home: %w", err)
		}
		if len(knownHosts) == 0 {
			knownHosts = []string{filepath.Join(home, ".ssh", "known_hosts")}
		}
		if len(privateKeys) == 0 {
			privateKeys = []string{
				filepath.Join(home, ".ssh", "id_ed25519"),
				filepath.Join(home, ".ssh", "id_ecdsa"),
				filepath.Join(home, ".ssh", "id_rsa"),
			}
		}
	}
	callback, err := workspaceSSHHostKeyCallback(knownHosts...)
	if err != nil {
		return nil, err
	}
	authMethods, err := workspaceSSHAuthMethods(runner.Password, runner.Auth, privateKeys...)
	if err != nil {
		return nil, err
	}
	config := &ssh.ClientConfig{
		User: runner.User, Auth: authMethods, HostKeyCallback: callback, Timeout: 10 * time.Second,
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384,
			ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
		},
	}
	connection, err := (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).
		DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, err
	}
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, endpoint, config)
	if err != nil {
		connection.Close()
		return nil, err
	}
	return ssh.NewClient(clientConnection, channels, requests), nil
}

func workspaceSSHAuthMethods(password, authMode string, privateKeyPaths ...string) ([]ssh.AuthMethod, error) {
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
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err == nil {
			keyMethods = append(keyMethods, ssh.PublicKeys(signer))
		}
	}
	var methods []ssh.AuthMethod
	switch normalizeWorkspaceSSHAuth(authMode) {
	case "password":
		methods = passwordMethods()
		if len(methods) == 0 {
			return nil, fmt.Errorf("SSH password authentication requires a configured password")
		}
	case "key":
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

func workspaceSSHHostKeyCallback(paths ...string) (ssh.HostKeyCallback, error) {
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

func normalizeWorkspaceSSHAuth(value string) string {
	switch strings.TrimSpace(value) {
	case "password":
		return "password"
	case "key":
		return "key"
	default:
		return "auto"
	}
}

func remoteScript(command Command) (string, error) {
	if command.Executable == "" {
		return "", fmt.Errorf("remote executable is required")
	}
	var script strings.Builder
	script.WriteString("#!/bin/sh\nset -eu\n")
	script.WriteString("export PATH=\"$HOME/.local/bin:$HOME/bin:$PATH\"\n")
	script.WriteString("for aha_bin in \"$HOME\"/.nvm/versions/node/*/bin; do [ -d \"$aha_bin\" ] && PATH=\"$aha_bin:$PATH\"; done\n")
	script.WriteString("export PATH\n")
	if command.Dir != "" {
		script.WriteString("cd ")
		script.WriteString(shellQuote(command.Dir))
		script.WriteByte('\n')
	}
	for key, value := range command.Env {
		if !environmentName.MatchString(key) {
			return "", fmt.Errorf("invalid environment name %q", key)
		}
		script.WriteString("export ")
		script.WriteString(key)
		script.WriteByte('=')
		script.WriteString(shellQuote(value))
		script.WriteByte('\n')
	}
	if command.Stdin != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(command.Stdin))
		script.WriteString("printf %s ")
		script.WriteString(shellQuote(encoded))
		script.WriteString(" | base64 -d | ")
	}
	script.WriteString("exec ")
	script.WriteString(shellQuote(command.Executable))
	for _, argument := range command.Args {
		script.WriteByte(' ')
		script.WriteString(shellQuote(argument))
	}
	script.WriteByte('\n')
	return script.String(), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
