//go:build !windows

package hardware

import "os/exec"

func configureADBCommand(_ *exec.Cmd) {}
