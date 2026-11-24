//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package workspaceterminal

import "errors"

func StartLocal(string, int, int) (Process, error) {
	return nil, errors.New("workspace_terminal_unsupported")
}

func LocalSupported() bool { return false }
