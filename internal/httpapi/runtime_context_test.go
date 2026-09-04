package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

type runtimeContextRunner struct {
	command workspace.Command
}

func (runner *runtimeContextRunner) Run(
	_ context.Context,
	command workspace.Command,
	_ workspace.LineHandler,
) (workspace.Result, error) {
	runner.command = command
	return workspace.Result{
		ExitCode: 0,
		Stdout: `{"payload":{"type":"token_count","info":{"model_context_window":258400,"last_token_usage":{"input_tokens":100}}}}
{"payload":{"type":"token_count","info":{"model_context_window":997500,"last_token_usage":{"input_tokens":128592}}}}
`,
	}, nil
}

func TestCodexRuntimeContextUsesRemoteRunner(t *testing.T) {
	t.Parallel()
	runner := &runtimeContextRunner{}
	sample, ok := codexRuntimeContextFromRunner(
		context.Background(),
		runner,
		"/home/test/repo",
		"session-123",
	)
	if !ok || sample.InputTokens != 128592 || sample.ContextWindow != 997500 {
		t.Fatalf("remote runtime sample = %#v, ok=%v", sample, ok)
	}
	if runner.command.Executable != "sh" ||
		runner.command.Dir != "/home/test/repo" ||
		!strings.Contains(strings.Join(runner.command.Args, " "), "session-123") {
		t.Fatalf("remote runtime command = %#v", runner.command)
	}
}
