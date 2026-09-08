package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
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
	Filesystem        string
	Approval          string
}

type ExecutionEvent struct {
	Type string
	Data map[string]any
}

type MemoryPatch struct {
	Decisions    []string `json:"decisions"`
	Facts        []string `json:"facts"`
	Excluded     []string `json:"excluded"`
	Progress     []string `json:"progress"`
	Verification []string `json:"verification"`
	NextActions  []string `json:"next_actions"`
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
	Path       string
	BaseCommit string
	Branch     string
}

type WorkspacePreparer interface {
	Prepare(context.Context, domain.Workspace, string, string, string, string, string) (PreparedWorkspace, error)
}

type SecretResolver interface {
	Get(string) (string, bool)
	PutMany(map[string]string) error
}

type Service struct {
	store                 *store.Store
	secrets               SecretResolver
	executor              Executor
	preparer              WorkspacePreparer
	hub                   *EventHub
	now                   func() time.Time
	prompts               *prompt.Engine
	codex                 *codexaccount.Manager
	agentAPI              *agentapi.Capabilities
	agentAPIURL           string
	agentAPIAllowInsecure bool

	mu              sync.Mutex
	cancels         map[string]context.CancelFunc
	settleMu        sync.Mutex
	scheduleMu      sync.Mutex
	mergeMu         sync.Mutex
	mergeDelay      time.Duration
	mergeTimers     map[string]*time.Timer
	sharedContextMu sync.Mutex
	sharedContexts  map[string]struct{}
}

func (s *Service) SetWorkspacePreparer(preparer WorkspacePreparer) {
	s.preparer = preparer
}

func (s *Service) SetCodexAccountManager(manager *codexaccount.Manager) {
	s.codex = manager
}

func (s *Service) SetAgentAPI(capabilities *agentapi.Capabilities, baseURL string, allowInsecure ...bool) {
	s.agentAPI = capabilities
	s.agentAPIURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	s.agentAPIAllowInsecure = len(allowInsecure) > 0 && allowInsecure[0]
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
	KnowledgePolicy   string
	SkillIDs          []string
}

func NewService(database *store.Store, secretStore *secrets.FileStore, executor Executor) *Service {
	return &Service{
		store:          database,
		secrets:        secretStore,
		executor:       executor,
		hub:            NewEventHub(),
		now:            time.Now,
		prompts:        prompt.NewEngine(database),
		cancels:        map[string]context.CancelFunc{},
		mergeDelay:     time.Second,
		mergeTimers:    map[string]*time.Timer{},
		sharedContexts: map[string]struct{}{},
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
	skillIDs, err := s.validateTaskSkillIDs(ctx, project.ID, input.SkillIDs)
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
		KnowledgePolicy:         normalizeKnowledgePolicy(input.KnowledgePolicy),
		SkillIDs:                skillIDs,
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
		if !item.Enabled || item.Status != "active" || (item.Scope != "global" && (item.Scope != "project" || item.ProjectID != projectID)) {
			return nil, fmt.Errorf("skill %s is not enabled for this project", id)
		}
		seen[id] = true
		result = append(result, id)
	}
	return result, nil
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
		if item.Scope == "global" || (item.Scope == "project" && item.ProjectID == task.ProjectID) {
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
	content = strings.TrimSpace(content)
	if content == "" && len(attachmentIDs) == 0 {
		return domain.Turn{}, fmt.Errorf("message or attachment is required")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = "main"
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Turn{}, err
	}
	if err := s.store.ValidateDraftAttachments(ctx, task.ID, attachmentIDs); err != nil {
		return domain.Turn{}, err
	}
	if task.CollaborationMode == "single" && agentID != "main" {
		return domain.Turn{}, fmt.Errorf("task is in single-agent mode")
	}
	agent, err := s.store.TaskAgent(ctx, taskID, agentID)
	if err != nil {
		return domain.Turn{}, err
	}
	snapshot, err := s.store.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		return domain.Turn{}, fmt.Errorf("运行配置不存在，请修改 %s 配置", agentID)
	}
	if _, err := s.store.Model(ctx, snapshot.ModelID); err != nil {
		return domain.Turn{}, fmt.Errorf("模型已删除，请修改 %s 模型配置", agentID)
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
	round, inbox, createdRound, err := s.store.EnqueueOwnerMessage(ctx, message, agentID, attachmentIDs)
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
	var globalKB, projectKB, staleKB []domain.KnowledgeEntry
	var skills []domain.Skill
	var productLine domain.ProductLine
	kbEnabled := knowledgeEnabled(project, task)
	if kbEnabled {
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
	conversation, _ := s.store.ConversationPageForAgent(ctx, task.ID, turn.AgentID, 0, 0, 100, nil)
	allTurns, _ := s.store.ListTurns(ctx, task.ID)
	reusableSession, reusableSessionErr := s.store.ReusableBackendSession(
		ctx, task.ID, turn.AgentID, workspace.ID, snapshot.Backend, model.ID,
		snapshot.EnvGroupRevision, snapshot.CodexAccountID,
	)
	includeRecentContext, includeTurnDiagnostics := recoveryContextNeeds(turn, allTurns, reusableSessionErr == nil, handoff.Summary)
	hardwareGroups, _ := s.store.HardwareGroups(ctx, task.ID)
	attachments := []prompt.AttachmentResource{}
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
			content, readErr := s.store.AttachmentContent(item)
			if readErr != nil {
				s.failTurn(ctx, &turn, task, fmt.Errorf("read attachment %s: %w", item.ID, readErr))
				return
			}
			attachments = append(attachments, prompt.AttachmentResource{Attachment: item, Content: string(content)})
		}
	}
	preview, err := s.prompts.Build(ctx, prompt.BuildInput{
		Project: project, Workspace: workspace, Task: task, Agent: agent, Snapshot: snapshot,
		Memory: memory, GlobalKnowledge: globalKB, ProjectKnowledge: projectKB, StaleKnowledge: staleKB, Skills: skills,
		ProductLine: productLine, KnowledgeEnabled: kbEnabled,
		Conversation: conversation.Items, Turns: allTurns, Hardware: hardwareGroups, Attachments: attachments, UserMessage: userMessage,
		Handoff: handoff.Summary, AgentAPIURL: agentAPIURL, CurrentTurnID: turn.ID, CurrentRoundID: turn.RoundID,
		IncludeRecentContext: includeRecentContext, IncludeTurnDiagnostics: includeTurnDiagnostics,
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
	if err := s.materializeSharedContext(ctx, workspace, workDir, preview.SharedRoot, sharedFiles); err != nil {
		s.failTurn(ctx, &turn, task, fmt.Errorf("materialize prompt context: shared snapshot: %w", err))
		return
	}
	if err := workspacepkg.MaterializeContext(ctx, workspace, workDir, preview.ContextRoot, contextFiles); err != nil {
		s.failTurn(ctx, &turn, task, fmt.Errorf("materialize prompt context: agent resources: %w", err))
		return
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
	capabilityToken := ""
	if s.agentAPI != nil && agentAPIURL != "" {
		capabilityToken, err = s.agentAPI.Issue(task.ID, turn.AgentID, turn.ID, 4*time.Hour)
		if err != nil {
			s.failTurn(ctx, &turn, task, fmt.Errorf("issue Agent API capability: %w", err))
			return
		}
		defer s.agentAPI.Revoke(capabilityToken)
		environment["AHA2_AGENT_API_URL"] = agentAPIURL
		environment["AHA2_AGENT_API_TOKEN"] = capabilityToken
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
	result, executeErr := s.executor.Execute(ctx, ExecutionRequest{
		Turn: turn, Task: task, Project: project, Workspace: workspace, Snapshot: snapshot,
		Model: model, EnvGroup: envGroup, Environment: environment, Prompt: packedPrompt,
		ProviderSessionID: providerSession, Filesystem: filesystem, Approval: approval,
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
			s.interruptTurn(context.Background(), &turn, task)
			return
		}
		if errors.Is(executeErr, backend.ErrCodexIdleTimeout) {
			turn.WaitingReason = "backend_idle_timeout"
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
	if len(latestSessionUsage) > 0 {
		usageJSON, _ := json.Marshal(latestSessionUsage)
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
		category := "chat"
		kind := "agent_message"
		if turn.AgentID != "main" || s.mainReplyIsIntermediate(context.Background(), turn) {
			category = "update"
			kind = "agent_result"
		}
		_, _ = s.store.FinalizeTurnReply(context.Background(), message, domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: task.ID, RoundID: turn.RoundID, TurnID: turn.ID,
			AgentID: turn.AgentID, Category: category, Kind: kind, Summary: turn.Result,
			Payload: map[string]any{"attempt": turn.Attempt, "generation": turn.Generation}, CreatedAt: now,
		})
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
	s.afterTurnTerminal(context.Background(), task, turn)
}

func (s *Service) materializeSharedContext(
	ctx context.Context,
	workspace domain.Workspace,
	workDir string,
	root string,
	files map[string]string,
) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	s.sharedContextMu.Lock()
	defer s.sharedContextMu.Unlock()
	if _, ok := s.sharedContexts[root]; ok {
		return nil
	}
	if err := workspacepkg.MaterializeContext(ctx, workspace, workDir, root, files); err != nil {
		return err
	}
	s.sharedContexts[root] = struct{}{}
	return nil
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
	s.recordTurnDuration(ctx, *turn)
	s.emitTurn(ctx, *turn, "turn_failed", map[string]any{"error": cause.Error()})
	s.afterTurnTerminal(ctx, task, *turn)
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
	s.recordTurnDuration(ctx, *turn)
	s.emitTurn(ctx, *turn, "turn_interrupted", nil)
	s.afterTurnTerminal(ctx, task, *turn)
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
		if strings.TrimSpace(candidate.EntryID) != "" {
			existing, err := s.store.Knowledge(ctx, candidate.EntryID)
			if err != nil {
				return nil, nil, err
			}
			entry.ID, entry.CreatedAt = existing.ID, existing.CreatedAt
			entry.ParentID, entry.Slug = existing.ParentID, existing.Slug
			entry.SortOrder, entry.IsIndex = existing.SortOrder, existing.IsIndex
			applyKnowledgeHierarchy(&entry, candidate)
			if existing.IsIndex {
				entry.ProductLineID = ""
			}
			baseRevision = existing.Revision
			entry.Revision = baseRevision + 1
		}
		proposal := domain.KnowledgeProposal{
			ID: domain.NewID("knowledge_proposal"), EntryID: entry.ID, BaseRevision: baseRevision, Proposed: entry,
			SourceTaskID: taskID, SourceTurnID: turnID, Status: domain.KnowledgeProposalPending,
			ReviewMode: reviewMode, CreatedAt: now, UpdatedAt: now,
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
		if reviewSettings.AutoApprove {
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
	runContext, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[turn.ID] = cancel
	s.mu.Unlock()
	go s.runTurn(runContext, turn.ID)
}

func recoveryContextNeeds(current domain.Turn, turns []domain.Turn, hasReusableSession bool, handoff string) (bool, bool) {
	var previous domain.Turn
	for _, candidate := range turns {
		if candidate.AgentID != current.AgentID || candidate.Sequence >= current.Sequence {
			continue
		}
		if previous.ID == "" || candidate.Sequence > previous.Sequence {
			previous = candidate
		}
	}
	abnormalPrevious := previous.ID != "" && (previous.Status == domain.TurnFailed || previous.Status == domain.TurnInterrupted || previous.Status == domain.TurnBlocked || !previous.StalledAt.IsZero() || previous.Attempt > 1)
	diagnostics := abnormalPrevious || current.Attempt > 1
	recent := !hasReusableSession || strings.TrimSpace(handoff) != "" || abnormalPrevious
	return recent, diagnostics
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
		if final, _ := event.Data["final"].(bool); final {
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
	case "agent_stalled":
		category = "update"
		kind = "agent_stalled"
		summary = fmt.Sprintf("Backend 已连续 %s 没有活动，仍在等待响应", time.Duration(usageValue(event.Data["idle_ms"]))*time.Millisecond)
	case "agent_heartbeat":
		category = "update"
		kind = "agent_stalled_heartbeat"
		summary = fmt.Sprintf("Backend 仍无活动，已等待 %s", time.Duration(usageValue(event.Data["idle_ms"]))*time.Millisecond)
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
		if kind == "agent_message_update" {
			_, _ = s.store.UpsertBackendStreamItem(ctx, item)
		} else {
			_, _ = s.store.AddConversationItem(ctx, item)
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
