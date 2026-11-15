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
	var result knowledgevault.LibraryList
	err := s.withKnowledgeVaultReader(func(reader *knowledgevault.Reader) error {
		result = reader.Libraries(request.Context())
		return nil
	})
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
	var result knowledgevault.SearchResponse
	err := s.withKnowledgeVaultReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		result, readErr = reader.Search(request.Context(), knowledgevault.DocumentSearchOptions{
			Query: strings.TrimSpace(query.Get("q")), Library: strings.TrimSpace(query.Get("library")),
			ProjectID: strings.TrimSpace(query.Get("project")),
			Type:      strings.TrimSpace(query.Get("type")), Status: strings.TrimSpace(query.Get("status")),
			Tag: strings.TrimSpace(query.Get("tag")), Limit: limit,
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
	var result knowledgevault.SkillsResponse
	err := s.withKnowledgeVaultReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		result, readErr = reader.Skills(request.Context())
		return readErr
	})
	if errors.Is(err, knowledgevault.ErrDisabled) {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_vault_skills_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "enabled": true, "skills": result.Skills})
}
