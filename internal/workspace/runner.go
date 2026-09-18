package workspace

import (
	"context"
	"time"
)

type Command struct {
	Executable string
	Args       []string
	Dir        string
	Env        map[string]string
	Stdin      string
	Timeout    time.Duration
	// OutputLimit keeps only the latest bytes from stdout and stderr. Zero means unlimited.
	OutputLimit int
	// KillTree terminates the local process tree when the command context is cancelled.
	KillTree bool
	// ReverseForward, when set, asks the runner to expose an AHA-side address
	// inside the workspace for the lifetime of this command. The runner fills in
	// EnvName once the forward is up, so the command can be told where to connect
	// without knowing how the forward is carried. It is best-effort: a transport
	// that cannot forward still runs the command.
	ReverseForward *ReverseForward
}

type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

type LineHandler func(string)

type Runner interface {
	Run(context.Context, Command, LineHandler) (Result, error)
}
