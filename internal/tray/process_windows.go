//go:build windows

package tray

import (
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

type OSLauncher struct{}

func (OSLauncher) Start(command Command) (Child, error) {
	cmd := exec.Command(command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &windowsChild{cmd: cmd, done: make(chan struct{})}, nil
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
