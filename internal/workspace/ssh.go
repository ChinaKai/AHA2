package workspace

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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
	"unicode/utf16"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type SSHRunner struct {
	Host             string
	User             string
	Port             int
	Auth             string
	Password         string
	Platform         string
	KnownHostsPaths  []string
	PrivateKeyPaths  []string
	platformMu       sync.Mutex
	detectedPlatform string
}

func (runner *SSHRunner) Run(parent context.Context, command Command, onLine LineHandler) (Result, error) {
	if runner.Host == "" {
		return Result{}, fmt.Errorf("ssh host is required")
	}
	if runner.User == "" {
		return Result{}, fmt.Errorf("ssh user is required")
	}
	ctx := parent
	cancel := func() {}
	if command.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, command.Timeout)
	}
	defer cancel()
	platform, err := runner.remotePlatform(ctx)
	if err != nil {
		return Result{}, err
	}
	if isWindowsPlatform(platform) {
		payload, payloadErr := windowsRemotePayload(command)
		if payloadErr != nil {
			return Result{}, payloadErr
		}
		return runner.runRaw(ctx, windowsBootstrapCommand(), payload, 0, command.OutputLimit, onLine)
	}
	script, err := remoteScript(command)
	if err != nil {
		return Result{}, err
	}
	return runner.runRaw(ctx, "sh -s", script, 0, command.OutputLimit, onLine)
}

func (runner *SSHRunner) runRaw(parent context.Context, remoteCommand, stdin string, timeout time.Duration, outputLimit int, onLine LineHandler) (Result, error) {
	port := runner.Port
	if port == 0 {
		port = 22
	}
	ctx := parent
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
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
	session.Stdin = strings.NewReader(stdin)
	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	stderrPipe, err := session.StderrPipe()
	if err != nil {
		return Result{}, err
	}
	stdout, stderr := newOutputBuffer(outputLimit), newOutputBuffer(outputLimit)
	var wait sync.WaitGroup
	wait.Add(2)
	go scanOutput(stdoutPipe, stdout, onLine, &wait)
	go scanOutput(stderrPipe, stderr, nil, &wait)
	done := make(chan error, 1)
	go func() { done <- session.Run(remoteCommand) }()
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

func (runner *SSHRunner) connect(ctx context.Context, endpoint string) (*ssh.Client, error) {
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

func (runner *SSHRunner) remotePlatform(ctx context.Context) (string, error) {
	runner.platformMu.Lock()
	defer runner.platformMu.Unlock()
	if runner.detectedPlatform != "" {
		return runner.detectedPlatform, nil
	}
	if strings.TrimSpace(runner.Platform) != "" {
		runner.detectedPlatform = strings.TrimSpace(runner.Platform)
		return runner.detectedPlatform, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	const posixMarker = "AHA2_POSIX:"
	result, err := runner.runRaw(probeCtx, `sh -lc 'u=$(uname -srm) || exit; case "$u" in MINGW*|MSYS*|CYGWIN*) exit 64;; esac; printf "AHA2_POSIX:%s" "$u"'`, "", 0, 4096, nil)
	if err != nil {
		return "", err
	}
	if result.ExitCode == 0 {
		value := strings.TrimSpace(result.Stdout)
		if strings.HasPrefix(value, posixMarker) {
			runner.detectedPlatform = strings.TrimSpace(strings.TrimPrefix(value, posixMarker))
			return runner.detectedPlatform, nil
		}
	}
	const windowsMarker = "AHA2_WINDOWS:"
	windowsProbe := `[Console]::Out.Write("` + windowsMarker + `" + $env:PROCESSOR_ARCHITECTURE)`
	result, windowsErr := runner.runRaw(probeCtx, fixedPowerShellCommand(windowsProbe), "", 0, 4096, nil)
	if windowsErr != nil {
		return "", windowsErr
	}
	if result.ExitCode == 0 {
		value := strings.TrimSpace(result.Stdout)
		if strings.HasPrefix(value, windowsMarker) {
			architecture := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, windowsMarker)))
			if architecture == "" {
				architecture = "unknown"
			}
			runner.detectedPlatform = "windows/" + architecture
			return runner.detectedPlatform, nil
		}
	}
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = fmt.Sprintf("exit code %d", result.ExitCode)
	}
	return "", fmt.Errorf("detect SSH target platform: %s", detail)
}

func (runner *SSHRunner) DetectedPlatform() string {
	runner.platformMu.Lock()
	defer runner.platformMu.Unlock()
	if runner.detectedPlatform != "" {
		return runner.detectedPlatform
	}
	return strings.TrimSpace(runner.Platform)
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

func IsUnknownSSHHostKey(err error) bool {
	if err == nil {
		return false
	}
	var keyErr *knownhosts.KeyError
	return (errors.As(err, &keyErr) && len(keyErr.Want) == 0) ||
		strings.Contains(err.Error(), "knownhosts: key is unknown") ||
		strings.Contains(err.Error(), "SSH known_hosts is missing")
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

type windowsCommandMetadata struct {
	Executable string            `json:"executable"`
	Arguments  string            `json:"arguments"`
	Directory  string            `json:"directory,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

func windowsRemotePayload(command Command) (string, error) {
	if command.Executable == "" {
		return "", fmt.Errorf("remote executable is required")
	}
	for key := range command.Env {
		if !environmentName.MatchString(key) {
			return "", fmt.Errorf("invalid environment name %q", key)
		}
	}
	metadata, err := json.Marshal(windowsCommandMetadata{
		Executable: command.Executable, Arguments: windowsCommandLine(command.Args),
		Directory: command.Dir, Env: command.Env,
	})
	if err != nil {
		return "", err
	}
	if len(metadata) > 1<<20 {
		return "", fmt.Errorf("Windows SSH command metadata exceeds 1 MiB")
	}
	return fmt.Sprintf("%08x\n", len(metadata)) + string(metadata) + command.Stdin, nil
}

func windowsCommandLine(arguments []string) string {
	quoted := make([]string, len(arguments))
	for index, argument := range arguments {
		quoted[index] = windowsQuoteArgument(argument)
	}
	return strings.Join(quoted, " ")
}

func windowsQuoteArgument(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n\v\"") {
		return value
	}
	var result strings.Builder
	result.WriteByte('"')
	backslashes := 0
	for _, character := range value {
		switch character {
		case '\\':
			backslashes++
		case '"':
			result.WriteString(strings.Repeat("\\", backslashes*2+1))
			result.WriteRune(character)
			backslashes = 0
		default:
			result.WriteString(strings.Repeat("\\", backslashes))
			result.WriteRune(character)
			backslashes = 0
		}
	}
	result.WriteString(strings.Repeat("\\", backslashes*2))
	result.WriteByte('"')
	return result.String()
}

func windowsBootstrapCommand() string {
	return fixedPowerShellCommand(windowsSSHBootstrap)
}

func fixedPowerShellCommand(script string) string {
	if len(script) > 1024 {
		var compressed bytes.Buffer
		writer, _ := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
		_, _ = writer.Write([]byte(script))
		_ = writer.Close()
		packed := base64.StdEncoding.EncodeToString(compressed.Bytes())
		script = `$bytes=[Convert]::FromBase64String('` + packed + `');$memory=New-Object IO.MemoryStream(,$bytes);$gzip=New-Object IO.Compression.GzipStream($memory,[IO.Compression.CompressionMode]::Decompress);$reader=New-Object IO.StreamReader($gzip,[Text.Encoding]::UTF8);& ([ScriptBlock]::Create($reader.ReadToEnd()))`
	}
	encoded := utf16.Encode([]rune(script))
	data := make([]byte, len(encoded)*2)
	for index, value := range encoded {
		binary.LittleEndian.PutUint16(data[index*2:], value)
	}
	return "powershell.exe -NoLogo -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(data)
}

const windowsSSHBootstrap = `$ErrorActionPreference = 'Stop'
$utf8 = New-Object Text.UTF8Encoding($false)
[Console]::OutputEncoding = $utf8
function Read-Exactly([IO.Stream]$Stream, [byte[]]$Buffer, [int]$Count) {
  $offset = 0
  while ($offset -lt $Count) {
    $read = $Stream.Read($Buffer, $offset, $Count - $offset)
    if ($read -le 0) { throw 'AHA2_SSH_FRAME_TRUNCATED' }
    $offset += $read
  }
}
$child = $null
try {
  $inputStream = [Console]::OpenStandardInput()
  $header = New-Object byte[] 9
  Read-Exactly $inputStream $header 9
  if ($header[8] -ne 10) { throw 'AHA2_SSH_FRAME_INVALID' }
  $lengthText = [Text.Encoding]::ASCII.GetString($header, 0, 8)
  $metadataLength = [Convert]::ToInt32($lengthText, 16)
  if ($metadataLength -lt 2 -or $metadataLength -gt 1048576) { throw 'AHA2_SSH_METADATA_INVALID' }
  $metadataBytes = New-Object byte[] $metadataLength
  Read-Exactly $inputStream $metadataBytes $metadataLength
  $metadata = [Text.Encoding]::UTF8.GetString($metadataBytes) | ConvertFrom-Json
  if ($metadata.directory) { Set-Location -LiteralPath ([string]$metadata.directory) }
  if ($metadata.env) {
    foreach ($property in $metadata.env.PSObject.Properties) {
      [Environment]::SetEnvironmentVariable($property.Name, [string]$property.Value, 'Process')
    }
  }
  $resolved = Get-Command -Name ([string]$metadata.executable) -CommandType Application,ExternalScript -ErrorAction SilentlyContinue | Select-Object -First 1
  if (-not $resolved) {
    [Console]::Error.WriteLine('AHA2_COMMAND_NOT_FOUND:' + [string]$metadata.executable)
    exit 127
  }
  $extension = [IO.Path]::GetExtension($resolved.Source).ToLowerInvariant()
  $start = New-Object Diagnostics.ProcessStartInfo
  $start.UseShellExecute = $false
  $start.CreateNoWindow = $true
  $start.RedirectStandardInput = $true
  $start.RedirectStandardOutput = $true
  $start.RedirectStandardError = $true
  if ($extension -eq '.cmd' -or $extension -eq '.bat') {
    $start.FileName = $env:ComSpec
    $start.Arguments = '/d /s /c ""' + $resolved.Source.Replace('"', '""') + '" ' + [string]$metadata.arguments + '"'
  } elseif ($extension -eq '.ps1') {
    $start.FileName = [Diagnostics.Process]::GetCurrentProcess().MainModule.FileName
    $start.Arguments = '-NoLogo -NoProfile -NonInteractive -File "' + $resolved.Source.Replace('"', '\"') + '" ' + [string]$metadata.arguments
  } else {
    $start.FileName = $resolved.Source
    $start.Arguments = [string]$metadata.arguments
  }
  $child = New-Object Diagnostics.Process
  $child.StartInfo = $start
  if (-not $child.Start()) { throw 'AHA2_COMMAND_START_FAILED' }
  $stdoutTask = $child.StandardOutput.BaseStream.CopyToAsync([Console]::OpenStandardOutput())
  $stderrTask = $child.StandardError.BaseStream.CopyToAsync([Console]::OpenStandardError())
  $inputStream.CopyTo($child.StandardInput.BaseStream)
  $child.StandardInput.Close()
  $child.WaitForExit()
  $stdoutTask.GetAwaiter().GetResult()
  $stderrTask.GetAwaiter().GetResult()
  exit $child.ExitCode
} catch {
  [Console]::Error.WriteLine('AHA2_SSH_BOOTSTRAP_ERROR:' + $_.Exception.Message)
  if ($child -and -not $child.HasExited) { try { $child.Kill() } catch {} }
  exit 1
}`

func isWindowsPlatform(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "windows/") || strings.Contains(value, "windows_nt") || strings.Contains(value, "microsoft windows")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
