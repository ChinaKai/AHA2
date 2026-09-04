package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "tasks": items})
}

func (s *Server) createTask(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ProjectID         string `json:"project_id"`
		WorkspaceID       string `json:"workspace_id"`
		Title             string `json:"title"`
		Request           string `json:"request"`
		TargetBranch      string `json:"target_branch"`
		BaseCommit        string `json:"base_commit"`
		TaskBranch        string `json:"task_branch"`
		Isolation         string `json:"isolation"`
		WorktreeDir       string `json:"worktree_dir"`
		ModelID           string `json:"model_id"`
		ReasoningEffort   string `json:"reasoning_effort"`
		Filesystem        string `json:"filesystem"`
		Approval          string `json:"approval"`
		CollaborationMode string `json:"collaboration_mode"`
		MaxAgents         int    `json:"max_agents"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
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
		Isolation: payload.Isolation, WorktreeDir: payload.WorktreeDir, ModelID: payload.ModelID, ReasoningEffort: payload.ReasoningEffort,
		Filesystem: filesystem, Approval: approval, CollaborationMode: payload.CollaborationMode,
		MaxAgents: payload.MaxAgents,
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
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	round, _ := s.store.LatestRound(request.Context(), taskID)
	turns, _ := s.store.TurnsForRound(request.Context(), round.ID)
	agents, _ := s.app.TaskAgents(request.Context(), taskID)
	memory, _ := s.store.TaskMemory(request.Context(), taskID)
	now := time.Now().UTC()
	applyRoundTiming(&round, now)
	applyTurnTimings(turns, now)
	s.applyTurnContextUsage(request.Context(), turns)
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "task": task, "latest_round": round, "turns": turns, "agents": agents, "memory": memory,
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
	if _, err := s.store.Task(request.Context(), taskID); err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	if _, err := s.store.TaskAgent(request.Context(), taskID, agentID); err != nil {
		writeError(writer, http.StatusNotFound, "agent_not_found")
		return
	}
	categories := []string{}
	for _, value := range strings.Split(request.URL.Query().Get("categories"), ",") {
		value = strings.TrimSpace(value)
		switch value {
		case "chat", "update", "tool", "error":
			categories = append(categories, value)
		}
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
		writeError(writer, http.StatusNotFound, "task_not_found")
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
	workspace, _ := s.store.Workspace(request.Context(), task.WorkspaceID)
	metrics := contextMetrics(latest, sessions, workspace)
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

func contextMetrics(turn domain.Turn, sessions []domain.BackendSession, workspace domain.Workspace) map[string]any {
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
		isCurrent := active.ID != "" && session.ID == active.ID
		if isCurrent && session.ID == turn.BackendSessionID && len(turn.Usage) > 0 {
			usage = turn.Usage
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
	sessionSize, sessionExists := backendSessionArtifactSize(active, workspace)
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
		"session_size_bytes":      sessionSize,
		"session_exists":          sessionExists,
		"session_active":          active.ID != "",
		"session_id":              active.ProviderSession,
		"session_status":          sessionStatus,
		"session_count":           len(sessions),
	}
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

func backendSessionArtifactSize(session domain.BackendSession, workspace domain.Workspace) (int64, bool) {
	if session.ProviderSession == "" || workspace.Transport != "native" {
		return 0, false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, false
	}
	var pattern string
	if session.Backend == "claude" {
		pattern = filepath.Join(home, ".claude", "projects", "*", "*"+session.ProviderSession+"*.jsonl")
	} else {
		pattern = filepath.Join(home, ".codex", "sessions", "*", "*", "*", "*"+session.ProviderSession+"*.jsonl")
	}
	matches, _ := filepath.Glob(pattern)
	for _, path := range matches {
		if stat, err := os.Stat(path); err == nil {
			return stat.Size(), true
		}
	}
	return 0, false
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
		turn.ElapsedMS = elapsedMilliseconds(turn.QueuedAt, turn.FinishedAt, now)
		if !turn.PreparedAt.IsZero() {
			turn.QueueDurationMS = elapsedMilliseconds(turn.QueuedAt, turn.PreparedAt, now)
			turn.PrepareDurationMS = elapsedMilliseconds(turn.PreparedAt, turn.StartedAt, now)
		} else if turn.Status == domain.TurnQueued {
			turn.QueueDurationMS = elapsedMilliseconds(turn.QueuedAt, time.Time{}, now)
		}
		if !turn.StartedAt.IsZero() {
			turn.RunDurationMS = elapsedMilliseconds(turn.StartedAt, turn.FinishedAt, now)
		}
	}
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
	if turn.ContextWindow <= 0 {
		return 0
	}
	input := usageNumber(turn.Usage, "context_tokens")
	if input <= 0 {
		input = usageNumber(turn.Usage, "input_tokens")
		input += usageNumber(turn.Usage, "cache_read_input_tokens")
		input += usageNumber(turn.Usage, "cache_creation_input_tokens")
	}
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
		totalInput := usageNumber(turn.Usage, "input_tokens")
		if totalInput <= 0 {
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
		contextTokens := totalInput
		var previous map[string]any
		if backend == "codex" {
			previousTurn, err := s.store.PreviousTurnForSession(ctx, *turn)
			if err == nil {
				previous = previousTurn.Usage
				if !turn.Status.Terminal() && totalInput <= usageNumber(previous, "input_tokens") {
					var before map[string]any
					if beforeTurn, beforeErr := s.store.PreviousTurnForSession(ctx, previousTurn); beforeErr == nil {
						before = beforeTurn.Usage
					}
					contextTokens = activeCodexContextTokens(totalInput, previous, before, turn.PromptChars)
					previous = nil
				}
			}
		}
		if previous != nil || backend != "codex" {
			contextTokens = contextTokensForUsage(backend, turn.Usage, previous)
		}
		turn.Usage["total_input_tokens"] = totalInput
		turn.Usage["context_tokens"] = contextTokens
	}
}

func activeCodexContextTokens(totalInput float64, previous, before map[string]any, promptChars int) float64 {
	previousInput := usageNumber(previous, "input_tokens")
	if delta := totalInput - previousInput; delta > 0 {
		return delta
	}
	previousContext := usageNumber(previous, "context_tokens")
	if previousContext <= 0 && len(before) > 0 {
		previousContext = contextTokensForUsage("codex", previous, before)
	}
	promptEstimate := float64(promptChars) / 4
	if previousContext > 0 {
		return previousContext + promptEstimate
	}
	return promptEstimate
}

func contextTokensForUsage(backend string, current, previous map[string]any) float64 {
	currentInput := usageNumber(current, "input_tokens")
	if backend != "codex" || len(previous) == 0 {
		return currentInput
	}
	if delta := currentInput - usageNumber(previous, "input_tokens"); delta > 0 {
		return delta
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
		Content string `json:"content"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	turn, err := s.app.SubmitAgentMessage(request.Context(), request.PathValue("id"), agentID, payload.Content)
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
		ModelID         string `json:"model_id"`
		ReasoningEffort string `json:"reasoning_effort"`
		Filesystem      string `json:"filesystem"`
		Approval        string `json:"approval"`
		InheritMain     *bool  `json:"inherit_main"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	agent, err := s.app.UpdateAgentConfig(
		request.Context(), request.PathValue("id"), request.PathValue("agent"),
		app.UpdateAgentConfigInput{
			Backend: payload.Backend, ModelID: payload.ModelID, ReasoningEffort: payload.ReasoningEffort,
			Filesystem: payload.Filesystem, Approval: payload.Approval, InheritMain: payload.InheritMain,
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
		Title             *string `json:"title"`
		CollaborationMode *string `json:"collaboration_mode"`
		MaxAgents         *int    `json:"max_agents"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if payload.Title == nil && payload.CollaborationMode == nil && payload.MaxAgents == nil {
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
	task, err = s.store.Task(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	s.audit(request, "task.update", "task", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "task": task})
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
