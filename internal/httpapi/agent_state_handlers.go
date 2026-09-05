package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) agentCapabilitiesInfo(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	call, err := s.app.AgentCallContext(request.Context(), claims, false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	main := call.Turn.AgentID == "main"
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "capabilities": map[string]bool{
		"progress": true, "knowledge_read": true, "knowledge_feedback": true,
		"memory_update": main, "knowledge_publish": main, "skill_update": main,
		"workspace_read": main && call.Task.AgentCapabilities["workspace_read"],
		"task_create":    main && call.Task.AgentCapabilities["task_create"],
		"clone_hardware": main && call.Task.AgentCapabilities["clone_hardware"],
		"collaboration":  main && call.Task.CollaborationMode == "auto",
		"hardware":       true, "managed_process": true,
	}})
}

func (s *Server) agentProjectWorkspaces(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	items, err := s.app.AgentProjectWorkspaces(request.Context(), claims)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "workspaces": items})
}

func (s *Server) agentProjectRuntimes(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	items, err := s.app.AgentProjectRuntimes(request.Context(), claims)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "runtimes": items})
}

func (s *Server) createAgentTask(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload app.AgentTaskCreateInput
	if decodeJSON(request, &payload) != nil || strings.TrimSpace(payload.WorkspaceID) == "" || strings.TrimSpace(payload.Title) == "" || strings.TrimSpace(payload.Request) == "" {
		writeError(writer, http.StatusBadRequest, "agent_task_invalid")
		return
	}
	item, turn, err := s.app.CreateAgentTask(request.Context(), claims, payload)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	s.audit(request, "agent.task.create", "task", item.ID, map[string]any{"workspace_id": item.WorkspaceID, "clone_hardware": payload.CloneHardware})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "task": item, "turn": turn})
}

func (s *Server) agentTaskStatus(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	item, turns, err := s.app.AgentProjectTask(request.Context(), claims, request.PathValue("task"))
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "task": item, "turns": turns})
}

func (s *Server) updateAgentMemory(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		Append app.MemoryPatch `json:"append"`
	}
	if decodeJSON(request, &payload) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if !validMemoryPatch(payload.Append) {
		writeError(writer, http.StatusBadRequest, "memory_patch_invalid")
		return
	}
	memory, err := s.app.UpdateAgentMemory(request.Context(), claims, payload.Append)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	s.audit(request, "agent.memory.update", "task", claims.TaskID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "memory": memory})
}

func (s *Server) addAgentProgressMessage(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		Message string `json:"message"`
	}
	if decodeJSON(request, &payload) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := s.app.AddAgentProgress(request.Context(), claims, payload.Message); err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true})
}

func (s *Server) agentKnowledge(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	items, err := s.app.ApplicableAgentKnowledge(request.Context(), claims)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "knowledge": items})
}

func (s *Server) agentKnowledgeEntry(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	items, err := s.app.ApplicableAgentKnowledge(request.Context(), claims)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	for _, item := range items {
		if item.ID == request.PathValue("knowledge") {
			writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "knowledge": item})
			return
		}
	}
	writeError(writer, http.StatusNotFound, "knowledge_not_found")
}

func (s *Server) submitAgentKnowledge(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		Candidates []app.KnowledgeCandidate `json:"candidates"`
	}
	if decodeJSON(request, &payload) != nil || len(payload.Candidates) == 0 || len(payload.Candidates) > 20 {
		writeError(writer, http.StatusBadRequest, "knowledge_candidates_invalid")
		return
	}
	items, err := s.app.SubmitAgentKnowledge(request.Context(), claims, payload.Candidates)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	s.audit(request, "agent.knowledge.submit", "task", claims.TaskID, map[string]any{"count": len(items)})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "knowledge": items})
}

func (s *Server) submitAgentKnowledgeFeedback(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		Kind string `json:"kind"`
	}
	if decodeJSON(request, &payload) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	item, err := s.app.SubmitAgentKnowledgeFeedback(request.Context(), claims, app.KnowledgeFeedback{EntryID: request.PathValue("knowledge"), Kind: payload.Kind})
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	s.audit(request, "agent.knowledge.feedback", "knowledge", item.ID, map[string]any{"kind": payload.Kind})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "knowledge": item})
}

func (s *Server) submitAgentCollaboration(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		Actions      []app.AgentAction `json:"actions"`
		MainFollowup string            `json:"main_followup"`
	}
	if decodeJSON(request, &payload) != nil || len(payload.Actions) == 0 || len(payload.Actions) > 8 {
		writeError(writer, http.StatusBadRequest, "collaboration_batch_invalid")
		return
	}
	for index := range payload.Actions {
		if !payload.Actions[index].Required {
			payload.Actions[index].Required = true
		}
	}
	created, err := s.app.SubmitAgentCollaboration(request.Context(), claims, payload.Actions, payload.MainFollowup)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	s.audit(request, "agent.collaboration.submit", "turn", claims.TurnID, map[string]any{"created": created})
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "created_agents": created})
}

func (s *Server) agentSkills(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	items, _, err := s.app.SelectedAgentSkills(request.Context(), claims)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "skills": items})
}

func (s *Server) agentSkill(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	items, _, err := s.app.SelectedAgentSkills(request.Context(), claims)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	for _, item := range items {
		if item.ID == request.PathValue("skill") {
			writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "skill": item, "files": item.PackageFiles})
			return
		}
	}
	writeError(writer, http.StatusNotFound, "skill_not_found")
}

func (s *Server) updateAgentSkill(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	call, err := s.app.AgentCallContext(request.Context(), claims, true)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	items, _, err := s.app.SelectedAgentSkills(request.Context(), claims)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	var skill domain.Skill
	for _, item := range items {
		if item.ID == request.PathValue("skill") {
			skill = item
			break
		}
	}
	if skill.ID == "" {
		writeError(writer, http.StatusNotFound, "skill_not_found")
		return
	}
	var payload struct {
		BaseVersion int                `json:"base_version"`
		Name        string             `json:"name"`
		Description string             `json:"description"`
		Files       []domain.SkillFile `json:"files"`
	}
	if decodeJSON(request, &payload) != nil || payload.BaseVersion <= 0 || len(payload.Files) == 0 {
		writeError(writer, http.StatusBadRequest, "skill_update_invalid")
		return
	}
	if skill.Scope == "project" && skill.ProjectID != call.Project.ID {
		writeError(writer, http.StatusForbidden, "skill_scope_forbidden")
		return
	}
	if strings.TrimSpace(payload.Name) != "" {
		skill.Name = strings.TrimSpace(payload.Name)
	}
	if strings.TrimSpace(payload.Description) != "" {
		skill.Description = strings.TrimSpace(payload.Description)
	}
	for _, file := range payload.Files {
		if file.Path == "SKILL.md" {
			parts := strings.SplitN(file.Content, "---", 3)
			if len(parts) == 3 {
				skill.Instructions = strings.TrimSpace(parts[2])
			}
		}
	}
	skill.UpdatedAt = time.Now().UTC()
	updated, err := s.store.UpdateSkillPackage(request.Context(), skill, payload.BaseVersion, payload.Files)
	if err != nil {
		if strings.Contains(err.Error(), "version conflict") {
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "skill_version_conflict", "current_version": skill.Version})
			return
		}
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "skill_update_failed", "message": err.Error()})
		return
	}
	s.audit(request, "agent.skill.update", "skill", updated.ID, map[string]any{"version": updated.Version, "files": len(payload.Files)})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "skill": updated})
}

func writeAgentControlError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrAgentCallForbidden):
		writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
	case errors.Is(err, app.ErrAgentTurnInactive):
		writeError(writer, http.StatusConflict, "agent_turn_inactive")
	case errors.Is(err, app.ErrRevisionConflict):
		writeError(writer, http.StatusConflict, "knowledge_revision_conflict")
	default:
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "agent_operation_failed", "message": err.Error()})
	}
}

func validMemoryPatch(patch app.MemoryPatch) bool {
	groups := [][]string{patch.Decisions, patch.Facts, patch.Excluded, patch.Progress, patch.Verification, patch.NextActions}
	count := 0
	for _, group := range groups {
		count += len(group)
		for _, value := range group {
			if strings.TrimSpace(value) == "" || len([]rune(value)) > 4000 {
				return false
			}
		}
	}
	return count > 0 && count <= 100
}
