//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package workspaceterminal

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type unixTerminal struct {
	*os.File
	process *os.Process
}

func StartLocal(root string, cols, rows int) (Process, error) {
	shell := os.Getenv("SHELL")
	if !filepath.IsAbs(shell) {
		shell = "/bin/sh"
	}
	command := exec.Command(shell, "-i")
	command.Dir = root
	command.Env = shellEnvironment()
	file, err := pty.StartWithSize(command, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	// pty opens the master in blocking mode on some Unix platforms. Hand a
	// nonblocking descriptor to Go so close can interrupt pending input.
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	_ = file.Close()
	if err == nil {
		err = unix.SetNonblock(fd, true)
		if err != nil {
			_ = unix.Close(fd)
		}
	}
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, err
	}
	file = os.NewFile(uintptr(fd), "terminal PTY")
	go func() { _ = command.Wait() }()
	return &unixTerminal{File: file, process: command.Process}, nil
}

func (p *unixTerminal) Resize(cols, rows int) error {
	control, err := p.File.SyscallConn()
	if err != nil {
		return err
	}
	var resizeErr error
	err = control.Control(func(fd uintptr) {
		resizeErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	})
	if err != nil {
		return err
	}
	return resizeErr
}

func (p *unixTerminal) Close() error {
	// Closing the master hangs up the controlling terminal and its foreground
	// process group. Kill the shell as well if it has ignored that hangup.
	err := p.File.Close()
	_ = p.process.Kill()
	return err
}

func LocalSupported() bool { return true }
