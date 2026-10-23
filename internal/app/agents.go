package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/prompt"
	"github.com/ChinaKai/AHA2/internal/store"
)

type UpdateAgentConfigInput struct {
	Backend             string
	ModelSource         string
	ModelID             string
	WireModel           string
	CodexAccountID      string
	ReasoningEffort     string
	StreamIdleTimeoutMS *int
	StreamMaxRetries    *int
	Filesystem          string
	Approval            string
	ProxyEnabled        *bool
	FastMode            *bool
	InheritMain         *bool
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
		handoff = &domain.AgentSessionHandoff{
			ID: domain.NewID("handoff"), TaskID: taskID, AgentID: agentID, Mode: "compact",
			Summary: "compact", Status: "pending", CreatedAt: now,
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
	batches, err := s.store.OpenCollaborationBatchIDs(ctx)
	if err != nil {
		return err
	}
	for _, batchID := range batches {
		s.maybeScheduleCollaborationFallback(ctx, batchID)
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
		agents[index].RuntimeConfigValid = false
		snapshot, snapshotErr := s.store.RuntimeSnapshot(ctx, agents[index].RuntimeConfigSnapshotID)
		if snapshotErr != nil {
			agents[index].RuntimeConfigError = "运行配置不存在，请重新配置 Agent"
			continue
		}
		agents[index].Backend = snapshot.Backend
		agents[index].ModelSource = domain.ModelSourceProvider
		if snapshot.EnvGroupID == domain.ClaudeNativeEnvGroupID {
			agents[index].ModelSource = domain.ModelSourceClaudeNative
		} else if snapshot.CodexAccountID != "" {
			agents[index].ModelSource = domain.ModelSourceOfficial
		}
		agents[index].ModelID = snapshot.ModelID
		agents[index].WireModel = snapshot.WireModel
		agents[index].CodexAccountID = snapshot.CodexAccountID
		agents[index].ReasoningEffort = snapshot.ReasoningEffort
		agents[index].StreamIdleTimeoutMS = snapshot.StreamIdleTimeoutMS
		agents[index].StreamMaxRetries = snapshot.StreamMaxRetries
		agents[index].Filesystem, agents[index].Approval = parsePermissionsJSON(snapshot.PermissionsJSON)
		agents[index].ProxyEnabled = snapshot.ProxyEnabled
		agents[index].FastMode = snapshot.FastMode
		model, unavailable := s.runtimeSnapshotAvailability(ctx, snapshot)
		if unavailable != "" {
			agents[index].RuntimeConfigError = unavailable + "，请修改模型配置"
			continue
		}
		agents[index].ModelName = model.DisplayName
		agents[index].RuntimeConfigValid = true
	}
	return agents, nil
}

// runtimeSnapshotAvailability identifies the two resources that can be removed
// while preserving a historical Task snapshot: its model and its Codex account.
// A missing account must invalidate the Agent even though the account's synthetic
// model row may still be shared by another account.
func (s *Service) runtimeSnapshotAvailability(ctx context.Context, snapshot domain.RuntimeConfigSnapshot) (domain.Model, string) {
	model, err := s.store.Model(ctx, snapshot.ModelID)
	if err != nil {
		return domain.Model{}, "模型已删除"
	}
	if snapshot.CodexAccountID != "" {
		if _, err := s.store.CodexAccount(ctx, snapshot.CodexAccountID); err != nil {
			return domain.Model{}, "Codex 账号已删除"
		}
	}
	return model, ""
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
	var previousSnapshot domain.RuntimeConfigSnapshot
	var snapshot domain.RuntimeConfigSnapshot
	inheritedSnapshots := map[string]domain.RuntimeConfigSnapshot{}
	if agentID == "main" {
		if agents, listErr := s.store.ListTaskAgents(ctx, taskID); listErr == nil {
			for _, child := range agents {
				if child.AgentID == "main" || !child.InheritMain {
					continue
				}
				if childSnapshot, snapshotErr := s.store.RuntimeSnapshot(ctx, child.RuntimeConfigSnapshotID); snapshotErr == nil {
					inheritedSnapshots[child.AgentID] = childSnapshot
				}
			}
		}
	}
	if agentID != "main" && input.InheritMain != nil && *input.InheritMain {
		previousSnapshot, err = s.store.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
		if err != nil {
			return domain.TaskAgent{}, err
		}
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
		previousSnapshot = base
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
	if backendSessionIdentityChanged(previousSnapshot, snapshot) {
		_ = s.store.CloseBackendSessionsForAgent(ctx, taskID, agentID)
	}
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
				childSnapshot, captured := inheritedSnapshots[child.AgentID]
				if captured && backendSessionIdentityChanged(childSnapshot, snapshot) {
					_ = s.store.CloseBackendSessionsForAgent(ctx, taskID, child.AgentID)
				}
			}
		}
	}
	agents, err := s.TaskAgents(ctx, taskID)
	if err != nil {
		return domain.TaskAgent{}, err
	}
	for _, item := range agents {
		if item.AgentID == agentID {
			s.recordAgentConfigUpdate(ctx, item, now)
			s.emit(ctx, taskID, "agent", agentID, "agent_config_updated", map[string]any{
				"agent_id": agentID, "runtime_config_snapshot_id": snapshot.ID, "inherit_main": inheritMain,
			})
			return item, nil
		}
	}
	return domain.TaskAgent{}, sql.ErrNoRows
}

func backendSessionIdentityChanged(previous, next domain.RuntimeConfigSnapshot) bool {
	return previous.WorkspaceID != next.WorkspaceID ||
		previous.Backend != next.Backend ||
		previous.ModelID != next.ModelID ||
		previous.WireModel != next.WireModel ||
		previous.EnvGroupID != next.EnvGroupID ||
		previous.EnvGroupRevision != next.EnvGroupRevision ||
		previous.CodexAccountID != next.CodexAccountID
}

func (s *Service) recordAgentConfigUpdate(ctx context.Context, agent domain.TaskAgent, now time.Time) {
	modelName := agent.ModelName
	if agent.ModelSource == domain.ModelSourceOfficial || agent.ModelSource == domain.ModelSourceClaudeNative || modelName == "" {
		modelName = agent.WireModel
	}
	accountName := ""
	if agent.CodexAccountID != "" {
		if account, err := s.store.CodexAccount(ctx, agent.CodexAccountID); err == nil {
			accountName = account.Label
			if accountName == "" {
				accountName = account.Email
			}
			if accountName == "" {
				accountName = account.AccountID
			}
		}
	}
	source := "Env"
	if agent.ModelSource == domain.ModelSourceOfficial {
		source = "Official"
	} else if agent.ModelSource == domain.ModelSourceClaudeNative {
		source = "Official"
	}
	summary := fmt.Sprintf("%s 配置已更新：%s · %s · %s", agent.AgentID, agent.Backend, source, modelName)
	if agent.InheritMain {
		summary = fmt.Sprintf("%s 配置已更新：继承 Main", agent.AgentID)
	}
	_, _ = s.store.AddConversationItem(ctx, domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: agent.TaskID, AgentID: "aha", StreamAgentID: agent.AgentID,
		FromAgentID: "aha", ToAgentID: agent.AgentID, RouteKind: "config_update",
		Category: "update", Kind: "agent_config_updated", Summary: summary,
		Payload: map[string]any{
			"agent_id": agent.AgentID, "backend": agent.Backend, "model_source": agent.ModelSource,
			"model_id": agent.ModelID, "model_name": modelName, "wire_model": agent.WireModel,
			"codex_account_id": agent.CodexAccountID, "codex_account_name": accountName,
			"reasoning_effort": agent.ReasoningEffort, "filesystem": agent.Filesystem,
			"stream_idle_timeout_ms": agent.StreamIdleTimeoutMS, "stream_max_retries": agent.StreamMaxRetries,
			"approval": agent.Approval, "proxy_enabled": agent.ProxyEnabled, "inherit_main": agent.InheritMain,
			"fast_mode": agent.FastMode,
		},
		CreatedAt: now,
	})
}

func (s *Service) deriveRuntimeSnapshot(
	ctx context.Context,
	task domain.Task,
	base domain.RuntimeConfigSnapshot,
	input UpdateAgentConfigInput,
) (domain.RuntimeConfigSnapshot, error) {
	backend := strings.TrimSpace(input.Backend)
	if backend == "" {
		backend = base.Backend
	}
	modelSource := strings.TrimSpace(input.ModelSource)
	if modelSource == "" {
		modelSource = domain.ModelSourceProvider
		if base.EnvGroupID == domain.ClaudeNativeEnvGroupID {
			modelSource = domain.ModelSourceClaudeNative
		} else if base.CodexAccountID != "" {
			modelSource = domain.ModelSourceOfficial
		}
	}
	modelID := strings.TrimSpace(input.ModelID)
	wireModel := strings.TrimSpace(input.WireModel)
	accountID := strings.TrimSpace(input.CodexAccountID)
	if modelSource == domain.ModelSourceOfficial {
		if wireModel == "" {
			wireModel = base.WireModel
		}
		if accountID == "" {
			accountID = base.CodexAccountID
		}
	} else if modelSource == domain.ModelSourceClaudeNative {
		if wireModel == "" {
			wireModel = base.WireModel
		}
	} else if modelID == "" {
		modelID = base.ModelID
	}
	model, envGroup, accountID, err := s.resolveRuntimeSelection(ctx, runtimeSelectionInput{
		WorkspaceID: task.WorkspaceID, Backend: backend, ModelSource: modelSource, ModelID: modelID,
		WireModel: wireModel, CodexAccountID: accountID,
	})
	if err != nil {
		return domain.RuntimeConfigSnapshot{}, err
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
	proxyEnabled := base.ProxyEnabled
	if input.ProxyEnabled != nil {
		proxyEnabled = *input.ProxyEnabled
	}
	// Fast is a per-runtime choice like effort or proxy, and an edit that does not
	// mention it must not silently drop the tier the Task was created with.
	fastMode := base.FastMode
	if input.FastMode != nil {
		fastMode = *input.FastMode
	}
	streamIdleTimeoutMS := base.StreamIdleTimeoutMS
	if input.StreamIdleTimeoutMS != nil {
		streamIdleTimeoutMS = *input.StreamIdleTimeoutMS
	}
	streamMaxRetries := base.StreamMaxRetries
	if input.StreamMaxRetries != nil {
		streamMaxRetries = *input.StreamMaxRetries
	}
	if err := validateCodexStreamSettings(streamIdleTimeoutMS, streamMaxRetries); err != nil {
		return domain.RuntimeConfigSnapshot{}, err
	}
	return domain.RuntimeConfigSnapshot{
		ID: domain.NewID("runtime"), WorkspaceID: task.WorkspaceID, Backend: backend,
		ModelID: model.ID, WireModel: model.WireModel, EnvGroupID: envGroup.ID,
		EnvGroupRevision: envGroup.Revision, CodexAccountID: accountID, ReasoningEffort: effort,
		StreamIdleTimeoutMS: streamIdleTimeoutMS, StreamMaxRetries: streamMaxRetries,
		ProxyEnabled:    proxyEnabled,
		FastMode:        fastMode,
		PermissionsJSON: permissionsJSON(filesystem, approval), CreatedAt: s.now().UTC(),
	}, nil
}

func validateCodexStreamSettings(idleTimeoutMS, maxRetries int) error {
	if idleTimeoutMS < 0 || idleTimeoutMS > 1800000 || (idleTimeoutMS > 0 && idleTimeoutMS < 30000) {
		return fmt.Errorf("stream_idle_timeout_ms must be 0 or between 30000 and 1800000")
	}
	if maxRetries < 0 || maxRetries > 10 {
		return fmt.Errorf("stream_max_retries must be between 0 and 10")
	}
	return nil
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
	instruction, err := s.prompts.RenderInboxBatch(ctx, inboxTemplateItems(items))
	if err != nil {
		return domain.Turn{}, false, err
	}
	now := s.now().UTC()
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: taskID, RoundID: round.ID, AgentID: agentID,
		ParentTurnID: parentTurnID, Attempt: 1, Generation: generation, Required: true,
		Title: agent.Title, Instruction: instruction, InputMessageID: round.InputMessageID,
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

func inboxTemplateItems(items []domain.AgentInboxItem) []prompt.InboxTemplateItem {
	result := make([]prompt.InboxTemplateItem, 0, len(items))
	for _, item := range items {
		rendered := prompt.InboxTemplateItem{
			Sequence: item.Sequence, SourceKind: item.SourceKind, SourceAgentID: item.SourceAgentID,
			Content: strings.TrimSpace(item.Content),
		}
		if channelContext, ok := item.Payload["channel_context"].(map[string]any); ok {
			actor, _ := channelContext["actor"].(map[string]any)
			displayName := strings.TrimSpace(fmt.Sprint(actor["display_name"]))
			if displayName != "" && displayName != "<nil>" {
				rendered.SourceAgentID = displayName
				rendered.ChannelSender = displayName
			}
			if chatName := strings.TrimSpace(fmt.Sprint(channelContext["chat_display_name"])); chatName != "" && chatName != "<nil>" {
				rendered.ChannelConversation = chatName
			}
			if rawMentions, ok := channelContext["mentions"].([]any); ok {
				var names []string
				for _, rawMention := range rawMentions {
					mention, _ := rawMention.(map[string]any)
					name := strings.TrimSpace(fmt.Sprint(mention["display_name"]))
					if name != "" && name != "<nil>" {
						names = append(names, name)
					}
				}
				rendered.MentionedParticipants = strings.Join(names, ", ")
			}
			if rawReplyContext, ok := channelContext["reply_context"]; ok {
				data, _ := json.Marshal(rawReplyContext)
				var replyContext domain.ChannelReplyContext
				if json.Unmarshal(data, &replyContext) == nil {
					rendered.ReplyContext = &replyContext
				}
			}
		}
		if raw, ok := item.Payload["attachments"]; ok {
			data, _ := json.Marshal(raw)
			_ = json.Unmarshal(data, &rendered.Attachments)
		}
		result = append(result, rendered)
	}
	return result
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
	if batchID, inserted, batchErr := s.store.RecordCollaborationResult(ctx, turn, string(turn.Status), content); batchErr == nil && batchID != "" {
		if inserted {
			conversationID := domain.CollaborationResultConversationID(turn.ID)
			s.emit(ctx, task.ID, "conversation", conversationID, "collaboration_result_routed", map[string]any{
				"round_id": turn.RoundID, "turn_id": turn.ID, "agent_id": turn.AgentID,
				"batch_id": batchID, "conversation_item_id": conversationID, "status": turn.Status,
			})
			s.emit(ctx, task.ID, "collaboration", batchID, "collaboration_result_recorded", map[string]any{
				"batch_id": batchID, "agent_id": turn.AgentID,
				"child_turn_id": turn.ID, "status": turn.Status,
			})
			s.maybeScheduleCollaborationFallback(ctx, batchID)
		}
		return false
	}
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

func (s *Service) WaitAgentCollaboration(
	ctx context.Context,
	claims agentapi.Claims,
	batchID string,
	after int64,
	timeout time.Duration,
) (domain.CollaborationProgress, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return domain.CollaborationProgress{}, err
	}
	if call.Turn.AgentID != "main" {
		return domain.CollaborationProgress{}, ErrAgentCallForbidden
	}
	batch, err := s.store.CollaborationBatch(ctx, strings.TrimSpace(batchID))
	if err != nil || batch.TaskID != call.Task.ID || batch.RoundID != call.Turn.RoundID {
		return domain.CollaborationProgress{}, ErrAgentCallForbidden
	}
	if after < 0 {
		after = 0
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		progress, progressErr := s.store.CollaborationProgress(ctx, batch.ID, after)
		if progressErr != nil {
			return domain.CollaborationProgress{}, progressErr
		}
		if len(progress.Results) > 0 || progress.Complete {
			status := batch.Status
			if progress.Complete && (status == "" || status == domain.CollaborationBatchPending) {
				status = domain.CollaborationBatchComplete
			}
			_ = s.store.MarkCollaborationDelivered(ctx, batch.ID, progress.NextCursor, status, s.now().UTC())
			progress.DeliveredCursor = progress.NextCursor
			progress.Status = status
			return progress, nil
		}
		select {
		case <-ctx.Done():
			return domain.CollaborationProgress{}, ctx.Err()
		case <-timer.C:
			return progress, nil
		case <-ticker.C:
		}
	}
}

func (s *Service) finalizeMainCollaboration(ctx context.Context, turn domain.Turn) {
	var batchIDs []string
	if referenced, err := s.store.CollaborationBatchIDsForInbox(ctx, turn.InboxBatchID); err == nil {
		batchIDs = append(batchIDs, referenced...)
	}
	if batches, err := s.store.CollaborationBatchesForParentTurn(ctx, turn.ID); err == nil {
		for _, batch := range batches {
			batchIDs = append(batchIDs, batch.ID)
		}
	}
	seen := map[string]bool{}
	for _, batchID := range batchIDs {
		if seen[batchID] {
			continue
		}
		seen[batchID] = true
		batch, err := s.store.CollaborationBatch(ctx, batchID)
		if err != nil || batch.Status == domain.CollaborationBatchConsumed {
			continue
		}
		progress, progressErr := s.store.CollaborationProgress(ctx, batch.ID, 0)
		if progressErr != nil {
			continue
		}
		if progress.Complete && batch.DeliveredCursor >= progress.NextCursor {
			_ = s.store.MarkCollaborationConsumed(ctx, batch.ID, s.now().UTC())
			continue
		}
		_ = s.store.MarkCollaborationFallback(ctx, batch.ID, s.now().UTC())
		if batch.DeliveredCursor < progress.NextCursor {
			s.maybeScheduleCollaborationFallback(ctx, batch.ID)
		}
	}
}

func (s *Service) maybeScheduleCollaborationFallback(ctx context.Context, batchID string) {
	batch, err := s.store.CollaborationBatch(ctx, batchID)
	if err != nil || batch.Status == domain.CollaborationBatchConsumed {
		return
	}
	parent, err := s.store.Turn(ctx, batch.ParentTurnID)
	if err != nil || !parent.Status.Terminal() {
		return
	}
	round, err := s.store.Round(ctx, batch.RoundID)
	if err != nil || round.Status.Terminal() {
		return
	}
	if _, err := s.store.ActiveTurnForAgent(ctx, batch.TaskID, "main"); err == nil {
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	s.armCollaborationWake(batch.ParentTurnID)
}

func (s *Service) armCollaborationWake(parentTurnID string) {
	s.collaborationMu.Lock()
	defer s.collaborationMu.Unlock()
	if timer := s.collaborationTimers[parentTurnID]; timer != nil {
		timer.Reset(s.collaborationWakeDelay())
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(s.collaborationWakeDelay(), func() {
		s.collaborationMu.Lock()
		if s.collaborationTimers[parentTurnID] == timer {
			delete(s.collaborationTimers, parentTurnID)
		}
		s.collaborationMu.Unlock()
		s.enqueueCollaborationWake(context.Background(), parentTurnID)
	})
	s.collaborationTimers[parentTurnID] = timer
}

func (s *Service) collaborationWakeDelay() time.Duration {
	if s.collaborationDelay <= 0 {
		return 350 * time.Millisecond
	}
	return s.collaborationDelay
}

func (s *Service) enqueueCollaborationWake(ctx context.Context, parentTurnID string) {
	parent, err := s.store.Turn(ctx, parentTurnID)
	if err != nil || !parent.Status.Terminal() {
		return
	}
	round, err := s.store.Round(ctx, parent.RoundID)
	if err != nil || round.Status.Terminal() {
		return
	}
	if _, err := s.store.ActiveTurnForAgent(ctx, parent.TaskID, "main"); err == nil {
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	pending, err := s.store.PendingAgentInbox(ctx, parent.TaskID, "main")
	if err != nil {
		return
	}
	for _, item := range pending {
		itemParentID := strings.TrimSpace(fmt.Sprint(item.Payload["parent_turn_id"]))
		if item.SourceKind == "collaboration_fallback" && itemParentID == parentTurnID {
			return
		}
	}
	batches, err := s.store.CollaborationBatchesForParentTurn(ctx, parentTurnID)
	if err != nil || len(batches) == 0 {
		return
	}

	type batchWake struct {
		batch    domain.CollaborationBatch
		progress domain.CollaborationProgress
	}
	wakes := make([]batchWake, 0, len(batches))
	allComplete := true
	hasUnread := false
	keyParts := make([]string, 0, len(batches)*2+1)
	keyParts = append(keyParts, parentTurnID)
	for _, batch := range batches {
		progress, progressErr := s.store.CollaborationProgress(ctx, batch.ID, batch.DeliveredCursor)
		if progressErr != nil {
			return
		}
		if !progress.Complete {
			allComplete = false
		}
		if len(progress.Results) > 0 {
			hasUnread = true
		}
		wakes = append(wakes, batchWake{batch: batch, progress: progress})
		keyParts = append(keyParts, batch.ID, fmt.Sprintf("%d", progress.NextCursor))
	}
	if !hasUnread {
		return
	}
	digest := sha256.Sum256([]byte(strings.Join(keyParts, "\x00")))
	wakeID := "inbox_collaboration_fallback_" + hex.EncodeToString(digest[:12])

	var content strings.Builder
	content.WriteString("Sub-agent collaboration has unread results.\n")
	if allComplete {
		content.WriteString("All required sub-agents are complete. Integrate every result before the final answer.\n")
	} else {
		content.WriteString("The collaboration is still running. Process these results incrementally, then continue waiting before giving the final answer.\n")
	}
	content.WriteString("\nOpen batches and cursors:\n")
	batchIDs := make([]string, 0, len(wakes))
	cursors := map[string]int64{}
	for _, wake := range wakes {
		batchIDs = append(batchIDs, wake.batch.ID)
		cursors[wake.batch.ID] = wake.progress.NextCursor
		content.WriteString("- `")
		content.WriteString(wake.batch.ID)
		content.WriteString("` after_cursor=")
		content.WriteString(fmt.Sprintf("%d", wake.progress.NextCursor))
		content.WriteString("\n")
		if strings.TrimSpace(wake.batch.IntegrationInstructions) != "" {
			content.WriteString("  Integration instructions: ")
			content.WriteString(strings.TrimSpace(wake.batch.IntegrationInstructions))
			content.WriteString("\n")
		}
	}
	content.WriteString("\nUnread sub-agent results:\n")
	for _, wake := range wakes {
		for _, result := range wake.progress.Results {
			content.WriteString("\n### ")
			content.WriteString(result.AgentID)
			content.WriteString("\n")
			content.WriteString(result.Content)
			content.WriteString("\n")
		}
	}
	if !allComplete {
		content.WriteString("\nContinue polling every open batch with its saved cursor. Return the final answer only after every batch reports complete.\n")
	}

	_, err = s.store.EnqueueAgentRoute(ctx, domain.AgentInboxItem{
		ID: wakeID, TaskID: parent.TaskID, RoundID: parent.RoundID,
		TargetAgentID: "main", SourceAgentID: "aha", SourceKind: "collaboration_fallback",
		SourceTurnID: parentTurnID, Content: strings.TrimSpace(content.String()),
		Payload: map[string]any{
			"batch_id": wakes[0].batch.ID, "batch_ids": batchIDs, "cursors": cursors,
			"parent_turn_id": parentTurnID, "all_complete": allComplete,
		},
		Status: "pending", CreatedAt: s.now().UTC(),
	})
	if err != nil {
		return
	}
	now := s.now().UTC()
	for _, wake := range wakes {
		_ = s.store.MarkCollaborationDelivered(
			ctx, wake.batch.ID, wake.progress.NextCursor, domain.CollaborationBatchFallback, now,
		)
		_ = s.store.MarkCollaborationFallback(ctx, wake.batch.ID, now)
	}
	_, _, _ = s.scheduleAgent(ctx, parent.TaskID, "main")
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
	s.armMainResultMerge(taskID, roundID)
}

func (s *Service) armMainResultMerge(taskID, roundID string) {
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
		s.scheduleMainAfterAgentResult(context.Background(), taskID, roundID)
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
	collaborationBatchID string,
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
			"collaboration_batch_id": collaborationBatchID,
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

func (s *Service) retryTurn(ctx context.Context, turn domain.Turn) bool {
	mainIdleRetry := turn.AgentID == "main" && turn.WaitingReason == "backend_idle_timeout"
	if turn.Status != domain.TurnFailed || turn.Attempt >= 2 || turn.AgentID == "main" && !mainIdleRetry {
		return false
	}
	retry := turn
	retry.ID = domain.NewID("turn")
	retry.ParentTurnID = turn.ID
	retry.Attempt++
	retry.Status = domain.TurnQueued
	retry.WaitingReason = ""
	retry.BackendSessionID = ""
	retry.QueuedAt = s.now().UTC()
	retry.PreparedAt = time.Time{}
	retry.ContextReadyAt = time.Time{}
	retry.SessionReadyAt = time.Time{}
	retry.StartedAt = time.Time{}
	retry.FirstEventAt = time.Time{}
	retry.LastActivityAt = time.Time{}
	retry.StalledAt = time.Time{}
	retry.BackendFinishedAt = time.Time{}
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

func (s *Service) afterTurnTerminal(ctx context.Context, task domain.Task, turn domain.Turn) {
	_ = s.store.MarkInboxBatchProcessed(ctx, turn.InboxBatchID, s.now().UTC())
	_ = s.store.MarkLiveInboxProcessedForTurn(ctx, turn.ID, s.now().UTC())
	if s.retryTurn(ctx, turn) {
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
	routedOutcome := false
	if turn.AgentID != "main" {
		routedOutcome = s.routeAgentOutcome(ctx, task, turn)
	} else {
		s.finalizeMainCollaboration(ctx, turn)
	}
	_, _, _ = s.scheduleAgent(ctx, task.ID, turn.AgentID)
	if routedOutcome {
		s.scheduleMainAfterAgentResult(ctx, task.ID, turn.RoundID)
	}
	s.settleRound(ctx, task.ID, turn.RoundID)
}
