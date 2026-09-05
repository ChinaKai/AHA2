package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
)

var (
	ErrAgentCallForbidden = errors.New("agent control operation is forbidden")
	ErrAgentTurnInactive  = errors.New("agent turn is not active")
	ErrRevisionConflict   = errors.New("resource revision conflict")
)

type AgentCallContext struct {
	Task    domain.Task
	Turn    domain.Turn
	Project domain.Project
}

type AgentTaskCreateInput struct {
	WorkspaceID   string `json:"workspace_id"`
	Title         string `json:"title"`
	Request       string `json:"request"`
	CloneHardware bool   `json:"clone_hardware"`
}

func (s *Service) AgentCallContext(ctx context.Context, claims agentapi.Claims, mainOnly bool) (AgentCallContext, error) {
	turn, err := s.store.Turn(ctx, claims.TurnID)
	if err != nil || turn.TaskID != claims.TaskID || turn.AgentID != claims.AgentID {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	if turn.Status != domain.TurnRunning && turn.Status != domain.TurnStarting {
		return AgentCallContext{}, ErrAgentTurnInactive
	}
	if mainOnly && turn.AgentID != "main" {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	task, err := s.store.Task(ctx, turn.TaskID)
	if err != nil {
		return AgentCallContext{}, err
	}
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		return AgentCallContext{}, err
	}
	return AgentCallContext{Task: task, Turn: turn, Project: project}, nil
}

func (s *Service) UpdateAgentMemory(ctx context.Context, claims agentapi.Claims, patch MemoryPatch) (domain.TaskMemory, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return domain.TaskMemory{}, err
	}
	memory, err := s.store.TaskMemory(ctx, call.Task.ID)
	if err != nil {
		return domain.TaskMemory{}, err
	}
	s.applyMemoryPatch(ctx, call.Task, memory, patch)
	return s.store.TaskMemory(ctx, call.Task.ID)
}

func (s *Service) AddAgentProgress(ctx context.Context, claims agentapi.Claims, message string) error {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("progress message is required")
	}
	if len([]rune(message)) > 4000 {
		return fmt.Errorf("progress message exceeds 4000 characters")
	}
	_, err = s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: call.Task.ID, RoundID: call.Turn.RoundID, TurnID: call.Turn.ID,
		AgentID: call.Turn.AgentID, StreamAgentID: call.Turn.AgentID, FromAgentID: call.Turn.AgentID, ToAgentID: "owner",
		RouteKind: "agent_progress", Category: "update", Kind: "agent_message_update", Summary: message,
		Payload: map[string]any{"api": true}, CreatedAt: s.now().UTC(),
	})
	if err == nil {
		s.emitTurn(ctx, call.Turn, "agent_progress", map[string]any{"message": message})
	}
	return err
}

func (s *Service) SubmitAgentKnowledge(ctx context.Context, claims agentapi.Claims, candidates []KnowledgeCandidate) ([]domain.KnowledgeEntry, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return nil, err
	}
	if !knowledgeEnabled(call.Project, call.Task) {
		return nil, ErrAgentCallForbidden
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Body) == "" || len([]rune(candidate.Title)) > 300 || len([]rune(candidate.Body)) > 20000 {
			return nil, fmt.Errorf("knowledge title or body is invalid")
		}
		if candidate.Confidence < 0 || candidate.Confidence > 1 {
			return nil, fmt.Errorf("knowledge confidence must be between 0 and 1")
		}
		if strings.TrimSpace(candidate.EntryID) == "" {
			continue
		}
		existing, err := s.store.Knowledge(ctx, candidate.EntryID)
		if err != nil || existing.Scope == "project" && existing.ProjectID != call.Project.ID {
			return nil, ErrAgentCallForbidden
		}
		if candidate.BaseRevision <= 0 || candidate.BaseRevision != existing.Revision {
			return nil, ErrRevisionConflict
		}
		requestedScope := candidate.Scope
		if requestedScope != "global" {
			requestedScope = "project"
		}
		if requestedScope != existing.Scope {
			return nil, ErrAgentCallForbidden
		}
	}
	lines, _ := s.store.ListProductLines(ctx, call.Project.ID)
	line := resolveProductLine(lines, call.Task.TargetBranch, call.Project.DefaultBranch)
	return s.createKnowledgeCandidates(ctx, call.Project.ID, call.Task.ID, call.Turn.ID, call.Task.TaskBranch, line.ID, candidates), nil
}

func (s *Service) SubmitAgentKnowledgeFeedback(ctx context.Context, claims agentapi.Claims, feedback KnowledgeFeedback) (domain.KnowledgeEntry, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return domain.KnowledgeEntry{}, err
	}
	entry, err := s.store.Knowledge(ctx, strings.TrimSpace(feedback.EntryID))
	if err != nil || entry.Scope == "project" && entry.ProjectID != call.Project.ID {
		return domain.KnowledgeEntry{}, ErrAgentCallForbidden
	}
	kind := strings.TrimSpace(feedback.Kind)
	if kind != "helped" && kind != "stale" && kind != "wrong" {
		return domain.KnowledgeEntry{}, fmt.Errorf("invalid knowledge feedback")
	}
	entry, err = s.store.FeedbackKnowledge(ctx, entry.ID, kind, timeString(s.now().UTC()))
	if err == nil {
		s.linkTaskMemoryKnowledge(ctx, call.Task.ID, entry)
		s.emit(ctx, call.Task.ID, "knowledge", entry.ID, "knowledge_feedback_recorded", map[string]any{"kind": kind, "title": entry.Title})
	}
	return entry, err
}

func (s *Service) SubmitAgentCollaboration(ctx context.Context, claims agentapi.Claims, actions []AgentAction, mainFollowup string) (int, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return 0, err
	}
	created := s.spawnAgentTurns(ctx, call.Task, call.Turn, actions)
	if created > 0 && strings.TrimSpace(mainFollowup) != "" {
		now := s.now().UTC()
		_, _ = s.store.EnqueueAgentRoute(ctx, domain.AgentInboxItem{
			ID: domain.NewID("inbox"), TaskID: call.Task.ID, RoundID: call.Turn.RoundID,
			TargetAgentID: "main", SourceAgentID: "aha", SourceKind: "main_followup",
			SourceTurnID: call.Turn.ID, Content: strings.TrimSpace(mainFollowup),
			Payload: map[string]any{"parent_turn_id": call.Turn.ID}, Status: "pending", CreatedAt: now,
		})
	}
	return created, nil
}

func (s *Service) SelectedAgentSkills(ctx context.Context, claims agentapi.Claims) ([]domain.Skill, AgentCallContext, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return nil, AgentCallContext{}, err
	}
	return s.activeTaskSkills(ctx, call.Task), call, nil
}

func (s *Service) ApplicableAgentKnowledge(ctx context.Context, claims agentapi.Claims) ([]domain.KnowledgeEntry, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return nil, err
	}
	lines, _ := s.store.ListProductLines(ctx, call.Project.ID)
	line := resolveProductLine(lines, call.Task.TargetBranch, call.Project.DefaultBranch)
	project, _ := s.store.ListApplicableKnowledge(ctx, call.Project.ID, line.ID, []domain.KnowledgeStatus{domain.KnowledgeVerified})
	global, _ := s.store.ListKnowledge(ctx, "global", "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
	return append(project, global...), nil
}

func (s *Service) AgentProjectWorkspaces(ctx context.Context, claims agentapi.Claims) ([]domain.Workspace, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return nil, err
	}
	if !call.Task.AgentCapabilities["workspace_read"] {
		return nil, ErrAgentCallForbidden
	}
	return s.store.ListWorkspaces(ctx, call.Project.ID)
}

func (s *Service) CreateAgentTask(ctx context.Context, claims agentapi.Claims, input AgentTaskCreateInput) (domain.Task, domain.Turn, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return domain.Task{}, domain.Turn{}, err
	}
	if !call.Task.AgentCapabilities["task_create"] {
		return domain.Task{}, domain.Turn{}, ErrAgentCallForbidden
	}
	if input.CloneHardware && !call.Task.AgentCapabilities["clone_hardware"] {
		return domain.Task{}, domain.Turn{}, ErrAgentCallForbidden
	}
	workspace, err := s.store.Workspace(ctx, strings.TrimSpace(input.WorkspaceID))
	if err != nil || workspace.ProjectID != call.Project.ID {
		return domain.Task{}, domain.Turn{}, ErrAgentCallForbidden
	}
	snapshot, err := s.store.RuntimeSnapshot(ctx, call.Turn.RuntimeConfigSnapshotID)
	if err != nil {
		return domain.Task{}, domain.Turn{}, err
	}
	modelSource := domain.ModelSourceProvider
	if snapshot.CodexAccountID != "" {
		modelSource = domain.ModelSourceOfficial
	}
	filesystem, approval := parsePermissionsJSON(snapshot.PermissionsJSON)
	item, err := s.CreateTask(ctx, CreateTaskInput{
		ProjectID: call.Project.ID, WorkspaceID: workspace.ID, Title: input.Title, Request: input.Request,
		Isolation: "inplace", Backend: snapshot.Backend, ModelSource: modelSource,
		ModelID: snapshot.ModelID, WireModel: snapshot.WireModel,
		CodexAccountID: snapshot.CodexAccountID, ReasoningEffort: snapshot.ReasoningEffort,
		Filesystem: filesystem, Approval: approval, ProxyEnabled: snapshot.ProxyEnabled,
		CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: call.Task.KnowledgePolicy,
	})
	if err != nil {
		return domain.Task{}, domain.Turn{}, err
	}
	if input.CloneHardware {
		if err := s.cloneTaskHardware(ctx, call.Task.ID, item.ID); err != nil {
			_ = s.store.UpdateTaskStatus(ctx, item.ID, item.Status, domain.TaskBlocked, timeString(s.now().UTC()), "")
			return item, domain.Turn{}, fmt.Errorf("clone hardware: %w", err)
		}
	}
	turn, err := s.SubmitMessage(ctx, item.ID, item.OriginalRequest)
	return item, turn, err
}

func (s *Service) AgentProjectTask(ctx context.Context, claims agentapi.Claims, taskID string) (domain.Task, []domain.Turn, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return domain.Task{}, nil, err
	}
	if !call.Task.AgentCapabilities["task_create"] {
		return domain.Task{}, nil, ErrAgentCallForbidden
	}
	item, err := s.store.Task(ctx, strings.TrimSpace(taskID))
	if err != nil || item.ProjectID != call.Project.ID {
		return domain.Task{}, nil, ErrAgentCallForbidden
	}
	turns, err := s.store.ListTurns(ctx, item.ID)
	return item, turns, err
}

func (s *Service) cloneTaskHardware(ctx context.Context, sourceTaskID, targetTaskID string) error {
	groups, err := s.store.HardwareGroups(ctx, sourceTaskID)
	if err != nil {
		return err
	}
	secretsToStore := map[string]string{}
	for index := range groups {
		groups[index].TaskID = targetTaskID
		if groups[index].CredentialRef == "" {
			continue
		}
		if s.secrets == nil {
			return fmt.Errorf("secret store is unavailable")
		}
		value, ok := s.secrets.Get(groups[index].CredentialRef)
		if !ok {
			return fmt.Errorf("hardware credential %s is unavailable", groups[index].ID)
		}
		groups[index].CredentialRef = "hardware/" + targetTaskID + "/" + groups[index].ID + "/credential"
		secretsToStore[groups[index].CredentialRef] = value
	}
	if len(secretsToStore) > 0 {
		if s.secrets == nil {
			return fmt.Errorf("secret store is unavailable")
		}
		if err := s.secrets.PutMany(secretsToStore); err != nil {
			return err
		}
	}
	return s.store.ReplaceHardwareGroups(ctx, targetTaskID, groups)
}
