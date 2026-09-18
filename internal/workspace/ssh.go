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
	if command.ReverseForward != nil {
		return runner.runWithReverseForward(ctx, command, platform, onLine)
	}
	return runner.runConnected(ctx, command, platform, onLine)
}

// runConnected runs the command on its own SSH connection.
func (runner *SSHRunner) runConnected(ctx context.Context, command Command, platform string, onLine LineHandler) (Result, error) {
	client, err := runner.dial(ctx)
	if err != nil {
		return Result{}, err
	}
	defer client.Close()
	return runner.execCommand(ctx, client, command, platform, onLine)
}

// runWithReverseForward runs the command on a connection that also carries a
// forwarding to an AHA-side address, so the command can reach AHA even when the
// workspace cannot dial it directly.
//
// The forward lives exactly as long as this connection, which is the lifetime of
// the inner command: when the backend process exits, the listener and the tunnel
// go with it.
//
// Which address the command gets is decided by asking the workspace itself, over
// this connection, rather than by assuming. Whether a forward works depends on the
// workspace's own network position, and AHA cannot see that from outside: an
// established forward that does not carry traffic looks exactly like a working one
// until something tries it. Guessing wrong here is unusually costly, because the
// Turn does not fail — it runs to its full duration with every Agent API call
// timing out, which reads as the Agent hanging rather than as a broken address.
func (runner *SSHRunner) runWithReverseForward(ctx context.Context, command Command, platform string, onLine LineHandler) (Result, error) {
	client, err := runner.dial(ctx)
	if err != nil {
		return Result{}, err
	}
	defer client.Close()

	configured := command.Env[command.ReverseForward.EnvName]
	tunnelURL, cause := runner.openForward(client, command.ReverseForward)

	if tunnelURL != "" {
		switch probeErr := runner.probeAgentAPI(ctx, client, platform, tunnelURL); {
		case probeErr == nil || probeErr == errProbeUnavailable:
			// Either the tunnel answers, or the workspace has no tool to ask with.
			// Both cases take the tunnel: it is the only candidate that can work
			// for a remote workspace whose configured address is loopback.
			setCommandEnv(command, command.ReverseForward.EnvName, tunnelURL)
			return runner.execCommand(ctx, client, command, platform, onLine)
		default:
			cause = fmt.Sprintf("%s；隧道已建立但工作区无法通过它访问 AHA（%s）", cause, probeErr)
		}
	}

	// The forward is unusable. Accept the configured address only if the workspace
	// can actually reach it; otherwise this Turn would run to its full duration
	// failing every Agent API call.
	if configured != "" {
		switch probeErr := runner.probeAgentAPI(ctx, client, platform, configured); {
		case probeErr == nil, probeErr == errProbeUnavailable:
			// Reachable, or unverifiable. A workspace that is merely minimal must
			// keep the behaviour it had before; only positive evidence that nothing
			// answers is grounds for stopping the Turn.
			return runner.execCommand(ctx, client, command, platform, onLine)
		default:
			cause = fmt.Sprintf("%s；配置地址 %s 也不可达（%s）", cause, configured, probeErr)
		}
	}
	return Result{}, fmt.Errorf(
		"Agent API 不可达，已停止本 Turn 以免它在整个时限内空转：%s。\n"+
			"该工作区解析出的 Agent API 地址是 loopback，从工作区内部指向它自己，因此只能依赖反向隧道。\n"+
			"请检查 AHA 宿主到 %s 的 SSH 连接是否允许端口转发（sshd 需 AllowTcpForwarding yes 与 PermitListen 放行）。",
		cause, runner.Host)
}

// openForward establishes the reverse forward and returns the workspace-side URL
// that reaches it, or an empty URL and the reason it could not be established.
func (runner *SSHRunner) openForward(client *ssh.Client, forward *ReverseForward) (string, string) {
	listener, err := client.ListenTCP(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return "", fmt.Sprintf("服务端拒绝或未响应端口转发请求（%v）", err)
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.Port == 0 {
		listener.Close()
		return "", "服务端没有分配转发端口"
	}
	go serveReverseForward(listener, forward.Target)
	// The workspace reaches the forward on its own loopback, so the value is the
	// same regardless of how AHA itself is addressed or whether its listener is
	// public. That is the point: no address configured anywhere is involved.
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(address.Port)), ""
}

func setCommandEnv(command Command, name, value string) {
	if command.Env == nil {
		command.Env = map[string]string{}
	}
	command.Env[name] = value
}

// errProbeUnavailable means the workspace has no tool to make the request, so the
// probe is inconclusive rather than negative. Treating it as a failure would fail
// Turns on workspaces that are merely minimal.
var errProbeUnavailable = errors.New("工作区中没有 curl、wget 或 Python，无法验证")

// probeAgentAPI asks the workspace itself whether baseURL answers /healthz with an
// AHA2 health payload, using the connection that carries any forward.
//
// It deliberately reuses the workspace's own tooling rather than dialing from AHA:
// the question is what the workspace can reach, not what AHA can.
func (runner *SSHRunner) probeAgentAPI(ctx context.Context, client *ssh.Client, platform, baseURL string) error {
	if baseURL == "" {
		return fmt.Errorf("地址为空")
	}
	if isWindowsPlatform(platform) {
		// A Windows workspace reaches loopback through a different bootstrap, and
		// reading it with the POSIX probe below would give a wrong answer. Reporting
		// "cannot verify" keeps today's behaviour rather than guessing.
		return errProbeUnavailable
	}
	script := `set -eu
export NO_PROXY='*' no_proxy='*'
probe_url="$1/healthz"
if command -v curl >/dev/null 2>&1; then
  body=$(curl --noproxy '*' -fsS --connect-timeout 3 --max-time 6 "$probe_url") || exit 1
elif command -v wget >/dev/null 2>&1; then
  body=$(wget -qO- -T 6 "$probe_url") || exit 1
elif command -v python3 >/dev/null 2>&1; then
  body=$(python3 -c 'import sys,urllib.request;print(urllib.request.urlopen(sys.argv[1],timeout=6).read(4096).decode())' "$probe_url") || exit 1
else
  exit 3
fi
printf '%s' "$body"`
	result, err := runner.execCommand(ctx, client, Command{
		Executable: "sh",
		Args:       []string{"-c", script, "aha-agent-api-verify", baseURL},
		Timeout:    20 * time.Second, OutputLimit: 4096,
	}, platform, nil)
	if err != nil {
		return err
	}
	if result.ExitCode == 3 {
		return errProbeUnavailable
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit code %d", result.ExitCode)
		}
		return fmt.Errorf("%s", detail)
	}
	return verifyAHA2HealthPayload(result.Stdout)
}

// execCommand builds the transport-specific invocation and runs it on an
// established connection.
func (runner *SSHRunner) execCommand(ctx context.Context, client *ssh.Client, command Command, platform string, onLine LineHandler) (Result, error) {
	if isWindowsPlatform(platform) {
		payload, err := windowsRemotePayload(command)
		if err != nil {
			return Result{}, err
		}
		return runner.execOnClient(ctx, client, windowsBootstrapCommand(), payload, command.OutputLimit, onLine)
	}
	script, err := remoteScript(command)
	if err != nil {
		return Result{}, err
	}
	return runner.execOnClient(ctx, client, "sh -s", script, command.OutputLimit, onLine)
}

func (runner *SSHRunner) dial(ctx context.Context) (*ssh.Client, error) {
	port := runner.Port
	if port == 0 {
		port = 22
	}
	return runner.connect(ctx, net.JoinHostPort(runner.Host, strconv.Itoa(port)))
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
	client, err := runner.connect(ctx, net.JoinHostPort(runner.Host, strconv.Itoa(port)))
	if err != nil {
		return Result{}, err
	}
	defer client.Close()
	return runner.execOnClient(ctx, client, remoteCommand, stdin, outputLimit, onLine)
}

// execOnClient runs one command over an already-established connection. Splitting
// it out lets a caller reuse the same connection for a reverse forward, whose
// lifetime must match the command's.
func (runner *SSHRunner) execOnClient(ctx context.Context, client *ssh.Client, remoteCommand, stdin string, outputLimit int, onLine LineHandler) (Result, error) {
	start := time.Now()
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
