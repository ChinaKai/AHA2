package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// The Phase 2 vault API is read-only by construction: every handler below only
// calls knowledgevault.Reader methods that open the index with mode=ro and
// never touch the existing knowledge tables. All reads run inside
// Manager.WithReader so the runtime switch (and index rebuild) cannot close the
// SQLite connection underneath a handler.

// withKnowledgeVaultReader runs fn under the manager's read lock. A nil manager
// and a switched-off feature report the same ErrDisabled so the legacy UI sees
// a plain 404 in both cases.
func (s *Server) withKnowledgeVaultReader(fn func(*knowledgevault.Reader) error) error {
	if s.knowledgeVault == nil {
		return knowledgevault.ErrDisabled
	}
	return s.knowledgeVault.WithReader(fn)
}

func writeKnowledgeVaultDisabled(writer http.ResponseWriter) {
	writeError(writer, http.StatusNotFound, "knowledge_vault_disabled")
}

func (s *Server) knowledgeVaultLibraries(writer http.ResponseWriter, request *http.Request) {
	// The app layer overlays the authoritative AHA2 project names: the vault
	// manifest only caches derived display names, and older rebuilds stored the
	// generated knowledge-home title there.
	var result knowledgevault.LibraryList
	result, err := s.app.VaultLibraries(request.Context())
	if errors.Is(err, knowledgevault.ErrDisabled) {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_libraries_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "enabled": result.Enabled,
		"vault_ready": result.VaultReady, "index_ready": result.IndexReady,
		"vault_name": result.VaultName, "obsidian_vault": result.ObsidianVault,
		"obsidian_ready": result.ObsidianReady,
		"vault_path":     result.VaultPath, "libraries": result.Libraries,
		"projects": result.Projects, "archives": result.Archived,
		"orphans": result.Orphans, "rebuilt_at": result.RebuiltAt,
		"warning": result.Warning,
	})
}

func (s *Server) knowledgeVaultTree(writer http.ResponseWriter, request *http.Request) {
	library := strings.TrimSpace(request.URL.Query().Get("library"))
	project := strings.TrimSpace(request.URL.Query().Get("project"))
	var result knowledgevault.TreeResult
	err := s.withKnowledgeVaultReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		result, readErr = reader.Tree(request.Context(), library, project)
		return readErr
	})
	if errors.Is(err, knowledgevault.ErrDisabled) {
		writeKnowledgeVaultDisabled(writer)
		return
	}
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
	var document knowledgevault.Doc
	err := s.withKnowledgeVaultReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		document, readErr = reader.Document(request.Context(), request.PathValue("id"), request.URL.Query().Get("path"))
		return readErr
	})
	switch {
	case errors.Is(err, knowledgevault.ErrDisabled):
		writeKnowledgeVaultDisabled(writer)
		return
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

func (s *Server) knowledgeVaultDocumentDiff(writer http.ResponseWriter, request *http.Request) {
	var diff knowledgevault.DocDiff
	err := s.withKnowledgeVaultReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		diff, readErr = reader.Diff(request.Context(), request.PathValue("id"))
		return readErr
	})
	switch {
	case errors.Is(err, knowledgevault.ErrDisabled):
		writeKnowledgeVaultDisabled(writer)
		return
	case errors.Is(err, knowledgevault.ErrReaderNotReady):
		writeError(writer, http.StatusNotFound, "knowledge_vault_index_missing")
		return
	case errors.Is(err, knowledgevault.ErrDocumentNotFound):
		writeError(writer, http.StatusNotFound, "knowledge_vault_document_not_found")
		return
	case err != nil:
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_document_diff_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "enabled": true, "diff": diff})
}

func (s *Server) knowledgeVaultSearch(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	limit := 30
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	includeArchives := true
	if value := strings.TrimSpace(query.Get("include_archives")); value != "" {
		switch strings.ToLower(value) {
		case "0", "false", "no", "off":
			includeArchives = false
		default:
			includeArchives = true
		}
	}
	var result knowledgevault.SearchResponse
	err := s.withKnowledgeVaultReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		result, readErr = reader.Search(request.Context(), knowledgevault.DocumentSearchOptions{
			Query: strings.TrimSpace(query.Get("q")), Library: strings.TrimSpace(query.Get("library")),
			ProjectID: strings.TrimSpace(query.Get("project")),
			Type:      strings.TrimSpace(query.Get("type")), Status: strings.TrimSpace(query.Get("status")),
			Tag: strings.TrimSpace(query.Get("tag")), Limit: limit,
			IncludeArchives: includeArchives,
		})
		return readErr
	})
	if errors.Is(err, knowledgevault.ErrDisabled) {
		writeKnowledgeVaultDisabled(writer)
		return
	}
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
	limit := 120
	if value := strings.TrimSpace(request.URL.Query().Get("limit")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	includeUnchanged := false
	switch strings.ToLower(strings.TrimSpace(request.URL.Query().Get("include_unchanged"))) {
	case "1", "true", "yes":
		includeUnchanged = true
	}
	var result knowledgevault.RecentResponse
	err := s.withKnowledgeVaultReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		result, readErr = reader.Recent(request.Context(), strings.TrimSpace(request.URL.Query().Get("project")), limit, includeUnchanged)
		return readErr
	})
	if errors.Is(err, knowledgevault.ErrDisabled) {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_recent_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "enabled": true, "index_ready": result.IndexReady, "items": result.Items,
	})
}

func (s *Server) knowledgeVaultInbox(writer http.ResponseWriter, request *http.Request) {
	var result knowledgevault.InboxResponse
	err := s.withKnowledgeVaultReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		result, readErr = reader.Inbox(request.Context(), strings.TrimSpace(request.URL.Query().Get("project")))
		return readErr
	})
	if errors.Is(err, knowledgevault.ErrDisabled) {
		writeKnowledgeVaultDisabled(writer)
		return
	}
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
	catalog, err := s.app.VaultSkillCatalog(request.Context(), strings.TrimSpace(request.URL.Query().Get("project_id")))
	if errors.Is(err, knowledgevault.ErrDisabled) {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_skills_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "enabled": true, "index_ready": catalog.IndexReady,
		"runtime_read": catalog.RuntimeRead, "bound_project_ids": catalog.BoundProjectIDs,
		"skills": catalog.Skills,
	})
}

// updateKnowledgeVaultSkillAutoAttach persists only the owner preference. It
// never edits the SKILL.md package or the legacy skills table.
func (s *Server) updateKnowledgeVaultSkillAutoAttach(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Key           string `json:"key"`
		AutoAttach    *bool  `json:"auto_attach"`
		ConfirmGlobal bool   `json:"confirm_global"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(payload.Key) == "" || payload.AutoAttach == nil {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_auto_attach", "key and auto_attach are required")
		return
	}
	view, err := s.app.SetVaultSkillAutoAttach(request.Context(), payload.Key, *payload.AutoAttach, payload.ConfirmGlobal)
	switch {
	case errors.Is(err, knowledgevault.ErrDisabled):
		writeKnowledgeVaultDisabled(writer)
		return
	case errors.Is(err, app.ErrVaultSkillNotFound):
		writeErrorDetail(writer, http.StatusNotFound, "vault_skill_not_found", err.Error())
		return
	case errors.Is(err, app.ErrVaultSkillNotAutoAttachable):
		writeErrorDetail(writer, http.StatusBadRequest, "vault_skill_not_auto_attachable", err.Error())
		return
	case errors.Is(err, app.ErrVaultGlobalConfirmationRequired):
		writeErrorDetail(writer, http.StatusConflict, "global_confirmation_required", "全局 vault 技能自动启用需要二次确认")
		return
	case err != nil:
		writeErrorDetail(writer, http.StatusInternalServerError, "vault_skill_auto_attach_failed", err.Error())
		return
	}
	s.audit(request, "knowledge_vault.skill_auto_attach", "vault_skill", view.Key,
		map[string]any{"auto_attach": *payload.AutoAttach, "scope": view.Scope, "project_id": view.ProjectID})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "skill": view})
}
