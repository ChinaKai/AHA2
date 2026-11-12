package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/codexaccount"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/prompt"
	"github.com/ChinaKai/AHA2/internal/proxyconfig"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

type ExecutionRequest struct {
	Turn              domain.Turn
	Task              domain.Task
	Project           domain.Project
	Workspace         domain.Workspace
	Snapshot          domain.RuntimeConfigSnapshot
	Model             domain.Model
	EnvGroup          domain.EnvGroup
	Environment       map[string]string
	Prompt            string
	ProviderSessionID string
	// BackendSessionID is AHA's own session id, used to derive the per-session
	// home that keeps this run out of the operator's own backend state.
	BackendSessionID string
	// AgentAPIForward asks the runner to carry an AHA-side address into a workspace
	// that cannot dial AHA directly. Empty when no forward is needed.
	AgentAPIForward *workspacepkg.ReverseForward
	Filesystem      string
	Approval        string
	MCPServers      []backend.MCPServer
	AllowedTools    []string
	// LiveInbox is set for Claude and Codex Turns. Each adapter uses it whenever
	// the runner can stream stdin (Claude stream-json, Codex app-server
	// turn/steer) and falls back to the one-shot path otherwise.
	LiveInbox backend.LiveInbox
}

type ExecutionEvent struct {
	Type string
	Data map[string]any
}

type MemoryPatch struct {
	CurrentGoal    *string  `json:"current_goal,omitempty"`
	GoalChangeType string   `json:"goal_change_type,omitempty"`
	GoalReason     string   `json:"goal_reason,omitempty"`
	Decisions      []string `json:"decisions"`
	Facts          []string `json:"facts"`
	Excluded       []string `json:"excluded"`
	Progress       []string `json:"progress"`
	Verification   []string `json:"verification"`
	NextActions    []string `json:"next_actions"`
}

type KnowledgeCandidate struct {
	EntryID       string  `json:"entry_id"`
	BaseRevision  int     `json:"base_revision"`
	Scope         string  `json:"scope"`
	ParentID      *string `json:"parent_id"`
	Slug          *string `json:"slug"`
	SortOrder     *int    `json:"sort_order"`
	IsIndex       *bool   `json:"is_index"`
	Type          string  `json:"type"`
	Title         string  `json:"title"`
	Body          string  `json:"body"`
	Confidence    float64 `json:"confidence"`
	ProductLineID string  `json:"product_line_id"`
	Reason        string  `json:"reason"`
}

type KnowledgeFeedback struct {
	EntryID string `json:"entry_id"`
	Kind    string `json:"kind"`
}

type AgentAction struct {
	AgentID         string `json:"agent_id"`
	Title           string `json:"title"`
	Assignment      string `json:"assignment"`
	Required        bool   `json:"required"`
	Backend         string `json:"backend"`
	ModelID         string `json:"model_id"`
	ReasoningEffort string `json:"reasoning_effort"`
	Filesystem      string `json:"filesystem"`
	Approval        string `json:"approval"`
}

type ExecutionResult struct {
	Reply             string
	ExitCode          int
	ProviderSessionID string
}

type Executor interface {
	Execute(context.Context, ExecutionRequest, func(ExecutionEvent)) (ExecutionResult, error)
}

type PreparedWorkspace struct {
	Path         string
	BaseCommit   string
	Branch       string
	TargetBranch string
}

type WorkspacePreparer interface {
	Prepare(context.Context, domain.Workspace, string, string, string, string, string) (PreparedWorkspace, error)
}

type SecretResolver interface {
	Get(string) (string, bool)
	PutMany(map[string]string) error
	DeleteMany([]string) error
}

type ProxyRuntime interface {
	ApplyEnvironment(context.Context, map[string]string) error
}

// PetChannelSender is implemented by the channel runtime. Owner-delegate
// channel sends are executed only after an owner approval, so the pet service
// needs a narrow send path that does not depend on an Agent API Turn token.
type PetChannelSender interface {
	SendPetTaskChannelMessage(ctx context.Context, taskID, turnID, requestID, purpose, message string, mentionIDs, attachmentIDs []string, at time.Time) (domain.ChannelDelivery, error)
	SetInstanceEnabled(ctx context.Context, ownerID, id string, enabled bool, expectedRevision int) (domain.ChannelInstance, error)
	ResetBinding(ctx context.Context, ownerID, id string, expectedRevision int) (domain.ChannelInstance, error)
	ArchiveInstance(ctx context.Context, ownerID, id string, expectedRevision int) (domain.ChannelInstance, error)
	PurgeInstance(ctx context.Context, ownerID, id string, expectedRevision int) (domain.ChannelInstance, error)
}

// PetSecurityApplier applies an owner-approved security settings change to the
// live HTTP gate, not just the database.
type PetSecurityApplier interface {
	ApplyPetSecuritySettings(ctx context.Context, validateOrigin bool, accessScope string) (domain.SecuritySettings, error)
}

// PetHardwareDisconnector closes live hardware terminals before the pet removes
// a resource after owner approval.
type PetHardwareDisconnector interface {
	Disconnect(taskID, hardwareID, transport string)
}

type Service struct {
	store    *store.Store
	secrets  SecretResolver
	executor Executor
	preparer WorkspacePreparer
	hub      *EventHub
	now      func() time.Time
	// mcpToolFetch and mcpToolRefreshBudget are the tool-refresh seams: tests
	// substitute the fetch and shrink the probe budget so the timeout path
	// runs in milliseconds. A nil fetch uses the real transports; a
	// non-positive budget falls back to mcpToolRefreshTimeout.
	mcpToolFetch         func(context.Context, domain.MCPServer) ([]domain.MCPTool, error)
	mcpToolRefreshBudget time.Duration
	prompts              *prompt.Engine
	codex                *codexaccount.Manager
	proxyRuntime         ProxyRuntime
	petChannels          PetChannelSender
	petSecurity          PetSecurityApplier
	petHardware          PetHardwareDisconnector
	agentAPI             *agentapi.Capabilities
	agentAPIURL          string
	buildVersion         string
	webVersion           string
	startedAt            time.Time

	mu                  sync.Mutex
	cancels             map[string]context.CancelFunc
	runContext          context.Context
	runWG               sync.WaitGroup
	closing             bool
	settleMu            sync.Mutex
	scheduleMu          sync.Mutex
	mergeMu             sync.Mutex
	mergeDelay          time.Duration
	mergeTimers         map[string]*time.Timer
	collaborationMu     sync.Mutex
	collaborationDelay  time.Duration
	collaborationTimers map[string]*time.Timer
	assistantMu         sync.Mutex
	petTaskMu           sync.Mutex
	petBrainMu          sync.Mutex
	petBrainAt          map[string]time.Time
	petAutonomyLast     time.Time
	petAutonomyNext     time.Time
	petBrainRuns        map[string][]time.Time
	sharedContextMu     sync.Mutex
	sharedContexts      map[string]*sharedContextState
	latestShared        map[string]string

	// scheduleSnapshotHook is a test seam that runs after scheduleAgent has read
	// the pending inbox snapshot and before it claims it. Production leaves it nil.
	scheduleSnapshotHook func()
}

func (s *Service) SetProxyRuntime(runtime ProxyRuntime) {
	s.proxyRuntime = runtime
}

func (s *Service) SetPetChannelSender(sender PetChannelSender) {
	s.petChannels = sender
}

func (s *Service) SetPetSecurityApplier(applier PetSecurityApplier) {
	s.petSecurity = applier
}

func (s *Service) SetPetHardwareDisconnector(disconnector PetHardwareDisconnector) {
	s.petHardware = disconnector
}

type sharedContextState struct {
	scope string
	refs  int
}

func (s *Service) SetWorkspacePreparer(preparer WorkspacePreparer) {
	s.preparer = preparer
}

func (s *Service) SetCodexAccountManager(manager *codexaccount.Manager) {
	s.codex = manager
}

func (s *Service) SetAgentAPI(capabilities *agentapi.Capabilities, baseURL string) {
	s.agentAPI = capabilities
	s.agentAPIURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

// SetBuildInfo lets the pet's read-only inspection snapshot report the same
// build identity as the HTTP server without reaching into internal/httpapi.
func (s *Service) SetBuildInfo(version, webVersion string, startedAt time.Time) {
	s.buildVersion = strings.TrimSpace(version)
	s.webVersion = strings.TrimSpace(webVersion)
	if s.webVersion == "" {
		s.webVersion = s.buildVersion
	}
	s.startedAt = startedAt
}

type CreateTaskInput struct {
	ID                  string
	ProjectID           string
	WorkspaceID         string
	Title               string
	Request             string
	CurrentGoal         string
	TargetBranch        string
	BaseCommit          string
	TaskBranch          string
	Isolation           string
	WorktreeDir         string
	Backend             string
	ModelSource         string
	ModelID             string
	WireModel           string
	CodexAccountID      string
	ReasoningEffort     string
	FastMode            bool
	StreamIdleTimeoutMS *int
	StreamMaxRetries    *int
	Filesystem          string
	Approval            string
	ProxyEnabled        bool
	CollaborationMode   string
	MaxAgents           int
	KnowledgePolicy     string
	SkillIDs            []string
	// ApplyAutoSkills defaults to true. Web task creation pre-checks auto-attach
	// skills in the picker and explicitly disables this so a user's unchecked
	// selection is honoured; internal/API callers get the defaults.
	ApplyAutoSkills   *bool
	AgentCapabilities map[string]bool
	MCPServerIDs      []string
	StartMode         string
	// AllowSystemProject is reserved for trusted AHA services (channel runtime
	// and pet system task) that create Tasks inside system-managed projects.
	// It is never set from a user/agent HTTP payload.
	AllowSystemProject bool
}

func NewService(database *store.Store, secretStore *secrets.FileStore, executor Executor) *Service {
	return &Service{
		store:                database,
		secrets:              secretStore,
		executor:             executor,
		hub:                  NewEventHub(),
		now:                  time.Now,
		mcpToolRefreshBudget: mcpToolRefreshTimeout,
		prompts:              prompt.NewEngine(database),
		cancels:              map[string]context.CancelFunc{},
		runContext:           context.Background(),
		mergeDelay:           time.Second,
		mergeTimers:          map[string]*time.Timer{},
		collaborationDelay:   350 * time.Millisecond,
		collaborationTimers:  map[string]*time.Timer{},
		sharedContexts:       map[string]*sharedContextState{},
		latestShared:         map[string]string{},
	}
}

func (s *Service) SetRunContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closing && len(s.cancels) == 0 {
		s.runContext = ctx
	}
}

func (s *Service) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.StopPetTaskNotifications()
	s.mu.Lock()
	s.closing = true
	cancels := make([]context.CancelFunc, 0, len(s.cancels))
	for _, cancel := range s.cancels {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	s.collaborationMu.Lock()
	for parentTurnID, timer := range s.collaborationTimers {
		timer.Stop()
		delete(s.collaborationTimers, parentTurnID)
	}
	s.collaborationMu.Unlock()
	done := make(chan struct{})
	go func() {
		s.runWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) Hub() *EventHub {
	return s.hub
}

func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput) (domain.Task, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Request = strings.TrimSpace(input.Request)
	if input.Title == "" || input.Request == "" {
		return domain.Task{}, fmt.Errorf("task title and request are required")
	}
	startMode := strings.TrimSpace(input.StartMode)
	if startMode == "" {
		startMode = "immediate"
	}
	if startMode != "immediate" && startMode != "manual" {
		return domain.Task{}, fmt.Errorf("invalid task start mode")
	}
	project, err := s.store.Project(ctx, input.ProjectID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("project: %w", err)
	}
	workspace, err := s.store.Workspace(ctx, input.WorkspaceID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("workspace: %w", err)
	}
	if workspace.ProjectID != project.ID {
		return domain.Task{}, fmt.Errorf("workspace does not belong to project")
	}
	if domain.IsSystemManagedProject(project) && !input.AllowSystemProject {
		return domain.Task{}, fmt.Errorf("系统托管的项目不允许创建普通任务")
	}
	isolation := strings.TrimSpace(input.Isolation)
	worktreeDir := strings.TrimSpace(input.WorktreeDir)
	if project.ProjectType != "git" {
		isolation = "inplace"
	}
	if isolation == "" {
		isolation = "inplace"
	}
	if isolation != "worktree" && isolation != "inplace" {
		return domain.Task{}, fmt.Errorf("invalid task isolation")
	}
	if isolation == "inplace" {
		worktreeDir = ""
		input.TargetBranch = ""
		input.TaskBranch = ""
	} else if worktreeDir == "" {
		worktreeDir = workspacepkg.DefaultWorktreeDir(workspace)
	}
	model, envGroup, accountID, err := s.resolveRuntimeSelection(ctx, runtimeSelectionInput{
		WorkspaceID: workspace.ID, Backend: input.Backend, ModelSource: input.ModelSource, ModelID: input.ModelID,
		WireModel: input.WireModel, CodexAccountID: input.CodexAccountID,
	})
	if err != nil {
		return domain.Task{}, err
	}
	now := s.now().UTC()
	streamIdleTimeoutMS := domain.DefaultTaskStreamIdleTimeoutMS
	if input.StreamIdleTimeoutMS != nil {
		streamIdleTimeoutMS = *input.StreamIdleTimeoutMS
	}
	streamMaxRetries := domain.DefaultTaskStreamMaxRetries
	if input.StreamMaxRetries != nil {
		streamMaxRetries = *input.StreamMaxRetries
	}
	if err := validateCodexStreamSettings(streamIdleTimeoutMS, streamMaxRetries); err != nil {
		return domain.Task{}, err
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID:                  domain.NewID("runtime"),
		WorkspaceID:         workspace.ID,
		Backend:             model.Backend,
		ModelID:             model.ID,
		WireModel:           model.WireModel,
		EnvGroupID:          envGroup.ID,
		EnvGroupRevision:    envGroup.Revision,
		CodexAccountID:      accountID,
		ProxyEnabled:        input.ProxyEnabled,
		ReasoningEffort:     input.ReasoningEffort,
		FastMode:            input.FastMode,
		StreamIdleTimeoutMS: streamIdleTimeoutMS,
		StreamMaxRetries:    streamMaxRetries,
		PermissionsJSON:     permissionsJSON(input.Filesystem, input.Approval),
		CreatedAt:           now,
	}
	if snapshot.ReasoningEffort == "" {
		snapshot.ReasoningEffort = model.DefaultEffort
	}
	collaborationMode, err := validateCollaborationMode(input.CollaborationMode)
	if err != nil {
		return domain.Task{}, err
	}
	if input.ApplyAutoSkills == nil || *input.ApplyAutoSkills {
		autoIDs, autoErr := s.autoAttachSkillIDs(ctx, project.ID)
		if autoErr != nil {
			return domain.Task{}, autoErr
		}
		input.SkillIDs = mergeSkillIDs(input.SkillIDs, autoIDs)
	}
	skillIDs, err := s.validateTaskSkillIDs(ctx, project.ID, input.SkillIDs)
	if err != nil {
		return domain.Task{}, err
	}
	mcpServerIDs, err := s.normalizeTaskMCPServers(ctx, project, input.MCPServerIDs)
	if err != nil {
		return domain.Task{}, err
	}
	taskStatus := domain.TaskPreparing
	if startMode == "manual" {
		taskStatus = domain.TaskDraft
	}
	// Most callers let the request double as the goal. The Pet Task supplies a
	// stable mission goal while keeping its introduction as the request.
	currentGoal := strings.TrimSpace(input.CurrentGoal)
	if currentGoal == "" {
		currentGoal = input.Request
	}
	task := domain.Task{
		ID:                      strings.TrimSpace(input.ID),
		ProjectID:               project.ID,
		WorkspaceID:             workspace.ID,
		Title:                   input.Title,
		OriginalRequest:         input.Request,
		CurrentGoal:             currentGoal,
		Status:                  taskStatus,
		TargetBranch:            input.TargetBranch,
		BaseCommit:              input.BaseCommit,
		TaskBranch:              input.TaskBranch,
		Isolation:               isolation,
		WorktreeDir:             worktreeDir,
		RuntimeConfigSnapshotID: snapshot.ID,
		CollaborationMode:       collaborationMode,
		MaxAgents:               input.MaxAgents,
		KnowledgePolicy:         normalizeKnowledgePolicy(input.KnowledgePolicy),
		SkillIDs:                skillIDs,
		AgentCapabilities:       input.AgentCapabilities,
		MCPServerIDs:            mcpServerIDs,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if task.ID == "" {
		task.ID = domain.NewID("task")
	}
	if task.MaxAgents < 1 {
		task.MaxAgents = 3
	}
	created, err := s.store.CreateTaskWithSnapshot(ctx, snapshot, task)
	if err != nil {
		return domain.Task{}, err
	}
	task = created
	if startMode == "manual" {
		s.emit(ctx, task.ID, "task", task.ID, "task_created", map[string]any{"title": task.Title, "workspace_id": task.WorkspaceID, "start_mode": startMode})
		return task, nil
	}
	return s.prepareAndActivateTask(ctx, workspace, task, "task_created")
}

// StartTask activates a manually-created Draft exactly once. The status
// compare-and-swap happens before workspace preparation so concurrent start
// requests cannot create duplicate worktrees or initial Turns.
func (s *Service) StartTask(ctx context.Context, taskID string) (domain.Task, error) {
	task, err := s.store.Task(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return domain.Task{}, err
	}
	if task.Status != domain.TaskDraft {
		return task, fmt.Errorf("task is not a draft")
	}
	workspace, err := s.store.Workspace(ctx, task.WorkspaceID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("workspace: %w", err)
	}
	now := s.now().UTC()
	if err := s.store.UpdateTaskStatus(ctx, task.ID, domain.TaskDraft, domain.TaskPreparing, timeString(now), ""); err != nil {
		return domain.Task{}, err
	}
	task.Status = domain.TaskPreparing
	return s.prepareAndActivateTask(ctx, workspace, task, "task_started")
}

func (s *Service) prepareAndActivateTask(ctx context.Context, workspace domain.Workspace, task domain.Task, eventType string) (domain.Task, error) {
	now := s.now().UTC()
	if s.preparer != nil {
		prepared, prepareErr := s.preparer.Prepare(ctx, workspace, task.ID, task.TargetBranch, task.TaskBranch, task.Isolation, task.WorktreeDir)
		if prepareErr != nil {
			_ = s.store.UpdateTaskStatus(ctx, task.ID, domain.TaskPreparing, domain.TaskBlocked, timeString(now), "")
			task.Status = domain.TaskBlocked
			s.emit(ctx, task.ID, "task", task.ID, "task_workspace_failed", map[string]any{"error": prepareErr.Error()})
			return task, fmt.Errorf("prepare task workspace: %w", prepareErr)
		}
		task.TaskWorkspacePath = prepared.Path
		task.BaseCommit = prepared.BaseCommit
		task.TaskBranch = prepared.Branch
		if prepared.TargetBranch != "" {
			task.TargetBranch = prepared.TargetBranch
		}
		if err := s.store.UpdateTaskWorkspace(ctx, task.ID, task.TaskWorkspacePath, task.BaseCommit, task.TaskBranch, task.TargetBranch, timeString(now)); err != nil {
			return task, err
		}
	}
	if err := s.store.UpdateTaskStatus(ctx, task.ID, domain.TaskPreparing, domain.TaskActive, timeString(now), ""); err != nil {
		return domain.Task{}, err
	}
	task.Status = domain.TaskActive
	s.emit(ctx, task.ID, "task", task.ID, eventType, map[string]any{"title": task.Title, "workspace_id": task.WorkspaceID})
	return task, nil
}

func normalizeKnowledgePolicy(value string) string {
	switch strings.TrimSpace(value) {
	case "enabled":
		return "enabled"
	case "disabled":
		return "disabled"
	default:
		return "inherit"
	}
}

func knowledgeEnabled(project domain.Project, task domain.Task) bool {
	if task.KnowledgePolicy == "enabled" {
		return true
	}
	if task.KnowledgePolicy == "disabled" {
		return false
	}
	return project.KnowledgePolicy != "disabled"
}

func (s *Service) validateTaskSkillIDs(ctx context.Context, projectID string, ids []string) ([]string, error) {
	result := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		item, err := s.store.Skill(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("skill %s is unavailable", id)
		}
		if !item.Enabled || item.Status != "active" || !skillAppliesToProject(item, projectID) {
			return nil, fmt.Errorf("skill %s is not enabled for this project", id)
		}
		seen[id] = true
		result = append(result, id)
	}
	return result, nil
}

func skillAppliesToProject(item domain.Skill, projectID string) bool {
	if item.Scope == "global" {
		return true
	}
	if item.Scope != "project" {
		return false
	}
	return item.ProjectID == projectID || item.BoundProjectID == projectID
}

func (s *Service) autoAttachSkillIDs(ctx context.Context, projectID string) ([]string, error) {
	items, err := s.store.ListSkills(ctx, "", "", true)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0)
	for _, item := range items {
		// Auto-attach follows the skill's own Project. Bound external skills are
		// available for manual selection in the bound project, but they are
		// read-only mirrors and must not be enabled implicitly.
		if item.AutoAttach && (item.Scope == "global" || (item.Scope == "project" && item.ProjectID == projectID)) {
			result = append(result, item.ID)
		}
	}
	return result, nil
}

func mergeSkillIDs(existing, extra []string) []string {
	seen := make(map[string]bool, len(existing)+len(extra))
	result := make([]string, 0, len(existing)+len(extra))
	for _, group := range [][]string{existing, extra} {
		for _, id := range group {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			result = append(result, id)
		}
	}
	return result
}

func (s *Service) UpdateTaskSkills(ctx context.Context, taskID string, ids []string) (domain.Task, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	validated, err := s.validateTaskSkillIDs(ctx, task.ProjectID, ids)
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.store.UpdateTaskSkills(ctx, taskID, validated, timeString(s.now().UTC())); err != nil {
		return domain.Task{}, err
	}
	return s.store.Task(ctx, taskID)
}

func (s *Service) activeTaskSkills(ctx context.Context, task domain.Task) []domain.Skill {
	items, err := s.store.SkillsByIDs(ctx, task.SkillIDs)
	if err != nil {
		items = nil
		for _, id := range task.SkillIDs {
			item, itemErr := s.store.Skill(ctx, id)
			if itemErr == nil {
				items = append(items, item)
			}
		}
	}
	result := make([]domain.Skill, 0, len(items))
	for _, item := range items {
		if !item.Enabled || item.Status != "active" {
			continue
		}
		if skillAppliesToProject(item, task.ProjectID) {
			result = append(result, item)
		}
	}
	return result
}

func (s *Service) SubmitMessage(ctx context.Context, taskID, content string) (domain.Turn, error) {
	return s.SubmitAgentMessageWithAttachments(ctx, taskID, "main", content, nil)
}

func (s *Service) SubmitAgentMessage(ctx context.Context, taskID, agentID, content string) (domain.Turn, error) {
	return s.SubmitAgentMessageWithAttachments(ctx, taskID, agentID, content, nil)
}

func (s *Service) SubmitAgentMessageWithAttachments(ctx context.Context, taskID, agentID, content string, attachmentIDs []string) (domain.Turn, error) {
	return s.submitMessageWithSource(ctx, taskID, agentID, content, attachmentIDs, "", "", "", nil, "owner", "owner_message", false)
}

// SubmitPetDelegatedMessage writes a target task message that is visibly from
// the pet, not from the owner. It uses the same Turn pipeline while keeping the
// source distinct so the target Agent does not inherit owner identity.
func (s *Service) SubmitPetDelegatedMessage(ctx context.Context, taskID, content string) (domain.Turn, error) {
	return s.SubmitPetDelegatedMessageWithAttachments(ctx, taskID, content, nil)
}

func (s *Service) SubmitPetDelegatedMessageWithAttachments(ctx context.Context, taskID, content string, attachmentIDs []string) (domain.Turn, error) {
	return s.submitMessageWithSource(ctx, taskID, "main", content, attachmentIDs, "", "", "", nil, "pet", "pet_delegate_message", false)
}

func (s *Service) SubmitChannelMessage(ctx context.Context, receiptID, receiptLeaseID, conversationID, taskID, content string, provenance map[string]any) (domain.Turn, error) {
	return s.SubmitChannelMessageWithAttachments(ctx, receiptID, receiptLeaseID, conversationID, taskID, content, provenance, nil)
}

func (s *Service) SubmitChannelMessageWithAttachments(ctx context.Context, receiptID, receiptLeaseID, conversationID, taskID, content string, provenance map[string]any, attachmentIDs []string) (domain.Turn, error) {
	return s.submitMessageWithSource(ctx, taskID, "main", content, attachmentIDs, receiptID, receiptLeaseID, conversationID, provenance, "owner", "owner_message", false)
}

// submitMessageWithSource enqueues a user message and schedules the Agent.
// channelContinuation references an already-processed receipt instead of
// consuming a fresh one, for decisions made outside the channel.
func (s *Service) submitMessageWithSource(ctx context.Context, taskID, agentID, content string, attachmentIDs []string, channelReceiptID, channelReceiptLeaseID, channelConversationID string, channelProvenance map[string]any, sender, sourceKind string, channelContinuation bool) (domain.Turn, error) {
	result, err := s.submitMessageWithSourceResult(
		ctx, taskID, agentID, content, attachmentIDs,
		channelReceiptID, channelReceiptLeaseID, channelConversationID,
		channelProvenance, sender, sourceKind, channelContinuation, nil,
	)
	return result.Turn, err
}

type submittedMessage struct {
	Turn      domain.Turn
	MessageID string
	Duplicate bool
}

// localOwnerMessageMeta carries the provenance of a message written by the
// loopback owner-message ingest endpoint. A nil value keeps the existing Web,
// pet-delegated, and channel write paths unchanged.
type localOwnerMessageMeta struct {
	Source          string
	ClientRequestID string
}

func (s *Service) submitMessageWithSourceResult(ctx context.Context, taskID, agentID, content string, attachmentIDs []string, channelReceiptID, channelReceiptLeaseID, channelConversationID string, channelProvenance map[string]any, sender, sourceKind string, channelContinuation bool, local *localOwnerMessageMeta) (submittedMessage, error) {
	content = strings.TrimSpace(content)
	if content == "" && len(attachmentIDs) == 0 {
		return submittedMessage{}, fmt.Errorf("message or attachment is required")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = "main"
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return submittedMessage{}, err
	}
	if task.ID == petTaskID {
		task = s.migratePetTaskGoal(ctx, task)
	}
	if task.Status == domain.TaskDraft {
		return submittedMessage{}, fmt.Errorf("task must be started before sending messages")
	}
	if err := s.store.ValidateDraftAttachments(ctx, task.ID, attachmentIDs); err != nil {
		return submittedMessage{}, err
	}
	if task.CollaborationMode == "single" && agentID != "main" {
		return submittedMessage{}, fmt.Errorf("task is in single-agent mode")
	}
	agent, err := s.store.TaskAgent(ctx, taskID, agentID)
	if err != nil {
		return submittedMessage{}, err
	}
	snapshot, err := s.store.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		return submittedMessage{}, fmt.Errorf("运行配置不存在，请修改 %s 配置", agentID)
	}
	if _, unavailable := s.runtimeSnapshotAvailability(ctx, snapshot); unavailable != "" {
		return submittedMessage{}, fmt.Errorf("%s，请修改 %s 模型配置", unavailable, agentID)
	}
	if task.Status == domain.TaskCompleted || task.Status == domain.TaskCancelled || task.Status == domain.TaskBlocked {
		return submittedMessage{}, fmt.Errorf("task is %s", task.Status)
	}
	if task.Status == domain.TaskWaitingUser || task.Status == domain.TaskFailed {
		now := s.now().UTC()
		if err := s.store.UpdateTaskStatus(ctx, task.ID, task.Status, domain.TaskActive, timeString(now), ""); err != nil {
			return submittedMessage{}, err
		}
		task.Status = domain.TaskActive
	}
	// Re-read the status after the transition above so a concurrent
	// CompleteTask/Cancel/Block cannot still take a new message. The claim
	// transaction repeats this check atomically before creating the Turn.
	if current, currentErr := s.store.Task(ctx, task.ID); currentErr == nil &&
		(current.Status == domain.TaskCompleted || current.Status == domain.TaskCancelled || current.Status == domain.TaskBlocked) {
		return submittedMessage{}, fmt.Errorf("task is %s", current.Status)
	}
	now := s.now().UTC()
	message := domain.Message{
		ID:        domain.NewID("message"),
		TaskID:    task.ID,
		Role:      "user",
		Sender:    sender,
		Content:   content,
		CreatedAt: now,
	}
	if local != nil {
		message.Source = local.Source
		message.ClientRequestID = local.ClientRequestID
	}
	if actor, ok := channelProvenance["actor"].(map[string]any); ok {
		if displayName := strings.TrimSpace(fmt.Sprint(actor["display_name"])); displayName != "" && displayName != "<nil>" {
			message.Sender = displayName
		}
	}
	if agentID == "main" {
		s.cancelMainResultMerge(task.ID)
	}
	var round domain.TaskRound
	var inbox domain.AgentInboxItem
	var createdRound, inserted, duplicate bool
	var enqueuedMessageID string
	if channelReceiptID != "" {
		if channelContinuation {
			round, inbox, createdRound, err = s.store.EnqueueChannelContinuationMessage(ctx, channelReceiptID, channelConversationID, message, agentID, channelProvenance, attachmentIDs...)
			inserted = err == nil
		} else {
			round, inbox, createdRound, inserted, err = s.store.EnqueueChannelOwnerMessage(ctx, channelReceiptID, channelReceiptLeaseID, channelConversationID, message, agentID, channelProvenance, attachmentIDs...)
		}
	} else if sourceKind == "pet_delegate_message" {
		round, inbox, createdRound, err = s.store.EnqueuePetDelegatedMessage(ctx, message, agentID, attachmentIDs)
		inserted = err == nil
	} else if local != nil {
		round, inbox, createdRound, inserted, duplicate, enqueuedMessageID, err = s.store.EnqueueLocalOwnerMessage(ctx, message, agentID, attachmentIDs)
	} else {
		round, inbox, createdRound, err = s.store.EnqueueOwnerMessage(ctx, message, agentID, attachmentIDs)
		inserted = err == nil
	}
	if err != nil {
		return submittedMessage{}, err
	}
	if duplicate {
		// A concurrent request with the same idempotency key already committed
		// this message. Report the committed identity and leave scheduling to
		// that request; retries that need to repair delivery go through
		// SubmitLocalOwnerMessage's explicit reschedule path after a status check.
		return submittedMessage{MessageID: enqueuedMessageID, Duplicate: true}, nil
	}
	if createdRound && inserted {
		s.emit(ctx, task.ID, "round", round.ID, "round_started", map[string]any{
			"round_id": round.ID, "round_sequence": round.Sequence,
		})
	}
	if inserted {
		s.emit(ctx, task.ID, "inbox", inbox.ID, "agent_message_queued", map[string]any{
			"round_id": round.ID, "agent_id": agentID, "source": "owner", "inbox_sequence": inbox.Sequence,
		})
	}
	turn, started, err := s.scheduleAgent(ctx, task.ID, agentID)
	if err != nil {
		// The message is already committed at this point, so keep its identity
		// for callers that must report a persisted-but-unscheduled result.
		return submittedMessage{MessageID: message.ID, Duplicate: duplicate}, err
	}
	if !started {
		return submittedMessage{MessageID: message.ID, Duplicate: duplicate}, nil
	}
	return submittedMessage{Turn: turn, MessageID: message.ID, Duplicate: duplicate}, nil
}

func (s *Service) InterruptTurn(ctx context.Context, turnID string) error {
	turn, err := s.store.Turn(ctx, turnID)
	if err != nil {
		return err
	}
	const attempts = 3
	for attempt := 1; ; attempt++ {
		if turn.Status.Terminal() {
			return nil
		}
		s.mu.Lock()
		cancel := s.cancels[turnID]
		s.mu.Unlock()
		if cancel != nil {
			// The running goroutine settles the turn once it observes the
			// cancellation; do not race it on the stored status.
			cancel()
			return nil
		}
		if attempt > attempts {
			return fmt.Errorf("interrupt turn %s: status keeps changing", turnID)
		}
		// No goroutine in this process owns the turn, so cancelling cannot end
		// it. Mark it interrupted with a compare-and-set on the observed status;
		// a concurrent terminal update wins and the loop then returns nil.
		task, err := s.store.Task(ctx, turn.TaskID)
		if err != nil {
			return err
		}
		if s.interruptTurn(ctx, &turn, task) {
			return nil
		}
		if turn, err = s.store.Turn(ctx, turnID); err != nil {
			return err
		}
	}
}

func (s *Service) InterruptRound(ctx context.Context, roundID string) error {
	round, err := s.store.Round(ctx, roundID)
	if err != nil {
		return err
	}
	if round.Status.Terminal() {
		return nil
	}
	turns, err := s.store.TurnsForRound(ctx, roundID)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	previous := round.Status
	round.Status = domain.RoundInterrupted
	round.FinishedAt = now
	if err := s.store.UpdateRound(ctx, round, previous); err != nil {
		return err
	}
	s.cancelMainResultMerge(round.TaskID)
	_ = s.store.CancelPendingInboxForRound(ctx, round.ID, now)
	task, err := s.store.Task(ctx, round.TaskID)
	if err == nil && !task.Status.Terminal() && task.Status != domain.TaskWaitingUser {
		_ = s.store.UpdateTaskStatus(ctx, task.ID, task.Status, domain.TaskWaitingUser, timeString(now), "")
	}
	s.mu.Lock()
	for _, turn := range turns {
		if !turn.Status.Terminal() {
			if cancel := s.cancels[turn.ID]; cancel != nil {
				cancel()
			}
		}
	}
	s.mu.Unlock()
	s.emit(ctx, round.TaskID, "round", round.ID, "round_interrupted", map[string]any{
		"round_id": round.ID, "round_sequence": round.Sequence,
	})
	return nil
}

func (s *Service) CompleteTask(ctx context.Context, taskID string) error {
	return s.completeTask(ctx, taskID, nil)
}

func (s *Service) completeTask(ctx context.Context, taskID string, decision *domain.TaskReviewDecision) error {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status == domain.TaskCompleted {
		return nil
	}
	// The store update is guarded by NOT EXISTS(active turn); a scheduler that
	// wins the race makes it return ErrActiveTurn instead of completing.
	now := s.now().UTC()
	if err := s.store.CompleteTaskWithReviewDecision(ctx, task.ID, task.Status, timeString(now), decision); err != nil {
		return err
	}
	s.emit(ctx, task.ID, "task", task.ID, "task_completed", nil)
	// Scratch is temporary by contract. A failure here leaves only disposable
	// files behind, so it must not turn a committed completion into an error.
	if cleanupErr := s.CleanupTaskScratch(ctx, task); cleanupErr != nil {
		log.Printf("cleanup task scratch %s: %v", task.ID, cleanupErr)
	}
	return nil
}

// CleanupTaskScratch removes the Task-level scratch directory on the host that
// owns the Task workdir. Callers use it when a Task is closed; failed Tasks
// keep their scratch until they are completed, cancelled, or deleted.
func (s *Service) CleanupTaskScratch(ctx context.Context, task domain.Task) error {
	item, err := s.store.Workspace(ctx, task.WorkspaceID)
	if err != nil {
		return err
	}
	if item.SSHCredentialRef != "" && s.secrets != nil {
		item.SSHPassword, _ = s.secrets.Get(item.SSHCredentialRef)
	}
	workDir := task.TaskWorkspacePath
	if strings.TrimSpace(workDir) == "" {
		workDir = item.RootPath
	}
	return workspacepkg.RemoveTaskScratch(ctx, item, workDir, task.ID)
}

func (s *Service) ReopenTask(ctx context.Context, taskID string) error {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status == domain.TaskWaitingUser || task.Status == domain.TaskActive {
		return nil
	}
	if _, activeErr := s.store.ActiveTurn(ctx, taskID); activeErr == nil {
		return store.ErrActiveTurn
	} else if !errors.Is(activeErr, sql.ErrNoRows) {
		return activeErr
	}
	switch task.Status {
	case domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled:
	default:
		return fmt.Errorf("task is %s", task.Status)
	}
	now := s.now().UTC()
	if err := s.store.UpdateTaskStatus(ctx, task.ID, task.Status, domain.TaskWaitingUser, timeString(now), ""); err != nil {
		return err
	}
	s.emit(ctx, task.ID, "task", task.ID, "task_reopened", map[string]any{"previous_status": task.Status})
	return nil
}

const (
	waitingReasonBackendIdleTimeout = "backend_idle_timeout"
	waitingReasonContextWindow      = "context_window_exceeded"
	// maxTurnAttempts bounds a Main turn to its first run plus one automatic
	// retry, including the context-window recovery below.
	maxTurnAttempts = 2
)

func (s *Service) runTurn(ctx context.Context, turnID string) {
	defer func() {
		// A canceled preparation can return before any terminal write happens.
		// Settle it while the cancel handle is still registered, then remove the
		// handle so a later InterruptTurn can fall back to the stored status.
		s.settleCanceledTurn(ctx, turnID)
		s.mu.Lock()
		delete(s.cancels, turnID)
		s.mu.Unlock()
	}()
	turn, err := s.store.Turn(ctx, turnID)
	if err != nil {
		return
	}
	userMessage := turn.Instruction
	channelContext, _ := s.store.ChannelContextForInboxBatch(ctx, turn.InboxBatchID)
	task, err := s.store.Task(ctx, turn.TaskID)
	if err != nil {
		return
	}
	if err := s.transitionTurn(ctx, &turn, domain.TurnPreparing, "turn_preparing"); err != nil {
		return
	}
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	workspace, err := s.store.Workspace(ctx, task.WorkspaceID)
	if err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	if workspace.SSHCredentialRef != "" && s.secrets != nil {
		workspace.SSHPassword, _ = s.secrets.Get(workspace.SSHCredentialRef)
	}
	agentAPIURL, err := s.AgentAPIURLForWorkspace(ctx, workspace)
	if err != nil {
		s.failTurn(ctx, &turn, task, fmt.Errorf("resolve Agent API URL: %w", err))
		return
	}
	snapshot, err := s.store.RuntimeSnapshot(ctx, turn.RuntimeConfigSnapshotID)
	if err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	model, unavailable := s.runtimeSnapshotAvailability(ctx, snapshot)
	if unavailable != "" {
		s.failTurn(ctx, &turn, task, fmt.Errorf("%s，请修改 %s 模型配置", unavailable, turn.AgentID))
		return
	}
	envGroup, err := s.store.EnvGroup(ctx, snapshot.EnvGroupID)
	if err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	handoff, _ := s.store.PendingAgentSessionHandoff(ctx, task.ID, turn.AgentID)
	var globalKB, projectKB, staleKB []domain.KnowledgeEntry
	var skills []domain.Skill
	var productLine domain.ProductLine
	kbEnabled := knowledgeEnabled(project, task)
	if len(channelContext) > 0 && channelRouteMode(channelContext) != "task_route" {
		kbEnabled = true
		instanceID := strings.TrimSpace(fmt.Sprint(channelContext["instance_id"]))
		endpoint := strings.TrimSpace(fmt.Sprint(channelContext["endpoint"]))
		conversationID := strings.TrimSpace(fmt.Sprint(channelContext["conversation_id"]))
		projectKB, _ = s.store.ChannelAllowedKnowledge(ctx, instanceID, endpoint, conversationID)
	} else if kbEnabled {
		lines, _ := s.store.ListProductLines(ctx, project.ID)
		productLine = resolveProductLine(lines, task.TargetBranch, project.DefaultBranch)
		globalKB, _ = s.store.ListKnowledge(ctx, "global", "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
		projectKB, _ = s.store.ListApplicableKnowledge(ctx, project.ID, productLine.ID, []domain.KnowledgeStatus{domain.KnowledgeVerified})
		globalStale, _ := s.store.ListKnowledge(ctx, "global", "", []domain.KnowledgeStatus{domain.KnowledgeStale})
		projectStale, _ := s.store.ListApplicableKnowledge(ctx, project.ID, productLine.ID, []domain.KnowledgeStatus{domain.KnowledgeStale})
		pending, _ := s.store.ListKnowledgeProposals(ctx, "", "")
		pendingEntries := map[string]bool{}
		for _, proposal := range pending {
			if proposal.Status == domain.KnowledgeProposalPending {
				pendingEntries[proposal.EntryID] = true
			}
		}
		for _, entry := range append(globalStale, projectStale...) {
			if !entry.IsIndex && !pendingEntries[entry.ID] {
				staleKB = append(staleKB, entry)
			}
		}
	}
	skills = s.activeTaskSkills(ctx, task)
	agent, err := s.store.TaskAgent(ctx, task.ID, turn.AgentID)
	if err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	conversation, _ := s.store.ConversationPageForAgent(ctx, task.ID, turn.AgentID, 0, 0, 200, nil)
	allTurns, _ := s.store.ListTurns(ctx, task.ID)
	identityContext := prompt.BackendSessionContext(channelContext)
	var reusableSession domain.BackendSession
	var reusableSessionErr error
	if snapshot.Backend == "dsh" {
		reusableSession, reusableSessionErr = s.store.ReusableDSHBackendSession(
			ctx, task.ID, turn.AgentID, workspace.ID, identityContext,
		)
		if reusableSessionErr == nil && !s.dshSessionCanReuse(ctx, reusableSession, model, envGroup) {
			reusableSession = domain.BackendSession{}
			reusableSessionErr = sql.ErrNoRows
		}
	} else {
		reusableSession, reusableSessionErr = s.store.ReusableBackendSession(
			ctx, task.ID, turn.AgentID, workspace.ID, snapshot.Backend, model.ID,
			snapshot.EnvGroupRevision, snapshot.CodexAccountID, identityContext,
		)
	}
	if reusableSessionErr != nil && !errors.Is(reusableSessionErr, sql.ErrNoRows) {
		s.failTurn(ctx, &turn, task, fmt.Errorf("load reusable backend session: %w", reusableSessionErr))
		return
	}
	releaseLeftoverWriter := reusableSessionErr == nil &&
		workspacepkg.SupportsSessionHome(snapshot.Backend) &&
		func() bool {
			previous := previousTurnForAgent(turn, allTurns)
			return previous.ID != "" && !previous.Status.EndedCleanly()
		}()
	recoveryRecentContext, includeRecoveryHandoff, includeTurnDiagnostics := recoveryContextNeeds(turn, allTurns, reusableSessionErr == nil, handoff.Summary)
	// The model needs an explicit dialogue anchor even when a backend session is
	// resumed. Image-only follow-ups otherwise have no referent except a possibly
	// stale current_goal.
	includeRecentContext := includeRecentContextForTurn(turn.AgentID, recoveryRecentContext)
	promptConversation := conversation.Items
	if includeRecentContext {
		if recentConversation, recentErr := s.store.ConversationPageForAgent(ctx, task.ID, turn.AgentID, 0, 0, 200, []string{"chat"}); recentErr == nil {
			promptConversation = recentConversation.Items
		}
	}
	hardwareGroups, _ := s.store.HardwareGroups(ctx, task.ID)
	mailAccounts, mailTemplates, mailDefaultTemplates := s.MailTaskResources(ctx, task)
	attachmentItems := []domain.Attachment{}
	seenAttachments := map[string]bool{}
	for _, conversationItem := range conversation.Items {
		raw, ok := conversationItem.Payload["attachments"]
		if !ok {
			continue
		}
		data, _ := json.Marshal(raw)
		var metadata []domain.Attachment
		if json.Unmarshal(data, &metadata) != nil {
			continue
		}
		for _, reference := range metadata {
			if reference.ID == "" || seenAttachments[reference.ID] {
				continue
			}
			seenAttachments[reference.ID] = true
			item, attachmentErr := s.store.Attachment(ctx, task.ID, reference.ID)
			if attachmentErr != nil {
				s.failTurn(ctx, &turn, task, fmt.Errorf("load attachment %s: %w", reference.ID, attachmentErr))
				return
			}
			attachmentItems = append(attachmentItems, item)
		}
	}
	// conversation.Items is oldest-first, so the allocator walks it backwards:
	// the current input's attachments get the budget before older ones.
	decisions := allocateInlineAttachments(
		attachmentItems,
		prompt.MaxInlineAttachmentBytes,
		prompt.MaxInlineAttachmentBytesPerTurn,
		func(item domain.Attachment) ([]byte, error) { return s.store.AttachmentContent(item) },
	)
	attachments := make([]prompt.AttachmentResource, 0, len(attachmentItems))
	for index, item := range attachmentItems {
		if decisions[index].Err != nil {
			s.failTurn(ctx, &turn, task, fmt.Errorf("read attachment %s: %w", item.ID, decisions[index].Err))
			return
		}
		// Oversized and budget-exhausted attachments stay metadata-only. Reading
		// them on every Turn and rewriting them into Context would multiply the
		// 200MiB storage cap into per-Turn memory and remote write time.
		resource := prompt.AttachmentResource{
			Attachment: item, Inline: decisions[index].Inline, BudgetExhausted: decisions[index].BudgetExhausted,
		}
		if decisions[index].Inline {
			resource.Content = string(decisions[index].Content)
		}
		attachments = append(attachments, resource)
	}
	responseLanguage := domain.LanguageAuto
	if settings, settingsErr := s.store.GeneralSettings(ctx); settingsErr == nil {
		responseLanguage = settings.Language
	}
	identitySupplement := ""
	if task.ID == petTaskID {
		// The assistant page normally seeds these, but a Turn can also run after a
		// restart or a direct message. Filling missing files here keeps the prompt's
		// core paths readable without ever replacing the pet's own content.
		if err := s.ensurePetWorkspaceFiles(ctx, nil); err != nil {
			log.Printf("prepare pet workspace for turn: %v", err)
		}
		// Built once per Turn so identity and principles come back after every
		// compaction without the pet having to re-read them.
		english := responseLanguage == domain.LanguageEnglish
		identitySupplement = petCoreDigestPrompt(s.petCoreDigest(ctx, english), english)
	}
	preview, err := s.prompts.Build(ctx, prompt.BuildInput{
		Project: project, Workspace: workspace, Task: task, Agent: agent, Snapshot: snapshot,
		GlobalKnowledge: globalKB, ProjectKnowledge: projectKB, StaleKnowledge: staleKB, Skills: skills,
		ProductLine: productLine, KnowledgeEnabled: kbEnabled,
		Conversation: promptConversation, Turns: allTurns, Hardware: hardwareGroups,
		MailAccounts: mailAccounts, MailTemplates: mailTemplates, MailDefaultTemplates: mailDefaultTemplates,
		Attachments: attachments, UserMessage: userMessage,
		RecoveryConversation: conversation.Items,
		Handoff:              handoff.Summary, AgentAPIURL: agentAPIURL, CurrentTurnID: turn.ID, CurrentRoundID: turn.RoundID,
		IncludeRecentContext: includeRecentContext, IncludeRecoveryHandoff: includeRecoveryHandoff, IncludeTurnDiagnostics: includeTurnDiagnostics,
		ChannelContext: channelContext, Identity: petTaskIdentity(task), IdentitySupplement: identitySupplement,
		Language: responseLanguage,
	})
	if err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	contextFiles := make(map[string]string, len(preview.ContextManifest))
	for _, item := range preview.ContextManifest {
		contextFiles[item.Path] = item.Content
	}
	sharedFiles := make(map[string]string, len(preview.SharedManifest))
	for _, item := range preview.SharedManifest {
		sharedFiles[item.Path] = item.Content
	}
	workDir := task.TaskWorkspacePath
	if workDir == "" {
		workDir = workspace.RootPath
	}
	scratchDir, err := workspacepkg.EnsureScratchDir(ctx, workspace, workDir, task.ID, turn.AgentID)
	if err != nil {
		s.failTurn(ctx, &turn, task, fmt.Errorf("prepare task scratch: %w", err))
		return
	}
	releaseSharedContext, err := s.materializeSharedContext(ctx, workspace, workDir, preview.SharedRoot, sharedFiles)
	if err != nil {
		s.failTurn(ctx, &turn, task, fmt.Errorf("materialize prompt context: shared snapshot: %w", err))
		return
	}
	defer releaseSharedContext()
	if err := workspacepkg.MaterializeContext(ctx, workspace, workDir, preview.ContextRoot, contextFiles); err != nil {
		s.failTurn(ctx, &turn, task, fmt.Errorf("materialize prompt context: agent resources: %w", err))
		return
	}
	if releaseLeftoverWriter {
		// Resuming a provider session is only unsafe while another process still
		// holds it. A turn that did not end cleanly is the signal that AHA may
		// have lost track of its backend process (a service restart cannot kill a
		// process running behind a WSL/SSH hop), so confirm that writer is gone
		// before appending to the same transcript. A cleanly finished turn keeps
		// its session and is never interrupted here.
		home, _ := workspacepkg.SessionHomeFor(workspacepkg.SessionHomeInput{
			Backend: snapshot.Backend, Workspace: workspace, WorkDir: workDir,
			SessionID: reusableSession.ID, CodexAccountID: reusableSession.CodexAccountID,
			EnvGroupID: snapshot.EnvGroupID,
		})
		// The two identifiers are alternatives, not a primary and a fallback: the
		// query matches a process by either one. The session home identifies an
		// isolated run exactly, and the resume id covers backends that keep their
		// default config directory. Supplying both widens coverage rather than
		// weakening it, and it is also what a Windows target needs, which exposes
		// a process command line but not its environment.
		query := workspacepkg.WriterQuery{
			EnvName: home.EnvName, EnvValue: home.Dir,
			ResumeSessionID: reusableSession.ProviderSession,
		}
		if snapshot.Backend == "dsh" {
			query.SessionFlag = "--session-id"
		}
		if err := s.stopLeftoverBackendWriter(ctx, task, workspace, query); err != nil {
			// The old writer could not be confirmed dead; starting a second one
			// on the same transcript is exactly the corruption being prevented.
			_ = s.store.CloseBackendSessionsForAgent(ctx, task.ID, turn.AgentID)
			reusableSession, reusableSessionErr = domain.BackendSession{}, sql.ErrNoRows
		}
	}
	packedPrompt := preview.EffectivePrompt
	turn.ContextWindow = model.ContextWindow
	turn.PromptChars = len([]rune(packedPrompt))
	turn.PromptSnapshot = packedPrompt
	turn.ContextReadyAt = s.now().UTC()
	if err := s.store.UpdateTurn(ctx, turn, turn.Status); err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	if err := s.transitionTurn(ctx, &turn, domain.TurnStarting, "turn_starting"); err != nil {
		return
	}
	var providerSession string
	var sessionUsageBaseline map[string]any
	session, err := reusableSession, reusableSessionErr
	if err == nil {
		providerSession = session.ProviderSession
		turn.BackendSessionID = session.ID
		if strings.TrimSpace(session.ContextUsageJSON) != "" {
			_ = json.Unmarshal([]byte(session.ContextUsageJSON), &sessionUsageBaseline)
		}
	}
	sessionID := turn.BackendSessionID
	if sessionID == "" {
		sessionID = domain.NewID("backend_session")
		turn.BackendSessionID = sessionID
		_ = s.store.UpdateTurn(ctx, turn, turn.Status)
	}
	environment := make(map[string]string, len(envGroup.Environment)+len(envGroup.SecretNames))
	for key, value := range envGroup.Environment {
		environment[key] = value
	}
	for environmentName, secretRef := range envGroup.SecretRefs {
		if value, ok := s.secrets.Get(secretRef); ok {
			environment[environmentName] = value
		}
	}
	// The scratch path is per Agent and per Turn, so it wins over any static
	// env-group value with the same name.
	environment["AHA2_SCRATCH_DIR"] = scratchDir
	capabilityToken := ""
	agentAPIForward := (*workspacepkg.ReverseForward)(nil)
	if s.agentAPI != nil && agentAPIURL != "" {
		capabilityToken, err = s.agentAPI.Issue(task.ID, turn.AgentID, turn.ID, s.agentAPICapabilityTTL(ctx))
		if err != nil {
			s.failTurn(ctx, &turn, task, fmt.Errorf("issue Agent API capability: %w", err))
			return
		}
		defer s.agentAPI.Revoke(capabilityToken)
		environment["AHA2_AGENT_API_URL"] = agentAPIURL
		environment["AHA2_AGENT_API_TOKEN"] = capabilityToken
		agentAPIForward = agentAPIReverseForward(workspace, agentAPIURL)
	}
	if snapshot.ProxyEnabled && s.proxyRuntime != nil {
		if settingsErr := s.proxyRuntime.ApplyEnvironment(ctx, environment); settingsErr != nil {
			s.failTurn(ctx, &turn, task, fmt.Errorf("apply proxy settings: %w", settingsErr))
			return
		}
	}
	if snapshot.ProxyEnabled && s.proxyRuntime == nil {
		settings, settingsErr := s.store.ProxySettings(ctx)
		if settingsErr != nil {
			s.failTurn(ctx, &turn, task, fmt.Errorf("读取代理设置失败: %w", settingsErr))
			return
		}
		settings, settingsErr = proxyconfig.Normalize(settings)
		if settingsErr != nil {
			s.failTurn(ctx, &turn, task, fmt.Errorf("代理设置无效: %w", settingsErr))
			return
		}
		proxyconfig.ApplyEnvironment(environment, settings)
	}
	appendAgentAPIToNoProxy(environment, agentAPIURL)
	if snapshot.CodexAccountID != "" {
		if snapshot.Backend != "codex" || s.codex == nil {
			s.failTurn(ctx, &turn, task, fmt.Errorf("Codex 官方账号运行时不可用"))
			return
		}
		unlock := s.codex.LockAccount(snapshot.CodexAccountID)
		profileDir, profileErr := s.codex.PrepareProfile(
			ctx, snapshot.CodexAccountID, workspace, workDir, sessionID,
		)
		unlock()
		if profileErr != nil {
			s.failTurn(ctx, &turn, task, profileErr)
			return
		}
		environment["CODEX_HOME"] = profileDir
		defer func() {
			unlock := s.codex.LockAccount(snapshot.CodexAccountID)
			defer unlock()
			s.codex.SyncProfile(context.Background(), snapshot.CodexAccountID, workspace, profileDir)
		}()
	} else if snapshot.Backend == "codex" {
		// Env-provider runs must use their own Codex home. Reusing the operator's
		// ~/.codex would inherit their global config (including mcp_servers) and
		// let an AHA turn share rollout files with the operator's own Codex.
		envHome := workspacepkg.CodexEnvHomeDir(workspace, workDir, sessionID)
		if err := workspacepkg.EnsureCodexEnvHome(ctx, workspace, workspacepkg.RunnerFor(workspace), envHome); err != nil {
			s.failTurn(ctx, &turn, task, fmt.Errorf("prepare Codex Env home: %w", err))
			return
		}
		environment["CODEX_HOME"] = envHome
	} else if snapshot.Backend == "claude" {
		// Env-provider Claude runs keep their transcript out of the operator's
		// own ~/.claude. The native source authenticates with that account, so it
		// keeps the default directory and is not redirected here.
		home, _ := workspacepkg.SessionHomeFor(workspacepkg.SessionHomeInput{
			Backend: "claude", Workspace: workspace, WorkDir: workDir,
			SessionID: sessionID, EnvGroupID: snapshot.EnvGroupID,
		})
		if home.EnvName != "" {
			if err := workspacepkg.EnsureClaudeConfigDir(ctx, workspace, workspacepkg.RunnerFor(workspace), home.Dir); err != nil {
				s.failTurn(ctx, &turn, task, fmt.Errorf("prepare Claude config dir: %w", err))
				return
			}
			environment[home.EnvName] = home.Dir
		}
	} else if snapshot.Backend == "dsh" {
		home, _ := workspacepkg.SessionHomeFor(workspacepkg.SessionHomeInput{
			Backend: "dsh", Workspace: workspace, WorkDir: workDir, SessionID: sessionID,
		})
		if err := workspacepkg.EnsureDSHHomeDir(ctx, workspace, workspacepkg.RunnerFor(workspace), home.Dir); err != nil {
			s.failTurn(ctx, &turn, task, fmt.Errorf("prepare DSH home: %w", err))
			return
		}
		environment[home.EnvName] = home.Dir
	}
	turn.SessionReadyAt = s.now().UTC()
	if err := s.store.UpdateTurn(ctx, turn, turn.Status); err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	if err := s.transitionTurn(ctx, &turn, domain.TurnRunning, "turn_running"); err != nil {
		return
	}
	filesystem, approval := parsePermissionsJSON(snapshot.PermissionsJSON)
	latestSessionUsage := cloneUsageMap(sessionUsageBaseline)
	mcpServers, allowedTools := s.mcpRuntimeForTask(ctx, task, agentAPIURL)
	var liveInbox backend.LiveInbox
	if snapshot.Backend == "claude" || snapshot.Backend == "codex" {
		// The adapter claims this inbox when the runner can stream stdin. On a
		// transport that cannot, the inbox stays unclaimed and the next Turn
		// receives the messages through the normal path.
		liveInbox = turnLiveInbox{service: s, task: task, turn: turn}
	}
	result, executeErr := s.executor.Execute(ctx, ExecutionRequest{
		Turn: turn, Task: task, Project: project, Workspace: workspace, Snapshot: snapshot,
		Model: model, EnvGroup: envGroup, Environment: environment, Prompt: packedPrompt,
		ProviderSessionID: providerSession, BackendSessionID: sessionID,
		AgentAPIForward: agentAPIForward,
		Filesystem:      filesystem, Approval: approval,
		MCPServers: mcpServers, AllowedTools: allowedTools,
		LiveInbox: liveInbox,
	}, func(event ExecutionEvent) {
		now := s.now().UTC()
		watchdogEvent := event.Type == "agent_stalled" || event.Type == "agent_heartbeat" || event.Type == "agent_idle_timeout"
		if !watchdogEvent {
			if turn.FirstEventAt.IsZero() {
				turn.FirstEventAt = now
			}
			turn.LastActivityAt = now
		}
		switch event.Type {
		case "agent_stalled", "agent_idle_timeout":
			if turn.StalledAt.IsZero() {
				turn.StalledAt = now
			}
		case "agent_resumed":
			turn.StalledAt = time.Time{}
		}
		if event.Type == "agent_usage" {
			if usage, ok := event.Data["usage"].(map[string]any); ok {
				latestSessionUsage = cloneUsageMap(usage)
				turn.Usage = turnUsage(snapshot.Backend, usage, sessionUsageBaseline)
			}
		}
		_ = s.store.UpdateTurn(context.Background(), turn, turn.Status)
		s.recordExecutionEvent(context.Background(), turn, event)
	})
	turn.BackendFinishedAt = s.now().UTC()
	_ = s.store.UpdateTurn(context.Background(), turn, turn.Status)
	if executeErr != nil {
		if errors.Is(executeErr, context.Canceled) {
			if !s.runCanceled() {
				// A user interruption settles the running turn; a shutdown
				// cancellation is left to RecoverInterrupted so its claimed inbox
				// messages are requeued rather than marked processed.
				s.interruptTurn(context.Background(), &turn, task)
			}
			return
		}
		switch {
		case errors.Is(executeErr, backend.ErrBackendIdleTimeout):
			turn.WaitingReason = waitingReasonBackendIdleTimeout
		case errors.Is(executeErr, backend.ErrContextWindowExceeded):
			turn.WaitingReason = waitingReasonContextWindow
			if turn.Attempt < maxTurnAttempts {
				// Retrying the same provider session cannot recover: its
				// transcript is already over the window. Supersede it so the
				// retry starts from a fresh context and can fit again.
				if err := s.store.CloseBackendSessionsForAgent(ctx, task.ID, turn.AgentID); err != nil {
					log.Printf("context window recovery: close backend sessions for %s/%s: %v", task.ID, turn.AgentID, err)
					turn.WaitingReason = ""
				} else {
					s.recordContextWindowReset(ctx, turn)
				}
			}
		}
		s.failTurn(context.Background(), &turn, task, executeErr)
		return
	}
	now := s.now().UTC()
	backendSession := domain.BackendSession{
		ID: sessionID, TaskID: task.ID, AgentID: turn.AgentID, WorkspaceID: workspace.ID,
		Backend: snapshot.Backend, ModelID: model.ID, EnvGroupRevision: snapshot.EnvGroupRevision,
		CodexAccountID: snapshot.CodexAccountID, IdentityContext: identityContext,
		ProviderSession: result.ProviderSessionID, Status: "active", CreatedAt: now, LastUsedAt: now,
	}
	if len(latestSessionUsage) > 0 {
		usageJSON, _ := json.Marshal(latestSessionUsage)
		backendSession.ContextUsageJSON = string(usageJSON)
	}
	if !session.CreatedAt.IsZero() {
		backendSession.CreatedAt = session.CreatedAt
	}
	if errors.Is(reusableSessionErr, sql.ErrNoRows) {
		_ = s.store.CloseBackendSessionsForAgent(context.Background(), task.ID, turn.AgentID)
	}
	_ = s.store.UpsertBackendSession(context.Background(), backendSession)
	turn.BackendSessionID = sessionID
	turn.Result = strings.TrimSpace(result.Reply)
	turn.ExitCode = &result.ExitCode
	turn.FinishedAt = now
	previous := turn.Status
	if result.ExitCode == 0 && turn.Result != "" {
		turn.Status = domain.TurnSucceeded
	} else {
		turn.Status = domain.TurnFailed
		if turn.Result == "" {
			turn.Error = fmt.Sprintf("backend exited with code %d without a reply", result.ExitCode)
		}
	}
	if err := s.store.UpdateTurn(context.Background(), turn, previous); err != nil {
		return
	}
	s.updateOrchestrationRouteStatus(context.Background(), turn)
	if turn.Result != "" {
		message := domain.Message{
			ID: domain.NewID("message"), TaskID: task.ID, TurnID: turn.ID, Role: "assistant",
			Sender: turn.AgentID, Content: turn.Result, CreatedAt: now,
		}
		category := "chat"
		kind := "agent_message"
		if turn.AgentID != "main" || s.mainReplyIsIntermediate(context.Background(), turn) {
			category = "update"
			kind = "agent_result"
		}
		finalItem, finalizeErr := s.store.FinalizeTurnReply(context.Background(), message, domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: turn.RoundID, TurnID: turn.ID,
			AgentID: turn.AgentID, Category: category, Kind: kind, Summary: turn.Result,
			Payload: map[string]any{"attempt": turn.Attempt, "generation": turn.Generation}, CreatedAt: now,
		})
		if finalizeErr == nil && turn.AgentID == "main" && category == "chat" {
			s.emit(context.Background(), task.ID, "conversation", finalItem.ID, "agent_reply", map[string]any{
				"round_id": turn.RoundID, "turn_id": turn.ID, "agent_id": turn.AgentID, "text": turn.Result,
			})
		}
	}
	if turn.Status == domain.TurnSucceeded && handoff.ID != "" {
		_ = s.store.ConsumeAgentSessionHandoff(context.Background(), handoff.ID, now)
	}
	if turn.Status == domain.TurnSucceeded {
		s.recordTurnDuration(context.Background(), turn)
		s.emitTurn(context.Background(), turn, "turn_succeeded", map[string]any{"exit_code": result.ExitCode})
	} else {
		if turn.Error == "" {
			turn.Error = fmt.Sprintf("backend exited with code %d", result.ExitCode)
		}
		_, _ = s.store.AddConversationItem(context.Background(), domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: turn.RoundID, TurnID: turn.ID,
			AgentID: turn.AgentID, Category: "error", Kind: "turn_failed", Summary: turn.Error,
			Payload: map[string]any{"attempt": turn.Attempt, "exit_code": result.ExitCode}, CreatedAt: now,
		})
		s.recordTurnDuration(context.Background(), turn)
		s.emitTurn(context.Background(), turn, "turn_failed", map[string]any{
			"exit_code": result.ExitCode, "error": turn.Error,
		})
	}
	if turn.AgentID == "main" {
		terminalState := "succeeded"
		if turn.Status != domain.TurnSucceeded {
			terminalState = "failed"
		}
		s.SyncAgentChannelMessageStatuses(context.Background(), task.ID, turn.ID, "", terminalState, "", "")
	}
	s.afterTurnTerminal(context.Background(), task, turn)
}

// recordContextWindowReset tells the owner why the session was rotated. The
// failed turn itself keeps the provider's original 400 text as its error, so
// this is an explanation rather than a replacement for it.
func (s *Service) recordContextWindowReset(ctx context.Context, turn domain.Turn) {
	_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: turn.TaskID, RoundID: turn.RoundID, TurnID: turn.ID,
		AgentID: "aha", StreamAgentID: turn.AgentID, FromAgentID: "aha", ToAgentID: turn.AgentID,
		RouteKind: "session_control", Category: "update", Kind: "backend_session_auto_reset",
		Summary:   "上下文超出模型窗口，AHA 已重置该 Agent 的会话并自动重试一次",
		Payload:   map[string]any{"reason": waitingReasonContextWindow, "attempt": turn.Attempt},
		CreatedAt: s.now().UTC(),
	})
}

func (s *Service) dshSessionCanReuse(ctx context.Context, session domain.BackendSession, model domain.Model, envGroup domain.EnvGroup) bool {
	if session.Backend != "dsh" || model.Backend != "dsh" || envGroup.Backend != "dsh" {
		return false
	}
	previousProviderID, previousEnvGroupID, err := s.store.ModelProviderAndEnvGroup(ctx, session.ModelID)
	if err != nil || previousProviderID == "" || previousEnvGroupID == "" {
		return false
	}
	previousEnvGroup, err := s.store.EnvGroup(ctx, previousEnvGroupID)
	if err != nil {
		return false
	}
	return sameDSHSessionSecurityDomain(previousProviderID, previousEnvGroup, model.ProviderID, envGroup)
}

func (s *Service) dshSnapshotsShareSessionDomain(ctx context.Context, previous, next domain.RuntimeConfigSnapshot) bool {
	previousProviderID, previousEnvGroupID, previousErr := s.store.ModelProviderAndEnvGroup(ctx, previous.ModelID)
	nextProviderID, nextEnvGroupID, nextErr := s.store.ModelProviderAndEnvGroup(ctx, next.ModelID)
	if previousErr != nil || nextErr != nil || previousEnvGroupID == "" || nextEnvGroupID == "" {
		return false
	}
	previousEnvGroup, previousErr := s.store.EnvGroup(ctx, previousEnvGroupID)
	nextEnvGroup, nextErr := s.store.EnvGroup(ctx, nextEnvGroupID)
	if previousErr != nil || nextErr != nil {
		return false
	}
	return sameDSHSessionSecurityDomain(previousProviderID, previousEnvGroup, nextProviderID, nextEnvGroup)
}

func sameDSHSessionSecurityDomain(previousProviderID string, previous domain.EnvGroup, nextProviderID string, next domain.EnvGroup) bool {
	if previousProviderID == "" || previousProviderID != nextProviderID ||
		previous.ProviderID != previousProviderID || next.ProviderID != nextProviderID ||
		previous.Backend != "dsh" || next.Backend != "dsh" {
		return false
	}
	if !maps.Equal(credentialRefSet(previous.SecretRefs), credentialRefSet(next.SecretRefs)) {
		return false
	}
	return dshEnvGroupOrigin(previous.Environment) != "" &&
		dshEnvGroupOrigin(previous.Environment) == dshEnvGroupOrigin(next.Environment)
}

func credentialRefSet(refs map[string]string) map[string]bool {
	result := map[string]bool{}
	for _, ref := range refs {
		if ref = strings.TrimSpace(ref); ref != "" {
			result[ref] = true
		}
	}
	return result
}

func dshEnvGroupOrigin(environment map[string]string) string {
	for _, key := range []string{"OPENAI_BASE_URL", "ANTHROPIC_BASE_URL"} {
		raw := strings.TrimSpace(environment[key])
		if raw == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return ""
		}
		return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
	}
	return ""
}

func channelRouteMode(channelContext map[string]any) string {
	route, _ := channelContext["route"].(map[string]any)
	return strings.TrimSpace(fmt.Sprint(route["mode"]))
}

func (s *Service) materializeSharedContext(
	ctx context.Context,
	workspace domain.Workspace,
	workDir string,
	root string,
	files map[string]string,
) (func(), error) {
	if strings.TrimSpace(root) == "" {
		return func() {}, nil
	}
	s.sharedContextMu.Lock()
	state, ok := s.sharedContexts[root]
	scope := sharedContextScope(workspace, root)
	if ok && state.refs == 0 && (state.scope != scope || s.latestShared[state.scope] != root) {
		delete(s.sharedContexts, root)
		state, ok = nil, false
	}
	if !ok {
		if err := workspacepkg.MaterializeContext(ctx, workspace, workDir, root, files); err != nil {
			s.sharedContextMu.Unlock()
			return nil, err
		}
		state = &sharedContextState{scope: scope}
		s.sharedContexts[root] = state
	}
	state.refs++
	s.latestShared[scope] = root
	if err := s.pruneSharedContextsLocked(ctx, workspace, workDir, scope, root); err == nil {
		s.dropPrunedSharedContextsLocked(scope)
	}
	s.sharedContextMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.sharedContextMu.Lock()
			defer s.sharedContextMu.Unlock()
			if current := s.sharedContexts[root]; current != nil && current.refs > 0 {
				current.refs--
			}
			latest := s.latestShared[scope]
			if latest == "" {
				return
			}
			if err := s.pruneSharedContextsLocked(context.Background(), workspace, workDir, scope, latest); err == nil {
				s.dropPrunedSharedContextsLocked(scope)
			}
		})
	}, nil
}

func sharedContextScope(workspace domain.Workspace, root string) string {
	if workspace.Transport == "native" {
		root = filepath.Clean(filepath.Dir(root))
		if runtime.GOOS == "windows" {
			root = strings.ToLower(root)
		}
		return "native\x00" + root
	}
	root = strings.ReplaceAll(root, "\\", "/")
	unc := strings.HasPrefix(root, "//")
	root = path.Dir(strings.TrimPrefix(root, "//"))
	if unc {
		root = "//" + strings.TrimPrefix(root, "/")
	}
	if workspacepkg.IsWindowsWorkspace(workspace) {
		root = strings.ToLower(root)
	}
	return workspace.Transport + "\x00" + root
}

func (s *Service) pruneSharedContextsLocked(
	ctx context.Context,
	workspace domain.Workspace,
	workDir string,
	scope string,
	currentRoot string,
) error {
	keep := make([]string, 0, len(s.sharedContexts)+1)
	for root, state := range s.sharedContexts {
		if state.scope == scope && state.refs > 0 {
			keep = append(keep, root)
		}
	}
	if latest := s.latestShared[scope]; latest != "" {
		keep = append(keep, latest)
	}
	return workspacepkg.PruneSharedContexts(ctx, workspace, workDir, currentRoot, keep)
}

func (s *Service) dropPrunedSharedContextsLocked(scope string) {
	latest := s.latestShared[scope]
	for root, state := range s.sharedContexts {
		if state.scope == scope && state.refs == 0 && root != latest {
			delete(s.sharedContexts, root)
		}
	}
}

func cloneUsageMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func turnUsage(backendName string, current, baseline map[string]any) map[string]any {
	result := cloneUsageMap(current)
	if backendName != "codex" || len(baseline) == 0 {
		return result
	}
	for _, key := range []string{"input_tokens", "cached_input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "output_tokens", "reasoning_output_tokens"} {
		currentValue := usageValue(current[key])
		baselineValue := usageValue(baseline[key])
		if currentValue >= baselineValue {
			result[key] = currentValue - baselineValue
		}
	}
	return result
}

func usageValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		result, _ := typed.Float64()
		return result
	default:
		return 0
	}
}

func appendAgentAPIToNoProxy(environment map[string]string, rawURL string) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" {
		return
	}
	host := parsed.Hostname()
	for _, value := range strings.Split(environment["NO_PROXY"], ",") {
		if strings.EqualFold(strings.TrimSpace(value), host) {
			return
		}
	}
	if environment["NO_PROXY"] == "" {
		environment["NO_PROXY"] = host
	} else {
		environment["NO_PROXY"] += "," + host
	}
}

func (s *Service) mainReplyIsIntermediate(ctx context.Context, turn domain.Turn) bool {
	if turn.AgentID != "main" {
		return false
	}
	if pending, err := s.store.PendingInboxCount(ctx, turn.TaskID); err == nil && pending > 0 {
		return true
	}
	turns, err := s.store.TurnsForRound(ctx, turn.RoundID)
	if err != nil {
		return false
	}
	for agentID, other := range latestTurnsByAgent(turns) {
		if agentID == "main" || other.ID == turn.ID {
			continue
		}
		if !other.Status.Terminal() {
			return true
		}
		if !turn.StartedAt.IsZero() && !other.FinishedAt.IsZero() && other.FinishedAt.After(turn.StartedAt) {
			return true
		}
	}
	return false
}

func (s *Service) transitionTurn(ctx context.Context, turn *domain.Turn, status domain.TurnStatus, eventType string) error {
	previous := turn.Status
	now := s.now().UTC()
	turn.Status = status
	switch status {
	case domain.TurnPreparing:
		turn.PreparedAt = now
	case domain.TurnRunning:
		turn.StartedAt = now
	}
	if err := s.store.UpdateTurn(ctx, *turn, previous); err != nil {
		return err
	}
	_ = s.store.UpdateTaskAgentStatus(ctx, turn.TaskID, turn.AgentID, string(status), now)
	s.updateOrchestrationRouteStatus(ctx, *turn)
	s.emitTurn(ctx, *turn, eventType, map[string]any{"status": status})
	return nil
}

// terminalWriteTimeout bounds a terminal-state write that must outlive the run
// context. It only covers one settlement, so a short timeout keeps a stuck
// database from blocking shutdown.
const terminalWriteTimeout = 5 * time.Second

// terminalWriteContext detaches a terminal settlement from the context that ran
// the turn. A canceled run context or a disconnected client must not prevent
// the interrupted/failed state from being stored, otherwise the turn stays
// non-terminal after its cancel handle is gone.
func terminalWriteContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), terminalWriteTimeout)
}

// runCanceled reports whether the shared run context has been canceled, i.e.
// the process is shutting down. A turn canceled by that shared context is left
// non-terminal on purpose: RecoverInterrupted requeues its claimed inbox items
// on the next start, while settling it here would mark them processed and
// silently drop queued owner messages. A user interruption cancels only the
// turn's own context, so this stays false and keeps the original semantics.
func (s *Service) runCanceled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing || s.runContext != nil && s.runContext.Err() != nil
}

// settleCanceledTurn settles a turn whose run context was canceled before it
// started executing. Such a turn returns through preparation error paths that
// cannot write with the canceled context, and the runTurn defer removes the
// cancel handle afterwards; without this the turn becomes a zombie.
//
// Only queued/preparing/starting turns are touched: once execution started the
// executor's own cancellation path settles the turn, and a stored succeeded or
// failed result always wins because terminal statuses are skipped. A shutdown
// cancellation is not a user interruption, so it leaves the turn non-terminal
// for RecoverInterrupted to requeue its claimed inbox items.
func (s *Service) settleCanceledTurn(runCtx context.Context, turnID string) {
	if runCtx.Err() == nil || s.runCanceled() {
		return
	}
	ctx, cancel := terminalWriteContext()
	defer cancel()
	turn, err := s.store.Turn(ctx, turnID)
	if err != nil || turn.Status.Terminal() {
		return
	}
	switch turn.Status {
	case domain.TurnQueued, domain.TurnPreparing, domain.TurnStarting:
	default:
		return
	}
	task, err := s.store.Task(ctx, turn.TaskID)
	if err != nil {
		return
	}
	s.interruptTurn(ctx, &turn, task)
}

func (s *Service) failTurn(ctx context.Context, turn *domain.Turn, task domain.Task, cause error) {
	if ctx.Err() != nil || errors.Is(cause, context.Canceled) {
		if s.runCanceled() {
			// Shutdown canceled the turn. Leave it non-terminal so the claimed
			// inbox stays recoverable instead of being marked processed through
			// afterTurnTerminal.
			return
		}
		// A user interruption is an interruption, not a backend failure, and the
		// settlement must not inherit the canceled run context.
		s.interruptTurn(context.Background(), turn, task)
		return
	}
	ctx, cancel := terminalWriteContext()
	defer cancel()
	now := s.now().UTC()
	previous := turn.Status
	turn.Status, turn.Error, turn.FinishedAt = domain.TurnFailed, cause.Error(), now
	code := 1
	turn.ExitCode = &code
	if err := s.store.UpdateTurn(ctx, *turn, previous); err != nil {
		return
	}
	s.updateOrchestrationRouteStatus(ctx, *turn)
	_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: turn.RoundID, TurnID: turn.ID,
		AgentID: turn.AgentID, Category: "error", Kind: "turn_failed", Summary: cause.Error(),
		Payload: map[string]any{"attempt": turn.Attempt}, CreatedAt: now,
	})
	s.recordTurnDuration(ctx, *turn)
	s.emitTurn(ctx, *turn, "turn_failed", map[string]any{"error": cause.Error()})
	if turn.AgentID == "main" {
		s.SyncAgentChannelMessageStatuses(ctx, task.ID, turn.ID, "", "failed", "", "")
	}
	s.afterTurnTerminal(ctx, task, *turn)
}

// interruptTurn reports whether this call moved the turn to interrupted.
// The caller's context is intentionally ignored: interruption is often the
// reason it is already canceled, so the settlement must run detached to store
// the terminal status and its conversation item.
func (s *Service) interruptTurn(_ context.Context, turn *domain.Turn, task domain.Task) bool {
	ctx, cancel := terminalWriteContext()
	defer cancel()
	now := s.now().UTC()
	previous := turn.Status
	turn.Status, turn.FinishedAt = domain.TurnInterrupted, now
	code := 130
	turn.ExitCode = &code
	if err := s.store.UpdateTurn(ctx, *turn, previous); err != nil {
		return false
	}
	s.updateOrchestrationRouteStatus(ctx, *turn)
	_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: turn.RoundID, TurnID: turn.ID,
		AgentID: turn.AgentID, Category: "update", Kind: "turn_interrupted",
		Summary: turn.AgentID + " 已中断", CreatedAt: now,
	})
	s.recordTurnDuration(ctx, *turn)
	s.emitTurn(ctx, *turn, "turn_interrupted", nil)
	if turn.AgentID == "main" {
		s.SyncAgentChannelMessageStatuses(ctx, task.ID, turn.ID, "", "interrupted", "", "")
	}
	s.afterTurnTerminal(ctx, task, *turn)
	return true
}

func (s *Service) recordTurnDuration(ctx context.Context, turn domain.Turn) {
	total := elapsedBetween(turn.QueuedAt, turn.FinishedAt)
	queue := elapsedBetween(turn.QueuedAt, turn.PreparedAt)
	prepare := elapsedBetween(turn.PreparedAt, turn.StartedAt)
	run := elapsedBetween(turn.StartedAt, turn.FinishedAt)
	contextPrepare := elapsedBetween(turn.PreparedAt, turn.ContextReadyAt)
	sessionWake := elapsedBetween(turn.ContextReadyAt, turn.SessionReadyAt)
	backendStartEnd := turn.FirstEventAt
	if backendStartEnd.IsZero() {
		backendStartEnd = turn.BackendFinishedAt
	}
	backendStart := elapsedBetween(turn.StartedAt, backendStartEnd)
	active := elapsedBetween(turn.FirstEventAt, turn.BackendFinishedAt)
	finalize := elapsedBetween(turn.BackendFinishedAt, turn.FinishedAt)
	_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID: "conversation-turn-duration-" + turn.ID, TaskID: turn.TaskID, RoundID: turn.RoundID, TurnID: turn.ID,
		AgentID: "aha", StreamAgentID: turn.AgentID, FromAgentID: "aha", ToAgentID: turn.AgentID,
		RouteKind: "turn_status", Category: "update", Kind: "turn_duration",
		Summary: fmt.Sprintf("Turn %d 已结束，耗时 %s", turn.Sequence, time.Duration(total)*time.Millisecond),
		Payload: map[string]any{
			"agent_id": turn.AgentID, "turn_sequence": turn.Sequence, "attempt": turn.Attempt,
			"status": turn.Status, "elapsed_ms": total, "queue_duration_ms": queue,
			"prepare_duration_ms": prepare, "run_duration_ms": run,
			"context_prepare_duration_ms": contextPrepare, "session_wake_duration_ms": sessionWake,
			"backend_start_duration_ms": backendStart, "active_duration_ms": active,
			"finalize_duration_ms": finalize,
		},
		CreatedAt: turn.FinishedAt,
	})
}

func elapsedBetween(start, end time.Time) int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func (s *Service) applyMemoryPatch(ctx context.Context, task domain.Task, turn domain.Turn, memory domain.TaskMemory, patch MemoryPatch) error {
	memory.TaskID = task.ID
	memory.CurrentGoal = task.CurrentGoal
	if patch.CurrentGoal != nil {
		goal := strings.TrimSpace(*patch.CurrentGoal)
		if goal == "" || len([]rune(goal)) > 2000 {
			return fmt.Errorf("current goal is invalid")
		}
		changeType := strings.TrimSpace(patch.GoalChangeType)
		if changeType == "" {
			changeType = "revise"
		}
		reason := strings.TrimSpace(patch.GoalReason)
		now := s.now().UTC()
		if err := s.store.UpdateTaskGoalWithEvent(ctx, domain.TaskGoalChange{
			TaskID: task.ID, RoundID: turn.RoundID, TurnID: turn.ID,
			SourceMessageID: turn.InputMessageID, GoalAfter: goal,
			ChangeType: changeType, Reason: reason, CreatedAt: now,
		}); err != nil {
			return err
		}
		memory.CurrentGoal = goal
	}
	memory.Decisions = appendUnique(memory.Decisions, patch.Decisions...)
	memory.Facts = appendUnique(memory.Facts, patch.Facts...)
	memory.Excluded = appendUnique(memory.Excluded, patch.Excluded...)
	memory.Progress = appendUnique(memory.Progress, patch.Progress...)
	memory.Verification = appendUnique(memory.Verification, patch.Verification...)
	memory.NextActions = appendUnique(memory.NextActions, patch.NextActions...)
	memory.UpdatedAt = s.now().UTC()
	return s.store.UpsertTaskMemory(ctx, memory)
}

func (s *Service) createKnowledgeCandidates(ctx context.Context, projectID, taskID, turnID, branch, productLineID string, candidates []KnowledgeCandidate) ([]domain.KnowledgeEntry, []domain.KnowledgeProposal, error) {
	now := s.now().UTC()
	reviewSettings, _ := s.store.KnowledgeReviewSettings(ctx)
	reviewMode := "manual"
	if reviewSettings.AutoApprove {
		reviewMode = "auto"
	}
	result := make([]domain.KnowledgeEntry, 0, len(candidates))
	proposals := make([]domain.KnowledgeProposal, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Body) == "" {
			continue
		}
		scope := candidate.Scope
		if scope != "global" {
			scope = "project"
		}
		if scope == "global" && strings.TrimSpace(candidate.EntryID) == "" && (candidate.ParentID == nil || strings.TrimSpace(*candidate.ParentID) == "") {
			parentID := store.GlobalBehaviorLessonsKnowledgeID
			if strings.TrimSpace(candidate.Type) == "diagnostic" {
				parentID = store.GlobalTechnicalLessonsKnowledgeID
			}
			candidate.ParentID = &parentID
		}
		lineID := strings.TrimSpace(candidate.ProductLineID)
		if lineID == "" && scope == "project" {
			lineID = productLineID
		}
		entry := domain.KnowledgeEntry{
			ID: domain.NewID("knowledge"), Scope: scope, Type: candidate.Type, Title: candidate.Title,
			Body: candidate.Body, Status: domain.KnowledgeCandidate, Confidence: candidate.Confidence, Revision: 1,
			ContentHash:  fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(candidate.Title)+"\n"+strings.TrimSpace(candidate.Body)))),
			SourceTaskID: taskID, SourceTurnID: turnID, BranchScope: branch, ProductLineID: lineID, CreatedAt: now, UpdatedAt: now,
		}
		if scope == "project" {
			entry.ProjectID = projectID
		}
		entry.Slug = store.KnowledgeSlug(entry.Title, entry.ID)
		applyKnowledgeHierarchy(&entry, candidate)
		baseRevision := 0
		foreignRevision := false
		if strings.TrimSpace(candidate.EntryID) != "" {
			existing, err := s.store.Knowledge(ctx, candidate.EntryID)
			if err != nil {
				return nil, nil, err
			}
			entry.ID, entry.CreatedAt = existing.ID, existing.CreatedAt
			entry.Scope, entry.ProjectID = existing.Scope, existing.ProjectID
			foreignRevision = existing.Scope == "project" && existing.ProjectID != projectID
			if foreignRevision {
				entry.BoundProjectID, entry.BindingMode = projectID, "project"
				entry.BranchScope, entry.ProductLineID = existing.BranchScope, existing.ProductLineID
			}
			entry.ParentID, entry.Slug = existing.ParentID, existing.Slug
			entry.SortOrder, entry.IsIndex = existing.SortOrder, existing.IsIndex
			applyKnowledgeHierarchy(&entry, candidate)
			if existing.IsIndex {
				entry.ProductLineID = ""
			}
			baseRevision = existing.Revision
			entry.Revision = baseRevision + 1
		}
		proposalReviewMode := reviewMode
		if foreignRevision {
			proposalReviewMode = "manual"
		}
		proposal := domain.KnowledgeProposal{
			ID: domain.NewID("knowledge_proposal"), EntryID: entry.ID, BaseRevision: baseRevision, Proposed: entry,
			SourceTaskID: taskID, SourceTurnID: turnID, Status: domain.KnowledgeProposalPending,
			ReviewMode: proposalReviewMode, Reason: strings.TrimSpace(candidate.Reason), CreatedAt: now, UpdatedAt: now,
		}
		created, err := s.store.CreateKnowledgeProposal(ctx, proposal)
		if err != nil {
			return nil, nil, err
		}
		if baseRevision > 0 {
			if stale, err := s.store.Knowledge(ctx, entry.ID); err == nil {
				s.linkTaskMemoryKnowledge(ctx, taskID, stale)
			}
		}
		s.emit(ctx, taskID, "knowledge_proposal", created.ID, "knowledge_proposal_created", map[string]any{"entry_id": created.EntryID, "title": created.Proposed.Title, "base_revision": created.BaseRevision})
		if reviewSettings.AutoApprove && !foreignRevision {
			approved, published, err := s.store.ApproveKnowledgeProposal(ctx, created.ID, s.now().UTC())
			if err != nil {
				return nil, nil, fmt.Errorf("auto-approve knowledge proposal %s: %w", created.ID, err)
			}
			s.linkTaskMemoryKnowledge(ctx, taskID, published)
			s.emit(ctx, taskID, "knowledge_proposal", approved.ID, "knowledge_proposal_approved", map[string]any{"entry_id": published.ID, "revision": published.Revision, "review_mode": "auto"})
			result = append(result, published)
			proposals = append(proposals, approved)
		} else {
			result = append(result, created.Proposed)
			proposals = append(proposals, created)
		}
	}
	return result, proposals, nil
}

func applyKnowledgeHierarchy(entry *domain.KnowledgeEntry, candidate KnowledgeCandidate) {
	if candidate.ParentID != nil {
		entry.ParentID = strings.TrimSpace(*candidate.ParentID)
	}
	if candidate.Slug != nil {
		entry.Slug = strings.TrimSpace(*candidate.Slug)
	}
	if candidate.SortOrder != nil {
		entry.SortOrder = *candidate.SortOrder
	}
	if candidate.IsIndex != nil {
		entry.IsIndex = *candidate.IsIndex
	}
}

func (s *Service) linkTaskMemoryKnowledge(ctx context.Context, taskID string, entry domain.KnowledgeEntry) {
	memory, err := s.store.TaskMemory(ctx, taskID)
	if err != nil {
		return
	}
	if memory.Extra == nil {
		memory.Extra = map[string]any{}
	}
	refs := []map[string]any{}
	if raw, ok := memory.Extra["knowledge_refs"]; ok {
		encoded, _ := json.Marshal(raw)
		_ = json.Unmarshal(encoded, &refs)
	}
	ref := map[string]any{
		"id": entry.ID, "scope": entry.Scope, "title": entry.Title, "type": entry.Type,
		"revision": entry.Revision, "status": entry.Status, "product_line_id": entry.ProductLineID,
	}
	found := false
	for index := range refs {
		if fmt.Sprint(refs[index]["id"]) == entry.ID {
			refs[index] = ref
			found = true
			break
		}
	}
	if !found {
		refs = append(refs, ref)
	}
	memory.Extra["knowledge_refs"] = refs
	memory.UpdatedAt = s.now().UTC()
	_ = s.store.UpsertTaskMemory(ctx, memory)
}

func (s *Service) applyKnowledgeFeedback(ctx context.Context, taskID string, feedback []KnowledgeFeedback) {
	for _, item := range feedback {
		entryID, kind := strings.TrimSpace(item.EntryID), strings.TrimSpace(item.Kind)
		if entryID == "" || (kind != "helped" && kind != "stale" && kind != "wrong") {
			continue
		}
		entry, err := s.store.FeedbackKnowledge(ctx, entryID, kind, timeString(s.now().UTC()))
		if err == nil {
			s.linkTaskMemoryKnowledge(ctx, taskID, entry)
			s.emit(ctx, taskID, "knowledge", entry.ID, "knowledge_feedback_recorded", map[string]any{"kind": kind, "title": entry.Title})
		}
	}
}

func resolveProductLine(lines []domain.ProductLine, branches ...string) domain.ProductLine {
	var fallback domain.ProductLine
	for _, line := range lines {
		if line.Default {
			fallback = line
		}
		for _, branch := range branches {
			branch = strings.TrimSpace(branch)
			if branch == "" || strings.TrimSpace(line.BranchPattern) == "" {
				continue
			}
			if matched, _ := path.Match(line.BranchPattern, branch); matched || line.BranchPattern == branch {
				return line
			}
		}
	}
	return fallback
}

func (s *Service) startTurn(turn domain.Turn) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	runContext := s.runContext
	if runContext == nil {
		runContext = context.Background()
	}
	runContext, cancel := context.WithCancel(runContext)
	s.cancels[turn.ID] = cancel
	s.runWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.runWG.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("turn %s panicked: %v", turn.ID, recovered)
				recordPanic(s.store.DataDir(), "turn "+turn.ID, recovered)
			}
		}()
		s.runTurn(runContext, turn.ID)
	}()
}

// previousTurnForAgent returns the latest turn of the same agent that precedes
// current in sequence order.
func previousTurnForAgent(current domain.Turn, turns []domain.Turn) domain.Turn {
	var previous domain.Turn
	for _, candidate := range turns {
		if candidate.AgentID != current.AgentID || candidate.Sequence >= current.Sequence {
			continue
		}
		if previous.ID == "" || candidate.Sequence > previous.Sequence {
			previous = candidate
		}
	}
	return previous
}

// stopLeftoverBackendWriter makes sure the process that wrote the given backend
// session is no longer running before AHA appends to the same thread again.
//
// Only Codex needs this: its provider session is a rollout file that two
// processes would interleave. A session belongs to exactly one backend session
// id, so the isolated CODEX_HOME of that session identifies the writer without
// touching the operator's own Codex processes.
func (s *Service) stopLeftoverBackendWriter(ctx context.Context, task domain.Task, workspace domain.Workspace, query workspacepkg.WriterQuery) error {
	runner := workspacepkg.RunnerFor(workspace)
	pids, err := workspacepkg.FindWriterPIDs(ctx, runner, query)
	if err != nil {
		return err
	}
	if len(pids) == 0 {
		return nil
	}
	_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: task.ID, AgentID: "aha", StreamAgentID: "main",
		FromAgentID: "aha", ToAgentID: "main", RouteKind: "recovery",
		Category: "update", Kind: "backend_writer_stopped",
		Summary:   fmt.Sprintf("已终止上一轮遗留的后端进程（%d 个），避免同一会话被并发写入", len(pids)),
		Payload:   map[string]any{"pids": pids, "env_name": query.EnvName, "env_value": query.EnvValue, "resume_session_id": query.ResumeSessionID},
		CreatedAt: s.now().UTC(),
	})
	return workspacepkg.TerminateProcesses(ctx, runner, pids)
}

func recoveryContextNeeds(current domain.Turn, turns []domain.Turn, hasReusableSession bool, handoff string) (bool, bool, bool) {
	previous := previousTurnForAgent(current, turns)
	abnormalPrevious := previous.ID != "" && !previous.Status.EndedCleanly()
	recoveryHandoff := previous.ID != "" && previous.Status == domain.TurnInterrupted && previous.RoundID != "" && previous.RoundID == current.RoundID && previous.InputMessageID == current.InputMessageID
	diagnostics := abnormalPrevious || current.Attempt > 1
	recent := !hasReusableSession || strings.TrimSpace(handoff) != "" || abnormalPrevious
	return recent, recoveryHandoff, diagnostics
}

func includeRecentContextForTurn(agentID string, recovery bool) bool {
	return agentID == "main" || recovery
}

func (s *Service) spawnAgentTurns(
	ctx context.Context,
	task domain.Task,
	parent domain.Turn,
	actions []AgentAction,
	integrationInstructions string,
) (string, int, error) {
	seen := map[string]bool{}
	var routed []string
	var routes []map[string]any
	var members []domain.CollaborationMember
	batchID := domain.NewID("collaboration_batch")
	for index, action := range actions {
		agentID := normalizedSubAgentID(action.AgentID, index+1)
		if seen[agentID] || strings.TrimSpace(action.Assignment) == "" {
			continue
		}
		seen[agentID] = true
		if actualAgentID, ok := s.enqueueAgentAction(ctx, task, parent, action, index+1, batchID); ok {
			routed = append(routed, actualAgentID)
			now := s.now().UTC()
			members = append(members, domain.CollaborationMember{
				BatchID: batchID, AgentID: actualAgentID, Required: true,
				Status: domain.CollaborationMemberQueued, CreatedAt: now, UpdatedAt: now,
			})
			title := strings.TrimSpace(action.Title)
			if title == "" {
				if agent, err := s.store.TaskAgent(ctx, task.ID, actualAgentID); err == nil {
					title = agent.Title
				}
			}
			routes = append(routes, map[string]any{
				"agent_id": actualAgentID, "title": title, "assignment": strings.TrimSpace(action.Assignment),
				"status": string(domain.TurnQueued),
			})
		}
	}
	if len(routed) == 0 {
		return "", 0, nil
	}
	now := s.now().UTC()
	if err := s.store.CreateCollaborationBatch(ctx, domain.CollaborationBatch{
		ID: batchID, TaskID: task.ID, RoundID: parent.RoundID, ParentTurnID: parent.ID,
		IntegrationInstructions: integrationInstructions,
		Status:                  domain.CollaborationBatchPending, CreatedAt: now, UpdatedAt: now,
	}, members); err != nil {
		return "", 0, err
	}
	if len(routed) > 0 {
		// A Turn may submit more than one batch. The card is identified by the
		// parent Turn, so a later batch belongs on the card that is already there:
		// inserting a second row for the same Turn collided on that id, and the
		// discarded error left a card naming fewer agents than were running.
		merged, mergeErr := s.store.MergeAgentBatchDispatch(ctx, parent.ID, batchID, routed, routes)
		if mergeErr != nil {
			s.emit(ctx, task.ID, "agent_batch", parent.ID, "agent_batch_card_failed", map[string]any{
				"parent_turn_id": parent.ID, "error": mergeErr.Error(),
			})
		} else if !merged {
			if _, err := s.store.AddConversationItem(ctx, domain.ConversationItem{
				ID: "conversation-agent-batch-" + parent.ID, TaskID: task.ID, RoundID: parent.RoundID, TurnID: parent.ID,
				AgentID: "aha", StreamAgentID: "main", FromAgentID: "aha", ToAgentID: "main",
				RouteKind: "orchestration", Category: "update", Kind: "agent_batch_dispatched",
				Summary: fmt.Sprintf("AHA 已向 %d 个子 Agent 路由任务", len(routed)),
				Payload: map[string]any{"parent_turn_id": parent.ID, "batch_id": batchID, "batch_ids": []string{batchID}, "agent_ids": routed, "agent_routes": routes}, CreatedAt: now,
			}); err != nil {
				// The sub agents are already queued, so the dispatch stands; the
				// failure is recorded rather than swallowed, because a silently
				// missing card is what let this go unnoticed.
				s.emit(ctx, task.ID, "agent_batch", parent.ID, "agent_batch_card_failed", map[string]any{
					"parent_turn_id": parent.ID, "error": err.Error(),
				})
			}
		}
		s.emit(ctx, task.ID, "agent_batch", parent.ID, "agent_batch_dispatched", map[string]any{
			"round_id": parent.RoundID, "parent_turn_id": parent.ID, "batch_id": batchID, "agent_ids": routed,
		})
	}
	for _, agentID := range routed {
		_, _, _ = s.scheduleAgent(ctx, task.ID, agentID)
	}
	return batchID, len(routed), nil
}

func normalizedSubAgentID(value string, fallback int) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(value, "sub-") {
		valid := true
		for _, character := range value[4:] {
			if character < '0' || character > '9' {
				valid = false
				break
			}
		}
		if valid && len(value) > 4 {
			return value
		}
	}
	return fmt.Sprintf("sub-%03d", fallback)
}

func (s *Service) settleRound(ctx context.Context, taskID, roundID string) {
	if roundID == "" {
		return
	}
	s.settleMu.Lock()
	defer s.settleMu.Unlock()
	round, err := s.store.Round(ctx, roundID)
	if err != nil || round.Status.Terminal() {
		return
	}
	turns, err := s.store.TurnsForRound(ctx, roundID)
	if err != nil || len(turns) == 0 {
		return
	}
	for _, turn := range turns {
		if !turn.Status.Terminal() {
			return
		}
	}
	pending, err := s.store.PendingInboxCount(ctx, taskID)
	if err != nil || pending > 0 {
		return
	}
	openCollaborations, err := s.store.OpenCollaborationBatchCountForRound(ctx, roundID)
	if err != nil || openCollaborations > 0 {
		return
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return
	}
	latest := latestTurnsByAgent(turns)
	mainTurn, hasMain := latest["main"]
	previous := round.Status
	now := s.now().UTC()
	round.FinishedAt = now
	nextTaskStatus := domain.TaskWaitingUser
	switch {
	case hasMain && mainTurn.Status == domain.TurnSucceeded:
		round.Status = domain.RoundCompleted
	case hasMain && mainTurn.Status == domain.TurnInterrupted:
		round.Status = domain.RoundInterrupted
	default:
		round.Status = domain.RoundFailed
		nextTaskStatus = domain.TaskFailed
	}
	if err := s.store.UpdateRound(ctx, round, previous); err != nil {
		return
	}
	if !task.Status.Terminal() && task.Status != nextTaskStatus {
		completedAt := ""
		if nextTaskStatus == domain.TaskFailed {
			completedAt = timeString(now)
		}
		_ = s.store.UpdateTaskStatus(ctx, task.ID, task.Status, nextTaskStatus, timeString(now), completedAt)
	}
	s.emit(ctx, taskID, "round", roundID, "round_"+string(round.Status), map[string]any{
		"round_id": roundID, "round_sequence": round.Sequence,
	})
}

func latestTurnsByAgent(turns []domain.Turn) map[string]domain.Turn {
	result := map[string]domain.Turn{}
	for _, turn := range turns {
		current, ok := result[turn.AgentID]
		if !ok || turn.Generation > current.Generation || turn.Generation == current.Generation && turn.Attempt >= current.Attempt {
			result[turn.AgentID] = turn
		}
	}
	return result
}

func (s *Service) recordExecutionEvent(ctx context.Context, turn domain.Turn, event ExecutionEvent) {
	if event.Type == "agent_progress" && strings.EqualFold(fmt.Sprint(event.Data["phase"]), "streaming") {
		return
	}
	category, kind, summary := "", event.Type, ""
	durableUpdate := false
	switch event.Type {
	case "agent_progress":
		category = "update"
		summary = firstText(event.Data, "message", "phase", "status")
	case "agent_reasoning":
		category = "update"
		kind = "agent_reasoning_update"
		summary = firstText(event.Data, "text")
		durableUpdate = true
	case "agent_message":
		text := firstText(event.Data, "text", "message")
		if final, _ := event.Data["final"].(bool); final {
			break
		}
		category = "update"
		kind = "agent_message_update"
		summary = text
		durableUpdate, _ = event.Data["intermediate"].(bool)
	case "agent_command_started", "agent_command_finished":
		category = "tool"
		summary = firstText(event.Data, "command", "tool_name")
	case "agent_error":
		category = "error"
		summary = firstText(event.Data, "message", "error")
	case "agent_stalled":
		category = "update"
		kind = "agent_stalled"
		summary = fmt.Sprintf("Backend 已连续 %s 未产生新的流式事件，仍在等待响应", time.Duration(usageValue(event.Data["idle_ms"]))*time.Millisecond)
	case "agent_heartbeat":
		category = "update"
		kind = "agent_stalled_heartbeat"
		summary = fmt.Sprintf("Backend 仍未产生新的流式事件，已等待 %s", time.Duration(usageValue(event.Data["idle_ms"]))*time.Millisecond)
	case "agent_resumed":
		category = "update"
		kind = "agent_resumed"
		summary = "Backend 已恢复活动"
	case "agent_idle_timeout":
		category = "error"
		kind = "agent_idle_timeout"
		summary = fmt.Sprintf("Backend 空闲超过 %s，AHA 已中断并准备重试", time.Duration(usageValue(event.Data["idle_ms"]))*time.Millisecond)
	}
	if category != "" {
		if summary == "" {
			summary = event.Type
		}
		item := domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: turn.TaskID, RoundID: turn.RoundID, TurnID: turn.ID,
			AgentID: turn.AgentID, Category: category, Kind: kind, Summary: summary,
			Payload: event.Data, CreatedAt: s.now().UTC(),
		}
		if durableUpdate {
			item.StreamAgentID = turn.AgentID
			item.FromAgentID = turn.AgentID
			item.ToAgentID = "owner"
			item.RouteKind = "agent_progress"
			if stored, storeErr := s.store.AddConversationItem(ctx, item); storeErr == nil {
				event.Data["conversation_item_id"] = stored.ID
			}
		} else if kind == "agent_message_update" {
			if stored, storeErr := s.store.UpsertBackendStreamItem(ctx, item); storeErr == nil {
				event.Data["conversation_item_id"] = stored.ID
			}
		} else {
			if stored, storeErr := s.store.AddConversationItem(ctx, item); storeErr == nil {
				event.Data["conversation_item_id"] = stored.ID
			}
		}
	}
	s.emitTurn(ctx, turn, event.Type, event.Data)
}

func firstText(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(fmt.Sprint(data[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func (s *Service) emitTurn(ctx context.Context, turn domain.Turn, eventType string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["round_id"] = turn.RoundID
	data["turn_id"] = turn.ID
	data["agent_id"] = turn.AgentID
	data["attempt"] = turn.Attempt
	data["generation"] = turn.Generation
	data["required"] = turn.Required
	s.emit(ctx, turn.TaskID, "turn", turn.ID, eventType, data)
}

func (s *Service) emit(ctx context.Context, taskID, aggregateType, aggregateID, eventType string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["entity_type"] = aggregateType
	data["entity_id"] = aggregateID
	event := domain.Event{
		ID: domain.NewID("event"), AggregateType: "task", AggregateID: taskID,
		Type: eventType, Data: data, OccurredAt: s.now().UTC(),
	}
	sequence, err := s.store.AppendEvent(ctx, event)
	if err != nil {
		return
	}
	event.Sequence = sequence
	if eventClass, semanticType, semanticPayload, coalesceKey, ok := channelSemanticEvent(taskID, eventType, data); ok {
		if semanticType == "agent_reply" {
			if attachments, attachmentErr := s.store.TurnOutputAttachments(ctx, taskID, firstText(data, "turn_id")); attachmentErr == nil && len(attachments) > 0 {
				semanticPayload["attachments"] = attachments
			}
		}
		if eventClass == "status" {
			if task, taskErr := s.store.Task(ctx, taskID); taskErr == nil {
				semanticPayload["task_code"] = task.Code
				semanticPayload["task_title"] = task.Title
			}
		}
		sourceKey := event.ID
		conversationItemID := strings.TrimSpace(fmt.Sprint(data["conversation_item_id"]))
		if conversationItemID != "" && semanticType == "agent_message_update" {
			sourceKey = "conversation-stream:" + conversationItemID
		}
		_ = s.store.AppendChannelSourceAndProject(ctx, domain.ChannelSourceEvent{
			ID: domain.NewID("channel_source_event"), SourceKey: sourceKey, TaskID: taskID,
			RoundID: strings.TrimSpace(fmt.Sprint(data["round_id"])), TurnID: strings.TrimSpace(fmt.Sprint(data["turn_id"])),
			ConversationItemID: conversationItemID, EventClass: eventClass, EventType: semanticType,
			SemanticPayload: semanticPayload, OccurredAt: event.OccurredAt,
		}, coalesceKey)
	}
	s.hub.Publish(taskID, event)
}

func channelSemanticEvent(taskID, eventType string, data map[string]any) (string, string, map[string]any, string, bool) {
	agentID := ""
	if rawAgentID, exists := data["agent_id"]; exists && rawAgentID != nil {
		agentID = strings.TrimSpace(fmt.Sprint(rawAgentID))
	}
	if agentID != "" && agentID != "main" {
		return "", "", nil, "", false
	}
	roundID := strings.TrimSpace(fmt.Sprint(data["round_id"]))
	payload := map[string]any{"task_id": taskID, "round_id": roundID, "turn_id": strings.TrimSpace(fmt.Sprint(data["turn_id"]))}
	eventClass, semanticType := "", eventType
	switch eventType {
	case "agent_reply":
		eventClass, semanticType, payload["text"] = "message", "agent_reply", firstText(data, "text")
	case "agent_message":
		if intermediate, _ := data["intermediate"].(bool); !intermediate {
			return "", "", nil, "", false
		}
		eventClass, semanticType, payload["text"] = "update", "agent_message_update", firstText(data, "text", "message")
	case "agent_progress", "agent_stalled", "agent_resumed", "agent_idle_timeout":
		eventClass, semanticType, payload["text"] = "update", eventType, firstText(data, "message", "phase", "status")
	case "agent_error", "turn_failed":
		eventClass, semanticType, payload["error"] = "error", "agent_error", firstText(data, "message", "error")
	case "round_completed":
		eventClass, semanticType, payload["status"] = "status", "waiting_user", "waiting_user"
	case "round_failed", "round_interrupted", "task_completed":
		eventClass, semanticType, payload["status"] = "status", "terminal", eventType
	default:
		return "", "", nil, "", false
	}
	return eventClass, semanticType, payload, store.ChannelCoalesceKey(taskID, roundID, semanticType), true
}

func appendUnique(existing []string, values ...string) []string {
	seen := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		seen[item] = struct{}{}
	}
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		existing = append(existing, item)
		seen[item] = struct{}{}
	}
	return existing
}

func timeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func permissionsJSON(filesystem, approval string) string {
	if filesystem == "" {
		filesystem = "workspace-write"
	}
	if approval == "" {
		approval = "never"
	}
	payload, _ := json.Marshal(map[string]string{"filesystem": filesystem, "approval": approval})
	return string(payload)
}

func parsePermissionsJSON(value string) (filesystem, approval string) {
	filesystem, approval = "workspace-write", "never"
	var payload map[string]string
	if json.Unmarshal([]byte(value), &payload) == nil {
		if filesystem = payload["filesystem"]; filesystem == "" {
			filesystem = "workspace-write"
		}
		if approval = payload["approval"]; approval == "" {
			approval = "never"
		}
	}
	return filesystem, approval
}
