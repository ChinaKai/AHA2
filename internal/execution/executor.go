package execution

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

type BackendSettingsProvider interface {
	BackendSettings(context.Context) (domain.BackendSettings, error)
}

type Executor struct {
	Codex    backend.Codex
	Claude   backend.Claude
	Settings BackendSettingsProvider
}

func (executor Executor) Execute(ctx context.Context, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	codex, claude, err := executor.configuredAdapters(ctx)
	if err != nil {
		return app.ExecutionResult{}, err
	}
	if request.Snapshot.Backend == "claude" {
		return executor.runClaude(ctx, claude, request, emit)
	}
	return executor.runCodex(ctx, codex, request, emit)
}

func (executor Executor) configuredAdapters(ctx context.Context) (backend.Codex, backend.Claude, error) {
	settings := domain.BackendSettings{
		IdleTimeoutSeconds: domain.DefaultBackendIdleTimeoutSeconds,
		TurnTimeoutSeconds: domain.DefaultBackendTurnTimeoutSeconds,
	}
	if executor.Settings != nil {
		var err error
		settings, err = executor.Settings.BackendSettings(ctx)
		if err != nil {
			return backend.Codex{}, backend.Claude{}, fmt.Errorf("read backend timeout settings: %w", err)
		}
	}
	codex := executor.Codex
	codex.IdleTimeout = time.Duration(settings.IdleTimeoutSeconds) * time.Second
	codex.Timeout = time.Duration(settings.TurnTimeoutSeconds) * time.Second
	claude := executor.Claude
	claude.IdleTimeout = time.Duration(settings.IdleTimeoutSeconds) * time.Second
	claude.Timeout = time.Duration(settings.TurnTimeoutSeconds) * time.Second
	return codex, claude, nil
}

func (executor Executor) runCodex(ctx context.Context, adapter backend.Codex, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	result, err := adapter.Execute(ctx, backend.Request{
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

func (executor Executor) runClaude(ctx context.Context, adapter backend.Claude, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	environment := make(map[string]string, len(request.Environment)+1)
	for key, value := range request.Environment {
		environment[key] = value
	}
	if request.Model.ContextWindow > 0 && environment["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] == "" {
		environment["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = strconv.FormatInt(request.Model.ContextWindow, 10)
	}
	result, err := adapter.Execute(ctx, backend.Request{
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
