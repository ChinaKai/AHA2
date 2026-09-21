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

// newBackendRequest fills the fields every backend shares.
//
// Both backends need the same set, and building them independently is how one
// path silently loses a field: Claude was left without the Agent API forward, so
// its Turns ran against an address only reachable through a tunnel that was never
// established. Sharing the construction makes that class of omission impossible
// rather than merely fixed.
func newBackendRequest(request app.ExecutionRequest, model string, environment map[string]string) backend.Request {
	return backend.Request{
		Runner:  workspace.RunnerFor(request.Workspace),
		WorkDir: taskWorkDir(request),
		Model:   model, ContextWindow: request.Model.ContextWindow,
		ReasoningEffort: request.Snapshot.ReasoningEffort,
		Environment:     environment,
		Prompt:          request.Prompt, ProviderSessionID: request.ProviderSessionID,
		Filesystem: request.Filesystem, Approval: request.Approval,
		ReverseForward: request.AgentAPIForward,
	}
}

func (executor Executor) runCodex(ctx context.Context, adapter backend.Codex, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	backendRequest := newBackendRequest(request, request.Model.WireModel, request.Environment)
	backendRequest.StreamIdleTimeoutMS = request.Snapshot.StreamIdleTimeoutMS
	backendRequest.StreamMaxRetries = request.Snapshot.StreamMaxRetries
	result, err := adapter.Execute(ctx, backendRequest, func(event backend.Event) {
		emit(app.ExecutionEvent{Type: event.Type, Data: event.Data})
	})
	return app.ExecutionResult{
		Reply: result.Reply, ExitCode: result.ExitCode, ProviderSessionID: result.ProviderSessionID,
	}, err
}

func (executor Executor) runClaude(ctx context.Context, adapter backend.Claude, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	environment, model := claudeExecutionRuntime(request)
	environment = lendNativeClaudeCredential(ctx, request, environment)
	if request.Model.ContextWindow > 0 && environment["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] == "" {
		environment["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = strconv.FormatInt(request.Model.ContextWindow, 10)
	}
	workDir := taskWorkDir(request)
	home, _ := workspace.SessionHomeFor(workspace.SessionHomeInput{
		Backend: "claude", Workspace: request.Workspace, WorkDir: workDir,
		SessionID: request.BackendSessionID, EnvGroupID: request.Snapshot.EnvGroupID,
	})
	if home.EnvName != "" {
		runner := workspace.RunnerFor(request.Workspace)
		// A session can only be resumed from the config directory that holds its
		// transcript. Sessions created before this isolation existed live in the
		// backend's own default directory, so continue those there instead of
		// pointing --resume at an empty directory and failing the turn.
		//
		// That default directory belongs to the workspace host, whose home is not
		// necessarily the control plane's: a WSL workspace keeps its transcripts in
		// the distro's home. Asking for the control plane's home instead would look
		// in a directory that does not exist on the host, so the transcript would
		// never be found and the turn would fail with "No conversation found".
		configDir := home.Dir
		if request.ProviderSessionID != "" {
			if defaults := workspace.ClaudeDefaultConfigDirOn(ctx, runner); defaults != "" &&
				workspace.ClaudeTranscriptExists(ctx, runner, defaults, workDir, request.ProviderSessionID) {
				configDir = defaults
			}
		} else if err := workspace.EnsureClaudeConfigDir(ctx, request.Workspace, runner, home.Dir); err != nil {
			return app.ExecutionResult{}, fmt.Errorf("prepare Claude config dir: %w", err)
		}
		environment[home.EnvName] = configDir
	}
	result, err := adapter.Execute(ctx, newBackendRequest(request, model, environment), func(event backend.Event) {
		emit(app.ExecutionEvent{Type: event.Type, Data: event.Data})
	})
	return app.ExecutionResult{
		Reply: result.Reply, ExitCode: result.ExitCode, ProviderSessionID: result.ProviderSessionID,
	}, err
}

// lendNativeClaudeCredential adds the operator's borrowed Claude credential to a
// native-source run.
//
// The native source is meant to use the operator's own Claude login. A machine
// that holds that login in a shell profile has none as far as this
// non-interactive channel is concerned, so the workspace probe reports "not
// logged in" while the operator's own claude works. Lending the same value here
// that detection lends is what keeps "detection said logged in" true when the
// Turn actually runs; without it, detection would pass and the Turn would fail
// to authenticate, moving the failure from a visible result to an obscure one.
//
// Non-native sources carry their own credentials and are left untouched.
func lendNativeClaudeCredential(ctx context.Context, request app.ExecutionRequest, environment map[string]string) map[string]string {
	if request.Snapshot.EnvGroupID != domain.ClaudeNativeEnvGroupID {
		return environment
	}
	runner := workspace.RunnerFor(request.Workspace)
	for key, value := range workspace.ClaudeCredentialEnvironment(ctx, runner, request.Workspace) {
		environment[key] = value
	}
	return environment
}

func claudeExecutionRuntime(request app.ExecutionRequest) (map[string]string, string) {
	environment := make(map[string]string, len(request.Environment)+1)
	for key, value := range request.Environment {
		environment[key] = value
	}
	model := request.Model.WireModel
	if request.Snapshot.EnvGroupID == domain.ClaudeNativeEnvGroupID {
		// The native source must use Claude Code's own logged-in account,
		// even when AHA itself was launched with ANTHROPIC_* variables set.
		environment["ANTHROPIC_API_KEY"] = ""
		environment["ANTHROPIC_AUTH_TOKEN"] = ""
		environment["ANTHROPIC_BASE_URL"] = ""
		environment["ANTHROPIC_MODEL"] = ""
		if model == "default" {
			model = ""
		}
	}
	return environment, model
}

func taskWorkDir(request app.ExecutionRequest) string {
	if request.Task.TaskWorkspacePath != "" {
		return request.Task.TaskWorkspacePath
	}
	return request.Workspace.RootPath
}
