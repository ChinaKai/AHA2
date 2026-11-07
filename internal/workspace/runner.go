package workspace

import (
	"context"
	"errors"
	"time"
)

type Command struct {
	Executable string
	Args       []string
	Dir        string
	Env        map[string]string
	Stdin      string
	// Input keeps stdin open after Stdin has been written: every value received
	// is written as is, and closing the channel closes stdin. Only runners that
	// report StreamsInput accept it; the others reject a command that sets it.
	Input   <-chan string
	Timeout time.Duration
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

// errInputNotStreamed rejects Command.Input on a transport that sends stdin
// as one payload; silently dropping it would leave the caller waiting for
// writes the process never receives.
var errInputNotStreamed = errors.New("workspace: this transport cannot stream stdin after start")

// StreamsInput reports whether runner forwards Command.Input to a running
// process.
func StreamsInput(runner Runner) bool {
	streamer, ok := runner.(interface{ StreamsInput() bool })
	return ok && streamer.StreamsInput()
}
