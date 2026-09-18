package backend

import (
	"context"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

type Request struct {
	Runner              workspace.Runner
	WorkDir             string
	Model               string
	ContextWindow       int64
	ReasoningEffort     string
	StreamIdleTimeoutMS int
	StreamMaxRetries    int
	Environment         map[string]string
	Prompt              string
	ProviderSessionID   string
	// ReverseForward asks the transport to carry an AHA-side address into the
	// workspace for the lifetime of this run. Nil when the workspace can already
	// reach AHA by address.
	ReverseForward *workspace.ReverseForward
	Filesystem     string
	Approval       string
}

type Event struct {
	Type string
	Data map[string]any
}

type Result struct {
	Reply             string
	ExitCode          int
	ProviderSessionID string
}

type Adapter interface {
	Execute(context.Context, Request, func(Event)) (Result, error)
}
