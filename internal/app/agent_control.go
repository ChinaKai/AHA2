package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

var (
	ErrAgentCallForbidden = errors.New("agent control operation is forbidden")
	ErrAgentTurnInactive  = errors.New("agent turn is not active")
	ErrRevisionConflict   = errors.New("resource revision conflict")
	ErrKnowledgeReadOnly  = errors.New("knowledge entry is available through a read-only library binding")
)

type AgentCallContext struct {
	Task    domain.Task
	Turn    domain.Turn
	Project domain.Project
}

type AgentTaskCreateInput struct {
	WorkspaceID     string `json:"workspace_id"`
	Title           string `json:"title"`
	Request         string `json:"request"`
	CloneHardware   bool   `json:"clone_hardware"`
	Backend         string `json:"backend,omitempty"`
	ModelSource     string `json:"model_source,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	WireModel       string `json:"wire_model,omitempty"`
	CodexAccountID  string `json:"codex_account_id,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type AgentSkillCreateInput struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

type AgentRuntimeOption struct {
	Backend          string   `json:"backend"`
	ModelSource      string   `json:"model_source"`
	ModelID          string   `json:"model_id,omitempty"`
	WireModel        string   `json:"wire_model"`
	CodexAccountID   string   `json:"codex_account_id,omitempty"`
	DisplayName      string   `json:"display_name"`
	ProviderName     string   `json:"provider_name,omitempty"`
	DefaultEffort    string   `json:"default_reasoning_effort,omitempty"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
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

func (s *Service) AgentKnowledgePublishAllowed(ctx context.Context, call AgentCallContext) bool {
	if call.Turn.AgentID != "main" || !knowledgeEnabled(call.Project, call.Task) {
		return false
	}
	if channelContext, err := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); err == nil {
		return channelRouteMode(channelContext) == "task_route"
	}
	return true
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
	if err := s.applyMemoryPatch(ctx, call.Task, memory, patch); err != nil {
		return domain.TaskMemory{}, err
	}
	return s.store.TaskMemory(ctx, call.Task.ID)
}

func (s *Service) ReplaceAgentMemory(ctx context.Context, claims agentapi.Claims, replacement MemoryPatch) (domain.TaskMemory, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return domain.TaskMemory{}, err
	}
	memory, err := s.store.TaskMemory(ctx, call.Task.ID)
	if err != nil {
		return domain.TaskMemory{}, err
	}
	memory.TaskID = call.Task.ID
	memory.CurrentGoal = call.Task.CurrentGoal
	if replacement.CurrentGoal != nil {
		goal := strings.TrimSpace(*replacement.CurrentGoal)
		if goal == "" || len([]rune(goal)) > 2000 {
			return domain.TaskMemory{}, fmt.Errorf("current goal is invalid")
		}
		if err := s.store.UpdateTaskGoal(ctx, call.Task.ID, goal, timeString(s.now().UTC())); err != nil {
			return domain.TaskMemory{}, err
		}
		memory.CurrentGoal = goal
	}
	memory.Decisions = appendUnique(nil, replacement.Decisions...)
	memory.Facts = appendUnique(nil, replacement.Facts...)
	memory.Excluded = appendUnique(nil, replacement.Excluded...)
	memory.Progress = appendUnique(nil, replacement.Progress...)
	memory.Verification = appendUnique(nil, replacement.Verification...)
	memory.NextActions = appendUnique(nil, replacement.NextActions...)
	memory.UpdatedAt = s.now().UTC()
	if err := s.store.UpsertTaskMemory(ctx, memory); err != nil {
		return domain.TaskMemory{}, err
	}
	return s.store.TaskMemory(ctx, call.Task.ID)
}

func (s *Service) AddAgentProgress(ctx context.Context, claims agentapi.Claims, message string, attachmentIDs []string) error {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return err
	}
	message = strings.TrimSpace(message)
	if message == "" && len(attachmentIDs) == 0 {
		return fmt.Errorf("progress message or attachment is required")
	}
	if len([]rune(message)) > 4000 {
		return fmt.Errorf("progress message exceeds 4000 characters")
	}
	item := domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: call.Task.ID, RoundID: call.Turn.RoundID, TurnID: call.Turn.ID,
		AgentID: call.Turn.AgentID, StreamAgentID: call.Turn.AgentID, FromAgentID: call.Turn.AgentID, ToAgentID: "owner",
		RouteKind: "agent_progress", Category: "update", Kind: "agent_message_update", Summary: message,
		Payload: map[string]any{"api": true}, CreatedAt: s.now().UTC(),
	}
	_, err = s.store.AddConversationItemWithAttachments(ctx, item, attachmentIDs)
	if err == nil {
		s.emitTurn(ctx, call.Turn, "agent_progress", map[string]any{"message": message})
	}
	return err
}

func (s *Service) SubmitAgentKnowledge(ctx context.Context, claims agentapi.Claims, candidates []KnowledgeCandidate) ([]domain.KnowledgeEntry, error) {
	items, _, err := s.SubmitAgentKnowledgeProposals(ctx, claims, candidates)
	return items, err
}

func (s *Service) SubmitAgentKnowledgeProposals(ctx context.Context, claims agentapi.Claims, candidates []KnowledgeCandidate) ([]domain.KnowledgeEntry, []domain.KnowledgeProposal, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return nil, nil, err
	}
	if channelContext, channelErr := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); channelErr == nil && channelRouteMode(channelContext) != "task_route" {
		return nil, nil, ErrAgentCallForbidden
	}
	if !knowledgeEnabled(call.Project, call.Task) {
		return nil, nil, ErrAgentCallForbidden
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Body) == "" || len([]rune(candidate.Title)) > 300 || len([]rune(candidate.Body)) > 20000 {
			return nil, nil, fmt.Errorf("knowledge title or body is invalid")
		}
		if candidate.Confidence < 0 || candidate.Confidence > 1 {
			return nil, nil, fmt.Errorf("knowledge confidence must be between 0 and 1")
		}
		if strings.TrimSpace(candidate.EntryID) == "" && candidate.Slug != nil && !store.ValidKnowledgeSlug(strings.TrimSpace(*candidate.Slug)) {
			return nil, nil, store.ErrKnowledgeInvalidSlug
		}
		if candidate.SortOrder != nil && *candidate.SortOrder < 0 {
			return nil, nil, fmt.Errorf("knowledge sort order must be non-negative")
		}
		if strings.TrimSpace(candidate.EntryID) == "" {
			if candidate.BaseRevision != 0 || (candidate.IsIndex != nil && *candidate.IsIndex) {
				return nil, nil, ErrRevisionConflict
			}
			continue
		}
		existing, err := s.store.Knowledge(ctx, candidate.EntryID)
		if err != nil {
			return nil, nil, ErrAgentCallForbidden
		}
		if existing.Scope == "project" && existing.ProjectID != call.Project.ID {
			allowed, bindingErr := s.store.CanContributeKnowledgeLibrary(ctx, existing.ProjectID, call.Project.ID)
			if bindingErr != nil {
				return nil, nil, bindingErr
			}
			if !allowed {
				return nil, nil, ErrKnowledgeReadOnly
			}
		}
		if candidate.BaseRevision <= 0 || candidate.BaseRevision != existing.Revision {
			return nil, nil, ErrRevisionConflict
		}
		requestedScope := candidate.Scope
		if requestedScope != "global" {
			requestedScope = "project"
		}
		if requestedScope != existing.Scope {
			return nil, nil, ErrAgentCallForbidden
		}
	}
	lines, _ := s.store.ListProductLines(ctx, call.Project.ID)
	line := resolveProductLine(lines, call.Task.TargetBranch, call.Project.DefaultBranch)
	return s.createKnowledgeCandidates(ctx, call.Project.ID, call.Task.ID, call.Turn.ID, call.Task.TaskBranch, line.ID, candidates)
}

func (s *Service) SubmitAgentKnowledgeFeedback(ctx context.Context, claims agentapi.Claims, feedback KnowledgeFeedback) (domain.KnowledgeEntry, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return domain.KnowledgeEntry{}, err
	}
	if channelContext, channelErr := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); channelErr == nil && channelRouteMode(channelContext) != "task_route" {
		return domain.KnowledgeEntry{}, ErrAgentCallForbidden
	}
	entry, err := s.store.Knowledge(ctx, strings.TrimSpace(feedback.EntryID))
	if err != nil {
		return domain.KnowledgeEntry{}, ErrAgentCallForbidden
	}
	if entry.Scope == "project" && entry.ProjectID != call.Project.ID {
		allowed, bindingErr := s.store.CanContributeKnowledgeLibrary(ctx, entry.ProjectID, call.Project.ID)
		if bindingErr != nil {
			return domain.KnowledgeEntry{}, bindingErr
		}
		if !allowed {
			return domain.KnowledgeEntry{}, ErrKnowledgeReadOnly
		}
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
	if channelContext, channelErr := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); channelErr == nil && channelRouteMode(channelContext) != "task_route" {
		return 0, ErrAgentCallForbidden
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
	items := s.activeTaskSkills(ctx, call.Task)
	for index := range items {
		items[index].CanUpdate = items[index].Scope == "global" || items[index].ProjectID == call.Project.ID || items[index].BoundProjectID == call.Project.ID && items[index].BindingMode == "project"
	}
	return items, call, nil
}

func (s *Service) CreateAgentSkill(ctx context.Context, claims agentapi.Claims, input AgentSkillCreateInput) (domain.Skill, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return domain.Skill{}, err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Instructions = strings.TrimSpace(input.Instructions)
	if input.Name == "" || input.Instructions == "" {
		return domain.Skill{}, fmt.Errorf("skill name and instructions are required")
	}
	now := s.now().UTC()
	item := domain.Skill{
		ID: domain.NewID("skill"), Scope: "project", ProjectID: call.Project.ID,
		Name: input.Name, Description: input.Description, Instructions: input.Instructions,
		Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateSkill(ctx, item); err != nil {
		return domain.Skill{}, err
	}
	skillIDs := append(append([]string(nil), call.Task.SkillIDs...), item.ID)
	if _, err := s.UpdateTaskSkills(ctx, call.Task.ID, skillIDs); err != nil {
		_ = s.store.DeleteSkill(ctx, item.ID)
		return domain.Skill{}, err
	}
	return s.store.Skill(ctx, item.ID)
}

func (s *Service) ApplicableAgentKnowledge(ctx context.Context, claims agentapi.Claims) ([]domain.KnowledgeEntry, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return nil, err
	}
	if channelContext, channelErr := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID); channelErr == nil {
		if channelRouteMode(channelContext) == "task_route" {
			lines, _ := s.store.ListProductLines(ctx, call.Project.ID)
			line := resolveProductLine(lines, call.Task.TargetBranch, call.Project.DefaultBranch)
			project, _ := s.store.ListApplicableKnowledge(ctx, call.Project.ID, line.ID, []domain.KnowledgeStatus{domain.KnowledgeVerified, domain.KnowledgeStale})
			global, _ := s.store.ListKnowledge(ctx, "global", "", []domain.KnowledgeStatus{domain.KnowledgeVerified, domain.KnowledgeStale})
			items := append(project, global...)
			markAgentKnowledgeWritable(items, s.AgentKnowledgePublishAllowed(ctx, call))
			return items, nil
		}
		return s.store.ChannelAllowedKnowledge(
			ctx,
			strings.TrimSpace(fmt.Sprint(channelContext["instance_id"])),
			strings.TrimSpace(fmt.Sprint(channelContext["endpoint"])),
			strings.TrimSpace(fmt.Sprint(channelContext["conversation_id"])),
		)
	}
	lines, _ := s.store.ListProductLines(ctx, call.Project.ID)
	line := resolveProductLine(lines, call.Task.TargetBranch, call.Project.DefaultBranch)
	project, _ := s.store.ListApplicableKnowledge(ctx, call.Project.ID, line.ID, []domain.KnowledgeStatus{domain.KnowledgeVerified, domain.KnowledgeStale})
	global, _ := s.store.ListKnowledge(ctx, "global", "", []domain.KnowledgeStatus{domain.KnowledgeVerified, domain.KnowledgeStale})
	items := append(project, global...)
	markAgentKnowledgeWritable(items, s.AgentKnowledgePublishAllowed(ctx, call))
	return items, nil
}

func markAgentKnowledgeWritable(items []domain.KnowledgeEntry, allowed bool) {
	for index := range items {
		if !allowed {
			items[index].CanProposeRevision = false
		} else if items[index].Scope == "global" {
			items[index].CanProposeRevision = true
		}
	}
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

func (s *Service) AgentProjectRuntimes(ctx context.Context, claims agentapi.Claims) ([]AgentRuntimeOption, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return nil, err
	}
	if !call.Task.AgentCapabilities["task_create"] {
		return nil, ErrAgentCallForbidden
	}
	models, err := s.store.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]AgentRuntimeOption, 0, len(models))
	for _, model := range models {
		if model.Source == domain.ModelSourceOfficial || model.DefaultEnvGroupID == "" {
			continue
		}
		efforts, _ := model.Capabilities["reasoning_efforts"].([]string)
		if len(efforts) == 0 {
			if values, ok := model.Capabilities["reasoning_efforts"].([]any); ok {
				for _, value := range values {
					if effort, ok := value.(string); ok {
						efforts = append(efforts, effort)
					}
				}
			}
		}
		result = append(result, AgentRuntimeOption{
			Backend: model.Backend, ModelSource: domain.ModelSourceProvider, ModelID: model.ID,
			WireModel: model.WireModel, DisplayName: model.DisplayName, ProviderName: model.ProviderName,
			DefaultEffort: model.DefaultEffort, ReasoningEfforts: efforts,
		})
	}
	accounts, err := s.store.ListCodexAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		if !account.CredentialConfigured || account.Status != "ready" {
			continue
		}
		for _, model := range account.AvailableModels {
			result = append(result, AgentRuntimeOption{
				Backend: "codex", ModelSource: domain.ModelSourceOfficial, WireModel: model.WireModel,
				CodexAccountID: account.ID, DisplayName: model.DisplayName, ProviderName: account.Label,
				DefaultEffort: model.DefaultEffort, ReasoningEfforts: model.ReasoningEfforts,
			})
		}
	}
	return result, nil
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
	backend, modelID, wireModel := snapshot.Backend, snapshot.ModelID, snapshot.WireModel
	codexAccountID, reasoningEffort := snapshot.CodexAccountID, snapshot.ReasoningEffort
	if strings.TrimSpace(input.Backend) != "" || strings.TrimSpace(input.ModelID) != "" || strings.TrimSpace(input.WireModel) != "" || strings.TrimSpace(input.CodexAccountID) != "" {
		backend, modelSource, modelID = strings.TrimSpace(input.Backend), strings.TrimSpace(input.ModelSource), strings.TrimSpace(input.ModelID)
		wireModel, codexAccountID = strings.TrimSpace(input.WireModel), strings.TrimSpace(input.CodexAccountID)
		if modelSource == "" {
			modelSource = domain.ModelSourceProvider
		}
	}
	if strings.TrimSpace(input.ReasoningEffort) != "" {
		reasoningEffort = strings.TrimSpace(input.ReasoningEffort)
	}
	filesystem, approval := parsePermissionsJSON(snapshot.PermissionsJSON)
	item, err := s.CreateTask(ctx, CreateTaskInput{
		ProjectID: call.Project.ID, WorkspaceID: workspace.ID, Title: input.Title, Request: input.Request,
		Isolation: "inplace", Backend: backend, ModelSource: modelSource,
		ModelID: modelID, WireModel: wireModel,
		CodexAccountID: codexAccountID, ReasoningEffort: reasoningEffort,
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
