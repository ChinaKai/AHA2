package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
	Filesystem        string
	Approval          string
}

type ExecutionEvent struct {
	Type string
	Data map[string]any
}

type MemoryPatch struct {
	Decisions    []string
	Facts        []string
	Excluded     []string
	Progress     []string
	Verification []string
	NextActions  []string
}

type KnowledgeCandidate struct {
	Scope      string
	Type       string
	Title      string
	Body       string
	Confidence float64
}

type AgentAction struct {
	AgentID         string
	Title           string
	Assignment      string
	Required        bool
	Backend         string
	ModelID         string
	ReasoningEffort string
	Filesystem      string
	Approval        string
}

type ExecutionResult struct {
	Reply               string
	ExitCode            int
	ProviderSessionID   string
	MainFollowup        string
	MemoryPatch         MemoryPatch
	KnowledgeCandidates []KnowledgeCandidate
	AgentActions        []AgentAction
}

type Executor interface {
	Execute(context.Context, ExecutionRequest, func(ExecutionEvent)) (ExecutionResult, error)
}

type PreparedWorkspace struct {
	Path       string
	BaseCommit string
	Branch     string
}

type WorkspacePreparer interface {
	Prepare(context.Context, domain.Workspace, string, string, string, string, string) (PreparedWorkspace, error)
}

type SecretResolver interface {
	Get(string) (string, bool)
}

type Service struct {
	store    *store.Store
	secrets  SecretResolver
	executor Executor
	preparer WorkspacePreparer
	hub      *EventHub
	now      func() time.Time
	prompts  *prompt.Engine
	codex    *codexaccount.Manager

	mu          sync.Mutex
	cancels     map[string]context.CancelFunc
	settleMu    sync.Mutex
	scheduleMu  sync.Mutex
	mergeMu     sync.Mutex
	mergeDelay  time.Duration
	mergeTimers map[string]*time.Timer
}

func (s *Service) SetWorkspacePreparer(preparer WorkspacePreparer) {
	s.preparer = preparer
}

func (s *Service) SetCodexAccountManager(manager *codexaccount.Manager) {
	s.codex = manager
}

type CreateTaskInput struct {
	ProjectID         string
	WorkspaceID       string
	Title             string
	Request           string
	TargetBranch      string
	BaseCommit        string
	TaskBranch        string
	Isolation         string
	WorktreeDir       string
	Backend           string
	ModelSource       string
	ModelID           string
	WireModel         string
	CodexAccountID    string
	ReasoningEffort   string
	Filesystem        string
	Approval          string
	ProxyEnabled      bool
	CollaborationMode string
	MaxAgents         int
}

func NewService(database *store.Store, secretStore *secrets.FileStore, executor Executor) *Service {
	return &Service{
		store:       database,
		secrets:     secretStore,
		executor:    executor,
		hub:         NewEventHub(),
		now:         time.Now,
		prompts:     prompt.NewEngine(database),
		cancels:     map[string]context.CancelFunc{},
		mergeDelay:  time.Second,
		mergeTimers: map[string]*time.Timer{},
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
	isolation := strings.TrimSpace(input.Isolation)
	worktreeDir := strings.TrimSpace(input.WorktreeDir)
	if project.ProjectType != "git" {
		isolation = "inplace"
	}
	if isolation == "" {
		isolation = "worktree"
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
		Backend: input.Backend, ModelSource: input.ModelSource, ModelID: input.ModelID,
		WireModel: input.WireModel, CodexAccountID: input.CodexAccountID,
	})
	if err != nil {
		return domain.Task{}, err
	}
	now := s.now().UTC()
	snapshot := domain.RuntimeConfigSnapshot{
		ID:               domain.NewID("runtime"),
		WorkspaceID:      workspace.ID,
		Backend:          model.Backend,
		ModelID:          model.ID,
		WireModel:        model.WireModel,
		EnvGroupID:       envGroup.ID,
		EnvGroupRevision: envGroup.Revision,
		CodexAccountID:   accountID,
		ProxyEnabled:     input.ProxyEnabled,
		ReasoningEffort:  input.ReasoningEffort,
		PermissionsJSON:  permissionsJSON(input.Filesystem, input.Approval),
		CreatedAt:        now,
	}
	if snapshot.ReasoningEffort == "" {
		snapshot.ReasoningEffort = model.DefaultEffort
	}
	collaborationMode, err := validateCollaborationMode(input.CollaborationMode)
	if err != nil {
		return domain.Task{}, err
	}
	task := domain.Task{
		ID:                      domain.NewID("task"),
		ProjectID:               project.ID,
		WorkspaceID:             workspace.ID,
		Title:                   input.Title,
		OriginalRequest:         input.Request,
		CurrentGoal:             input.Request,
		Status:                  domain.TaskPreparing,
		TargetBranch:            input.TargetBranch,
		BaseCommit:              input.BaseCommit,
		TaskBranch:              input.TaskBranch,
		Isolation:               isolation,
		WorktreeDir:             worktreeDir,
		RuntimeConfigSnapshotID: snapshot.ID,
		CollaborationMode:       collaborationMode,
		MaxAgents:               input.MaxAgents,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if task.MaxAgents < 1 {
		task.MaxAgents = 3
	}
	created, err := s.store.CreateTaskWithSnapshot(ctx, snapshot, task)
	if err != nil {
		return domain.Task{}, err
	}
	task = created
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
		if err := s.store.UpdateTaskWorkspace(ctx, task.ID, task.TaskWorkspacePath, task.BaseCommit, task.TaskBranch, timeString(now)); err != nil {
			return task, err
		}
	}
	if err := s.store.UpdateTaskStatus(ctx, task.ID, domain.TaskPreparing, domain.TaskActive, timeString(now), ""); err != nil {
		return domain.Task{}, err
	}
	task.Status = domain.TaskActive
	s.emit(ctx, task.ID, "task", task.ID, "task_created", map[string]any{"title": task.Title, "workspace_id": task.WorkspaceID})
	return task, nil
}

func (s *Service) SubmitMessage(ctx context.Context, taskID, content string) (domain.Turn, error) {
	return s.SubmitAgentMessage(ctx, taskID, "main", content)
}

func (s *Service) SubmitAgentMessage(ctx context.Context, taskID, agentID, content string) (domain.Turn, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return domain.Turn{}, fmt.Errorf("message is required")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = "main"
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Turn{}, err
	}
	if task.CollaborationMode == "single" && agentID != "main" {
		return domain.Turn{}, fmt.Errorf("task is in single-agent mode")
	}
	if task.Status == domain.TaskCompleted || task.Status == domain.TaskCancelled || task.Status == domain.TaskBlocked {
		return domain.Turn{}, fmt.Errorf("task is %s", task.Status)
	}
	if task.Status == domain.TaskWaitingUser || task.Status == domain.TaskFailed {
		now := s.now().UTC()
		if err := s.store.UpdateTaskStatus(ctx, task.ID, task.Status, domain.TaskActive, timeString(now), ""); err != nil {
			return domain.Turn{}, err
		}
		task.Status = domain.TaskActive
	}
	now := s.now().UTC()
	message := domain.Message{
		ID:        domain.NewID("message"),
		TaskID:    task.ID,
		Role:      "user",
		Sender:    "owner",
		Content:   content,
		CreatedAt: now,
	}
	if agentID == "main" {
		s.cancelMainResultMerge(task.ID)
	}
	round, inbox, createdRound, err := s.store.EnqueueOwnerMessage(ctx, message, agentID)
	if err != nil {
		return domain.Turn{}, err
	}
	if createdRound {
		s.emit(ctx, task.ID, "round", round.ID, "round_started", map[string]any{
			"round_id": round.ID, "round_sequence": round.Sequence,
		})
	}
	s.emit(ctx, task.ID, "inbox", inbox.ID, "agent_message_queued", map[string]any{
		"round_id": round.ID, "agent_id": agentID, "source": "owner", "inbox_sequence": inbox.Sequence,
	})
	turn, started, err := s.scheduleAgent(ctx, task.ID, agentID)
	if err != nil {
		return domain.Turn{}, err
	}
	if !started {
		return domain.Turn{}, nil
	}
	return turn, nil
}

func (s *Service) InterruptTurn(ctx context.Context, turnID string) error {
	turn, err := s.store.Turn(ctx, turnID)
	if err != nil {
		return err
	}
	if turn.Status.Terminal() {
		return nil
	}
	s.mu.Lock()
	cancel := s.cancels[turnID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
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
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status == domain.TaskCompleted {
		return nil
	}
	if _, activeErr := s.store.ActiveTurn(ctx, taskID); activeErr == nil {
		return store.ErrActiveTurn
	} else if !errors.Is(activeErr, sql.ErrNoRows) {
		return activeErr
	}
	now := s.now().UTC()
	if err := s.store.UpdateTaskStatus(ctx, task.ID, task.Status, domain.TaskCompleted, timeString(now), timeString(now)); err != nil {
		return err
	}
	s.emit(ctx, task.ID, "task", task.ID, "task_completed", nil)
	return nil
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

func (s *Service) runTurn(ctx context.Context, turnID string) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, turnID)
		s.mu.Unlock()
	}()
	turn, err := s.store.Turn(ctx, turnID)
	if err != nil {
		return
	}
	userMessage := turn.Instruction
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
	snapshot, err := s.store.RuntimeSnapshot(ctx, turn.RuntimeConfigSnapshotID)
	if err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	model, err := s.store.Model(ctx, snapshot.ModelID)
	if err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	envGroup, err := s.store.EnvGroup(ctx, snapshot.EnvGroupID)
	if err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	memory, err := s.store.TaskMemory(ctx, task.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	handoff, _ := s.store.PendingAgentSessionHandoff(ctx, task.ID, turn.AgentID)
	globalKB, _ := s.store.ListKnowledge(ctx, "global", "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
	projectKB, _ := s.store.ListKnowledge(ctx, "project", project.ID, []domain.KnowledgeStatus{domain.KnowledgeVerified})
	agent, err := s.store.TaskAgent(ctx, task.ID, turn.AgentID)
	if err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	conversation, _ := s.store.ConversationPageForAgent(ctx, task.ID, turn.AgentID, 0, 0, 100, nil)
	allTurns, _ := s.store.ListTurns(ctx, task.ID)
	hardwareGroups, _ := s.store.HardwareGroups(ctx, task.ID)
	preview, err := s.prompts.Build(ctx, prompt.BuildInput{
		Project: project, Workspace: workspace, Task: task, Agent: agent, Snapshot: snapshot,
		Memory: memory, GlobalKnowledge: globalKB, ProjectKnowledge: projectKB,
		Conversation: conversation.Items, Turns: allTurns, Hardware: hardwareGroups, UserMessage: userMessage,
		Handoff: handoff.Summary,
	})
	if err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	contextFiles := make(map[string]string, len(preview.ContextManifest))
	for _, item := range preview.ContextManifest {
		contextFiles[item.Path] = item.Content
	}
	workDir := task.TaskWorkspacePath
	if workDir == "" {
		workDir = workspace.RootPath
	}
	if err := workspacepkg.MaterializeContext(ctx, workspace, workDir, preview.ContextRoot, contextFiles); err != nil {
		s.failTurn(ctx, &turn, task, fmt.Errorf("materialize prompt context: %w", err))
		return
	}
	packedPrompt := preview.EffectivePrompt
	turn.ContextWindow = model.ContextWindow
	turn.PromptChars = len([]rune(packedPrompt))
	turn.PromptSnapshot = packedPrompt
	if err := s.store.UpdateTurn(ctx, turn, turn.Status); err != nil {
		s.failTurn(ctx, &turn, task, err)
		return
	}
	if err := s.transitionTurn(ctx, &turn, domain.TurnStarting, "turn_starting"); err != nil {
		return
	}
	var providerSession string
	session, err := s.store.ReusableBackendSession(
		ctx, task.ID, turn.AgentID, workspace.ID, snapshot.Backend, model.ID,
		snapshot.EnvGroupRevision, snapshot.CodexAccountID,
	)
	if err == nil {
		providerSession = session.ProviderSession
		turn.BackendSessionID = session.ID
		if len(turn.Usage) == 0 && strings.TrimSpace(session.ContextUsageJSON) != "" {
			_ = json.Unmarshal([]byte(session.ContextUsageJSON), &turn.Usage)
			_ = s.store.UpdateTurn(ctx, turn, turn.Status)
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
	if snapshot.ProxyEnabled {
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
	if snapshot.CodexAccountID != "" {
		if snapshot.Backend != "codex" || s.codex == nil {
			s.failTurn(ctx, &turn, task, fmt.Errorf("Codex 官方账号运行时不可用"))
			return
		}
		unlock := s.codex.LockAccount(snapshot.CodexAccountID)
		defer unlock()
		profileDir, profileErr := s.codex.PrepareProfile(
			ctx, snapshot.CodexAccountID, workspace, workDir, sessionID,
		)
		if profileErr != nil {
			s.failTurn(ctx, &turn, task, profileErr)
			return
		}
		environment["CODEX_HOME"] = profileDir
		defer s.codex.SyncProfile(context.Background(), snapshot.CodexAccountID, workspace, profileDir)
	}
	if err := s.transitionTurn(ctx, &turn, domain.TurnRunning, "turn_running"); err != nil {
		return
	}
	filesystem, approval := parsePermissionsJSON(snapshot.PermissionsJSON)
	result, executeErr := s.executor.Execute(ctx, ExecutionRequest{
		Turn: turn, Task: task, Project: project, Workspace: workspace, Snapshot: snapshot,
		Model: model, EnvGroup: envGroup, Environment: environment, Prompt: packedPrompt,
		ProviderSessionID: providerSession, Filesystem: filesystem, Approval: approval,
	}, func(event ExecutionEvent) {
		if event.Type == "agent_usage" {
			if usage, ok := event.Data["usage"].(map[string]any); ok {
				turn.Usage = usage
				_ = s.store.UpdateTurn(context.Background(), turn, turn.Status)
			}
		}
		s.recordExecutionEvent(context.Background(), turn, event)
	})
	if executeErr != nil {
		if errors.Is(executeErr, context.Canceled) {
			s.interruptTurn(context.Background(), &turn, task)
			return
		}
		s.failTurn(context.Background(), &turn, task, executeErr)
		return
	}
	now := s.now().UTC()
	backendSession := domain.BackendSession{
		ID: sessionID, TaskID: task.ID, AgentID: turn.AgentID, WorkspaceID: workspace.ID,
		Backend: snapshot.Backend, ModelID: model.ID, EnvGroupRevision: snapshot.EnvGroupRevision,
		CodexAccountID:  snapshot.CodexAccountID,
		ProviderSession: result.ProviderSessionID, Status: "active", CreatedAt: now, LastUsedAt: now,
	}
	if len(turn.Usage) > 0 {
		usageJSON, _ := json.Marshal(turn.Usage)
		backendSession.ContextUsageJSON = string(usageJSON)
	}
	if !session.CreatedAt.IsZero() {
		backendSession.CreatedAt = session.CreatedAt
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
		_ = s.store.AddMessage(context.Background(), message)
		category := "chat"
		kind := "agent_message"
		if turn.AgentID != "main" || s.agentActionsMayRun(context.Background(), task, result.AgentActions) || s.mainReplyIsIntermediate(context.Background(), turn) {
			category = "update"
			kind = "agent_result"
		}
		_, _ = s.store.AddConversationItem(context.Background(), domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: turn.RoundID, TurnID: turn.ID,
			AgentID: turn.AgentID, Category: category, Kind: kind, Summary: turn.Result,
			Payload: map[string]any{"attempt": turn.Attempt, "generation": turn.Generation}, CreatedAt: now,
		})
	}
	if turn.Status == domain.TurnSucceeded && turn.AgentID == "main" {
		s.applyMemoryPatch(context.Background(), task, memory, result.MemoryPatch)
		s.createKnowledgeCandidates(context.Background(), project.ID, task.ID, turn.ID, task.TaskBranch, result.KnowledgeCandidates)
	}
	if turn.Status == domain.TurnSucceeded && handoff.ID != "" {
		_ = s.store.ConsumeAgentSessionHandoff(context.Background(), handoff.ID, now)
	}
	if turn.Status == domain.TurnSucceeded {
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
		s.emitTurn(context.Background(), turn, "turn_failed", map[string]any{
			"exit_code": result.ExitCode, "error": turn.Error,
		})
	}
	s.afterTurnTerminal(context.Background(), task, turn, result.AgentActions, result.MainFollowup)
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

func (s *Service) failTurn(ctx context.Context, turn *domain.Turn, task domain.Task, cause error) {
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
	s.emitTurn(ctx, *turn, "turn_failed", map[string]any{"error": cause.Error()})
	s.afterTurnTerminal(ctx, task, *turn, nil, "")
}

func (s *Service) interruptTurn(ctx context.Context, turn *domain.Turn, task domain.Task) {
	now := s.now().UTC()
	previous := turn.Status
	turn.Status, turn.FinishedAt = domain.TurnInterrupted, now
	code := 130
	turn.ExitCode = &code
	if err := s.store.UpdateTurn(ctx, *turn, previous); err != nil {
		return
	}
	s.updateOrchestrationRouteStatus(ctx, *turn)
	_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: turn.RoundID, TurnID: turn.ID,
		AgentID: turn.AgentID, Category: "update", Kind: "turn_interrupted",
		Summary: turn.AgentID + " 已中断", CreatedAt: now,
	})
	s.emitTurn(ctx, *turn, "turn_interrupted", nil)
	s.afterTurnTerminal(ctx, task, *turn, nil, "")
}

func (s *Service) applyMemoryPatch(ctx context.Context, task domain.Task, memory domain.TaskMemory, patch MemoryPatch) {
	memory.TaskID = task.ID
	memory.CurrentGoal = task.CurrentGoal
	memory.Decisions = appendUnique(memory.Decisions, patch.Decisions...)
	memory.Facts = appendUnique(memory.Facts, patch.Facts...)
	memory.Excluded = appendUnique(memory.Excluded, patch.Excluded...)
	memory.Progress = appendUnique(memory.Progress, patch.Progress...)
	memory.Verification = appendUnique(memory.Verification, patch.Verification...)
	memory.NextActions = appendUnique(memory.NextActions, patch.NextActions...)
	memory.UpdatedAt = s.now().UTC()
	_ = s.store.UpsertTaskMemory(ctx, memory)
}

func (s *Service) createKnowledgeCandidates(ctx context.Context, projectID, taskID, turnID, branch string, candidates []KnowledgeCandidate) {
	now := s.now().UTC()
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Body) == "" {
			continue
		}
		scope := candidate.Scope
		if scope != "global" {
			scope = "project"
		}
		entry := domain.KnowledgeEntry{
			ID: domain.NewID("knowledge"), Scope: scope, Type: candidate.Type, Title: candidate.Title,
			Body: candidate.Body, Status: domain.KnowledgeCandidate, Confidence: candidate.Confidence,
			SourceTaskID: taskID, SourceTurnID: turnID, BranchScope: branch, CreatedAt: now, UpdatedAt: now,
		}
		if scope == "project" {
			entry.ProjectID = projectID
		}
		_ = s.store.CreateKnowledge(ctx, entry)
		s.emit(ctx, taskID, "knowledge", entry.ID, "knowledge_candidate_created", map[string]any{"title": entry.Title, "scope": entry.Scope})
	}
}

func (s *Service) startTurn(turn domain.Turn) {
	runContext, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[turn.ID] = cancel
	s.mu.Unlock()
	go s.runTurn(runContext, turn.ID)
}

func (s *Service) spawnAgentTurns(ctx context.Context, task domain.Task, parent domain.Turn, actions []AgentAction) int {
	seen := map[string]bool{}
	var routed []string
	var routes []map[string]any
	for index, action := range actions {
		agentID := normalizedSubAgentID(action.AgentID, index+1)
		if seen[agentID] || strings.TrimSpace(action.Assignment) == "" {
			continue
		}
		seen[agentID] = true
		if actualAgentID, ok := s.enqueueAgentAction(ctx, task, parent, action, index+1); ok {
			routed = append(routed, actualAgentID)
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
	if len(routed) > 0 {
		now := s.now().UTC()
		_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
			ID: "conversation-agent-batch-" + parent.ID, TaskID: task.ID, RoundID: parent.RoundID, TurnID: parent.ID,
			AgentID: "aha", StreamAgentID: "main", FromAgentID: "aha", ToAgentID: "main",
			RouteKind: "orchestration", Category: "update", Kind: "agent_batch_dispatched",
			Summary: fmt.Sprintf("AHA 已向 %d 个子 Agent 路由任务", len(routed)),
			Payload: map[string]any{"parent_turn_id": parent.ID, "agent_ids": routed, "agent_routes": routes}, CreatedAt: now,
		})
		s.emit(ctx, task.ID, "agent_batch", parent.ID, "agent_batch_dispatched", map[string]any{
			"round_id": parent.RoundID, "parent_turn_id": parent.ID, "agent_ids": routed,
		})
	}
	for _, agentID := range routed {
		_, _, _ = s.scheduleAgent(ctx, task.ID, agentID)
	}
	return len(routed)
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
	switch event.Type {
	case "agent_progress":
		category = "update"
		summary = firstText(event.Data, "message", "phase", "status")
	case "agent_message":
		text := firstText(event.Data, "text", "message")
		if final, _ := event.Data["final"].(bool); final || strings.Contains(text, "<aha2_checkpoint>") {
			break
		}
		category = "update"
		kind = "agent_message_update"
		summary = text
	case "agent_command_started", "agent_command_finished":
		category = "tool"
		summary = firstText(event.Data, "command", "tool_name")
	case "agent_error":
		category = "error"
		summary = firstText(event.Data, "message", "error")
	}
	if category != "" {
		if summary == "" {
			summary = event.Type
		}
		_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: turn.TaskID, RoundID: turn.RoundID, TurnID: turn.ID,
			AgentID: turn.AgentID, Category: category, Kind: kind, Summary: summary,
			Payload: event.Data, CreatedAt: s.now().UTC(),
		})
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
	s.hub.Publish(taskID, event)
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
