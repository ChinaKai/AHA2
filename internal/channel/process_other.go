//go:build !windows

package channel

import "os/exec"

func attachPluginProcess(_ *exec.Cmd) (func(), error) {
	return func() {}, nil
}
