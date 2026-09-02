package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) listKnowledge(writer http.ResponseWriter, request *http.Request) {
	var statuses []domain.KnowledgeStatus
	for _, value := range strings.Split(request.URL.Query().Get("status"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			statuses = append(statuses, domain.KnowledgeStatus(value))
		}
	}
	items, err := s.store.ListKnowledge(request.Context(), request.URL.Query().Get("scope"), request.URL.Query().Get("project_id"), statuses)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_knowledge_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "knowledge": items})
}

func (s *Server) createKnowledge(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Scope       string  `json:"scope"`
		ProjectID   string  `json:"project_id"`
		Type        string  `json:"type"`
		Title       string  `json:"title"`
		Body        string  `json:"body"`
		BranchScope string  `json:"branch_scope"`
		Confidence  float64 `json:"confidence"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if payload.Scope != "global" {
		payload.Scope = "project"
		if payload.ProjectID == "" {
			writeError(writer, http.StatusBadRequest, "project_id_required")
			return
		}
	}
	if strings.TrimSpace(payload.Title) == "" || strings.TrimSpace(payload.Body) == "" {
		writeError(writer, http.StatusBadRequest, "knowledge_title_and_body_required")
		return
	}
	now := time.Now().UTC()
	item := domain.KnowledgeEntry{
		ID: domain.NewID("knowledge"), Scope: payload.Scope, ProjectID: payload.ProjectID, Type: payload.Type,
		Title: strings.TrimSpace(payload.Title), Body: strings.TrimSpace(payload.Body), Status: domain.KnowledgeCandidate,
		BranchScope: payload.BranchScope, Confidence: payload.Confidence, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateKnowledge(request.Context(), item); err != nil {
		writeError(writer, http.StatusInternalServerError, "create_knowledge_failed")
		return
	}
	s.audit(request, "knowledge.create", "knowledge", item.ID, map[string]any{"scope": item.Scope})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "knowledge": item})
}

func (s *Server) verifyKnowledge(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.store.VerifyKnowledge(request.Context(), id, now); err != nil {
		writeError(writer, http.StatusInternalServerError, "verify_knowledge_failed")
		return
	}
	s.audit(request, "knowledge.verify", "knowledge", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}
