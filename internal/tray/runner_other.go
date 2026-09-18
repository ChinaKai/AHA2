//go:build !windows

package tray

import "errors"

// OSCommandRunner is unavailable off Windows, where the tray does not run.
type OSCommandRunner struct{}

func (OSCommandRunner) Output(Command) (string, error) {
	return "", errors.New("AHA2 tray is only supported on Windows")
}
