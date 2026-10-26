package hardware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const adbExecutableEnvironment = "AHA2_ADB_PATH"

const (
	MaxADBExecOutputBytes = 16 * 1024 * 1024
	maxADBExecTimeout     = 120 * time.Second
)

var ErrADBOutputTooLarge = errors.New("ADB command output exceeds limit")

type ADBExecResult struct {
	Endpoint string
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Duration time.Duration
}

type adbTerminal struct {
	command  *exec.Cmd
	stdin    io.WriteCloser
	output   *io.PipeReader
	writer   *io.PipeWriter
	done     chan struct{}
	once     sync.Once
	endpoint string
}

func ExecuteADB(ctx context.Context, endpoint string, arguments []string, input []byte, timeout time.Duration) (ADBExecResult, error) {
	if len(arguments) == 0 {
		return ADBExecResult{}, fmt.Errorf("ADB command arguments are required")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > maxADBExecTimeout {
		timeout = maxADBExecTimeout
	}
	executable, err := resolveADBExecutable()
	if err != nil {
		return ADBExecResult{}, err
	}
	return executeADBWithExecutable(ctx, executable, endpoint, arguments, input, timeout)
}

func executeADBWithExecutable(ctx context.Context, executable, endpoint string, arguments []string, input []byte, timeout time.Duration) (ADBExecResult, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	resolvedEndpoint, err := connectADB(commandCtx, executable, endpoint)
	if err != nil {
		return ADBExecResult{}, err
	}
	commandArgs := append([]string{"-s", resolvedEndpoint}, arguments...)
	command := exec.CommandContext(commandCtx, executable, commandArgs...)
	command.Stdin = bytes.NewReader(input)
	stdout := &limitedADBWriter{limit: MaxADBExecOutputBytes}
	stderr := &limitedADBWriter{limit: MaxADBExecOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	configureADBCommand(command)
	err = command.Run()
	result := ADBExecResult{
		Endpoint: resolvedEndpoint,
		ExitCode: 0,
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		Duration: time.Since(startedAt),
	}
	if stdout.overflow || stderr.overflow {
		return ADBExecResult{}, ErrADBOutputTooLarge
	}
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		return ADBExecResult{}, fmt.Errorf("ADB command timed out after %s", timeout)
	}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			result.ExitCode = exitError.ExitCode()
			return result, nil
		}
		return ADBExecResult{}, fmt.Errorf("execute ADB command: %w", err)
	}
	return result, nil
}

type limitedADBWriter struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (writer *limitedADBWriter) Write(data []byte) (int, error) {
	remaining := writer.limit - writer.buffer.Len()
	if remaining > 0 {
		count := len(data)
		if count > remaining {
			count = remaining
		}
		_, _ = writer.buffer.Write(data[:count])
	}
	if len(data) > remaining {
		writer.overflow = true
	}
	return len(data), nil
}

func (writer *limitedADBWriter) Bytes() []byte {
	return append([]byte(nil), writer.buffer.Bytes()...)
}

func PairADB(ctx context.Context, endpoint, pairingCode string) (string, error) {
	pairingCode = strings.TrimSpace(pairingCode)
	if len(pairingCode) != 6 {
		return "", fmt.Errorf("ADB pairing code must contain 6 digits")
	}
	for _, character := range pairingCode {
		if character < '0' || character > '9' {
			return "", fmt.Errorf("ADB pairing code must contain 6 digits")
		}
	}
	executable, err := resolveADBExecutable()
	if err != nil {
		return "", err
	}
	return pairADBWithExecutable(ctx, executable, endpoint, pairingCode)
}

func pairADBWithExecutable(ctx context.Context, executable, endpoint, pairingCode string) (string, error) {
	pairCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(pairCtx, executable, "pair", endpoint)
	command.Stdin = strings.NewReader(pairingCode + "\n")
	configureADBCommand(command)
	output, err := command.CombinedOutput()
	message := redactADBPairingCode(strings.TrimSpace(string(output)), pairingCode)
	if err != nil {
		return "", fmt.Errorf("adb pair %s: %s", endpoint, adbFailure(message, err))
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "failed") || strings.Contains(lower, "error") {
		return "", fmt.Errorf("adb pair %s: %s", endpoint, message)
	}
	return message, nil
}

func redactADBPairingCode(message, pairingCode string) string {
	if pairingCode == "" {
		return message
	}
	message = strings.ReplaceAll(message, pairingCode, "<pairing-code>")
	if len(message) > 8192 {
		message = message[:8192]
	}
	return message
}

func openADBTerminal(ctx context.Context, endpoint string) (io.ReadWriteCloser, error) {
	executable, err := resolveADBExecutable()
	if err != nil {
		return nil, err
	}
	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resolvedEndpoint, err := connectADB(connectCtx, executable, endpoint)
	if err != nil {
		return nil, err
	}

	command := exec.Command(executable, "-s", resolvedEndpoint, "shell", "-t", "-t")
	configureADBCommand(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open adb shell input: %w", err)
	}
	terminalOutput, writer := io.Pipe()
	command.Stdout = writer
	command.Stderr = writer
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = writer.Close()
		_ = terminalOutput.Close()
		return nil, fmt.Errorf("start adb shell: %w", err)
	}
	terminal := &adbTerminal{
		command:  command,
		stdin:    stdin,
		output:   terminalOutput,
		writer:   writer,
		done:     make(chan struct{}),
		endpoint: resolvedEndpoint,
	}
	go func() {
		waitErr := command.Wait()
		if waitErr != nil {
			_ = writer.CloseWithError(fmt.Errorf("adb shell: %w", waitErr))
		} else {
			_ = writer.Close()
		}
		close(terminal.done)
	}()
	return terminal, nil
}

func connectADB(ctx context.Context, executable, endpoint string) (string, error) {
	resolvedEndpoint, err := resolveADBMdnsEndpoint(ctx, executable, endpoint)
	if err != nil {
		return "", err
	}
	connect := exec.CommandContext(ctx, executable, "connect", resolvedEndpoint)
	configureADBCommand(connect)
	output, err := connect.CombinedOutput()
	message := strings.TrimSpace(string(output))
	if err != nil {
		return "", fmt.Errorf("adb connect %s: %s", resolvedEndpoint, adbFailure(message, err))
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "failed to connect") || strings.Contains(lower, "cannot connect") {
		return "", fmt.Errorf("adb connect %s: %s", resolvedEndpoint, message)
	}
	// `adb connect` reports success for a device it cannot actually use: until
	// the device accepts this computer's ADB key it stays "unauthorized", and
	// the shell below would fail with a bare exit code. Check the real state so
	// the operator is told to confirm the prompt on the device, which is the
	// only fix for a TV or phone that never paired.
	if state, stateErr := adbDeviceState(ctx, executable, resolvedEndpoint); stateErr == nil {
		switch state {
		case "", "device":
		case "unauthorized":
			return "", fmt.Errorf("设备未授权（%s）：请在电视/手机上确认「允许 USB 调试」弹窗（建议勾选一律允许）。若没有弹窗，先在开发者选项里「撤销 USB 调试授权」再重连本机。", resolvedEndpoint)
		case "offline":
			return "", fmt.Errorf("设备处于离线状态（%s）：请在设备上重新确认授权，或关闭再打开该设备的网络调试后重试。", resolvedEndpoint)
		default:
			return "", fmt.Errorf("ADB 设备状态异常（%s）：%s", resolvedEndpoint, state)
		}
	}
	return resolvedEndpoint, nil
}

// adbDeviceState reports "device", "unauthorized", "offline" or "" when the
// state cannot be read. A failure here must not block a working connection.
func adbDeviceState(ctx context.Context, executable, endpoint string) (string, error) {
	stateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(stateCtx, executable, "-s", endpoint, "get-state")
	configureADBCommand(command)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.ToLower(strings.TrimSpace(string(output)))
		// Older adb prints the state on stderr and exits non-zero.
		for _, state := range []string{"unauthorized", "offline", "device"} {
			if strings.Contains(message, state) {
				return state, nil
			}
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func resolveADBMdnsEndpoint(ctx context.Context, executable, configuredEndpoint string) (string, error) {
	configuredHost, _, err := net.SplitHostPort(configuredEndpoint)
	if err != nil {
		return configuredEndpoint, nil
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	command := exec.CommandContext(discoveryCtx, executable, "mdns", "services")
	configureADBCommand(command)
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		return configuredEndpoint, nil
	}
	matches := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		serviceType := fields[len(fields)-2]
		if serviceType != "_adb-tls-connect._tcp" && serviceType != "_adb._tcp" {
			continue
		}
		endpoint := fields[len(fields)-1]
		host, _, splitErr := net.SplitHostPort(endpoint)
		if splitErr == nil && strings.EqualFold(host, configuredHost) {
			matches[endpoint] = true
		}
	}
	if matches[configuredEndpoint] || len(matches) == 0 {
		return configuredEndpoint, nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple ADB mDNS services found for %s; review the hardware endpoint", configuredHost)
	}
	for endpoint := range matches {
		return endpoint, nil
	}
	return configuredEndpoint, nil
}

func (terminal *adbTerminal) ResolvedEndpoint() string {
	return terminal.endpoint
}

func resolveADBExecutable() (string, error) {
	configured := strings.Trim(strings.TrimSpace(os.Getenv(adbExecutableEnvironment)), `"`)
	if configured != "" {
		if path, err := exec.LookPath(configured); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("configured ADB executable is unavailable: %s", configured)
	}
	candidates := []string{"adb"}
	name := "adb"
	if runtime.GOOS == "windows" {
		name = "adb.exe"
	}
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData != "" {
		candidates = append(candidates, filepath.Join(localAppData, "Microsoft", "WinGet", "Links", name))
		packageMatches, _ := filepath.Glob(filepath.Join(
			localAppData, "Microsoft", "WinGet", "Packages",
			"Google.PlatformTools_*", "platform-tools", name,
		))
		sort.Strings(packageMatches)
		candidates = append(candidates, packageMatches...)
	}
	for _, root := range []string{
		os.Getenv("ANDROID_HOME"),
		os.Getenv("ANDROID_SDK_ROOT"),
		filepath.Join(localAppData, "Android", "Sdk"),
	} {
		if strings.TrimSpace(root) != "" {
			candidates = append(candidates, filepath.Join(root, "platform-tools", name))
		}
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("ADB executable not found; install Android Platform Tools or set %s", adbExecutableEnvironment)
}

func adbFailure(message string, err error) string {
	if message != "" {
		return message
	}
	return err.Error()
}

func (terminal *adbTerminal) Read(buffer []byte) (int, error) {
	return terminal.output.Read(buffer)
}

func (terminal *adbTerminal) Write(data []byte) (int, error) {
	return terminal.stdin.Write(data)
}

func (terminal *adbTerminal) Close() error {
	var result error
	terminal.once.Do(func() {
		if err := terminal.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			result = err
		}
		if terminal.command.Process != nil {
			_ = terminal.command.Process.Kill()
		}
		select {
		case <-terminal.done:
		case <-time.After(2 * time.Second):
			if result == nil {
				result = errors.New("timed out stopping adb shell")
			}
		}
		_ = terminal.writer.Close()
		_ = terminal.output.Close()
	})
	return result
}
