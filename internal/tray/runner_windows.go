//go:build windows

package tray

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

// listenQueryTimeout bounds the startup query. It runs before the server is up,
// so a slow or wedged binary must not delay the tray indefinitely.
const listenQueryTimeout = 10 * time.Second

// OSCommandRunner executes the server binary for a one-shot query.
type OSCommandRunner struct{}

func (OSCommandRunner) Output(command Command) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), listenQueryTimeout)
	defer cancel()
	process := exec.CommandContext(ctx, command.Path, command.Args...)
	process.Dir = command.Dir
	var stdout bytes.Buffer
	process.Stdout = &stdout
	if err := process.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
