package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/managedprocess"
)

// ErrWorkspaceMCPUnavailable reports that the bound Workspace or the managed
// process registry cannot be read. It is deliberately coarse: the underlying
// error can carry a host path or an SSH detail, so it stays in the server log.
var ErrWorkspaceMCPUnavailable = errors.New("workspace is unavailable")

// ErrWorkspaceMCPProcessNotFound reports a managed process that is not owned by
// this Task. A process belonging to another Task, another Workspace or an
// unknown name all produce this same result, so the tool cannot probe.
var ErrWorkspaceMCPProcessNotFound = errors.New("managed process not found")

// ErrWorkspaceMCPProcessNameInvalid reports a name that fails the managed
// process grammar. Rejecting it before any lookup keeps path traversal and
// separator tricks out of the registry query.
var ErrWorkspaceMCPProcessNameInvalid = errors.New("managed process name is invalid")

// ManagedProcessReader is the read-only subset of the managed process registry.
// The workspace domain never starts or stops a process; those actions stay on
// the existing Main-only Agent process endpoints.
type ManagedProcessReader interface {
	List(taskID string) []managedprocess.Status
	Status(taskID, name string) (managedprocess.Status, error)
}

// SetManagedProcesses installs the read-only process view used by the workspace
// domain MCP. It accepts the same in-memory registry the Agent process API uses,
// so both surfaces report the same process state.
func (s *Service) SetManagedProcesses(reader ManagedProcessReader) {
	s.managedProcesses = reader
}

// AgentWorkspaceMCPAllowed is the single gate for the workspace domain MCP. The
// runtime injects the server only when this returns true, and every tool call
// re-checks it server-side. The pet Task, a Task without a bound Workspace and
// any channel-hosted Turn are outside this project-scoped domain.
func (s *Service) AgentWorkspaceMCPAllowed(ctx context.Context, call AgentCallContext) bool {
	if call.Task.ID == petTaskID || call.Task.ProjectID == petProjectID {
		return false
	}
	workspaceID := strings.TrimSpace(call.Task.WorkspaceID)
	if workspaceID == "" {
		return false
	}
	item, err := s.store.Workspace(ctx, workspaceID)
	if err != nil || item.ProjectID != call.Project.ID {
		return false
	}
	return !s.agentMCPChannelBound(ctx, call.Task.ID, call.Turn.InboxBatchID)
}

func (s *Service) agentWorkspaceMCPReadContext(ctx context.Context, claims agentapi.Claims) (AgentCallContext, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return AgentCallContext{}, err
	}
	if !s.AgentWorkspaceMCPAllowed(ctx, call) {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	return call, nil
}

// AgentWorkspaceMCPListItem is the non-sensitive metadata of the one Workspace
// bound to this Task. RootPath, SSH host/user/port/auth, credentials, the
// repository map, capabilities and every Agent API address are omitted.
type AgentWorkspaceMCPListItem struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Transport     string    `json:"transport"`
	Locality      string    `json:"locality"`
	Health        string    `json:"health"`
	Platform      string    `json:"platform,omitempty"`
	Distro        string    `json:"distro,omitempty"`
	ReadOnly      bool      `json:"read_only"`
	LastDetected  time.Time `json:"last_detected_at,omitempty"`
	AgentAPIState string    `json:"agent_api_status,omitempty"`
}

type AgentWorkspaceMCPListResult struct {
	Workspaces []AgentWorkspaceMCPListItem `json:"workspaces"`
	Total      int                         `json:"total"`
}

// AgentWorkspaceMCPList returns only the Workspace bound to the calling Task.
// The Project-wide list remains on the Main-only HTTP Agent API.
func (s *Service) AgentWorkspaceMCPList(ctx context.Context, claims agentapi.Claims) (AgentWorkspaceMCPListResult, error) {
	call, err := s.agentWorkspaceMCPReadContext(ctx, claims)
	if err != nil {
		return AgentWorkspaceMCPListResult{}, err
	}
	item, err := s.store.Workspace(ctx, strings.TrimSpace(call.Task.WorkspaceID))
	if err != nil || !agentWorkspaceMCPScope(call, item) {
		return AgentWorkspaceMCPListResult{}, ErrWorkspaceMCPUnavailable
	}
	return AgentWorkspaceMCPListResult{
		Workspaces: []AgentWorkspaceMCPListItem{{
			ID: item.ID, Name: item.Name, Transport: item.Transport, Locality: item.Locality,
			Health: item.Health, Platform: item.Platform, Distro: item.Distro,
			ReadOnly: item.ReadOnly, LastDetected: item.LastDetectedAt,
			AgentAPIState: item.AgentAPIStatus,
		}},
		Total: 1,
	}, nil
}

// AgentWorkspaceMCPProcess is the safe process shape. Executable, Args, Dir,
// OutputTail and Error are omitted: they can contain command lines, environment
// values, filesystem paths or SSH diagnostics.
type AgentWorkspaceMCPProcess struct {
	Name       string     `json:"name"`
	AgentID    string     `json:"agent_id"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
}

func agentWorkspaceMCPProcessView(status managedprocess.Status) AgentWorkspaceMCPProcess {
	return AgentWorkspaceMCPProcess{
		Name: status.Name, AgentID: status.AgentID, State: status.State,
		StartedAt: status.StartedAt, FinishedAt: status.FinishedAt, ExitCode: status.ExitCode,
	}
}

func (s *Service) agentWorkspaceMCPProcessContext(ctx context.Context, claims agentapi.Claims) (AgentCallContext, error) {
	call, err := s.agentWorkspaceMCPReadContext(ctx, claims)
	if err != nil {
		return AgentCallContext{}, err
	}
	if s.managedProcesses == nil {
		return AgentCallContext{}, ErrWorkspaceMCPUnavailable
	}
	return call, nil
}

// AgentWorkspaceMCPProcessList returns the managed processes started by this
// Task. The registry is keyed by Task, so the filter is server-side and cannot
// be widened by a client parameter.
func (s *Service) AgentWorkspaceMCPProcessList(ctx context.Context, claims agentapi.Claims) ([]AgentWorkspaceMCPProcess, error) {
	call, err := s.agentWorkspaceMCPProcessContext(ctx, claims)
	if err != nil {
		return nil, err
	}
	statuses := s.managedProcesses.List(call.Task.ID)
	result := make([]AgentWorkspaceMCPProcess, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, agentWorkspaceMCPProcessView(status))
	}
	return result, nil
}

// AgentWorkspaceMCPProcessGet returns one process owned by this Task. The name
// is validated against the same grammar as the start endpoint before lookup;
// anything else — including a path fragment — never reaches the registry.
func (s *Service) AgentWorkspaceMCPProcessGet(ctx context.Context, claims agentapi.Claims, name string) (AgentWorkspaceMCPProcess, error) {
	call, err := s.agentWorkspaceMCPProcessContext(ctx, claims)
	if err != nil {
		return AgentWorkspaceMCPProcess{}, err
	}
	if !managedprocess.ValidName(name) {
		return AgentWorkspaceMCPProcess{}, ErrWorkspaceMCPProcessNameInvalid
	}
	status, err := s.managedProcesses.Status(call.Task.ID, name)
	if errors.Is(err, managedprocess.ErrNotFound) {
		return AgentWorkspaceMCPProcess{}, ErrWorkspaceMCPProcessNotFound
	}
	if err != nil {
		return AgentWorkspaceMCPProcess{}, ErrWorkspaceMCPUnavailable
	}
	return agentWorkspaceMCPProcessView(status), nil
}

// AgentWorkspaceMCPRuntime is the non-secret runtime option shape. It omits the
// internal model row ID and the credential-bound Codex account ID; only values
// the model needs to choose a backend remain.
type AgentWorkspaceMCPRuntime struct {
	Backend          string   `json:"backend"`
	ModelSource      string   `json:"model_source"`
	WireModel        string   `json:"wire_model"`
	DisplayName      string   `json:"display_name"`
	ProviderName     string   `json:"provider_name,omitempty"`
	DefaultEffort    string   `json:"default_reasoning_effort,omitempty"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
}

// AgentWorkspaceMCPRuntimeList returns the runtime options available to the
// current Project, projected to non-sensitive fields. Models, accounts and
// Workspace capabilities are read through the same app helper as the HTTP
// Agent API, so the two surfaces cannot disagree about what is available.
func (s *Service) AgentWorkspaceMCPRuntimeList(ctx context.Context, claims agentapi.Claims) ([]AgentWorkspaceMCPRuntime, error) {
	call, err := s.agentWorkspaceMCPReadContext(ctx, claims)
	if err != nil {
		return nil, err
	}
	options, err := s.agentProjectRuntimeOptions(ctx, call)
	if err != nil {
		return nil, err
	}
	result := make([]AgentWorkspaceMCPRuntime, 0, len(options))
	for _, option := range options {
		result = append(result, AgentWorkspaceMCPRuntime{
			Backend: option.Backend, ModelSource: option.ModelSource, WireModel: option.WireModel,
			DisplayName: option.DisplayName, ProviderName: option.ProviderName,
			DefaultEffort: option.DefaultEffort, ReasoningEfforts: option.ReasoningEfforts,
		})
	}
	return result, nil
}

// agentWorkspaceMCPScope is the Project check shared by the gate and the view
// helpers. A Task's WorkspaceID is bound at creation, but re-checking the
// Project here makes a tampered row fail closed instead of exposing another
// Project's Workspace metadata.
func agentWorkspaceMCPScope(call AgentCallContext, item domain.Workspace) bool {
	return item.ID == call.Task.WorkspaceID && item.ProjectID == call.Project.ID
}
