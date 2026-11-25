package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var driveAbsolute = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

type SSHTerminal struct {
	client  *ssh.Client
	session *ssh.Session
	input   io.WriteCloser
	output  *io.PipeReader
	exited  chan struct{}
	exitErr error
}

func (runner *SSHRunner) StartTerminal(ctx context.Context, root string, cols, rows int) (*SSHTerminal, error) {
	platform, err := runner.remotePlatform(ctx)
	if err != nil {
		var probe sshPlatformProbeError
		if errors.As(err, &probe) {
			return nil, fmt.Errorf("detect SSH target platform: exit code %d (no platform marker)", probe.exitCode)
		}
		return nil, fmt.Errorf("detect SSH target platform: %w", terminalSSHConnectionError(err))
	}
	command, err := sshTerminalCommand(platform, root)
	if err != nil {
		return nil, err
	}
	client, err := runner.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("dial SSH: %w", terminalSSHConnectionError(err))
	}
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	terminal, err := startSSHPTY(client, command, cols, rows)
	if err != nil {
		_ = client.Close()
	}
	return terminal, err
}

func terminalSSHConnectionError(err error) error {
	var rejected *ssh.OpenChannelError
	if errors.As(err, &rejected) {
		return fmt.Errorf("SSH session channel rejected: reason %d", rejected.Reason)
	}
	var handshake sshHandshakeError
	if !errors.As(err, &handshake) {
		return err
	}
	var network *net.OpError
	var hostKey *knownhosts.KeyError
	var revokedKey *knownhosts.RevokedError
	var algorithm *ssh.AlgorithmNegotiationError
	switch {
	case errors.As(err, &network), errors.As(err, &hostKey), errors.As(err, &revokedKey), errors.Is(err, io.EOF):
		return err
	case errors.As(err, &algorithm):
		return errors.New("SSH handshake: no mutually supported algorithm")
	case strings.HasPrefix(handshake.Unwrap().Error(), "ssh: handshake failed: ssh: unable to authenticate,"):
		return errors.New("SSH handshake: unable to authenticate")
	default:
		// The SSH peer can include arbitrary text in a disconnect message.
		return fmt.Errorf("SSH handshake failed (%T)", errors.Unwrap(handshake.Unwrap()))
	}
}

func sshTerminalCommand(platform, root string) (string, error) {
	if strings.ContainsRune(root, 0) {
		return "", errors.New("invalid terminal root")
	}
	if strings.HasPrefix(strings.ToLower(platform), "windows") {
		// Use a literal path and stop if it cannot be selected. The child shell
		// inherits the OS directory set here, including when the SSH login uses cmd.
		if !driveAbsolute.MatchString(root) && !strings.HasPrefix(root, `\\`) {
			return "", errors.New("invalid terminal root")
		}
		root = "'" + strings.ReplaceAll(root, "'", "''") + "'"
		return fixedPowerShellCommand(`$ErrorActionPreference='Stop'; Set-Location -LiteralPath ` + root +
			`; [Environment]::CurrentDirectory=(Get-Location).ProviderPath; & "$PSHOME\powershell.exe" -NoLogo -NoProfile`), nil
	}
	if !path.IsAbs(root) {
		return "", errors.New("invalid terminal root")
	}
	// Fish cannot expand ${SHELL:-/bin/sh}. Let sh interpret the launch script
	// and pass the root as an argument, escaping backslashes outside quotes
	// because fish also interprets them within single quotes.
	quotedRoot := strings.ReplaceAll(shellQuote(root), `\`, `'\\'`)
	return `exec /bin/sh -c 'cd "$1" && exec "${SHELL:-/bin/sh}" -il' sh ` + quotedRoot, nil
}

func startSSHPTY(client *ssh.Client, command string, cols, rows int) (*SSHTerminal, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open SSH session: %w", terminalSSHConnectionError(err))
	}
	input, err := session.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open SSH terminal input: %w", err)
	}
	output, writer := io.Pipe()
	session.Stdout, session.Stderr = writer, writer
	if err := session.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{ssh.ECHO: 1}); err != nil {
		return nil, fmt.Errorf("request SSH PTY: %w", err)
	}
	if err := session.Start(command); err != nil {
		// ssh.Session.Start includes the command in its rejection error.
		if strings.HasPrefix(err.Error(), "ssh: command ") {
			return nil, errors.New("SSH server refused to start the shell")
		}
		return nil, fmt.Errorf("start SSH shell: %w", err)
	}
	terminal := &SSHTerminal{client: client, session: session, input: input, output: output, exited: make(chan struct{})}
	go func() {
		terminal.exitErr = session.Wait()
		close(terminal.exited)
		_ = writer.Close()
	}()
	return terminal, nil
}

func (p *SSHTerminal) Read(data []byte) (int, error)  { return p.output.Read(data) }
func (p *SSHTerminal) Write(data []byte) (int, error) { return p.input.Write(data) }
func (p *SSHTerminal) Resize(cols, rows int) error    { return p.session.WindowChange(rows, cols) }

func (p *SSHTerminal) ExitStatus() (int64, error) {
	<-p.exited
	var exit *ssh.ExitError
	switch {
	case p.exitErr == nil:
		return 0, nil
	case errors.As(p.exitErr, &exit):
		return int64(exit.ExitStatus()), nil
	default:
		// The SSH peer controls free-form exit messages and signals.
		return -1, fmt.Errorf("SSH shell exit status unavailable (%T)", p.exitErr)
	}
}

func (p *SSHTerminal) Close() error {
	_ = p.output.Close()
	// Closing this dedicated SSH connection tears down the remote PTY channel
	// and unblocks concurrent reads, writes and window change requests.
	return p.client.Close()
}
