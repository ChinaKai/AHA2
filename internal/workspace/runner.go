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
