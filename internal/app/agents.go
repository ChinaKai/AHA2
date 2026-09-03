package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type UpdateAgentConfigInput struct {
	Backend         string
	ModelID         string
	ReasoningEffort string
	Filesystem      string
	Approval        string
	InheritMain     *bool
}

func (s *Service) CompactAgentSession(ctx context.Context, taskID, agentID string) (domain.BackendSession, error) {
	return s.rotateAgentSession(ctx, taskID, agentID, true)
}

func (s *Service) ResetAgentSession(ctx context.Context, taskID, agentID string) (domain.BackendSession, error) {
	return s.rotateAgentSession(ctx, taskID, agentID, false)
}

func (s *Service) rotateAgentSession(
	ctx context.Context,
	taskID string,
	agentID string,
	compact bool,
) (domain.BackendSession, error) {
	if _, err := s.store.TaskAgent(ctx, taskID, agentID); err != nil {
		return domain.BackendSession{}, err
	}
	if _, err := s.store.ActiveTurnForAgent(ctx, taskID, agentID); err == nil {
		return domain.BackendSession{}, store.ErrActiveTurn
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.BackendSession{}, err
	}
	now := s.now().UTC()
	status := "reset"
	var handoff *domain.AgentSessionHandoff
	if compact {
		status = "compacted"
		summary, err := s.buildAgentCompactSummary(ctx, taskID, agentID)
		if err != nil {
			return domain.BackendSession{}, err
		}
		handoff = &domain.AgentSessionHandoff{
			ID: domain.NewID("handoff"), TaskID: taskID, AgentID: agentID, Mode: "compact",
			Summary: summary, Status: "pending", CreatedAt: now,
		}
	}
	session, err := s.store.RotateAgentBackendSession(ctx, taskID, agentID, status, handoff)
	if err != nil {
		return domain.BackendSession{}, err
	}
	kind := "backend_session_reset"
	summary := fmt.Sprintf("AHA 已重置 %s 的 Backend Session", agentID)
	if compact {
		kind = "backend_session_compacted"
		summary = fmt.Sprintf("AHA 已压缩并重置 %s 的 Backend Session", agentID)
	}
	_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: taskID, AgentID: "aha", StreamAgentID: agentID,
		FromAgentID: "aha", ToAgentID: agentID, RouteKind: "session_control",
		Category: "update", Kind: kind, Summary: summary,
		Payload: map[string]any{"old_backend_session_id": session.ID, "mode": status}, CreatedAt: now,
	})
	s.emit(ctx, taskID, "backend_session", session.ID, kind, map[string]any{
		"agent_id": agentID, "old_backend_session_id": session.ID, "mode": status,
	})
	return session, nil
}

func (s *Service) buildAgentCompactSummary(ctx context.Context, taskID, agentID string) (string, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return "", err
	}
	memory, _ := s.store.TaskMemory(ctx, taskID)
	turns, err := s.store.ListTurns(ctx, taskID)
	if err != nil {
		return "", err
	}
	var recent []domain.Turn
	for index := len(turns) - 1; index >= 0 && len(recent) < 8; index-- {
		if turns[index].AgentID == agentID && turns[index].Status.Terminal() {
			recent = append(recent, turns[index])
		}
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "# AHA Backend Session Handoff\n\nTask: %s (%s)\nAgent: %s\nCurrent goal: %s\n\n",
		task.Title, task.Code, agentID, task.CurrentGoal,
	)
	writeCompactList(&builder, "Decisions", memory.Decisions)
	writeCompactList(&builder, "Facts", memory.Facts)
	writeCompactList(&builder, "Progress", memory.Progress)
	writeCompactList(&builder, "Verification", memory.Verification)
	writeCompactList(&builder, "Next actions", memory.NextActions)
	builder.WriteString("\n## Recent Agent Turns\n")
	for _, turn := range recent {
		body := strings.TrimSpace(turn.Result)
		if body == "" {
			body = strings.TrimSpace(turn.Error)
		}
		fmt.Fprintf(&builder, "- Turn %d [%s]: %s\n", turn.Sequence, turn.Status, truncateCompactText(body, 420))
	}
	return truncateCompactText(builder.String(), 12000), nil
}

func writeCompactList(builder *strings.Builder, title string, values []string) {
	fmt.Fprintf(builder, "## %s\n", title)
	if len(values) == 0 {
		builder.WriteString("- none\n")
		return
	}
	start := 0
	if len(values) > 12 {
		start = len(values) - 12
	}
	for _, value := range values[start:] {
		fmt.Fprintf(builder, "- %s\n", truncateCompactText(value, 420))
	}
}

func truncateCompactText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit-1]) + "…"
}

func (s *Service) ResumePending(ctx context.Context) error {
	agents, err := s.store.AllPendingAgents(ctx)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if _, _, err := s.scheduleAgent(ctx, agent.TaskID, agent.AgentID); err != nil {
			return err
		}
	}
	return nil
}

func validateCollaborationMode(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "auto", nil
	}
	if value != "single" && value != "auto" {
		return "", fmt.Errorf("collaboration_mode must be single or auto")
	}
	return value, nil
}

func (s *Service) TaskAgents(ctx context.Context, taskID string) ([]domain.TaskAgent, error) {
	agents, err := s.store.ListTaskAgents(ctx, taskID)
	if err != nil {
		return nil, err
	}
	for index := range agents {
		snapshot, snapshotErr := s.store.RuntimeSnapshot(ctx, agents[index].RuntimeConfigSnapshotID)
		if snapshotErr != nil {
			continue
		}
		agents[index].Backend = snapshot.Backend
		agents[index].ModelID = snapshot.ModelID
		agents[index].ReasoningEffort = snapshot.ReasoningEffort
		agents[index].Filesystem, agents[index].Approval = parsePermissionsJSON(snapshot.PermissionsJSON)
		if model, modelErr := s.store.Model(ctx, snapshot.ModelID); modelErr == nil {
			agents[index].ModelName = model.DisplayName
		}
	}
	return agents, nil
}

func (s *Service) UpdateTaskCollaboration(ctx context.Context, taskID, mode string, maxAgents int) (domain.Task, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	mode, err = validateCollaborationMode(mode)
	if err != nil {
		return domain.Task{}, err
	}
	if maxAgents < 1 {
		return domain.Task{}, fmt.Errorf("max_agents must be at least 1")
	}
	count, err := s.store.TaskAgentCount(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	if maxAgents < count {
		return domain.Task{}, fmt.Errorf("max_agents cannot be lower than the current agent count")
	}
	now := s.now().UTC()
	if err := s.store.UpdateTaskCollaboration(ctx, taskID, mode, maxAgents, timeString(now)); err != nil {
		return domain.Task{}, err
	}
	task.CollaborationMode = mode
	task.MaxAgents = maxAgents
	task.UpdatedAt = now
	s.emit(ctx, taskID, "task", taskID, "task_collaboration_updated", map[string]any{
		"collaboration_mode": mode, "max_agents": maxAgents,
	})
	return task, nil
}

func (s *Service) UpdateAgentConfig(
	ctx context.Context,
	taskID string,
	agentID string,
	input UpdateAgentConfigInput,
) (domain.TaskAgent, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.TaskAgent{}, err
	}
	agent, err := s.store.TaskAgent(ctx, taskID, agentID)
	if err != nil {
		return domain.TaskAgent{}, err
	}
	now := s.now().UTC()
	inheritMain := false
	var snapshot domain.RuntimeConfigSnapshot
	if agentID != "main" && input.InheritMain != nil && *input.InheritMain {
		mainAgent, mainErr := s.store.TaskAgent(ctx, taskID, "main")
		if mainErr != nil {
			return domain.TaskAgent{}, mainErr
		}
		snapshot, err = s.store.RuntimeSnapshot(ctx, mainAgent.RuntimeConfigSnapshotID)
		inheritMain = true
	} else {
		base, baseErr := s.store.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
		if baseErr != nil {
			return domain.TaskAgent{}, baseErr
		}
		snapshot, err = s.deriveRuntimeSnapshot(ctx, task, base, input)
		if err == nil {
			err = s.store.CreateRuntimeSnapshot(ctx, snapshot)
		}
	}
	if err != nil {
		return domain.TaskAgent{}, err
	}
	if err := s.store.UpdateTaskAgentSnapshot(ctx, taskID, agentID, snapshot.ID, inheritMain, now); err != nil {
		return domain.TaskAgent{}, err
	}
	_ = s.store.CloseBackendSessionsForAgent(ctx, taskID, agentID)
	if agentID == "main" {
		if err := s.store.UpdateTaskRuntimeSnapshot(ctx, taskID, snapshot.ID, timeString(now)); err != nil {
			return domain.TaskAgent{}, err
		}
		if err := s.store.UpdateInheritedTaskAgentSnapshots(ctx, taskID, snapshot.ID, now); err != nil {
			return domain.TaskAgent{}, err
		}
		agents, _ := s.store.ListTaskAgents(ctx, taskID)
		for _, child := range agents {
			if child.AgentID != "main" && child.InheritMain {
				_ = s.store.CloseBackendSessionsForAgent(ctx, taskID, child.AgentID)
			}
		}
	}
	s.emit(ctx, taskID, "agent", agentID, "agent_config_updated", map[string]any{
		"agent_id": agentID, "runtime_config_snapshot_id": snapshot.ID, "inherit_main": inheritMain,
	})
	agents, err := s.TaskAgents(ctx, taskID)
	if err != nil {
		return domain.TaskAgent{}, err
	}
	for _, item := range agents {
		if item.AgentID == agentID {
			return item, nil
		}
	}
	return domain.TaskAgent{}, sql.ErrNoRows
}

func (s *Service) deriveRuntimeSnapshot(
	ctx context.Context,
	task domain.Task,
	base domain.RuntimeConfigSnapshot,
	input UpdateAgentConfigInput,
) (domain.RuntimeConfigSnapshot, error) {
	modelID := strings.TrimSpace(input.ModelID)
	if modelID == "" {
		modelID = base.ModelID
	}
	model, err := s.store.Model(ctx, modelID)
	if err != nil {
		return domain.RuntimeConfigSnapshot{}, fmt.Errorf("model: %w", err)
	}
	backend := strings.TrimSpace(input.Backend)
	if backend == "" {
		backend = model.Backend
	}
	if backend != model.Backend {
		return domain.RuntimeConfigSnapshot{}, fmt.Errorf("backend does not match model backend")
	}
	envGroupID := model.DefaultEnvGroupID
	if modelID == base.ModelID && envGroupID == "" {
		envGroupID = base.EnvGroupID
	}
	if envGroupID == "" {
		return domain.RuntimeConfigSnapshot{}, fmt.Errorf("model has no default env group")
	}
	envGroup, err := s.store.EnvGroup(ctx, envGroupID)
	if err != nil {
		return domain.RuntimeConfigSnapshot{}, fmt.Errorf("env group: %w", err)
	}
	effort := strings.TrimSpace(input.ReasoningEffort)
	if effort == "" {
		effort = base.ReasoningEffort
	}
	filesystem, approval := parsePermissionsJSON(base.PermissionsJSON)
	if value := strings.TrimSpace(input.Filesystem); value != "" {
		filesystem = value
	}
	if value := strings.TrimSpace(input.Approval); value != "" {
		approval = value
	}
	switch filesystem {
	case "read-only", "workspace-write", "danger-full-access":
	default:
		return domain.RuntimeConfigSnapshot{}, fmt.Errorf("invalid filesystem")
	}
	switch approval {
	case "never", "auto":
	default:
		return domain.RuntimeConfigSnapshot{}, fmt.Errorf("invalid approval")
	}
	return domain.RuntimeConfigSnapshot{
		ID: domain.NewID("runtime"), WorkspaceID: task.WorkspaceID, Backend: backend,
		ModelID: model.ID, WireModel: model.WireModel, EnvGroupID: envGroup.ID,
		EnvGroupRevision: envGroup.Revision, ReasoningEffort: effort,
		PermissionsJSON: permissionsJSON(filesystem, approval), CreatedAt: s.now().UTC(),
	}, nil
}

func (s *Service) scheduleAgent(ctx context.Context, taskID, agentID string) (domain.Turn, bool, error) {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	if _, err := s.store.ActiveTurnForAgent(ctx, taskID, agentID); err == nil {
		return domain.Turn{}, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.Turn{}, false, err
	}
	items, err := s.store.PendingAgentInbox(ctx, taskID, agentID)
	if err != nil || len(items) == 0 {
		return domain.Turn{}, false, err
	}
	agent, err := s.store.TaskAgent(ctx, taskID, agentID)
	if err != nil {
		return domain.Turn{}, false, err
	}
	round, err := s.store.Round(ctx, items[0].RoundID)
	if err != nil {
		return domain.Turn{}, false, err
	}
	if round.Status.Terminal() {
		_ = s.store.CancelPendingInboxForRound(ctx, round.ID, s.now().UTC())
		return domain.Turn{}, false, nil
	}
	generation := 0
	if latest, latestErr := s.store.LatestTurnForAgent(ctx, taskID, agentID); latestErr == nil {
		generation = latest.Generation + 1
	} else if !errors.Is(latestErr, sql.ErrNoRows) {
		return domain.Turn{}, false, latestErr
	}
	parentTurnID := ""
	for _, item := range items {
		if item.SourceTurnID != "" {
			parentTurnID = item.SourceTurnID
		}
	}
	now := s.now().UTC()
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: taskID, RoundID: round.ID, AgentID: agentID,
		ParentTurnID: parentTurnID, Attempt: 1, Generation: generation, Required: true,
		Title: agent.Title, Instruction: inboxInstruction(items), InputMessageID: round.InputMessageID,
		Status: domain.TurnQueued, RuntimeConfigSnapshotID: agent.RuntimeConfigSnapshotID,
		InboxBatchID: domain.NewID("inbox_batch"), QueuedAt: now,
	}
	turn, err = s.store.ClaimInboxAndCreateTurn(ctx, turn, items)
	if err != nil {
		if errors.Is(err, store.ErrActiveTurn) {
			return domain.Turn{}, false, nil
		}
		return domain.Turn{}, false, err
	}
	s.emitTurn(ctx, turn, "turn_queued", map[string]any{
		"sequence": turn.Sequence, "inbox_batch_id": turn.InboxBatchID, "inbox_count": len(items),
	})
	s.startTurn(turn)
	return turn, true, nil
}

func (s *Service) agentActionsMayRun(ctx context.Context, task domain.Task, actions []AgentAction) bool {
	if task.CollaborationMode != "auto" || len(actions) == 0 {
		return false
	}
	agents, err := s.store.ListTaskAgents(ctx, task.ID)
	if err != nil {
		return false
	}
	existing := make(map[string]bool, len(agents))
	for _, item := range agents {
		existing[item.AgentID] = true
	}
	for index, action := range actions {
		if strings.TrimSpace(action.Assignment) == "" {
			continue
		}
		agentID := normalizedSubAgentID(action.AgentID, index+1)
		if existing[agentID] || len(agents) < task.MaxAgents {
			return true
		}
	}
	return false
}

func inboxInstruction(items []domain.AgentInboxItem) string {
	var sections []string
	sections = append(sections, "Process the following messages routed to you by AHA. They are a fixed inbox batch. Preserve their order and source boundaries.")
	for _, item := range items {
		sections = append(sections, fmt.Sprintf(
			"## Inbox %d [%s from %s]\n%s",
			item.Sequence, item.SourceKind, item.SourceAgentID, strings.TrimSpace(item.Content),
		))
	}
	return strings.Join(sections, "\n\n")
}

func (s *Service) routeAgentOutcome(ctx context.Context, task domain.Task, turn domain.Turn) bool {
	if turn.AgentID == "main" {
		return false
	}
	sourceKind := "agent_result"
	content := strings.TrimSpace(turn.Result)
	if turn.Status != domain.TurnSucceeded {
		sourceKind = "agent_error"
		content = strings.TrimSpace(turn.Error)
		if content == "" {
			content = fmt.Sprintf("%s ended with status %s", turn.AgentID, turn.Status)
		}
	}
	content = fmt.Sprintf("%s [%s, attempt %d]\n%s", turn.AgentID, turn.Status, turn.Attempt, content)
	inserted, err := s.store.EnqueueAgentRoute(ctx, domain.AgentInboxItem{
		ID: domain.NewID("inbox"), TaskID: task.ID, RoundID: turn.RoundID,
		TargetAgentID: "main", SourceAgentID: turn.AgentID, SourceKind: sourceKind,
		SourceTurnID: turn.ID, Content: content,
		Payload: map[string]any{
			"agent_id": turn.AgentID, "status": turn.Status, "attempt": turn.Attempt,
			"turn_id": turn.ID, "result": turn.Result, "error": turn.Error,
		},
		Status: "pending", CreatedAt: s.now().UTC(),
	})
	if err == nil && inserted {
		s.emit(ctx, task.ID, "inbox", turn.ID, "agent_result_queued", map[string]any{
			"round_id": turn.RoundID, "agent_id": "main", "source_agent_id": turn.AgentID,
			"source_turn_id": turn.ID, "source_kind": sourceKind,
		})
	}
	return err == nil && inserted
}

func (s *Service) updateOrchestrationRouteStatus(ctx context.Context, turn domain.Turn) {
	if turn.AgentID == "main" {
		return
	}
	parentTurnID := turn.ParentTurnID
	for parentTurnID != "" {
		parent, err := s.store.Turn(ctx, parentTurnID)
		if err != nil {
			return
		}
		if parent.AgentID == "main" {
			_ = s.store.UpdateAgentBatchRouteStatus(ctx, parent.ID, turn.AgentID, turn)
			return
		}
		parentTurnID = parent.ParentTurnID
	}
}

func (s *Service) scheduleMainAfterAgentResult(ctx context.Context, taskID, roundID string) {
	if _, err := s.store.ActiveTurnForAgent(ctx, taskID, "main"); err == nil {
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	active, terminalUnrouted, err := s.store.SubAgentFanInState(ctx, roundID)
	if err != nil {
		return
	}
	if active == 0 && terminalUnrouted == 0 {
		s.cancelMainResultMerge(taskID)
		_, _, _ = s.scheduleAgent(ctx, taskID, "main")
		return
	}
	s.armMainResultMerge(taskID)
}

func (s *Service) armMainResultMerge(taskID string) {
	s.mergeMu.Lock()
	defer s.mergeMu.Unlock()
	if s.mergeTimers[taskID] != nil {
		return
	}
	delay := s.mergeDelay
	if delay <= 0 {
		delay = time.Second
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		s.mergeMu.Lock()
		if s.mergeTimers[taskID] == timer {
			delete(s.mergeTimers, taskID)
		}
		s.mergeMu.Unlock()
		_, _, _ = s.scheduleAgent(context.Background(), taskID, "main")
	})
	s.mergeTimers[taskID] = timer
}

func (s *Service) cancelMainResultMerge(taskID string) {
	s.mergeMu.Lock()
	if timer := s.mergeTimers[taskID]; timer != nil {
		timer.Stop()
		delete(s.mergeTimers, taskID)
	}
	s.mergeMu.Unlock()
}

func (s *Service) enqueueAgentAction(
	ctx context.Context,
	task domain.Task,
	parent domain.Turn,
	action AgentAction,
	fallback int,
) (string, bool) {
	if task.CollaborationMode != "auto" {
		s.recordOrchestrationError(ctx, task.ID, parent, "Task is in single-agent mode; sub-agent request was ignored")
		return "", false
	}
	agents, err := s.store.ListTaskAgents(ctx, task.ID)
	if err != nil {
		return "", false
	}
	existing := make(map[string]domain.TaskAgent, len(agents))
	for _, item := range agents {
		existing[item.AgentID] = item
	}
	agentID := normalizedSubAgentID(action.AgentID, fallback)
	if _, ok := existing[agentID]; !ok {
		for index := 1; ; index++ {
			candidate := fmt.Sprintf("sub-%03d", index)
			if _, used := existing[candidate]; !used {
				if strings.TrimSpace(action.AgentID) == "" || agentID == "main" || existing[agentID].AgentID != "" {
					agentID = candidate
				}
				break
			}
		}
		if len(agents) >= task.MaxAgents {
			s.recordOrchestrationError(ctx, task.ID, parent, fmt.Sprintf(
				"Agent limit reached: %d/%d", len(agents), task.MaxAgents,
			))
			return "", false
		}
		mainAgent, mainErr := s.store.TaskAgent(ctx, task.ID, "main")
		if mainErr != nil {
			return "", false
		}
		snapshotID := mainAgent.RuntimeConfigSnapshotID
		inheritMain := true
		if action.Backend != "" || action.ModelID != "" || action.ReasoningEffort != "" ||
			action.Filesystem != "" || action.Approval != "" {
			base, baseErr := s.store.RuntimeSnapshot(ctx, mainAgent.RuntimeConfigSnapshotID)
			if baseErr != nil {
				return "", false
			}
			snapshot, deriveErr := s.deriveRuntimeSnapshot(ctx, task, base, UpdateAgentConfigInput{
				Backend: action.Backend, ModelID: action.ModelID, ReasoningEffort: action.ReasoningEffort,
				Filesystem: action.Filesystem, Approval: action.Approval,
			})
			if deriveErr != nil {
				s.recordOrchestrationError(ctx, task.ID, parent, deriveErr.Error())
				return "", false
			}
			if !runtimeWithinMain(base, snapshot) {
				s.recordOrchestrationError(ctx, task.ID, parent, "sub-agent permissions exceed the main Agent security ceiling")
				return "", false
			}
			if err := s.store.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
				return "", false
			}
			snapshotID = snapshot.ID
			inheritMain = false
		}
		title := strings.TrimSpace(action.Title)
		if title == "" {
			title = "Sub-agent assignment"
		}
		now := s.now().UTC()
		_, err = s.store.UpsertTaskAgent(ctx, domain.TaskAgent{
			TaskID: task.ID, AgentID: agentID, Role: "sub", Status: "idle", Title: title,
			RuntimeConfigSnapshotID: snapshotID, InheritMain: inheritMain, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return "", false
		}
		s.emit(ctx, task.ID, "agent", agentID, "task_agent_created", map[string]any{
			"agent_id": agentID, "title": title, "inherit_main": inheritMain,
		})
	} else if action.Backend != "" || action.ModelID != "" || action.ReasoningEffort != "" ||
		action.Filesystem != "" || action.Approval != "" {
		_, err := s.UpdateAgentConfig(ctx, task.ID, agentID, UpdateAgentConfigInput{
			Backend: action.Backend, ModelID: action.ModelID, ReasoningEffort: action.ReasoningEffort,
			Filesystem: action.Filesystem, Approval: action.Approval,
		})
		if err != nil {
			s.recordOrchestrationError(ctx, task.ID, parent, err.Error())
			return "", false
		}
	}
	if current, ok := existing[agentID]; ok {
		if title := strings.TrimSpace(action.Title); title != "" && title != current.Title {
			current.Title = title
			current.UpdatedAt = s.now().UTC()
			_, _ = s.store.UpsertTaskAgent(ctx, current)
		}
	}
	now := s.now().UTC()
	inserted, err := s.store.EnqueueAgentRoute(ctx, domain.AgentInboxItem{
		ID: domain.NewID("inbox"), TaskID: task.ID, RoundID: parent.RoundID,
		TargetAgentID: agentID, SourceAgentID: "main", SourceKind: "assignment",
		SourceTurnID: parent.ID, Content: strings.TrimSpace(action.Assignment),
		Payload: map[string]any{
			"title": action.Title, "required": action.Required, "parent_turn_id": parent.ID,
		},
		Status: "pending", CreatedAt: now,
	})
	if err != nil || !inserted {
		return "", false
	}
	s.emit(ctx, task.ID, "inbox", agentID, "agent_assignment_queued", map[string]any{
		"round_id": parent.RoundID, "agent_id": agentID, "source_turn_id": parent.ID,
	})
	return agentID, true
}

func runtimeWithinMain(main, child domain.RuntimeConfigSnapshot) bool {
	mainFilesystem, mainApproval := parsePermissionsJSON(main.PermissionsJSON)
	childFilesystem, childApproval := parsePermissionsJSON(child.PermissionsJSON)
	return filesystemRank(childFilesystem) <= filesystemRank(mainFilesystem) &&
		approvalRank(childApproval) <= approvalRank(mainApproval)
}

func filesystemRank(value string) int {
	switch value {
	case "danger-full-access":
		return 2
	case "workspace-write":
		return 1
	default:
		return 0
	}
}

func approvalRank(value string) int {
	if value == "auto" {
		return 1
	}
	return 0
}

func (s *Service) recordOrchestrationError(ctx context.Context, taskID string, turn domain.Turn, message string) {
	_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: taskID, RoundID: turn.RoundID, TurnID: turn.ID,
		AgentID: "aha", StreamAgentID: "main", FromAgentID: "aha", ToAgentID: "main",
		RouteKind: "orchestration_error", Category: "error", Kind: "agent_action_rejected",
		Summary: message, CreatedAt: s.now().UTC(),
	})
	s.emitTurn(ctx, turn, "agent_action_rejected", map[string]any{"error": message})
}

func (s *Service) retrySubAgent(ctx context.Context, turn domain.Turn) bool {
	if turn.AgentID == "main" || turn.Status != domain.TurnFailed || turn.Attempt >= 2 {
		return false
	}
	retry := turn
	retry.ID = domain.NewID("turn")
	retry.ParentTurnID = turn.ID
	retry.Attempt++
	retry.Status = domain.TurnQueued
	retry.WaitingReason = ""
	retry.BackendSessionID = ""
	retry.InboxBatchID = ""
	retry.QueuedAt = s.now().UTC()
	retry.PreparedAt = time.Time{}
	retry.StartedAt = time.Time{}
	retry.FinishedAt = time.Time{}
	retry.ExitCode = nil
	retry.Result = ""
	retry.Error = ""
	retry.Usage = nil
	retry.PromptSnapshot = ""
	created, err := s.store.CreateAgentTurn(ctx, retry)
	if err != nil {
		return false
	}
	_ = s.store.UpdateTaskAgentStatus(ctx, created.TaskID, created.AgentID, "queued", created.QueuedAt)
	_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: created.TaskID, RoundID: created.RoundID,
		TurnID: created.ID, AgentID: created.AgentID, StreamAgentID: created.AgentID,
		FromAgentID: "aha", ToAgentID: created.AgentID, RouteKind: "retry",
		Category: "update", Kind: "agent_retry",
		Summary:   fmt.Sprintf("%s 自动重试 Attempt %d", created.AgentID, created.Attempt),
		CreatedAt: created.QueuedAt,
	})
	s.emitTurn(ctx, created, "agent_turn_retrying", nil)
	s.updateOrchestrationRouteStatus(ctx, created)
	s.startTurn(created)
	return true
}

func (s *Service) afterTurnTerminal(
	ctx context.Context,
	task domain.Task,
	turn domain.Turn,
	actions []AgentAction,
	mainFollowup string,
) {
	_ = s.store.MarkInboxBatchProcessed(ctx, turn.InboxBatchID, s.now().UTC())
	if s.retrySubAgent(ctx, turn) {
		s.settleRound(ctx, task.ID, turn.RoundID)
		return
	}
	status := "idle"
	if turn.Status == domain.TurnFailed || turn.Status == domain.TurnBlocked {
		status = "failed"
	}
	if turn.Status == domain.TurnInterrupted {
		status = "interrupted"
	}
	_ = s.store.UpdateTaskAgentStatus(ctx, task.ID, turn.AgentID, status, s.now().UTC())
	createdAgents := 0
	if turn.AgentID == "main" && turn.Status == domain.TurnSucceeded && len(actions) > 0 {
		createdAgents = s.spawnAgentTurns(ctx, task, turn, actions)
	}
	if createdAgents > 0 && strings.TrimSpace(mainFollowup) != "" {
		now := s.now().UTC()
		_, _ = s.store.EnqueueAgentRoute(ctx, domain.AgentInboxItem{
			ID: domain.NewID("inbox"), TaskID: task.ID, RoundID: turn.RoundID,
			TargetAgentID: "main", SourceAgentID: "aha", SourceKind: "main_followup",
			SourceTurnID: turn.ID, Content: strings.TrimSpace(mainFollowup),
			Payload: map[string]any{"parent_turn_id": turn.ID}, Status: "pending", CreatedAt: now,
		})
	}
	routedOutcome := false
	if turn.AgentID != "main" {
		routedOutcome = s.routeAgentOutcome(ctx, task, turn)
	}
	_, _, _ = s.scheduleAgent(ctx, task.ID, turn.AgentID)
	if routedOutcome {
		s.scheduleMainAfterAgentResult(ctx, task.ID, turn.RoundID)
	}
	s.settleRound(ctx, task.ID, turn.RoundID)
}
