//go:build !windows

package browser

import (
	"os/exec"
	"syscall"
)

// configureHostProcess puts the child in its own process group so Edge and the
// Node host can be terminated together when AHA stops.
func configureHostProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killHostProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	// Negative PID targets the process group created by Setpgid. Falling back to
	// the single process keeps this correct if the group is already gone.
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return command.Process.Kill()
}
