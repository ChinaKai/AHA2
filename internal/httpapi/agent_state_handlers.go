package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

func (s *Server) agentCapabilitiesInfo(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	call, err := s.app.AgentCallContext(request.Context(), claims, false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "capabilities": s.agentCapabilityMap(request.Context(), claims, call)})
}

// agentCapabilityMap is shared by the HTTP capabilities endpoint and the core
// MCP capabilities tool, so both surfaces report the same permission facts.
func (s *Server) agentCapabilityMap(ctx context.Context, claims agentapi.Claims, call app.AgentCallContext) map[string]bool {
	main := call.Turn.AgentID == "main"
	knowledgePublish := s.app.AgentKnowledgePublishAllowed(ctx, call)
	boundKnowledgeContribute, _ := s.store.HasContributingKnowledgeBinding(ctx, call.Project.ID)
	channelOutreach := false
	channelReply := false
	channelReplyDecision := false
	if main && s.channels != nil {
		channelReply = true
		_, _, channelErr := s.channels.AgentTaskChannelContacts(ctx, claims)
		channelOutreach = channelErr == nil
	}
	if s.store != nil {
		channelReplyDecision, _ = s.store.HasActiveChannelTaskRoute(ctx, call.Task.ID)
	}
	return map[string]bool{
		"core_mcp": true, "core_mcp_write": s.app.AgentWriteAllowed(ctx, call),
		"knowledge_mcp":       s.app.AgentKnowledgeMCPAllowed(ctx, call),
		"workspace_mcp":       s.app.AgentWorkspaceMCPAllowed(ctx, call),
		"browser_mcp":         s.app.AgentBrowserMCPAllowed(ctx, call),
		"hardware_mcp":        s.app.AgentHardwareMCPAllowed(ctx, call),
		"hardware_mcp_write":  s.app.AgentHardwareMCPWriteAllowed(ctx, call),
		"workspace_mcp_write": s.app.AgentWorkspaceMCPWriteAllowed(ctx, call),
		"desktop_mcp":         s.app.AgentDesktopMCPAllowed(ctx, call),
		"desktop_mcp_write":   s.app.AgentDesktopMCPWriteAllowed(ctx, call),
		"browser_mcp_write":   s.app.AgentBrowserMCPWriteAllowed(ctx, call),
		"progress":            true, "live_inbox_wait": true, "knowledge_read": true, "knowledge_feedback": true,
		"memory_update": main, "knowledge_publish": knowledgePublish, "knowledge_contribute_bound": knowledgePublish && boundKnowledgeContribute, "skill_create": main, "skill_update": main,
		// Reported separately so a caller can tell whether a global skill is
		// possible before sending one and being refused.
		"skill_create_global": main && s.app.AgentGlobalSkillAllowed(ctx, call),
		// These three project-level abilities are available to Main by default;
		// the Task-stored grants stay in the schema for compatibility only.
		"workspace_read": main, "task_create": main, "clone_hardware": main,
		"collaboration":    main && call.Task.CollaborationMode == "auto",
		"channel_outreach": channelOutreach, "channel_reply": channelReply, "channel_reply_decision": channelReplyDecision,
		"hardware": true, "managed_process": true,
		"browser": s.browser != nil,
		"desktop": main && s.desktop.Status(call.Task.ID).Supported,
	}
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
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "agent_task_invalid", err.Error())
		return
	}
	if strings.TrimSpace(payload.WorkspaceID) == "" || strings.TrimSpace(payload.Title) == "" || strings.TrimSpace(payload.Request) == "" {
		writeErrorDetail(writer, http.StatusBadRequest, "agent_task_invalid", "workspace_id, title and request are required")
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
	var payload app.MemoryUpdate
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if payload.Empty() {
		writeError(writer, http.StatusBadRequest, "memory_update_empty")
		return
	}
	memory, err := s.app.UpdateAgentMemory(request.Context(), claims, payload)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	s.audit(request, "agent.memory.update", "task", claims.TaskID, map[string]any{"goal_changed": payload.CurrentGoal != nil})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "memory": compactTaskMemory(memory)})
}

func (s *Server) addAgentProgressMessage(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		Message       string   `json:"message"`
		AttachmentIDs []string `json:"attachment_ids"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := s.app.AddAgentProgress(request.Context(), claims, payload.Message, payload.AttachmentIDs); err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true})
}

func (s *Server) waitAgentInbox(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		AfterCursor    int64 `json:"after_cursor"`
		TimeoutSeconds int   `json:"timeout_seconds"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "inbox_wait_invalid", err.Error())
		return
	}
	if payload.AfterCursor < 0 || payload.TimeoutSeconds < 0 || payload.TimeoutSeconds > 30 {
		writeErrorDetail(writer, http.StatusBadRequest, "inbox_wait_invalid", "after_cursor must be non-negative and timeout_seconds must be 0-30")
		return
	}
	messages, nextCursor, err := s.app.WaitAgentInbox(
		request.Context(), claims, payload.AfterCursor, time.Duration(payload.TimeoutSeconds)*time.Second,
	)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "messages": messages, "next_cursor": nextCursor,
	})
}

func (s *Server) claimAgentInbox(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		Limit int `json:"limit"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "inbox_claim_invalid", err.Error())
		return
	}
	if payload.Limit < 0 || payload.Limit > 50 {
		writeErrorDetail(writer, http.StatusBadRequest, "inbox_claim_invalid", "limit must be 0-50")
		return
	}
	claim, err := s.app.ClaimAgentInbox(request.Context(), claims, payload.Limit)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "messages": claim.Messages, "next_cursor": claim.NextCursor,
		"injection_text":           claim.InjectionText,
		"materialized_attachments": claim.MaterializedAttachments,
	})
}

func (s *Server) ackAgentInbox(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		MessageIDs []string `json:"message_ids"`
		Outcome    string   `json:"outcome"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "inbox_ack_invalid", err.Error())
		return
	}
	count, err := s.app.AckAgentInbox(
		request.Context(), claims, payload.MessageIDs, strings.TrimSpace(payload.Outcome),
	)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "acknowledged": count, "outcome": strings.TrimSpace(payload.Outcome),
	})
}

func (s *Server) setAgentChannelReplyDecision(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		Decision         string `json:"decision"`
		InboundReceiptID string `json:"inbound_receipt_id"`
		Reason           string `json:"reason"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := s.app.SetAgentChannelReplyDecision(
		request.Context(), claims, strings.TrimSpace(payload.Decision),
		strings.TrimSpace(payload.InboundReceiptID), strings.TrimSpace(payload.Reason),
	); err != nil {
		writeAgentControlError(writer, err)
		return
	}
	s.audit(request, "agent.channel.reply_decision", "turn", claims.TurnID, map[string]any{
		"decision": payload.Decision, "reason_chars": len([]rune(strings.TrimSpace(payload.Reason))),
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "decision": payload.Decision, "inbound_receipt_id": payload.InboundReceiptID,
	})
}

func (s *Server) agentKnowledgeGuide(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeError(writer, http.StatusNotFound, "knowledge_guide_not_found")
		return
	}
	// The guide is resolved by its fixed slug (or, when an id is supplied, by
	// checking that the indexed entry really is the root guide). It is never
	// resolved by a filesystem path from the client.
	query := request.URL.Query()
	if slug := strings.TrimSpace(query.Get("slug")); slug != "" && slug != "knowledge-base-guide" {
		writeError(writer, http.StatusNotFound, "knowledge_guide_not_found")
		return
	}
	if id := strings.TrimSpace(query.Get("id")); id != "" {
		found := false
		_ = s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
			entries, err := reader.Entries(request.Context(), "global", "")
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.ID == id && entry.Path == knowledgevault.GuideRelativePath {
					found = true
				}
			}
			return nil
		})
		if !found {
			writeError(writer, http.StatusNotFound, "knowledge_guide_not_found")
			return
		}
	}
	guide, err := s.knowledgeVault.Guide()
	if err != nil {
		if errors.Is(err, knowledgevault.ErrGuideNotFound) {
			writeError(writer, http.StatusNotFound, "knowledge_guide_not_found")
			return
		}
		writeErrorDetail(writer, http.StatusInternalServerError, "knowledge_guide_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "guide": guide})
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
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "knowledge_candidates_invalid", err.Error())
		return
	}
	if len(payload.Candidates) == 0 || len(payload.Candidates) > 20 {
		writeErrorDetail(writer, http.StatusBadRequest, "knowledge_candidates_invalid", "candidates must contain 1 to 20 entries")
		return
	}
	items, proposals, err := s.app.SubmitAgentKnowledgeProposals(request.Context(), claims, payload.Candidates)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	s.audit(request, "agent.knowledge.submit", "task", claims.TaskID, map[string]any{"count": len(items)})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "knowledge": items, "proposals": proposals})
}

func (s *Server) submitAgentKnowledgeFeedback(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		Kind string `json:"kind"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	item, err := s.app.SubmitAgentKnowledgeFeedback(request.Context(), claims, app.KnowledgeFeedback{EntryID: request.PathValue("knowledge"), Kind: payload.Kind})
	if err != nil {
		writeAgentKnowledgeWriteError(writer, err)
		return
	}
	s.audit(request, "agent.knowledge.feedback", "knowledge", item.ID, map[string]any{"kind": payload.Kind})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "knowledge": item})
}

// submitAgentKnowledgeEntry is the Phase 1 vault write API. The write source is
// decided by the application layer from the read source; this handler only
// decodes input and maps structured errors to status codes.
func (s *Server) submitAgentKnowledgeEntry(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload app.AgentKnowledgeEntryInput
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := s.app.SubmitAgentKnowledgeEntry(request.Context(), claims, payload)
	if err != nil {
		writeAgentKnowledgeWriteError(writer, err)
		return
	}
	status := http.StatusOK
	if payload.Mode == "" || strings.EqualFold(payload.Mode, "create") {
		status = http.StatusCreated
	}
	s.audit(request, "agent.knowledge.entry", "task", claims.TaskID, map[string]any{
		"source": result.Source, "mode": payload.Mode,
	})
	body := map[string]any{"ok": true, "source": result.Source}
	if result.Entry != nil {
		body["entry"] = result.Entry
	}
	if result.Knowledge != nil {
		body["knowledge"] = result.Knowledge
	}
	if result.Proposals != nil {
		body["proposals"] = result.Proposals
	}
	writeJSON(writer, status, body)
}

func (s *Server) submitAgentKnowledgeFeedbackEntry(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload app.AgentKnowledgeFeedbackInput
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := s.app.SubmitAgentKnowledgeFeedbackEntry(request.Context(), claims, payload)
	if err != nil {
		writeAgentKnowledgeWriteError(writer, err)
		return
	}
	s.audit(request, "agent.knowledge.feedback_entry", "knowledge", payload.EntryID, map[string]any{
		"source": result.Source, "kind": payload.Kind,
	})
	body := map[string]any{"ok": true, "source": result.Source}
	if result.Feedback != nil {
		body["feedback"] = result.Feedback
	}
	if result.Knowledge != nil {
		body["knowledge"] = result.Knowledge
	}
	writeJSON(writer, http.StatusOK, body)
}

func writeAgentKnowledgeWriteError(writer http.ResponseWriter, err error) {
	var writeErr *knowledgevault.WriteError
	if errors.As(err, &writeErr) {
		switch writeErr.Code {
		case knowledgevault.WriteErrorDuplicate:
			writeJSON(writer, http.StatusConflict, map[string]any{
				"ok": false, "error": writeErr.Code, "message": writeErr.Message,
				"duplicate_candidates": writeErr.Candidates,
			})
			return
		case knowledgevault.WriteErrorRevisionConflict, knowledgevault.WriteErrorIndexStale, knowledgevault.WriteErrorDisabled:
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": writeErr.Code, "message": writeErr.Message})
			return
		case knowledgevault.WriteErrorBudget:
			writeJSON(writer, http.StatusTooManyRequests, map[string]any{"ok": false, "error": writeErr.Code, "message": writeErr.Message})
			return
		case knowledgevault.WriteErrorNotFound:
			writeJSON(writer, http.StatusNotFound, map[string]any{"ok": false, "error": writeErr.Code, "message": writeErr.Message})
			return
		case knowledgevault.WriteErrorForbiddenScope:
			writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": writeErr.Code, "message": writeErr.Message})
			return
		case knowledgevault.WriteErrorProjectUnbound, knowledgevault.WriteErrorUnavailable:
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": writeErr.Code, "message": writeErr.Message})
			return
		default:
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": writeErr.Code, "message": writeErr.Message})
			return
		}
	}
	switch {
	case errors.Is(err, knowledgevault.ErrDisabled), errors.Is(err, knowledgevault.ErrVaultNotInitialized):
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "knowledge_vault_unavailable", "message": err.Error(),
		})
	default:
		writeAgentControlError(writer, err)
	}
}

func (s *Server) submitAgentCollaboration(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		Actions                 []app.AgentAction `json:"actions"`
		IntegrationInstructions string            `json:"integration_instructions"`
		MainFollowup            string            `json:"main_followup"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "collaboration_batch_invalid", err.Error())
		return
	}
	if len(payload.Actions) == 0 || len(payload.Actions) > 8 {
		writeErrorDetail(writer, http.StatusBadRequest, "collaboration_batch_invalid", "actions must contain 1 to 8 entries")
		return
	}
	for index := range payload.Actions {
		if !payload.Actions[index].Required {
			payload.Actions[index].Required = true
		}
	}
	instructions := strings.TrimSpace(payload.IntegrationInstructions)
	if instructions == "" {
		instructions = payload.MainFollowup
	}
	batchID, created, err := s.app.SubmitAgentCollaboration(request.Context(), claims, payload.Actions, instructions)
	if err != nil {
		var rejected *app.CollaborationAssignmentError
		if errors.As(err, &rejected) {
			// Rejected actions must never be reported as a successful dispatch.
			// A partial batch still returns the batch that was created, so the
			// caller can keep the accepted work and only re-dispatch the
			// rejected agents in a new Turn.
			body := map[string]any{
				"ok":              false,
				"error":           app.CollaborationAssignmentDuplicate,
				"message":         err.Error(),
				"created_agents":  rejected.Created,
				"rejected_agents": rejected.Rejected,
			}
			if rejected.BatchID != "" {
				body["batch_id"] = rejected.BatchID
				body["accepted_agents"] = rejected.Accepted
			}
			s.audit(request, "agent.collaboration.rejected", "turn", claims.TurnID, map[string]any{
				"batch_id": rejected.BatchID, "accepted": rejected.Created, "rejected": len(rejected.Rejected),
			})
			writeJSON(writer, http.StatusConflict, body)
			return
		}
		writeAgentControlError(writer, err)
		return
	}
	s.audit(request, "agent.collaboration.submit", "turn", claims.TurnID, map[string]any{"created": created})
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "batch_id": batchID, "created_agents": created})
}

func (s *Server) waitAgentCollaboration(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload struct {
		AfterCursor    int64 `json:"after_cursor"`
		TimeoutSeconds int   `json:"timeout_seconds"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "collaboration_wait_invalid", err.Error())
		return
	}
	if payload.AfterCursor < 0 || payload.TimeoutSeconds < 0 || payload.TimeoutSeconds > 30 {
		writeErrorDetail(writer, http.StatusBadRequest, "collaboration_wait_invalid", "after_cursor must be non-negative and timeout_seconds must be 0-30")
		return
	}
	timeout := time.Duration(payload.TimeoutSeconds) * time.Second
	progress, err := s.app.WaitAgentCollaboration(request.Context(), claims, request.PathValue("batch"), payload.AfterCursor, timeout)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "progress": progress})
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

func (s *Server) createAgentSkill(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	var payload app.AgentSkillCreateInput
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "skill_create_invalid", err.Error())
		return
	}
	item, err := s.app.CreateAgentSkill(request.Context(), claims, payload)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	s.audit(request, "agent.skill.create", "skill", item.ID, map[string]any{"version": item.Version, "project_id": item.ProjectID})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "skill": item})
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
	var payload struct {
		BaseVersion int                `json:"base_version"`
		Name        string             `json:"name"`
		Description string             `json:"description"`
		Files       []domain.SkillFile `json:"files"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "skill_update_invalid")
		return
	}
	if payload.BaseVersion <= 0 || len(payload.Files) == 0 {
		writeError(writer, http.StatusBadRequest, "skill_update_invalid")
		return
	}
	// Keep the historical permission boundary: an Agent may update only a Skill
	// selected on its own Task. Switching to the vault must not expose every
	// global package to any Turn that happens to know its key.
	selected, _, err := s.app.SelectedAgentSkills(request.Context(), claims)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	allowed := false
	for _, item := range selected {
		if item.ID == request.PathValue("skill") {
			allowed = true
			break
		}
	}
	if !allowed {
		writeError(writer, http.StatusNotFound, "skill_not_found")
		return
	}
	files := make([]knowledgevault.SkillPackageFile, 0, len(payload.Files))
	for _, file := range payload.Files {
		files = append(files, knowledgevault.SkillPackageFile{Path: file.Path, Content: file.Content})
	}
	updated, err := s.app.UpdateVaultSkill(request.Context(), request.PathValue("skill"), app.VaultSkillWriteInput{
		ProjectID: call.Project.ID, BaseVersion: payload.BaseVersion,
		Name: strings.TrimSpace(payload.Name), Description: strings.TrimSpace(payload.Description),
		Files: files, AllowGlobal: true,
	})
	if err != nil {
		writeAgentSkillWriteError(writer, err)
		return
	}
	s.audit(request, "agent.skill.update", "vault_skill", updated.ID, map[string]any{"version": updated.Version, "files": len(files)})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "skill": updated, "files": updated.PackageFiles})
}

// writeAgentSkillWriteError maps a vault skill write failure to the stable
// Agent Control API error shape. The legacy in-place update returned a version
// conflict as 409 and everything else as 400; keep that contract.
func writeAgentSkillWriteError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrVaultSkillNotFound):
		writeError(writer, http.StatusNotFound, "skill_not_found")
	case errors.Is(err, app.ErrVaultSkillForbidden):
		writeError(writer, http.StatusForbidden, "skill_scope_forbidden")
	case errors.Is(err, app.ErrVaultSkillReadOnly):
		writeError(writer, http.StatusForbidden, "skill_read_only")
	}
	var writeErr *knowledgevault.SkillWriteError
	if errors.As(err, &writeErr) {
		switch writeErr.Code {
		case knowledgevault.SkillWriteErrorConflictCode():
			writeJSON(writer, http.StatusConflict, map[string]any{
				"ok": false, "error": "skill_version_conflict", "current_version": writeErr.CurrentVersion,
			})
			return
		case knowledgevault.SkillWriteErrorNotFoundCode():
			writeError(writer, http.StatusNotFound, "skill_not_found")
			return
		case knowledgevault.SkillWriteErrorForbiddenCode():
			writeError(writer, http.StatusForbidden, "skill_scope_forbidden")
			return
		case knowledgevault.SkillWriteErrorDuplicateCode():
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "skill_already_exists", "message": writeErr.Message})
			return
		}
	}
	writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "skill_update_failed", "message": err.Error()})
}

func writeAgentControlError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrAgentCallForbidden):
		writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
	case errors.Is(err, app.ErrGlobalSkillForbidden):
		writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "global_skill_forbidden", "message": "全局 Skill 会影响本机所有 Project，只能在 Main 且非受限群聊路由的 Turn 中创建；请改用 scope=\"project\""})
	case errors.Is(err, app.ErrKnowledgeReadOnly):
		writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "knowledge_entry_read_only", "message": "该知识来自外部引用型绑定；请切换为项目协作型，或在来源 Project 中提交修订"})
	case errors.Is(err, app.ErrKnowledgeReadSourceMismatch):
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "knowledge_read_source_mismatch", "message": err.Error()})
	case errors.Is(err, app.ErrAgentTurnInactive):
		writeError(writer, http.StatusConflict, "agent_turn_inactive")
	case errors.Is(err, app.ErrAgentCollaborationRequiresAuto):
		writeJSON(writer, http.StatusForbidden, map[string]any{
			"ok": false, "error": "collaboration_requires_auto_mode",
			"message": "单 Agent 模式没有可派发的子 Agent；请由 Owner 切换到自动协作模式",
		})
	case errors.Is(err, app.ErrAgentLimitReached):
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "agent_limit_reached",
			"message": "已达到该 Task 的 Agent 数量上限；请调整 Max Agents 或复用已有 Agent",
		})
	case errors.Is(err, app.ErrRevisionConflict), errors.Is(err, store.ErrKnowledgeProposalRevision), errors.Is(err, store.ErrKnowledgeProposalPending):
		writeError(writer, http.StatusConflict, "knowledge_revision_conflict")
	case errors.Is(err, store.ErrKnowledgeFeedbackState):
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "knowledge_feedback_conflict", "message": "该 revision 已标记为过时或错误，请提交修订候选，或由 Owner 确认当前内容仍然有效"})
	default:
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "agent_operation_failed", "message": err.Error()})
	}
}
