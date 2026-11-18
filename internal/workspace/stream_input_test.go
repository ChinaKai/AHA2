package workspace

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestSSHRunnerStreamsInputAfterStart drives the real SSH runner against a
// session that echoes each stdin line while the command is still running. The
// first line proves the initial payload reaches the backend, the second proves
// stdin stays open after start, and stdin-eof proves closing the input channel
// ends the remote stdin.
func TestSSHRunnerStreamsInputAfterStart(t *testing.T) {
	hostSigner, clientKey := newSSHKeyPair(t)
	listener, commands, serverErrors := startStreamingSSHServer(t, hostSigner, echoStdinSession)
	runner := &SSHRunner{
		Host: "127.0.0.1", User: "dev", Platform: "linux",
		Port:            listener.Addr().(*net.TCPAddr).Port,
		KnownHostsPaths: []string{writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())},
		PrivateKeyPaths: []string{writePrivateKey(t, clientKey)},
	}
	command := Command{
		Executable: "claude",
		Args:       []string{"-p", "--input-format", "stream-json", "--replay-user-messages"},
		Dir:        "/workspace",
		Env:        map[string]string{"AHA2_AGENT_API_TOKEN": "a'b"},
		Stdin:      "initial\n",
		Timeout:    20 * time.Second,
	}
	input := make(chan string)
	lines := make(chan string, 16)
	done := make(chan struct{})
	var result Result
	var runErr error
	go func() {
		defer close(done)
		command.Input = input
		result, runErr = runner.Run(context.Background(), command, func(line string) { lines <- line })
	}()

	// The exec request carries only the constant bootstrap. The environment
	// (including the Turn capability) must arrive on stdin as a base64 metadata
	// line, followed by the backend's own stream.
	received := <-commands
	if received != streamBootstrapCommand() {
		t.Fatalf("remote command = %q, want the constant bootstrap", received)
	}
	if strings.Contains(received, "a'b") || strings.Contains(received, "AHA2_AGENT_API_TOKEN") {
		t.Fatalf("exec request leaked Turn environment: %q", received)
	}
	metadataLine := <-lines
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(metadataLine, "echo:"))
	if err != nil {
		t.Fatalf("bootstrap metadata is not base64: %q: %v", metadataLine, err)
	}
	wantScript, err := streamRemoteScript(command)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != wantScript {
		t.Fatalf("metadata script = %q, want %q", decoded, wantScript)
	}
	if strings.Contains(wantScript, "base64 -d") {
		t.Fatalf("streaming script consumes stdin instead of forwarding it: %s", wantScript)
	}
	if !strings.Contains(wantScript, "exec 'claude' '-p' '--input-format' 'stream-json'") {
		t.Fatalf("script does not exec the backend with inherited stdin: %s", wantScript)
	}
	if !strings.Contains(wantScript, "<&3") {
		t.Fatalf("script does not hand fd 3 to the backend: %s", wantScript)
	}
	waitTestLine(t, lines, "echo:initial")
	input <- "second\n"
	waitTestLine(t, lines, "echo:second")
	close(input)
	waitTestLine(t, lines, "stdin-eof")
	<-done
	if runErr != nil || result.ExitCode != 0 {
		t.Fatalf("result=%#v err=%v", result, runErr)
	}
	if err := <-serverErrors; err != nil {
		t.Fatalf("ssh server: %v", err)
	}
}

// TestStreamBootstrapKeepsTurnEnvironmentOutOfArgv runs the constant bootstrap
// under a real shell. The script that exports the Turn environment is carried
// only on stdin, so the exec argument stays free of the capability token, and
// fd 3 still hands the backend the rest of the stream.
func TestStreamBootstrapKeepsTurnEnvironmentOutOfArgv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	command := Command{
		Executable: "sh",
		Args:       []string{"-c", `printf 'TOKEN=%s\n' "$AHA2_AGENT_API_TOKEN"; cat`},
		Env:        map[string]string{"AHA2_AGENT_API_TOKEN": "secret-turn-token"},
		Stdin:      "first\n",
	}
	bootstrap := streamBootstrapCommand()
	if strings.Contains(bootstrap, "secret-turn-token") || strings.Contains(bootstrap, "AHA2_AGENT_API_TOKEN") {
		t.Fatalf("bootstrap argument leaked the Turn environment: %q", bootstrap)
	}
	metadata, err := streamRemoteMetadata(command)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(metadata, "secret-turn-token") {
		// The metadata is base64 on purpose; plaintext here would mean the helper
		// stopped encoding and the token would sit in the stdin contract.
		t.Fatalf("metadata is not encoded: %q", metadata)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, "sh", "-c", bootstrap)
	process.Stdin = strings.NewReader(metadata + command.Stdin + "second\n")
	output, err := process.CombinedOutput()
	if err != nil {
		t.Fatalf("bootstrap failed: %v\n%s", err, output)
	}
	if want := "TOKEN=secret-turn-token\nfirst\nsecond\n"; string(output) != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

// A cancelled turn must close the session and end the feeder even though the
// input channel is still open.
func TestSSHRunnerStreamingInputReturnsOnCancel(t *testing.T) {
	hostSigner, clientKey := newSSHKeyPair(t)
	listener, _, _ := startStreamingSSHServer(t, hostSigner, echoStdinSession)
	runner := &SSHRunner{
		Host: "127.0.0.1", User: "dev", Platform: "linux",
		Port:            listener.Addr().(*net.TCPAddr).Port,
		KnownHostsPaths: []string{writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())},
		PrivateKeyPaths: []string{writePrivateKey(t, clientKey)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := make(chan string)
	lines := make(chan string, 8)
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, Command{
			Executable: "claude", Stdin: "initial\n", Input: input, Timeout: 30 * time.Second,
		}, func(line string) { lines <- line })
		done <- err
	}()
	metadataLine := <-lines
	if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(metadataLine, "echo:")); err != nil {
		t.Fatalf("bootstrap metadata is not base64: %q: %v", metadataLine, err)
	}
	waitTestLine(t, lines, "echo:initial")
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

// A reverse-forwarded command must stream stdin the same way; this exercises
// the forwarding path with a real shell command rather than a fake session.
func TestSSHRunnerReverseForwardStreamsInput(t *testing.T) {
	ahaListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ahaListener.Close()
	aha := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, aha2HealthPayload)
	})}
	go func() { _ = aha.Serve(ahaListener) }()
	defer aha.Close()

	hostSigner, clientKey := newSSHKeyPair(t)
	listener, serverErr := startForwardCapableSSHServer(t, hostSigner)
	runner := &SSHRunner{
		Host: "127.0.0.1", User: "dev", Platform: "linux",
		Port:            listener.Addr().(*net.TCPAddr).Port,
		KnownHostsPaths: []string{writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())},
		PrivateKeyPaths: []string{writePrivateKey(t, clientKey)},
	}
	input := make(chan string)
	lines := make(chan string, 8)
	done := make(chan struct{})
	var result Result
	var runErr error
	go func() {
		defer close(done)
		result, runErr = runner.Run(context.Background(), Command{
			Executable: "cat",
			Stdin:      "first\n",
			Input:      input,
			Timeout:    20 * time.Second,
			ReverseForward: &ReverseForward{
				Target: ahaListener.Addr().String(), EnvName: "AHA2_AGENT_API_URL",
			},
		}, func(line string) { lines <- line })
	}()
	waitTestLine(t, lines, "first")
	input <- "second\n"
	waitTestLine(t, lines, "second")
	close(input)
	<-done
	if runErr != nil || result.ExitCode != 0 {
		t.Fatalf("result=%#v err=%v", result, runErr)
	}
	if result.Stdout != "first\nsecond\n" {
		t.Fatalf("stdout = %q, want streamed input", result.Stdout)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("ssh server: %v", err)
	}
}

// A command without Input keeps the historical one-shot shape: the script is
// the stdin of "sh -s" and carries the payload inline.
func TestSSHRunnerOneShotCommandKeepsScriptOnStdin(t *testing.T) {
	hostSigner, clientKey := newSSHKeyPair(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	config := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
		return nil, nil
	}}
	config.AddHostKey(hostSigner)
	received := make(chan string, 1)
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- serveMockSSHExec(listener, config, func(command, stdin string) (string, error) {
			received <- command + "\x00" + stdin
			return "ok\n", nil
		})
	}()
	runner := &SSHRunner{
		Host: "127.0.0.1", User: "dev", Platform: "linux",
		Port:            listener.Addr().(*net.TCPAddr).Port,
		KnownHostsPaths: []string{writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())},
		PrivateKeyPaths: []string{writePrivateKey(t, clientKey)},
	}
	result, err := runner.Run(context.Background(), Command{
		Executable: "echo", Stdin: "payload", Timeout: 20 * time.Second,
	}, nil)
	if err != nil || result.ExitCode != 0 || result.Stdout != "ok\n" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	got := <-received
	command, stdin, _ := strings.Cut(got, "\x00")
	if command != "sh -s" {
		t.Fatalf("one-shot remote command = %q, want sh -s", command)
	}
	if !strings.Contains(stdin, "base64 -d") || !strings.Contains(stdin, "exec 'echo'") {
		t.Fatalf("one-shot script does not carry the payload on stdin: %q", stdin)
	}
	if err := <-serverErrors; err != nil {
		t.Fatalf("ssh server: %v", err)
	}
}

func TestStreamRemoteScriptLeavesStdinForBackend(t *testing.T) {
	command := Command{
		Executable: "claude",
		Args:       []string{"--input-format", "stream-json"},
		Dir:        "/workspace",
		Env:        map[string]string{"AHA2_AGENT_API_URL": "http://127.0.0.1:1"},
		Stdin:      `{"type":"user"}` + "\n",
	}
	streamed, err := streamRemoteScript(command)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(streamed, "base64 -d") {
		t.Fatalf("streaming script consumes stdin: %s", streamed)
	}
	if !strings.Contains(streamed, "exec 'claude' '--input-format' 'stream-json'") {
		t.Fatalf("streaming script does not exec the backend: %s", streamed)
	}
	if !strings.Contains(streamed, "<&3") {
		t.Fatalf("streaming script does not use the preserved stdin: %s", streamed)
	}
	oneShot, err := remoteScript(command)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(oneShot, "base64 -d") {
		t.Fatalf("one-shot script lost the inline payload: %s", oneShot)
	}
}

// The WSL bridge cannot use "bash -s" for a streaming command: that stdin is
// the script. The script must move into an argument and the backend stdin must
// be passed through unchanged.
func TestWSLHostCommandStreamsScriptAsArgument(t *testing.T) {
	input := make(chan string)
	command := Command{
		Executable:  "claude",
		Args:        []string{"--input-format", "stream-json"},
		Stdin:       "initial\n",
		Input:       input,
		Timeout:     90 * time.Second,
		OutputLimit: 4096,
		KillTree:    true,
	}
	host, err := wslHostCommand("Ubuntu", command)
	if err != nil {
		t.Fatal(err)
	}
	if len(host.Args) != 6 || host.Args[0] != "-d" || host.Args[1] != "Ubuntu" || host.Args[2] != "-e" ||
		host.Args[3] != "bash" || host.Args[4] != "-c" || host.Args[5] != streamBootstrapCommand() {
		t.Fatalf("host args = %#v, want wsl -d Ubuntu -e bash -c <bootstrap>", host.Args)
	}
	if strings.Contains(host.Args[5], "AHA2_AGENT_API_URL") || strings.Contains(host.Args[5], "claude") {
		t.Fatalf("command line leaked the environment-bearing script: %q", host.Args[5])
	}
	metadata, err := streamRemoteMetadata(command)
	if err != nil {
		t.Fatal(err)
	}
	if host.Stdin != metadata+command.Stdin || host.Input != input {
		t.Fatalf("stdin wiring lost: stdin=%q input=%v", host.Stdin, host.Input)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(metadata))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decoded), "exec 'claude' '--input-format' 'stream-json'") ||
		!strings.Contains(string(decoded), "<&3") {
		t.Fatalf("metadata script lost the backend exec: %s", decoded)
	}
	if host.Timeout != command.Timeout || !host.KillTree || host.OutputLimit != command.OutputLimit {
		t.Fatalf("limits lost: %#v", host)
	}
}

// Without Input the WSL bridge keeps its historical shape.
func TestWSLHostCommandKeepsOneShotShape(t *testing.T) {
	command := Command{Executable: "echo", Stdin: "payload"}
	host, err := wslHostCommand("Ubuntu", command)
	if err != nil {
		t.Fatal(err)
	}
	if len(host.Args) != 5 || host.Args[3] != "bash" || host.Args[4] != "-s" {
		t.Fatalf("host args = %#v, want bash -s", host.Args)
	}
	want, err := remoteScript(command)
	if err != nil {
		t.Fatal(err)
	}
	if host.Stdin != want || host.Input != nil {
		t.Fatalf("one-shot stdin changed: %q input=%v", host.Stdin, host.Input)
	}
}

// WSLRunner.Run must hand the stream to the host process rather than closing
// it. The host process is replaced with a local helper so the test does not
// require a usable WSL distro; runProcess's streaming behavior is covered by
// TestRunProcessStreamsInputAfterStart.
func TestWSLRunnerFeedsStreamingInputToHostProcess(t *testing.T) {
	original := wslProcessRunner
	t.Cleanup(func() { wslProcessRunner = original })
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wslProcessRunner = func(parent context.Context, host Command, _ []string, onLine LineHandler) (Result, error) {
		helper := Command{
			Executable: executable,
			Args:       []string{"-test.run=^TestRunProcessHelperProcess$"},
			Env: map[string]string{
				processHelperModeEnv: "echo-input",
				processHelperExitEnv: "0",
			},
			Stdin:   host.Stdin,
			Input:   host.Input,
			Timeout: host.Timeout,
		}
		return runProcess(parent, helper, localEnvironment(os.Environ(), helper.Env), onLine)
	}
	input := make(chan string)
	lines := make(chan string, 8)
	done := make(chan struct{})
	var result Result
	var runErr error
	go func() {
		defer close(done)
		result, runErr = (WSLRunner{Distro: "Ubuntu"}).Run(context.Background(), Command{
			Executable: "claude", Stdin: "initial\n", Input: input, Timeout: 60 * time.Second,
		}, func(line string) { lines <- line })
	}()
	metadataLine := <-lines
	if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(metadataLine, "echo:")); err != nil {
		t.Fatalf("bootstrap metadata is not base64: %q: %v", metadataLine, err)
	}
	waitTestLine(t, lines, "echo:initial")
	input <- "second\n"
	waitTestLine(t, lines, "echo:second")
	close(input)
	waitTestLine(t, lines, "stdin-eof")
	<-done
	if runErr != nil || result.ExitCode != 0 {
		t.Fatalf("result=%#v err=%v", result, runErr)
	}
}

// TestWSLRunnerStreamsInputWithRealWSL is the only end-to-end check of the
// wsl.exe bridge. It is skipped whenever WSL or a distro is unavailable; this
// environment runs it through a Windows test binary.
func TestWSLRunnerStreamsInputWithRealWSL(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real WSL bridge requires Windows")
	}
	if !WSLExecutableFound() {
		t.Skip("wsl.exe is not available")
	}
	distros := WSLDistros()
	if len(distros) == 0 {
		t.Skip("no WSL distro is installed")
	}
	// Assert both the backend argv/environment and mid-turn stdin. With the
	// broken "--" form the script was re-parsed by the default shell: $# was 0
	// and the exported environment was gone, yet the command still exited 0.
	backend := `printf 'N=%s\n' "$#"
printf 'ARG1=%s\n' "$1"
printf 'ARG2=%s\n' "$2"
printf 'TOKEN=%s\n' "$AHA_X"
IFS= read -r first
printf 'FIRST=%s\n' "$first"
IFS= read -r second
printf 'SECOND=%s\n' "$second"`
	input := make(chan string)
	lines := make(chan string, 8)
	done := make(chan struct{})
	var result Result
	var runErr error
	go func() {
		defer close(done)
		result, runErr = (WSLRunner{Distro: distros[0]}).Run(context.Background(), Command{
			Executable: "sh",
			Args:       []string{"-c", backend, "x", "a", "b c"},
			Env:        map[string]string{"AHA_X": "token value"},
			Stdin:      "first\n",
			Input:      input,
			Timeout:    60 * time.Second,
		}, func(line string) { lines <- line })
	}()
	waitTestLine(t, lines, "N=2")
	waitTestLine(t, lines, "ARG1=a")
	waitTestLine(t, lines, "ARG2=b c")
	waitTestLine(t, lines, "TOKEN=token value")
	waitTestLine(t, lines, "FIRST=first")
	input <- "second\n"
	waitTestLine(t, lines, "SECOND=second")
	close(input)
	<-done
	if runErr != nil || result.ExitCode != 0 {
		t.Fatalf("result=%#v err=%v", result, runErr)
	}
	want := "N=2\nARG1=a\nARG2=b c\nTOKEN=token value\nFIRST=first\nSECOND=second\n"
	if result.Stdout != want {
		t.Fatalf("stdout = %q, want %q", result.Stdout, want)
	}
}

func waitTestLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	select {
	case line := <-lines:
		if line != want {
			t.Fatalf("line = %q, want %q", line, want)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}

// echoStdinSession is the remote side of the SSH streaming tests: it echoes
// each input line while stdin is still open and reports EOF.
func echoStdinSession(_ string, stdin io.Reader, stdout io.Writer) error {
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		if _, err := fmt.Fprintf(stdout, "echo:%s\n", scanner.Text()); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	_, err := io.WriteString(stdout, "stdin-eof\n")
	return err
}

// startStreamingSSHServer accepts one SSH connection, one session, and lets the
// handler read the session's stdin incrementally. commands receives the raw
// remote command for assertions.
func startStreamingSSHServer(
	t *testing.T,
	hostSigner ssh.Signer,
	handle func(command string, stdin io.Reader, stdout io.Writer) error,
) (net.Listener, <-chan string, <-chan error) {
	t.Helper()
	config := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
		return nil, nil
	}}
	config.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	commands := make(chan string, 4)
	serverErrors := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer connection.Close()
		server, channels, requests, err := ssh.NewServerConn(connection, config)
		if err != nil {
			serverErrors <- err
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		channelRequest, ok := <-channels
		if !ok {
			serverErrors <- fmt.Errorf("SSH session channel was not opened")
			return
		}
		channel, channelRequests, err := channelRequest.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer channel.Close()
		serverErrors <- serveStreamingSession(channel, channelRequests, handle, commands)
	}()
	return listener, commands, serverErrors
}

func serveStreamingSession(
	channel ssh.Channel,
	requests <-chan *ssh.Request,
	handle func(command string, stdin io.Reader, stdout io.Writer) error,
	commands chan<- string,
) error {
	var payload struct{ Command string }
	found := false
	for request := range requests {
		if request.Type != "exec" {
			_ = request.Reply(false, nil)
			continue
		}
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
			_ = request.Reply(false, nil)
			return err
		}
		_ = request.Reply(true, nil)
		found = true
		break
	}
	if !found {
		return fmt.Errorf("SSH exec request was not received")
	}
	commands <- payload.Command
	status := uint32(0)
	if err := handle(payload.Command, channel, channel); err != nil {
		status = 1
	}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
	return nil
}
