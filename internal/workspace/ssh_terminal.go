package workspace

import (
	"context"
	"errors"
	"io"
	"path"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

var driveAbsolute = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

type SSHTerminal struct {
	client  *ssh.Client
	session *ssh.Session
	input   io.WriteCloser
	output  *io.PipeReader
}

func (runner *SSHRunner) StartTerminal(ctx context.Context, root string, cols, rows int) (*SSHTerminal, error) {
	platform, err := runner.remotePlatform(ctx)
	if err != nil {
		return nil, err
	}
	command, err := sshTerminalCommand(platform, root)
	if err != nil {
		return nil, err
	}
	client, err := runner.dial(ctx)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	terminal, err := startSSHPTY(client, command, cols, rows)
	if err != nil {
		_ = client.Close()
	}
	return terminal, err
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
	return "cd " + shellQuote(root) + " && exec \"${SHELL:-/bin/sh}\" -i", nil
}

func startSSHPTY(client *ssh.Client, command string, cols, rows int) (*SSHTerminal, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	input, err := session.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, writer := io.Pipe()
	session.Stdout, session.Stderr = writer, writer
	if err := session.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{ssh.ECHO: 1}); err != nil {
		return nil, err
	}
	if err := session.Start(command); err != nil {
		return nil, err
	}
	go func() {
		_ = session.Wait()
		_ = writer.Close()
	}()
	return &SSHTerminal{client: client, session: session, input: input, output: output}, nil
}

func (p *SSHTerminal) Read(data []byte) (int, error)  { return p.output.Read(data) }
func (p *SSHTerminal) Write(data []byte) (int, error) { return p.input.Write(data) }
func (p *SSHTerminal) Resize(cols, rows int) error    { return p.session.WindowChange(rows, cols) }

func (p *SSHTerminal) Close() error {
	_ = p.output.Close()
	// Closing this dedicated SSH connection tears down the remote PTY channel
	// and unblocks concurrent reads, writes and window change requests.
	return p.client.Close()
}
