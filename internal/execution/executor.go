package execution

import (
	"context"
	"strconv"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

type Executor struct {
	Codex  backend.Codex
	Claude backend.Claude
}

func (executor Executor) Execute(ctx context.Context, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	if request.Snapshot.Backend == "claude" {
		return executor.runClaude(ctx, request, emit)
	}
	return executor.runCodex(ctx, request, emit)
}

func (executor Executor) runCodex(ctx context.Context, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	result, err := executor.Codex.Execute(ctx, backend.Request{
		Runner:  workspace.RunnerFor(request.Workspace),
		WorkDir: taskWorkDir(request),
		Model:   request.Model.WireModel, ContextWindow: request.Model.ContextWindow,
		ReasoningEffort: request.Snapshot.ReasoningEffort, Environment: request.Environment,
		Prompt: request.Prompt, ProviderSessionID: request.ProviderSessionID,
		Filesystem: request.Filesystem, Approval: request.Approval,
	}, func(event backend.Event) {
		emit(app.ExecutionEvent{Type: event.Type, Data: event.Data})
	})
	return app.ExecutionResult{
		Reply: result.Reply, ExitCode: result.ExitCode, ProviderSessionID: result.ProviderSessionID,
	}, err
}

func (executor Executor) runClaude(ctx context.Context, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	environment := make(map[string]string, len(request.Environment)+1)
	for key, value := range request.Environment {
		environment[key] = value
	}
	if request.Model.ContextWindow > 0 && environment["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] == "" {
		environment["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = strconv.FormatInt(request.Model.ContextWindow, 10)
	}
	result, err := executor.Claude.Execute(ctx, backend.Request{
		Runner:  workspace.RunnerFor(request.Workspace),
		WorkDir: taskWorkDir(request),
		Model:   request.Model.WireModel, ContextWindow: request.Model.ContextWindow,
		ReasoningEffort: request.Snapshot.ReasoningEffort, Environment: environment,
		Prompt: request.Prompt, ProviderSessionID: request.ProviderSessionID,
		Filesystem: request.Filesystem, Approval: request.Approval,
	}, func(event backend.Event) {
		emit(app.ExecutionEvent{Type: event.Type, Data: event.Data})
	})
	return app.ExecutionResult{
		Reply: result.Reply, ExitCode: result.ExitCode, ProviderSessionID: result.ProviderSessionID,
	}, err
}

func taskWorkDir(request app.ExecutionRequest) string {
	if request.Task.TaskWorkspacePath != "" {
		return request.Task.TaskWorkspacePath
	}
	return request.Workspace.RootPath
}
