package httpapi

import (
	"net/http"
)

func (s *Server) promptTemplates(writer http.ResponseWriter, request *http.Request) {
	items, err := s.app.PromptTemplates(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_prompt_templates_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "templates": items})
}

func (s *Server) updatePromptTemplate(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	id := request.PathValue("id")
	if err := s.app.UpdatePromptTemplate(request.Context(), id, payload.Content); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "update_prompt_template_failed", "message": err.Error()})
		return
	}
	s.audit(request, "prompt.template.update", "prompt_template", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) resetPromptTemplate(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if err := s.app.ResetPromptTemplate(request.Context(), id); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "reset_prompt_template_failed", "message": err.Error()})
		return
	}
	s.audit(request, "prompt.template.reset", "prompt_template", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}
