//go:build windows

package browser

import (
	"os/exec"
	"strconv"
)

func configureHostProcess(*exec.Cmd) {}

// killHostProcess terminates the Node host and the Edge processes Playwright
// started underneath it. taskkill /T is the only dependency-free way to walk
// that tree on Windows; the direct Kill is a fallback when taskkill is missing.
func killHostProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	pid := strconv.Itoa(command.Process.Pid)
	_ = exec.Command("taskkill", "/PID", pid, "/T", "/F").Run()
	return command.Process.Kill()
}
