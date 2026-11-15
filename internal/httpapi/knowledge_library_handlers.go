package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func (s *Server) listKnowledgeLibraries(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListKnowledgeLibraries(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_knowledge_libraries_failed")
		return
	}
	// Keep the endpoint readable for diagnostics, but do not advertise a legacy
	// library whose projects now read the vault: the Web channel source picker
	// must not be able to grant it.
	visible := make([]domain.KnowledgeLibrary, 0, len(items))
	for _, item := range items {
		if s.knowledgeLibraryVaultActive(request.Context(), item) {
			continue
		}
		visible = append(visible, item)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "libraries": visible})
}

func (s *Server) bindKnowledgeLibrary(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ProjectID   string `json:"project_id"`
		BindingMode string `json:"binding_mode"`
	}
	if decodeJSON(request, &payload) != nil || strings.TrimSpace(payload.ProjectID) == "" {
		writeError(writer, http.StatusBadRequest, "knowledge_library_project_required")
		return
	}
	if s.knowledgeVaultReadActive("project", strings.TrimSpace(payload.ProjectID)) || s.knowledgeLibraryVaultActiveByID(request.Context(), request.PathValue("id")) {
		writeKnowledgeVaultManagementConflict(writer)
		return
	}
	item, err := s.store.BindKnowledgeLibrary(request.Context(), request.PathValue("id"), strings.TrimSpace(payload.ProjectID), strings.TrimSpace(payload.BindingMode), time.Now().UTC())
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(writer, http.StatusNotFound, "knowledge_library_not_found")
		} else {
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "bind_knowledge_library_failed", "message": err.Error()})
		}
		return
	}
	s.audit(request, "knowledge_library.bind", "knowledge_library", item.ID, map[string]any{"project_id": item.BoundProjectID, "binding_mode": item.BindingMode})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "library": item})
}

func (s *Server) unbindKnowledgeLibrary(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeLibraryVaultActiveByID(request.Context(), request.PathValue("id")) {
		writeKnowledgeVaultManagementConflict(writer)
		return
	}
	item, err := s.store.UnbindKnowledgeLibrary(request.Context(), request.PathValue("id"))
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(writer, http.StatusNotFound, "knowledge_library_binding_not_found")
		} else {
			writeError(writer, http.StatusInternalServerError, "unbind_knowledge_library_failed")
		}
		return
	}
	s.audit(request, "knowledge_library.unbind", "knowledge_library", item.ID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "library": item})
}

func (s *Server) deleteKnowledgeLibrary(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeLibraryVaultActiveByID(request.Context(), request.PathValue("id")) {
		writeKnowledgeVaultManagementConflict(writer)
		return
	}
	result, err := s.store.DeleteKnowledgeLibrary(request.Context(), request.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrKnowledgeLibraryNotFound):
			writeError(writer, http.StatusNotFound, "knowledge_library_not_found")
		case errors.Is(err, store.ErrKnowledgeLibraryInUse):
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "delete_knowledge_library_failed", "message": err.Error()})
		default:
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"ok": false, "error": "delete_knowledge_library_failed", "message": err.Error()})
		}
		return
	}
	s.audit(request, "knowledge_library.delete", "knowledge_library", request.PathValue("id"), map[string]any{
		"knowledge": result.Knowledge, "proposals": result.Proposals, "skills": result.Skills,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "deleted": result})
}

func (s *Server) detachProjectKnowledge(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVaultReadActive("project", request.PathValue("id")) {
		writeKnowledgeVaultManagementConflict(writer)
		return
	}
	item, err := s.store.DetachProjectKnowledge(request.Context(), request.PathValue("id"))
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(writer, http.StatusNotFound, "project_not_found")
		} else {
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "detach_project_knowledge_failed", "message": err.Error()})
		}
		return
	}
	s.audit(request, "project_knowledge.detach", "project", request.PathValue("id"), map[string]any{"library_id": item.ID})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "library": item})
}

func (s *Server) deleteProjectKnowledge(writer http.ResponseWriter, request *http.Request) {
	if s.knowledgeVaultReadActive("project", request.PathValue("id")) {
		writeKnowledgeVaultManagementConflict(writer)
		return
	}
	result, err := s.store.DeleteProjectKnowledge(request.Context(), request.PathValue("id"))
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(writer, http.StatusNotFound, "project_not_found")
		} else {
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "delete_project_knowledge_failed", "message": err.Error()})
		}
		return
	}
	s.audit(request, "project_knowledge.delete", "project", request.PathValue("id"), map[string]any{
		"knowledge": result.Knowledge, "proposals": result.Proposals, "skills": result.Skills,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "deleted": result})
}
