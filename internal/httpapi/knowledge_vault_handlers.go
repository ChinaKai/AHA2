package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// The Phase 2 vault API is read-only by construction: every handler below only
// calls knowledgevault.Reader methods that open the index with mode=ro and
// never touch the existing knowledge tables.

func (s *Server) knowledgeVaultLibraries(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeError(writer, http.StatusNotFound, "knowledge_vault_disabled")
		return
	}
	result := s.knowledgeVault.Libraries(request.Context())
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "enabled": result.Enabled,
		"vault_ready": result.VaultReady, "index_ready": result.IndexReady,
		"vault_name": result.VaultName, "obsidian_vault": result.ObsidianVault,
		"obsidian_ready": result.ObsidianReady, "remote_host": result.RemoteHost,
		"vault_path": result.VaultPath, "libraries": result.Libraries,
		"projects": result.Projects, "archives": result.Archived,
		"orphans": result.Orphans, "rebuilt_at": result.RebuiltAt,
		"warning": result.Warning,
	})
}

func (s *Server) knowledgeVaultTree(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeError(writer, http.StatusNotFound, "knowledge_vault_disabled")
		return
	}
	library := strings.TrimSpace(request.URL.Query().Get("library"))
	project := strings.TrimSpace(request.URL.Query().Get("project"))
	result, err := s.knowledgeVault.Tree(request.Context(), library, project)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "invalid_knowledge_vault_library", "message": err.Error(),
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "enabled": true, "index_ready": result.IndexReady,
		"library": result.Library, "project_id": result.ProjectID,
		"count": result.Count, "nodes": result.Nodes, "warning": result.Warning,
	})
}

func (s *Server) knowledgeVaultDocument(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeError(writer, http.StatusNotFound, "knowledge_vault_disabled")
		return
	}
	document, err := s.knowledgeVault.Document(request.Context(), request.PathValue("id"), request.URL.Query().Get("path"))
	switch {
	case errors.Is(err, knowledgevault.ErrReaderNotReady):
		writeError(writer, http.StatusNotFound, "knowledge_vault_index_missing")
		return
	case errors.Is(err, knowledgevault.ErrDocumentNotFound):
		writeError(writer, http.StatusNotFound, "knowledge_vault_document_not_found")
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "knowledge_vault_document_failed", "message": err.Error(),
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "doc": document})
}

func (s *Server) knowledgeVaultSearch(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeError(writer, http.StatusNotFound, "knowledge_vault_disabled")
		return
	}
	query := request.URL.Query()
	limit := 30
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	result, err := s.knowledgeVault.Search(request.Context(), knowledgevault.DocumentSearchOptions{
		Query: strings.TrimSpace(query.Get("q")), Library: strings.TrimSpace(query.Get("library")),
		ProjectID: strings.TrimSpace(query.Get("project")),
		Type:      strings.TrimSpace(query.Get("type")), Status: strings.TrimSpace(query.Get("status")),
		Tag: strings.TrimSpace(query.Get("tag")), Limit: limit,
	})
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "invalid_knowledge_vault_search", "message": err.Error(),
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "enabled": true, "index_ready": result.IndexReady,
		"results": result.Results, "warning": result.Warning,
	})
}

func (s *Server) knowledgeVaultRecent(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeError(writer, http.StatusNotFound, "knowledge_vault_disabled")
		return
	}
	limit := 120
	if value := strings.TrimSpace(request.URL.Query().Get("limit")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	result, err := s.knowledgeVault.Recent(request.Context(), strings.TrimSpace(request.URL.Query().Get("project")), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_recent_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "enabled": true, "index_ready": result.IndexReady, "items": result.Items,
	})
}

func (s *Server) knowledgeVaultInbox(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeError(writer, http.StatusNotFound, "knowledge_vault_disabled")
		return
	}
	result, err := s.knowledgeVault.Inbox(request.Context(), strings.TrimSpace(request.URL.Query().Get("project")))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_inbox_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "enabled": true, "index_ready": result.IndexReady,
		"count": result.Count, "counts": result.Counts, "items": result.Items,
	})
}

func (s *Server) knowledgeVaultSkills(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVault == nil {
		writeError(writer, http.StatusNotFound, "knowledge_vault_disabled")
		return
	}
	result, err := s.knowledgeVault.Skills(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_skills_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "enabled": true, "skills": result.Skills})
}
