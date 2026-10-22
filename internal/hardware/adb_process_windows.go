//go:build windows

package hardware

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func configureADBCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
