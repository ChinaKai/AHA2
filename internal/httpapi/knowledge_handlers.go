package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func knowledgeContentHash(title, body string) string {
	value := sha256.Sum256([]byte(strings.TrimSpace(title) + "\n" + strings.TrimSpace(body)))
	return fmt.Sprintf("%x", value[:])
}

func (s *Server) listKnowledge(writer http.ResponseWriter, request *http.Request) {
	var statuses []domain.KnowledgeStatus
	for _, value := range strings.Split(request.URL.Query().Get("status"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			statuses = append(statuses, domain.KnowledgeStatus(value))
		}
	}
	scope, projectID := request.URL.Query().Get("scope"), request.URL.Query().Get("project_id")
	var items []domain.KnowledgeEntry
	var err error
	if scope == "project" && projectID != "" {
		items, err = s.store.ListProjectKnowledge(request.Context(), projectID, statuses)
	} else {
		items, err = s.store.ListKnowledge(request.Context(), scope, projectID, statuses)
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_knowledge_failed")
		return
	}
	proposals, err := s.store.ListKnowledgeProposals(request.Context(), request.URL.Query().Get("scope"), request.URL.Query().Get("project_id"))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_knowledge_proposals_failed")
		return
	}
	reviewSettings, err := s.store.KnowledgeReviewSettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_review_settings_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "knowledge": items, "proposals": proposals, "review_settings": reviewSettings})
}

type knowledgePayload struct {
	Scope          string  `json:"scope"`
	ProjectID      string  `json:"project_id"`
	ParentID       *string `json:"parent_id"`
	Slug           *string `json:"slug"`
	SortOrder      *int    `json:"sort_order"`
	IsIndex        *bool   `json:"is_index"`
	Type           string  `json:"type"`
	Title          string  `json:"title"`
	Body           string  `json:"body"`
	BranchScope    string  `json:"branch_scope"`
	ProductLineID  string  `json:"product_line_id"`
	VerifiedCommit string  `json:"verified_commit"`
	Confidence     float64 `json:"confidence"`
	Status         string  `json:"status"`
}

func normalizeKnowledgePayload(payload *knowledgePayload) error {
	payload.Scope = strings.TrimSpace(payload.Scope)
	payload.ProjectID = strings.TrimSpace(payload.ProjectID)
	if payload.Scope != "global" {
		payload.Scope = "project"
		if payload.ProjectID == "" {
			return fmt.Errorf("project_id_required")
		}
	} else {
		payload.ProjectID = ""
		payload.ProductLineID = ""
	}
	payload.Type = strings.TrimSpace(payload.Type)
	if payload.Type == "" {
		payload.Type = "practice"
	}
	payload.Title = strings.TrimSpace(payload.Title)
	payload.Body = strings.TrimSpace(payload.Body)
	if payload.Title == "" || payload.Body == "" {
		return fmt.Errorf("knowledge_title_and_body_required")
	}
	payload.ProductLineID = strings.TrimSpace(payload.ProductLineID)
	payload.BranchScope = strings.TrimSpace(payload.BranchScope)
	payload.VerifiedCommit = strings.TrimSpace(payload.VerifiedCommit)
	if payload.ParentID != nil {
		value := strings.TrimSpace(*payload.ParentID)
		payload.ParentID = &value
	}
	if payload.Slug != nil {
		value := strings.TrimSpace(*payload.Slug)
		if !store.ValidKnowledgeSlug(value) {
			return store.ErrKnowledgeInvalidSlug
		}
		payload.Slug = &value
	}
	if payload.SortOrder != nil && *payload.SortOrder < 0 {
		return fmt.Errorf("knowledge_sort_order_invalid")
	}
	if payload.Confidence < 0 {
		payload.Confidence = 0
	}
	if payload.Confidence > 1 {
		payload.Confidence = 1
	}
	return nil
}

func (s *Server) createKnowledge(writer http.ResponseWriter, request *http.Request) {
	var payload knowledgePayload
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := normalizeKnowledgePayload(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UTC()
	status := domain.KnowledgeCandidate
	if payload.Status == string(domain.KnowledgeVerified) {
		status = domain.KnowledgeVerified
	}
	item := domain.KnowledgeEntry{
		ID: domain.NewID("knowledge"), Scope: payload.Scope, ProjectID: payload.ProjectID, Type: payload.Type,
		Title: payload.Title, Body: payload.Body, Status: status, BranchScope: payload.BranchScope,
		ProductLineID: payload.ProductLineID, Confidence: payload.Confidence, Revision: 1,
		ContentHash: knowledgeContentHash(payload.Title, payload.Body), VerifiedCommit: payload.VerifiedCommit,
		CreatedAt: now, UpdatedAt: now,
	}
	item.Slug = store.KnowledgeSlug(item.Title, item.ID)
	if payload.ParentID != nil {
		item.ParentID = *payload.ParentID
	}
	if payload.Slug != nil {
		item.Slug = *payload.Slug
	}
	if payload.SortOrder != nil {
		item.SortOrder = *payload.SortOrder
	}
	if payload.IsIndex != nil {
		item.IsIndex = *payload.IsIndex
	}
	if status == domain.KnowledgeVerified {
		item.LastVerifiedAt = now
	}
	if err := s.store.CreateKnowledge(request.Context(), item); err != nil {
		writeKnowledgeMutationError(writer, "create_knowledge_failed", err)
		return
	}
	item, err := s.store.Knowledge(request.Context(), item.ID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "create_knowledge_failed")
		return
	}
	s.audit(request, "knowledge.create", "knowledge", item.ID, map[string]any{"scope": item.Scope})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "knowledge": item})
}

func (s *Server) updateKnowledge(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.Knowledge(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "knowledge_not_found")
		return
	}
	var payload knowledgePayload
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := normalizeKnowledgePayload(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	item.Scope, item.ProjectID, item.Type = payload.Scope, payload.ProjectID, payload.Type
	item.Title, item.Body = payload.Title, payload.Body
	item.BranchScope, item.ProductLineID = payload.BranchScope, payload.ProductLineID
	if payload.ParentID != nil {
		item.ParentID = *payload.ParentID
	}
	if payload.Slug != nil {
		item.Slug = *payload.Slug
	}
	if payload.SortOrder != nil {
		item.SortOrder = *payload.SortOrder
	}
	if payload.IsIndex != nil {
		item.IsIndex = *payload.IsIndex
	}
	item.VerifiedCommit, item.Confidence = payload.VerifiedCommit, payload.Confidence
	item.ContentHash = knowledgeContentHash(payload.Title, payload.Body)
	item.Revision++
	item.UpdatedAt = time.Now().UTC()
	if payload.Status != "" {
		item.Status = domain.KnowledgeStatus(payload.Status)
	}
	if item.Status == domain.KnowledgeVerified {
		item.LastVerifiedAt = item.UpdatedAt
	}
	if err := s.store.UpdateKnowledge(request.Context(), item); err != nil {
		writeKnowledgeMutationError(writer, "update_knowledge_failed", err)
		return
	}
	s.audit(request, "knowledge.update", "knowledge", item.ID, map[string]any{"revision": item.Revision})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "knowledge": item})
}

func (s *Server) deleteKnowledge(writer http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteKnowledge(request.Context(), request.PathValue("id")); err != nil {
		writeKnowledgeMutationError(writer, "delete_knowledge_failed", err)
		return
	}
	s.audit(request, "knowledge.delete", "knowledge", request.PathValue("id"), nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func writeKnowledgeMutationError(writer http.ResponseWriter, fallback string, err error) {
	switch {
	case errors.Is(err, store.ErrKnowledgeRoot), errors.Is(err, store.ErrKnowledgeRootManaged), errors.Is(err, store.ErrKnowledgeSiblingSlug), errors.Is(err, store.ErrKnowledgeHasChildren), errors.Is(err, store.ErrKnowledgeProposalPending), errors.Is(err, store.ErrKnowledgeProposalRevision), errors.Is(err, store.ErrKnowledgeProposalResolved):
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": fallback, "message": err.Error()})
	case errors.Is(err, store.ErrKnowledgeInvalidScope), errors.Is(err, store.ErrKnowledgeInvalidSlug), errors.Is(err, store.ErrKnowledgeParent), errors.Is(err, store.ErrKnowledgeCycle):
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": fallback, "message": err.Error()})
	default:
		writeError(writer, http.StatusInternalServerError, fallback)
	}
}

func (s *Server) approveKnowledgeProposal(writer http.ResponseWriter, request *http.Request) {
	proposal, entry, err := s.store.ApproveKnowledgeProposal(request.Context(), request.PathValue("id"), time.Now().UTC())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "knowledge_proposal_not_found")
			return
		}
		writeKnowledgeMutationError(writer, "approve_knowledge_proposal_failed", err)
		return
	}
	s.audit(request, "knowledge.proposal.approve", "knowledge_proposal", proposal.ID, map[string]any{"entry_id": entry.ID, "revision": entry.Revision})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "proposal": proposal, "knowledge": entry})
}

func (s *Server) rejectKnowledgeProposal(writer http.ResponseWriter, request *http.Request) {
	proposal, err := s.store.RejectKnowledgeProposal(request.Context(), request.PathValue("id"), time.Now().UTC())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "knowledge_proposal_not_found")
			return
		}
		writeKnowledgeMutationError(writer, "reject_knowledge_proposal_failed", err)
		return
	}
	s.audit(request, "knowledge.proposal.reject", "knowledge_proposal", proposal.ID, map[string]any{"entry_id": proposal.EntryID})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "proposal": proposal})
}

func (s *Server) verifyKnowledge(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.store.VerifyKnowledge(request.Context(), id, now); err != nil {
		writeKnowledgeMutationError(writer, "verify_knowledge_failed", err)
		return
	}
	s.audit(request, "knowledge.verify", "knowledge", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) feedbackKnowledge(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Kind string `json:"kind"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	kind := strings.TrimSpace(payload.Kind)
	if kind != "helped" && kind != "stale" && kind != "wrong" {
		writeError(writer, http.StatusBadRequest, "invalid_knowledge_feedback")
		return
	}
	item, err := s.store.FeedbackKnowledge(request.Context(), request.PathValue("id"), kind, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		if errors.Is(err, store.ErrKnowledgeFeedbackState) {
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "knowledge_feedback_conflict", "message": "该 revision 已标记为过时或错误，请先批准修订或确认当前内容仍然有效"})
			return
		}
		writeError(writer, http.StatusNotFound, "knowledge_not_found")
		return
	}
	s.audit(request, "knowledge.feedback", "knowledge", item.ID, map[string]any{"kind": kind})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "knowledge": item})
}

func (s *Server) listProductLines(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListProductLines(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_product_lines_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "product_lines": items})
}

func (s *Server) createProductLine(writer http.ResponseWriter, request *http.Request) {
	projectID := request.PathValue("id")
	if _, err := s.store.Project(request.Context(), projectID); err != nil {
		writeError(writer, http.StatusNotFound, "project_not_found")
		return
	}
	var payload struct {
		Name          string `json:"name"`
		BranchPattern string `json:"branch_pattern"`
		Default       bool   `json:"default"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if strings.TrimSpace(payload.Name) == "" {
		writeError(writer, http.StatusBadRequest, "product_line_name_required")
		return
	}
	now := time.Now().UTC()
	item := domain.ProductLine{ID: domain.NewID("line"), ProjectID: projectID, Name: strings.TrimSpace(payload.Name), BranchPattern: strings.TrimSpace(payload.BranchPattern), Default: payload.Default, CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreateProductLine(request.Context(), item); err != nil {
		writeError(writer, http.StatusInternalServerError, "create_product_line_failed")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "product_line": item})
}

func (s *Server) deleteProductLine(writer http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteProductLine(request.Context(), request.PathValue("line")); err != nil {
		writeError(writer, http.StatusInternalServerError, "delete_product_line_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) listSkills(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListSkills(request.Context(), request.URL.Query().Get("scope"), request.URL.Query().Get("project_id"), request.URL.Query().Get("enabled") == "1")
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_skills_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "skills": items})
}

type skillPayload struct {
	Scope        string `json:"scope"`
	ProjectID    string `json:"project_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
	Status       string `json:"status"`
	Enabled      *bool  `json:"enabled"`
}

func normalizeSkillPayload(payload *skillPayload) error {
	payload.Scope = strings.TrimSpace(payload.Scope)
	if payload.Scope != "project" {
		payload.Scope = "global"
		payload.ProjectID = ""
	} else if strings.TrimSpace(payload.ProjectID) == "" {
		return fmt.Errorf("project_id_required")
	}
	payload.Name = strings.TrimSpace(payload.Name)
	payload.Instructions = strings.TrimSpace(payload.Instructions)
	if payload.Name == "" || payload.Instructions == "" {
		return fmt.Errorf("skill_name_and_instructions_required")
	}
	if strings.TrimSpace(payload.Status) == "" {
		payload.Status = "active"
	}
	return nil
}

func (s *Server) createSkill(writer http.ResponseWriter, request *http.Request) {
	var payload skillPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := normalizeSkillPayload(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	now := time.Now().UTC()
	item := domain.Skill{ID: domain.NewID("skill"), Scope: payload.Scope, ProjectID: strings.TrimSpace(payload.ProjectID), Name: payload.Name, Description: strings.TrimSpace(payload.Description), Instructions: payload.Instructions, Version: 1, Status: payload.Status, Enabled: enabled, CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreateSkill(request.Context(), item); err != nil {
		writeError(writer, http.StatusInternalServerError, "create_skill_failed")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "skill": item})
}

func (s *Server) updateSkill(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.Skill(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "skill_not_found")
		return
	}
	var payload skillPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := normalizeSkillPayload(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	item.Scope, item.ProjectID, item.Name = payload.Scope, strings.TrimSpace(payload.ProjectID), payload.Name
	item.Description, item.Instructions = strings.TrimSpace(payload.Description), payload.Instructions
	item.Status, item.UpdatedAt = payload.Status, time.Now().UTC()
	if payload.Enabled != nil {
		item.Enabled = *payload.Enabled
	}
	item.Version++
	if err := s.store.UpdateSkill(request.Context(), item); err != nil {
		writeError(writer, http.StatusInternalServerError, "update_skill_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "skill": item})
}

func (s *Server) deleteSkill(writer http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteSkill(request.Context(), request.PathValue("id")); err != nil {
		writeError(writer, http.StatusInternalServerError, "delete_skill_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}
