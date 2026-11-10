//go:build windows

package tray

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// OSLauncher starts the managed AHA2 server process. When LogPath is set, the
// child's stdout and stderr are appended to a rotating log so panics, runtime
// fatal errors, and startup failures survive the hidden process.
type OSLauncher struct {
	LogPath     string
	MaxLogBytes int64
	// waitDelay bounds how long Wait waits for inherited stdout/stderr pipes
	// after the server exits. A crashed server can leave backend child processes
	// holding the pipe open; without a bound the supervisor would never observe
	// the exit and the crash would not be recorded or retried.
	waitDelay time.Duration
}

const defaultServerWaitDelay = 5 * time.Second

func (l OSLauncher) Start(command Command) (Child, error) {
	cmd := exec.Command(command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
	var out *rotatingLog
	if l.LogPath != "" {
		opened, err := openServerLog(l.LogPath, l.MaxLogBytes)
		if err == nil {
			out = opened
			cmd.Stdout = out
			cmd.Stderr = out
			delay := l.waitDelay
			if delay <= 0 {
				delay = defaultServerWaitDelay
			}
			cmd.WaitDelay = delay
			out.writeEntry("AHA2 tray starting server: %s", formatCommand(command))
		}
		// A log file that cannot be opened must not keep the server down; the
		// process still starts exactly as before, just without capture.
	}
	if err := cmd.Start(); err != nil {
		if out != nil {
			out.writeEntry("AHA2 tray server start failed: %v", err)
			_ = out.Close()
		}
		return nil, err
	}
	if out != nil {
		out.writeEntry("AHA2 tray server started pid=%d", cmd.Process.Pid)
	}
	child := &windowsChild{cmd: cmd, done: make(chan struct{})}
	if out == nil {
		return child, nil
	}
	return &loggedChild{windowsChild: child, log: out}, nil
}

// loggedChild records the child's exit code or wait error before the
// supervisor decides whether to retry, so a crash leaves a durable trace.
type loggedChild struct {
	*windowsChild
	log *rotatingLog
}

func (c *loggedChild) Wait() error {
	err := c.windowsChild.Wait()
	c.log.writeEntry("AHA2 tray server exited: %s", describeExit(err, c.cmd.ProcessState))
	_ = c.log.Close()
	return err
}

func describeExit(err error, state *os.ProcessState) string {
	if err == nil {
		if state != nil {
			return fmt.Sprintf("exit code %d", state.ExitCode())
		}
		return "exit code 0"
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		return fmt.Sprintf("exit code %d (0x%08X): %v", code, uint32(code), err)
	}
	if errors.Is(err, exec.ErrWaitDelay) && state != nil {
		code := state.ExitCode()
		return fmt.Sprintf("exit code %d (0x%08X): %v", code, uint32(code), err)
	}
	return err.Error()
}

type windowsChild struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
}

func (p *windowsChild) Wait() error {
	err := p.cmd.Wait()
	p.once.Do(func() { close(p.done) })
	return err
}

func (p *windowsChild) Stop(grace time.Duration) error {
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(p.cmd.Process.Pid)); err != nil {
		return p.killAndWait()
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
	}
	return p.killAndWait()
}

func (p *windowsChild) killAndWait() error {
	if err := p.cmd.Process.Kill(); err != nil {
		select {
		case <-p.done:
			return nil
		default:
			return err
		}
	}
	<-p.done
	return nil
}
