package httpapi

import (
	"database/sql"
	"net/http"
	"strings"
	"time"
)

func (s *Server) listKnowledgeLibraries(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListKnowledgeLibraries(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_knowledge_libraries_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "libraries": items})
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
	result, err := s.store.DeleteKnowledgeLibrary(request.Context(), request.PathValue("id"))
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(writer, http.StatusNotFound, "knowledge_library_not_found")
		} else {
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "delete_knowledge_library_failed", "message": err.Error()})
		}
		return
	}
	s.audit(request, "knowledge_library.delete", "knowledge_library", request.PathValue("id"), map[string]any{
		"knowledge": result.Knowledge, "proposals": result.Proposals, "skills": result.Skills,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "deleted": result})
}

func (s *Server) detachProjectKnowledge(writer http.ResponseWriter, request *http.Request) {
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
