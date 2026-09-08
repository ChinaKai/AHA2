package httpapi

import (
	"net/http"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) agentAPISettings(writer http.ResponseWriter, request *http.Request) {
	item, err := s.app.AgentAPISettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "agent_api_settings_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "agent_api": item})
}

func (s *Server) updateAgentAPISettings(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		URL           string `json:"url"`
		AllowInsecure bool   `json:"allow_insecure"`
	}
	if decodeJSON(request, &payload) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	item, err := s.app.UpdateAgentAPISettings(request.Context(), domain.AgentAPISettings{
		URL: payload.URL, AllowInsecure: payload.AllowInsecure,
	})
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "agent_api_settings_invalid", "message": err.Error()})
		return
	}
	s.audit(request, "settings.agent_api.update", "settings", "agent_api", map[string]any{
		"url_configured": item.URL != "", "allow_insecure": item.AllowInsecure,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "agent_api": item})
}
