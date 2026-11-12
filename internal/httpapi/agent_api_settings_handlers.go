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
		URL string `json:"url"`
		// StdioProxy is opt-in for SSH workspaces. It is a pointer so a client
		// that only edits the URL does not silently switch the tunnel off.
		StdioProxy *bool `json:"stdio_proxy"`
	}
	if decodeJSON(request, &payload) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	input := domain.AgentAPISettings{URL: payload.URL}
	if payload.StdioProxy != nil {
		input.StdioProxy = *payload.StdioProxy
	} else if current, currentErr := s.app.AgentAPISettings(request.Context()); currentErr == nil {
		input.StdioProxy = current.StdioProxy
	}
	item, err := s.app.UpdateAgentAPISettings(request.Context(), input)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "agent_api_settings_invalid", "message": err.Error()})
		return
	}
	s.audit(request, "settings.agent_api.update", "settings", "agent_api", map[string]any{
		"url_configured": item.URL != "",
		"stdio_proxy":    item.StdioProxy,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "agent_api": item})
}
