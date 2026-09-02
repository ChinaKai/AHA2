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

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/prompt"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
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

type ExecutionResult struct {
	Reply               string
	ExitCode            int
	ProviderSessionID   string
	MemoryPatch         MemoryPatch
	KnowledgeCandidates []KnowledgeCandidate
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
	Prepare(context.Context, domain.Workspace, string, string, string, bool) (PreparedWorkspace, error)
}

type SecretResolver interface {
	Get(string) (string, bool)
}

type Service struct {
	store      *store.Store
	secrets    SecretResolver
	executor   Executor
	preparer   WorkspacePreparer
	hub        *EventHub
	now        func() time.Time
	systemRule string

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func (s *Service) SetWorkspacePreparer(preparer WorkspacePreparer) {
	s.preparer = preparer
}

type CreateTaskInput struct {
	ProjectID       string
	WorkspaceID     string
	Title           string
	Request         string
	TargetBranch    string
	BaseCommit      string
	TaskBranch      string
	WorkspacePath   string
	ModelID         string
	ReasoningEffort string
	Filesystem      string
	Approval        string
}

func NewService(database *store.Store, secretStore *secrets.FileStore, executor Executor) *Service {
	return &Service{
		store:      database,
		secrets:    secretStore,
		executor:   executor,
		hub:        NewEventHub(),
		now:        time.Now,
		systemRule: "You are the AHA task agent. Work only inside the selected workspace. Preserve durable task memory and never expose secrets.",
		cancels:    map[string]context.CancelFunc{},
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
	model, err := s.store.Model(ctx, input.ModelID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("model: %w", err)
	}
	envGroupID := model.DefaultEnvGroupID
	if envGroupID == "" {
		return domain.Task{}, fmt.Errorf("model has no default env group")
	}
	envGroup, err := s.store.EnvGroup(ctx, envGroupID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("env group: %w", err)
	}
	if model.ProviderID != "" && envGroup.ProviderID != "" && model.ProviderID != envGroup.ProviderID {
		return domain.Task{}, fmt.Errorf("env group provider does not match model provider")
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
		ReasoningEffort:  input.ReasoningEffort,
		PermissionsJSON:  permissionsJSON(input.Filesystem, input.Approval),
		CreatedAt:        now,
	}
	if snapshot.ReasoningEffort == "" {
		snapshot.ReasoningEffort = model.DefaultEffort
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
		TaskWorkspacePath:       input.WorkspacePath,
		RuntimeConfigSnapshotID: snapshot.ID,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	created, err := s.store.CreateTaskWithSnapshot(ctx, snapshot, task)
	if err != nil {
		return domain.Task{}, err
	}
	task = created
	if s.preparer != nil {
		isolateGit := workspace.Isolation == "worktree" || (workspace.Isolation == "" && project.ProjectType == "git")
		prepared, prepareErr := s.preparer.Prepare(ctx, workspace, task.ID, task.TargetBranch, task.TaskBranch, isolateGit)
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
	content = strings.TrimSpace(content)
	if content == "" {
		return domain.Turn{}, fmt.Errorf("message is required")
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Turn{}, err
	}
	if task.Status.Terminal() || task.Status == domain.TaskBlocked {
		return domain.Turn{}, fmt.Errorf("task is %s", task.Status)
	}
	if task.Status == domain.TaskWaitingUser {
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
	turn := domain.Turn{
		ID:                      domain.NewID("turn"),
		TaskID:                  task.ID,
		AgentID:                 "main",
		InputMessageID:          message.ID,
		Status:                  domain.TurnQueued,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		QueuedAt:                now,
	}
	turn, err = s.store.CreateMessageAndTurn(ctx, message, turn)
	if err != nil {
		return domain.Turn{}, err
	}
	s.emit(ctx, task.ID, "turn", turn.ID, "turn_queued", map[string]any{"sequence": turn.Sequence})
	runContext, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[turn.ID] = cancel
	s.mu.Unlock()
	go s.runTurn(runContext, turn.ID, content)
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

func (s *Service) runTurn(ctx context.Context, turnID, userMessage string) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, turnID)
		s.mu.Unlock()
	}()
	turn, err := s.store.Turn(ctx, turnID)
	if err != nil {
		return
	}
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
	snapshot, err := s.store.RuntimeSnapshot(ctx, task.RuntimeConfigSnapshotID)
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
	globalKB, _ := s.store.ListKnowledge(ctx, "global", "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
	projectKB, _ := s.store.ListKnowledge(ctx, "project", project.ID, []domain.KnowledgeStatus{domain.KnowledgeVerified})
	packedPrompt := prompt.Build(prompt.PackInput{
		SystemPolicy: s.systemRule, Project: project, Workspace: workspace, Task: task, Memory: memory,
		GlobalKnowledge: globalKB, ProjectKnowledge: projectKB, UserMessage: userMessage,
	})
	if err := s.transitionTurn(ctx, &turn, domain.TurnStarting, "turn_starting"); err != nil {
		return
	}
	var providerSession string
	session, err := s.store.ReusableBackendSession(ctx, task.ID, "main", workspace.ID, snapshot.Backend, model.ID, snapshot.EnvGroupRevision)
	if err == nil {
		providerSession = session.ProviderSession
		turn.BackendSessionID = session.ID
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
	if err := s.transitionTurn(ctx, &turn, domain.TurnRunning, "turn_running"); err != nil {
		return
	}
	filesystem, approval := parsePermissionsJSON(snapshot.PermissionsJSON)
	result, executeErr := s.executor.Execute(ctx, ExecutionRequest{
		Turn: turn, Task: task, Project: project, Workspace: workspace, Snapshot: snapshot,
		Model: model, EnvGroup: envGroup, Environment: environment, Prompt: packedPrompt,
		ProviderSessionID: providerSession, Filesystem: filesystem, Approval: approval,
	}, func(event ExecutionEvent) {
		s.emit(context.Background(), task.ID, "turn", turn.ID, event.Type, event.Data)
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
	sessionID := turn.BackendSessionID
	if sessionID == "" {
		sessionID = domain.NewID("backend_session")
	}
	backendSession := domain.BackendSession{
		ID: sessionID, TaskID: task.ID, AgentID: "main", WorkspaceID: workspace.ID,
		Backend: snapshot.Backend, ModelID: model.ID, EnvGroupRevision: snapshot.EnvGroupRevision,
		ProviderSession: result.ProviderSessionID, Status: "active", CreatedAt: now, LastUsedAt: now,
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
	if turn.Result != "" {
		_ = s.store.AddMessage(context.Background(), domain.Message{
			ID: domain.NewID("message"), TaskID: task.ID, TurnID: turn.ID, Role: "assistant",
			Sender: "main", Content: turn.Result, CreatedAt: now,
		})
	}
	if turn.Status == domain.TurnSucceeded {
		s.applyMemoryPatch(context.Background(), task, memory, result.MemoryPatch)
		s.createKnowledgeCandidates(context.Background(), project.ID, task.ID, turn.ID, task.TaskBranch, result.KnowledgeCandidates)
		_ = s.store.UpdateTaskStatus(context.Background(), task.ID, task.Status, domain.TaskWaitingUser, timeString(now), "")
		s.emit(context.Background(), task.ID, "turn", turn.ID, "turn_succeeded", map[string]any{"exit_code": result.ExitCode})
	} else {
		_ = s.store.UpdateTaskStatus(context.Background(), task.ID, task.Status, domain.TaskFailed, timeString(now), timeString(now))
		s.emit(context.Background(), task.ID, "turn", turn.ID, "turn_failed", map[string]any{"exit_code": result.ExitCode, "error": turn.Error})
	}
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
	s.emit(ctx, turn.TaskID, "turn", turn.ID, eventType, map[string]any{"status": status})
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
	if !task.Status.Terminal() {
		_ = s.store.UpdateTaskStatus(ctx, task.ID, task.Status, domain.TaskFailed, timeString(now), timeString(now))
	}
	s.emit(ctx, task.ID, "turn", turn.ID, "turn_failed", map[string]any{"error": cause.Error()})
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
	_ = s.store.UpdateTaskStatus(ctx, task.ID, task.Status, domain.TaskWaitingUser, timeString(now), "")
	s.emit(ctx, task.ID, "turn", turn.ID, "turn_interrupted", nil)
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
