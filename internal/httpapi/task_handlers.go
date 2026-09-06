package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func (s *Server) listTasks(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListTasks(request.Context(), request.URL.Query().Get("project_id"))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_tasks_failed")
		return
	}
	s.applyTaskTokenTotals(request.Context(), items)
	if mirrors, mirrorErr := s.store.RemoteTaskMirrors(request.Context(), request.URL.Query().Get("project_id")); mirrorErr == nil {
		for _, mirror := range mirrors {
			items = append(items, mirror.Task)
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "tasks": items})
}

func (s *Server) applyTaskTokenTotals(ctx context.Context, tasks []domain.Task) {
	for index := range tasks {
		turns, turnsErr := s.store.ListTurns(ctx, tasks[index].ID)
		sessions, sessionsErr := s.store.ListBackendSessionsForTask(ctx, tasks[index].ID)
		if turnsErr != nil || sessionsErr != nil {
			continue
		}
		backends := map[string]string{}
		for _, turn := range turns {
			if _, ok := backends[turn.RuntimeConfigSnapshotID]; ok {
				continue
			}
			if snapshot, err := s.store.RuntimeSnapshot(ctx, turn.RuntimeConfigSnapshotID); err == nil {
				backends[turn.RuntimeConfigSnapshotID] = snapshot.Backend
			}
		}
		tasks[index].TotalTokens = int64(taskTotalTokens(turns, sessions, backends))
	}
}

func taskTotalTokens(
	turns []domain.Turn,
	sessions []domain.BackendSession,
	backendBySnapshot map[string]string,
) float64 {
	turnsBySession := map[string][]domain.Turn{}
	withoutSession := make([]domain.Turn, 0)
	for _, turn := range turns {
		if len(turn.Usage) == 0 {
			continue
		}
		if turn.BackendSessionID == "" {
			withoutSession = append(withoutSession, turn)
			continue
		}
		turnsBySession[turn.BackendSessionID] = append(turnsBySession[turn.BackendSessionID], turn)
	}
	total := float64(0)
	for _, session := range sessions {
		var usage map[string]any
		_ = json.Unmarshal([]byte(session.ContextUsageJSON), &usage)
		usage = backendSessionUsage(session.Backend, turnsBySession[session.ID], usage)
		delete(turnsBySession, session.ID)
		total += usageTotalTokens(usage, session.Backend)
	}
	for _, sessionTurns := range turnsBySession {
		latest := latestUsageTurn(sessionTurns)
		backend := backendBySnapshot[latest.RuntimeConfigSnapshotID]
		total += usageTotalTokens(backendSessionUsage(backend, sessionTurns, nil), backend)
	}
	for _, turn := range withoutSession {
		total += usageTotalTokens(turn.Usage, backendBySnapshot[turn.RuntimeConfigSnapshotID])
	}
	return total
}

func backendSessionUsage(backend string, turns []domain.Turn, fallback map[string]any) map[string]any {
	if len(turns) == 0 {
		return fallback
	}
	if backend != "claude" {
		if len(fallback) > 0 {
			return fallback
		}
		return latestUsageTurn(turns).Usage
	}
	result := map[string]any{}
	for _, key := range []string{
		"input_tokens", "cached_input_tokens", "cache_read_input_tokens",
		"cache_creation_input_tokens", "output_tokens", "reasoning_output_tokens",
	} {
		total := float64(0)
		present := false
		for _, turn := range turns {
			if _, ok := turn.Usage[key]; ok {
				present = true
			}
			total += usageNumber(turn.Usage, key)
		}
		if present {
			result[key] = total
		}
	}
	return result
}

func latestUsageTurn(turns []domain.Turn) domain.Turn {
	var latest domain.Turn
	for _, turn := range turns {
		if latest.ID == "" || turn.Sequence > latest.Sequence {
			latest = turn
		}
	}
	return latest
}

func (s *Server) createTask(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ProjectID         string   `json:"project_id"`
		WorkspaceID       string   `json:"workspace_id"`
		Title             string   `json:"title"`
		Request           string   `json:"request"`
		TargetBranch      string   `json:"target_branch"`
		BaseCommit        string   `json:"base_commit"`
		TaskBranch        string   `json:"task_branch"`
		Isolation         string   `json:"isolation"`
		WorktreeDir       string   `json:"worktree_dir"`
		Backend           string   `json:"backend"`
		ModelSource       string   `json:"model_source"`
		ModelID           string   `json:"model_id"`
		WireModel         string   `json:"wire_model"`
		CodexAccountID    string   `json:"codex_account_id"`
		ReasoningEffort   string   `json:"reasoning_effort"`
		Filesystem        string   `json:"filesystem"`
		Approval          string   `json:"approval"`
		ProxyEnabled      bool     `json:"proxy_enabled"`
		CollaborationMode string   `json:"collaboration_mode"`
		MaxAgents         int      `json:"max_agents"`
		KnowledgePolicy   string   `json:"knowledge_policy"`
		SkillIDs          []string `json:"skill_ids"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if workspace, err := s.store.Workspace(request.Context(), payload.WorkspaceID); err == nil && workspace.ReadOnly {
		writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "workspace_read_only", "message": "该 Workspace 属于其他设备，只能查看同步历史"})
		return
	}
	filesystem := strings.TrimSpace(payload.Filesystem)
	if filesystem == "" {
		filesystem = "workspace-write"
	}
	switch filesystem {
	case "read-only", "workspace-write", "danger-full-access":
	default:
		writeError(writer, http.StatusBadRequest, "invalid_filesystem")
		return
	}
	approval := strings.TrimSpace(payload.Approval)
	if approval == "" {
		approval = "never"
	}
	switch approval {
	case "never", "auto":
	default:
		writeError(writer, http.StatusBadRequest, "invalid_approval")
		return
	}
	item, err := s.app.CreateTask(request.Context(), app.CreateTaskInput{
		ProjectID: payload.ProjectID, WorkspaceID: payload.WorkspaceID, Title: payload.Title, Request: payload.Request,
		TargetBranch: payload.TargetBranch, BaseCommit: payload.BaseCommit, TaskBranch: payload.TaskBranch,
		Isolation: payload.Isolation, WorktreeDir: payload.WorktreeDir, Backend: payload.Backend,
		ModelSource: payload.ModelSource, ModelID: payload.ModelID, WireModel: payload.WireModel,
		CodexAccountID: payload.CodexAccountID, ReasoningEffort: payload.ReasoningEffort,
		Filesystem: filesystem, Approval: approval, CollaborationMode: payload.CollaborationMode,
		MaxAgents: payload.MaxAgents, ProxyEnabled: payload.ProxyEnabled, KnowledgePolicy: payload.KnowledgePolicy,
		SkillIDs: payload.SkillIDs,
	})
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "create_task_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.create", "task", item.ID, map[string]any{"project_id": item.ProjectID})
	turn, startErr := s.app.SubmitMessage(request.Context(), item.ID, item.OriginalRequest)
	if startErr != nil {
		writeJSON(writer, http.StatusCreated, map[string]any{
			"ok": true, "task": item, "start_error": startErr.Error(),
		})
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "task": item, "turn": turn})
}

func (s *Server) taskDetail(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	task, err := s.store.Task(request.Context(), taskID)
	if err != nil {
		mirror, mirrorErr := s.store.RemoteTaskDetailMirror(request.Context(), taskID)
		if mirrorErr != nil {
			writeError(writer, http.StatusNotFound, "task_not_found")
			return
		}
		var latest domain.TaskRound
		for _, round := range mirror.Rounds {
			if latest.ID == "" || round.Sequence > latest.Sequence {
				latest = round
			}
		}
		turns := make([]domain.Turn, 0)
		for _, turn := range mirror.Turns {
			if latest.ID == "" || turn.RoundID == latest.ID {
				turns = append(turns, turn)
			}
		}
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "task": mirror.Task, "latest_round": latest, "turns": turns, "agents": mirror.Agents, "memory": mirror.Memory, "hardware": mirror.Hardware, "event_cursor": 0, "server_time_ms": time.Now().UTC().UnixMilli()})
		return
	}
	round, _ := s.store.LatestRound(request.Context(), taskID)
	turns, _ := s.store.TurnsForRound(request.Context(), round.ID)
	agents, _ := s.app.TaskAgents(request.Context(), taskID)
	memory, _ := s.store.TaskMemory(request.Context(), taskID)
	hardwareGroups, _ := s.store.HardwareGroups(request.Context(), taskID)
	now := time.Now().UTC()
	applyRoundTiming(&round, now)
	applyTurnTimings(turns, now)
	s.applyTurnContextUsage(request.Context(), turns)
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "task": task, "latest_round": round, "turns": turns, "agents": agents, "memory": memory,
		"hardware":       hardwareGroups,
		"event_cursor":   s.store.MaxEventSequence(request.Context(), taskID),
		"server_time_ms": now.UnixMilli(),
	})
}

func (s *Server) taskConversation(writer http.ResponseWriter, request *http.Request) {
	s.conversationForAgent(writer, request, "main")
}

func (s *Server) agentConversation(writer http.ResponseWriter, request *http.Request) {
	s.conversationForAgent(writer, request, request.PathValue("agent"))
}

func (s *Server) conversationForAgent(writer http.ResponseWriter, request *http.Request, agentID string) {
	taskID := request.PathValue("id")
	categories := []string{}
	categoryQuery := request.URL.Query().Get("categories")
	for _, value := range strings.Split(categoryQuery, ",") {
		value = strings.TrimSpace(value)
		switch value {
		case "chat", "update", "tool", "error":
			categories = append(categories, value)
		}
	}
	if _, err := s.store.Task(request.Context(), taskID); err != nil {
		if strings.TrimSpace(categoryQuery) == "" {
			categories = []string{"chat", "update", "error"}
		}
		page, pageErr := s.store.RemoteConversationPageForAgent(
			request.Context(),
			taskID,
			agentID,
			queryInt(request, "before", 0),
			queryInt(request, "after", 0),
			int(queryInt(request, "limit", 50)),
			categories,
		)
		if pageErr != nil {
			writeError(writer, http.StatusNotFound, "task_not_found")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "conversation": page})
		return
	}
	if _, err := s.store.TaskAgent(request.Context(), taskID, agentID); err != nil {
		writeError(writer, http.StatusNotFound, "agent_not_found")
		return
	}
	page, err := s.store.ConversationPageForAgent(
		request.Context(),
		taskID,
		agentID,
		queryInt(request, "before", 0),
		queryInt(request, "after", 0),
		int(queryInt(request, "limit", 50)),
		categories,
	)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "conversation_failed")
		return
	}
	_ = s.store.MarkAgentConversationRead(request.Context(), taskID, agentID)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "conversation": page})
}

func (s *Server) taskContext(writer http.ResponseWriter, request *http.Request) {
	s.contextForAgent(writer, request, "main")
}

func (s *Server) agentContext(writer http.ResponseWriter, request *http.Request) {
	s.contextForAgent(writer, request, request.PathValue("agent"))
}

func (s *Server) contextForAgent(writer http.ResponseWriter, request *http.Request, agentID string) {
	taskID := request.PathValue("id")
	task, err := s.store.Task(request.Context(), taskID)
	if err != nil {
		mirror, mirrorErr := s.store.RemoteTaskDetailMirror(request.Context(), taskID)
		if mirrorErr != nil {
			writeError(writer, http.StatusNotFound, "task_not_found")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "task": mirror.Task, "turns": mirror.Turns, "memory": mirror.Memory, "project_knowledge": []domain.KnowledgeEntry{}, "global_knowledge": []domain.KnowledgeEntry{}, "context": map[string]any{"agent_id": agentID, "usage": map[string]any{}, "metrics": map[string]any{}}})
		return
	}
	memory, _ := s.store.TaskMemory(request.Context(), taskID)
	round, _ := s.store.LatestRound(request.Context(), taskID)
	turns, _ := s.store.TurnsForRound(request.Context(), round.ID)
	now := time.Now().UTC()
	applyRoundTiming(&round, now)
	applyTurnTimings(turns, now)
	s.applyTurnContextUsage(request.Context(), turns)
	agentTurns, _ := s.store.ListTurns(request.Context(), taskID)
	s.applyTurnContextUsage(request.Context(), agentTurns)
	projectKB, _ := s.store.ListKnowledge(request.Context(), "project", task.ProjectID, []domain.KnowledgeStatus{domain.KnowledgeVerified})
	globalKB, _ := s.store.ListKnowledge(request.Context(), "global", "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
	latest := latestTurnForAgent(agentTurns, agentID)
	sessions, _ := s.store.ListBackendSessionsForAgent(request.Context(), taskID, agentID)
	workspace, _ := s.runtimeWorkspace(request.Context(), task.WorkspaceID)
	metrics := contextMetrics(latest, sessions, workspace, agentTurns)
	if active, ok := activeBackendSession(sessions); ok {
		size, exists := backendSessionArtifactSize(
			request.Context(), active, workspace, taskRuntimeWorkDir(task, workspace),
		)
		metrics["session_size_bytes"] = size
		metrics["session_exists"] = exists
	}
	contextUsage := latest.Usage
	contextPercent := turnContextPercent(latest)
	if active, _ := metrics["session_active"].(bool); !active && latest.Status.Terminal() {
		contextUsage = cloneUsage(latest.Usage)
		contextUsage["context_tokens"] = float64(0)
		contextPercent = 0
	}
	contextData := map[string]any{
		"turn_id": latest.ID, "agent_id": latest.AgentID, "prompt": latest.PromptSnapshot,
		"prompt_chars": latest.PromptChars, "context_window": latest.ContextWindow,
		"usage": contextUsage, "context_percent": contextPercent, "metrics": metrics,
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "task": task, "latest_round": round, "turns": turns,
		"memory": memory, "project_knowledge": projectKB, "global_knowledge": globalKB,
		"context": contextData,
	})
}

func latestTurnForAgent(turns []domain.Turn, agentID string) domain.Turn {
	var latest domain.Turn
	for _, turn := range turns {
		if turn.AgentID == agentID && (latest.ID == "" || turn.Sequence > latest.Sequence) {
			latest = turn
		}
	}
	return latest
}

func contextMetrics(
	turn domain.Turn,
	sessions []domain.BackendSession,
	workspace domain.Workspace,
	turns []domain.Turn,
) map[string]any {
	backend := ""
	var active domain.BackendSession
	var latestSession domain.BackendSession
	for _, session := range sessions {
		latestSession = session
		if session.Status == "active" {
			active = session
			backend = session.Backend
		}
	}
	inputTokens := float64(0)
	cachedTokens := float64(0)
	outputTokens := float64(0)
	reasoningTokens := float64(0)
	currentTotal := float64(0)
	historyTotal := float64(0)
	currentIncluded := false
	for _, session := range sessions {
		var usage map[string]any
		_ = json.Unmarshal([]byte(session.ContextUsageJSON), &usage)
		sessionTurns := make([]domain.Turn, 0)
		for _, candidate := range turns {
			if candidate.BackendSessionID == session.ID && len(candidate.Usage) > 0 {
				sessionTurns = append(sessionTurns, candidate)
			}
		}
		usage = backendSessionUsage(session.Backend, sessionTurns, usage)
		isCurrent := active.ID != "" && session.ID == active.ID
		if isCurrent && session.ID == turn.BackendSessionID && len(usage) > 0 {
			currentIncluded = true
		}
		total := usageTotalTokens(usage, session.Backend)
		if isCurrent {
			currentTotal = total
		} else {
			historyTotal += total
		}
		inputTokens += usageNumber(usage, "input_tokens")
		cachedTokens += usageCachedTokens(usage)
		outputTokens += usageNumber(usage, "output_tokens")
		reasoningTokens += usageNumber(usage, "reasoning_output_tokens")
	}
	if !currentIncluded && len(turn.Usage) > 0 && (active.ID != "" || len(sessions) == 0) {
		currentTotal = usageTotalTokens(turn.Usage, backend)
		inputTokens += usageNumber(turn.Usage, "input_tokens")
		cachedTokens += usageCachedTokens(turn.Usage)
		outputTokens += usageNumber(turn.Usage, "output_tokens")
		reasoningTokens += usageNumber(turn.Usage, "reasoning_output_tokens")
	}
	contextTokens := usageNumber(turn.Usage, "context_tokens")
	contextPercent := turnContextPercent(turn)
	if active.ID == "" && turn.Status.Terminal() {
		contextTokens = 0
		contextPercent = 0
	}
	sessionStatus := active.Status
	if sessionStatus == "" {
		sessionStatus = latestSession.Status
	}
	return map[string]any{
		"total_tokens":            historyTotal + currentTotal,
		"history_tokens":          historyTotal,
		"current_total_tokens":    currentTotal,
		"input_tokens":            inputTokens,
		"cached_input_tokens":     cachedTokens,
		"output_tokens":           outputTokens,
		"reasoning_output_tokens": reasoningTokens,
		"aha_prompt_chars":        turn.PromptChars,
		"aha_prompt_tokens":       (turn.PromptChars + 3) / 4,
		"context_tokens":          contextTokens,
		"context_window":          turn.ContextWindow,
		"context_percent":         contextPercent,
		"session_size_bytes":      int64(0),
		"session_exists":          false,
		"session_active":          active.ID != "",
		"session_id":              active.ProviderSession,
		"session_status":          sessionStatus,
		"session_count":           len(sessions),
	}
}

func activeBackendSession(sessions []domain.BackendSession) (domain.BackendSession, bool) {
	for _, session := range sessions {
		if session.Status == "active" {
			return session, true
		}
	}
	return domain.BackendSession{}, false
}

func cloneUsage(usage map[string]any) map[string]any {
	result := make(map[string]any, len(usage)+1)
	for key, value := range usage {
		result[key] = value
	}
	return result
}

func usageTotalTokens(usage map[string]any, backend string) float64 {
	total := usageNumber(usage, "input_tokens") + usageNumber(usage, "output_tokens")
	if backend == "claude" {
		cacheRead := usageNumber(usage, "cached_input_tokens")
		if cacheRead <= 0 {
			cacheRead = usageNumber(usage, "cache_read_input_tokens")
		}
		total += cacheRead
	}
	return total
}

func usageCachedTokens(usage map[string]any) float64 {
	cacheRead := usageNumber(usage, "cached_input_tokens")
	if cacheRead <= 0 {
		cacheRead = usageNumber(usage, "cache_read_input_tokens")
	}
	return cacheRead + usageNumber(usage, "cache_creation_input_tokens")
}

func applyRoundTiming(round *domain.TaskRound, now time.Time) {
	start := round.StartedAt
	if start.IsZero() {
		start = round.CreatedAt
	}
	round.ElapsedMS = elapsedMilliseconds(start, round.FinishedAt, now)
}

func applyTurnTimings(turns []domain.Turn, now time.Time) {
	for index := range turns {
		turn := &turns[index]
		terminal := turn.Status.Terminal()
		turn.ElapsedMS = elapsedMilliseconds(turn.QueuedAt, turn.FinishedAt, now)
		if !turn.PreparedAt.IsZero() {
			turn.QueueDurationMS = elapsedMilliseconds(turn.QueuedAt, turn.PreparedAt, now)
			turn.PrepareDurationMS = elapsedMilliseconds(turn.PreparedAt, turn.StartedAt, now)
		} else if turn.Status == domain.TurnQueued {
			turn.QueueDurationMS = elapsedMilliseconds(turn.QueuedAt, time.Time{}, now)
		}
		turn.ContextPrepareDurationMS = stageElapsedMilliseconds(turn.PreparedAt, turn.ContextReadyAt, terminal, now)
		turn.SessionWakeDurationMS = stageElapsedMilliseconds(turn.ContextReadyAt, turn.SessionReadyAt, terminal, now)
		if !turn.StartedAt.IsZero() {
			turn.RunDurationMS = elapsedMilliseconds(turn.StartedAt, turn.FinishedAt, now)
			backendStartEnd := turn.FirstEventAt
			if backendStartEnd.IsZero() {
				backendStartEnd = turn.BackendFinishedAt
			}
			turn.BackendStartDurationMS = stageElapsedMilliseconds(turn.StartedAt, backendStartEnd, terminal, now)
		}
		turn.ActiveDurationMS = stageElapsedMilliseconds(turn.FirstEventAt, turn.BackendFinishedAt, terminal, now)
		turn.FinalizeDurationMS = stageElapsedMilliseconds(turn.BackendFinishedAt, turn.FinishedAt, terminal, now)
	}
}

func stageElapsedMilliseconds(start, end time.Time, terminal bool, now time.Time) int64 {
	if start.IsZero() || end.IsZero() && terminal {
		return 0
	}
	return elapsedMilliseconds(start, end, now)
}

func elapsedMilliseconds(start, end, now time.Time) int64 {
	if start.IsZero() {
		return 0
	}
	if end.IsZero() {
		end = now
	}
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func turnContextPercent(turn domain.Turn) float64 {
	if turn.ContextWindow <= 0 || usageNumber(turn.Usage, "context_inconsistent") > 0 {
		return 0
	}
	input := usageNumber(turn.Usage, "context_tokens")
	if input <= 0 {
		return 0
	}
	percent := input / float64(turn.ContextWindow) * 100
	if percent > 100 {
		percent = 100
	}
	return float64(int(percent*10+0.5)) / 10
}

func (s *Server) applyTurnContextUsage(ctx context.Context, turns []domain.Turn) {
	backends := map[string]string{}
	for index := range turns {
		turn := &turns[index]
		if turn.Usage == nil {
			turn.Usage = map[string]any{}
		}
		rawInput := usageNumber(turn.Usage, "input_tokens")
		if rawInput <= 0 {
			if !turn.Status.Terminal() && turn.PromptChars > 0 {
				turn.Usage["context_tokens"] = float64(turn.PromptChars) / 4
			}
			continue
		}
		backend, ok := backends[turn.RuntimeConfigSnapshotID]
		if !ok {
			snapshot, err := s.store.RuntimeSnapshot(ctx, turn.RuntimeConfigSnapshotID)
			if err == nil {
				backend = snapshot.Backend
			}
			backends[turn.RuntimeConfigSnapshotID] = backend
		}
		contextTokens := contextTokensForUsage(backend, turn.Usage)
		turn.Usage["total_input_tokens"] = rawInput
		if turn.ContextWindow > 0 && contextTokens > float64(turn.ContextWindow) {
			delete(turn.Usage, "context_tokens")
			turn.Usage["context_inconsistent"] = float64(1)
			continue
		}
		turn.Usage["context_tokens"] = contextTokens
		delete(turn.Usage, "context_inconsistent")
	}
	s.applyCodexRuntimeContext(ctx, turns)
}

func (s *Server) applyCodexRuntimeContext(ctx context.Context, turns []domain.Turn) {
	if len(turns) == 0 {
		return
	}
	task, err := s.store.Task(ctx, turns[0].TaskID)
	if err != nil {
		return
	}
	workspace, err := s.runtimeWorkspace(ctx, task.WorkspaceID)
	if err != nil {
		return
	}
	sessions, err := s.store.ListBackendSessionsForTask(ctx, task.ID)
	if err != nil {
		return
	}
	byID := map[string]domain.BackendSession{}
	byAgent := map[string]domain.BackendSession{}
	for _, session := range sessions {
		if session.Backend != "codex" || session.Status != "active" {
			continue
		}
		byID[session.ID] = session
		byAgent[session.AgentID] = session
	}
	latest := map[string]int{}
	for index := range turns {
		current, ok := latest[turns[index].AgentID]
		if !ok || turns[index].Sequence > turns[current].Sequence {
			latest[turns[index].AgentID] = index
		}
	}
	samples := map[string]runtimeContextSample{}
	missing := map[string]bool{}
	for _, index := range latest {
		turn := &turns[index]
		session, ok := byID[turn.BackendSessionID]
		if !ok {
			session, ok = byAgent[turn.AgentID]
		}
		if !ok {
			continue
		}
		sample, cached := samples[session.ID]
		if !cached && !missing[session.ID] {
			var found bool
			sample, found = codexRuntimeContext(
				ctx, session, workspace, taskRuntimeWorkDir(task, workspace),
			)
			if found {
				samples[session.ID] = sample
			} else {
				missing[session.ID] = true
			}
		}
		if sample.ContextWindow <= 0 || sample.InputTokens <= 0 {
			continue
		}
		turn.ContextWindow = sample.ContextWindow
		if turn.Usage == nil {
			turn.Usage = map[string]any{}
		}
		if sample.InputTokens > float64(sample.ContextWindow) {
			delete(turn.Usage, "context_tokens")
			turn.Usage["context_inconsistent"] = float64(1)
			continue
		}
		turn.Usage["context_tokens"] = sample.InputTokens
		delete(turn.Usage, "context_inconsistent")
	}
}

func (s *Server) runtimeWorkspace(ctx context.Context, workspaceID string) (domain.Workspace, error) {
	item, err := s.store.Workspace(ctx, workspaceID)
	if err != nil {
		return domain.Workspace{}, err
	}
	if item.SSHCredentialRef != "" && s.secrets != nil {
		item.SSHPassword, _ = s.secrets.Get(item.SSHCredentialRef)
	}
	return item, nil
}

func taskRuntimeWorkDir(task domain.Task, workspace domain.Workspace) string {
	if task.TaskWorkspacePath != "" {
		return task.TaskWorkspacePath
	}
	return workspace.RootPath
}

func contextTokensForUsage(backend string, current map[string]any) float64 {
	currentInput := usageNumber(current, "input_tokens")
	if backend == "claude" {
		return currentInput + usageNumber(current, "cache_read_input_tokens") +
			usageNumber(current, "cache_creation_input_tokens")
	}
	return currentInput
}

func usageNumber(usage map[string]any, key string) float64 {
	switch value := usage[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		result, _ := value.Float64()
		return result
	default:
		return 0
	}
}

func (s *Server) submitMessage(writer http.ResponseWriter, request *http.Request) {
	s.submitMessageForAgent(writer, request, "main")
}

func (s *Server) submitAgentMessage(writer http.ResponseWriter, request *http.Request) {
	s.submitMessageForAgent(writer, request, request.PathValue("agent"))
}

func (s *Server) submitMessageForAgent(writer http.ResponseWriter, request *http.Request, agentID string) {
	var payload struct {
		Content       string   `json:"content"`
		AttachmentIDs []string `json:"attachment_ids"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	turn, err := s.app.SubmitAgentMessageWithAttachments(request.Context(), request.PathValue("id"), agentID, payload.Content, payload.AttachmentIDs)
	if err != nil {
		status := http.StatusBadRequest
		code := "submit_message_failed"
		if errors.Is(err, store.ErrActiveTurn) {
			status = http.StatusConflict
			code = "active_turn_exists"
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": code, "message": err.Error()})
		return
	}
	result := map[string]any{"ok": true, "queued": true}
	if turn.ID != "" {
		result["turn"] = turn
		result["started"] = true
	} else {
		result["started"] = false
	}
	writeJSON(writer, http.StatusAccepted, result)
}

func (s *Server) taskAgents(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if _, err := s.store.Task(request.Context(), taskID); err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	agents, err := s.app.TaskAgents(request.Context(), taskID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_task_agents_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "agents": agents})
}

func (s *Server) updateTaskCollaboration(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		CollaborationMode string `json:"collaboration_mode"`
		MaxAgents         int    `json:"max_agents"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	task, err := s.app.UpdateTaskCollaboration(
		request.Context(), request.PathValue("id"), payload.CollaborationMode, payload.MaxAgents,
	)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "update_collaboration_failed", "message": err.Error(),
		})
		return
	}
	s.audit(request, "task.update_collaboration", "task", task.ID, map[string]any{
		"collaboration_mode": task.CollaborationMode, "max_agents": task.MaxAgents,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "task": task})
}

func (s *Server) updateAgentConfig(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Backend         string `json:"backend"`
		ModelSource     string `json:"model_source"`
		ModelID         string `json:"model_id"`
		WireModel       string `json:"wire_model"`
		CodexAccountID  string `json:"codex_account_id"`
		ReasoningEffort string `json:"reasoning_effort"`
		Filesystem      string `json:"filesystem"`
		Approval        string `json:"approval"`
		ProxyEnabled    *bool  `json:"proxy_enabled"`
		InheritMain     *bool  `json:"inherit_main"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	agent, err := s.app.UpdateAgentConfig(
		request.Context(), request.PathValue("id"), request.PathValue("agent"),
		app.UpdateAgentConfigInput{
			Backend: payload.Backend, ModelSource: payload.ModelSource, ModelID: payload.ModelID,
			WireModel: payload.WireModel, CodexAccountID: payload.CodexAccountID, ReasoningEffort: payload.ReasoningEffort,
			Filesystem: payload.Filesystem, Approval: payload.Approval, ProxyEnabled: payload.ProxyEnabled, InheritMain: payload.InheritMain,
		},
	)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "update_agent_failed", "message": err.Error(),
		})
		return
	}
	s.audit(request, "agent.update_config", "task_agent", agent.TaskID+":"+agent.AgentID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "agent": agent})
}

func (s *Server) compactAgentSession(writer http.ResponseWriter, request *http.Request) {
	s.rotateAgentSession(writer, request, true)
}

func (s *Server) resetAgentSession(writer http.ResponseWriter, request *http.Request) {
	s.rotateAgentSession(writer, request, false)
}

func (s *Server) rotateAgentSession(writer http.ResponseWriter, request *http.Request, compact bool) {
	taskID, agentID := request.PathValue("id"), request.PathValue("agent")
	var session domain.BackendSession
	var err error
	action := "reset"
	if compact {
		action = "compact"
		session, err = s.app.CompactAgentSession(request.Context(), taskID, agentID)
	} else {
		session, err = s.app.ResetAgentSession(request.Context(), taskID, agentID)
	}
	if err != nil {
		status := http.StatusBadRequest
		code := "session_" + action + "_failed"
		if errors.Is(err, store.ErrActiveTurn) {
			status = http.StatusConflict
			code = "active_turn_exists"
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": code, "message": err.Error()})
		return
	}
	s.audit(request, "agent.session_"+action, "task_agent", taskID+":"+agentID, map[string]any{
		"old_backend_session_id": session.ID,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "agent_id": agentID, "old_backend_session_id": session.ID, "status": session.Status,
	})
}

func (s *Server) updateTaskTitle(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	var payload struct {
		Title             *string          `json:"title"`
		CollaborationMode *string          `json:"collaboration_mode"`
		MaxAgents         *int             `json:"max_agents"`
		KnowledgePolicy   *string          `json:"knowledge_policy"`
		SkillIDs          *[]string        `json:"skill_ids"`
		AgentCapabilities *map[string]bool `json:"agent_capabilities"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if payload.Title == nil && payload.CollaborationMode == nil && payload.MaxAgents == nil && payload.KnowledgePolicy == nil && payload.SkillIDs == nil && payload.AgentCapabilities == nil {
		writeError(writer, http.StatusBadRequest, "task_update_required")
		return
	}
	task, err := s.store.Task(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	if payload.Title != nil {
		title := strings.TrimSpace(*payload.Title)
		if title == "" {
			writeError(writer, http.StatusBadRequest, "task_title_required")
			return
		}
		if err := s.store.UpdateTaskTitle(request.Context(), id, title, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			writeError(writer, http.StatusInternalServerError, "update_task_title_failed")
			return
		}
	}
	if payload.CollaborationMode != nil || payload.MaxAgents != nil {
		mode := task.CollaborationMode
		maxAgents := task.MaxAgents
		if payload.CollaborationMode != nil {
			mode = *payload.CollaborationMode
		}
		if payload.MaxAgents != nil {
			maxAgents = *payload.MaxAgents
		}
		if _, err := s.app.UpdateTaskCollaboration(request.Context(), id, mode, maxAgents); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{
				"ok": false, "error": "update_collaboration_failed", "message": err.Error(),
			})
			return
		}
	}
	if payload.KnowledgePolicy != nil {
		policy := strings.TrimSpace(*payload.KnowledgePolicy)
		if policy != "inherit" && policy != "enabled" && policy != "disabled" {
			writeError(writer, http.StatusBadRequest, "invalid_knowledge_policy")
			return
		}
		if err := s.store.UpdateTaskKnowledgePolicy(request.Context(), id, policy, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			writeError(writer, http.StatusInternalServerError, "update_knowledge_policy_failed")
			return
		}
	}
	if payload.SkillIDs != nil {
		if _, err := s.app.UpdateTaskSkills(request.Context(), id, *payload.SkillIDs); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "update_task_skills_failed", "message": err.Error()})
			return
		}
	}
	if payload.AgentCapabilities != nil {
		capabilities := normalizeTaskAgentCapabilities(*payload.AgentCapabilities)
		if err := s.store.UpdateTaskAgentCapabilities(request.Context(), id, capabilities, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			writeError(writer, http.StatusInternalServerError, "update_agent_capabilities_failed")
			return
		}
	}
	task, err = s.store.Task(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	s.audit(request, "task.update", "task", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "task": task})
}

func normalizeTaskAgentCapabilities(input map[string]bool) map[string]bool {
	result := map[string]bool{}
	for _, name := range []string{"workspace_read", "task_create", "clone_hardware"} {
		if input[name] {
			result[name] = true
		}
	}
	if result["clone_hardware"] {
		result["task_create"] = true
	}
	if result["task_create"] {
		result["workspace_read"] = true
	}
	return result
}

func (s *Server) completeTask(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if err := s.app.CompleteTask(request.Context(), taskID); err != nil {
		status := http.StatusBadRequest
		code := "complete_task_failed"
		if errors.Is(err, store.ErrActiveTurn) {
			status = http.StatusConflict
			code = "active_turn_exists"
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": code, "message": err.Error()})
		return
	}
	s.audit(request, "task.complete", "task", taskID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) reopenTask(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if err := s.app.ReopenTask(request.Context(), taskID); err != nil {
		status := http.StatusBadRequest
		code := "reopen_task_failed"
		if errors.Is(err, store.ErrActiveTurn) {
			status = http.StatusConflict
			code = "active_turn_exists"
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": code, "message": err.Error()})
		return
	}
	s.audit(request, "task.reopen", "task", taskID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) interruptTurn(writer http.ResponseWriter, request *http.Request) {
	turnID := request.PathValue("id")
	if err := s.app.InterruptTurn(request.Context(), turnID); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "interrupt_turn_failed", "message": err.Error()})
		return
	}
	s.audit(request, "turn.interrupt", "turn", turnID, nil)
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) interruptRound(writer http.ResponseWriter, request *http.Request) {
	roundID := request.PathValue("id")
	if err := s.app.InterruptRound(request.Context(), roundID); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "interrupt_round_failed", "message": err.Error()})
		return
	}
	s.audit(request, "round.interrupt", "round", roundID, nil)
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) allEvents(writer http.ResponseWriter, request *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusInternalServerError, "streaming_unsupported")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	channel, unsubscribe := s.app.Hub().SubscribeAll()
	defer unsubscribe()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event := <-channel:
			writeSSE(writer, event)
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(writer, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) taskEvents(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if _, err := s.store.Task(request.Context(), taskID); err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusInternalServerError, "streaming_unsupported")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	channel, unsubscribe := s.app.Hub().Subscribe(taskID)
	defer unsubscribe()
	after := queryInt(request, "after", 0)
	if lastID := strings.TrimSpace(request.Header.Get("Last-Event-ID")); lastID != "" {
		if value, err := strconv.ParseInt(lastID, 10, 64); err == nil && value > after {
			after = value
		}
	}
	cursor := after
	for {
		history, _ := s.store.EventsAfter(request.Context(), "task", taskID, cursor, 500)
		for _, event := range history {
			writeSSE(writer, event)
			cursor = event.Sequence
		}
		if len(history) < 500 {
			break
		}
	}
	flusher.Flush()
	heartbeat := time.NewTicker(time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event := <-channel:
			if event.Sequence <= cursor {
				continue
			}
			writeSSE(writer, event)
			cursor = event.Sequence
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(writer, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(writer http.ResponseWriter, event any) {
	data, _ := json.Marshal(event)
	if item, ok := event.(domain.Event); ok && item.Sequence > 0 {
		_, _ = fmt.Fprintf(writer, "id: %d\nevent: update\ndata: %s\n\n", item.Sequence, data)
		return
	}
	_, _ = fmt.Fprintf(writer, "event: update\ndata: %s\n\n", data)
}
