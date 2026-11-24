//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package workspaceterminal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type unixTerminal struct {
	*os.File
	command   *exec.Cmd
	closeOnce sync.Once
	closeErr  error
}

func StartLocal(root string, cols, rows int) (Process, error) {
	shell := os.Getenv("SHELL")
	if !filepath.IsAbs(shell) {
		shell = "/bin/sh"
	}
	command := exec.Command(shell, "-i")
	command.Dir = root
	command.Env = shellEnvironment()
	// StartWithSize uses setsid before claiming the controlling terminal.
	// The shell becomes leader of both a new session and a new process group.
	file, err := pty.StartWithSize(command, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	// pty opens the master in blocking mode on some Unix platforms. Hand a
	// nonblocking descriptor to Go so close can interrupt pending input.
	terminal := &unixTerminal{File: file, command: command}
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err == nil {
		err = unix.SetNonblock(fd, true)
		if err != nil {
			_ = unix.Close(fd)
		}
	}
	if err != nil {
		_ = terminal.Close()
		return nil, err
	}
	_ = file.Close()
	terminal.File = os.NewFile(uintptr(fd), "terminal PTY")
	return terminal, nil
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
	p.closeOnce.Do(func() {
		// Keep the shell PID reserved until the group cleanup finishes. Even
		// if the shell has exited, Wait must not let an unrelated session
		// reuse its PID while we are still using it as a session identifier.
		killTerminalGroups(p.command.Process.Pid)
		p.closeErr = p.File.Close()
		go func() { _ = p.command.Wait() }()
	})
	return p.closeErr
}

func killTerminalGroups(session int) {
	// Stop the shell from accepting more work. With job control, a command
	// such as "nohup sleep &" belongs to a different group in this session.
	_ = unix.Kill(-session, unix.SIGKILL)
	killed := map[int]bool{session: true}
	for _, pid := range terminalProcessIDs() {
		group, err := unix.Getpgid(pid)
		if err != nil || group <= 0 || killed[group] {
			continue
		}
		if sid, err := unix.Getsid(pid); err == nil && sid == session {
			_ = unix.Kill(-group, unix.SIGKILL)
			killed[group] = true
		}
	}
}

func terminalProcessIDs() []int {
	var pids []int
	if runtime.GOOS == "linux" {
		if entries, err := os.ReadDir("/proc"); err == nil {
			for _, entry := range entries {
				if pid, err := strconv.Atoi(entry.Name()); err == nil && pid > 0 {
					pids = append(pids, pid)
				}
			}
			return pids
		}
	}
	// POSIX ps supplies PIDs on Unix hosts without Linux procfs. Group and
	// session ownership are still checked using the kernel's current values.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=")
	command.Env = shellEnvironment()
	output, err := command.Output()
	if err != nil {
		return nil
	}
	for _, value := range strings.Fields(string(output)) {
		if pid, err := strconv.Atoi(value); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

func LocalSupported() bool { return true }
