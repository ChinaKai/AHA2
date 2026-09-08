package httpapi

import (
	"errors"
	"net/http"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/channel"
)

func (s *Server) agentChannelContext(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusForbidden, "channel_context_unavailable")
		return
	}
	claims, _ := agentClaimsFromContext(request.Context())
	value, err := s.channels.AgentChannelContext(request.Context(), claims)
	if err != nil {
		writeAgentChannelError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "channel_context": value})
}

func (s *Server) agentChannelCatalog(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusForbidden, "channel_context_unavailable")
		return
	}
	claims, _ := agentClaimsFromContext(request.Context())
	value, err := s.channels.AgentCatalog(request.Context(), claims)
	if err != nil {
		writeAgentChannelError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "catalog": value})
}

func (s *Server) previewAgentChannelAction(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusForbidden, "channel_context_unavailable")
		return
	}
	var payload channel.ActionPreviewInput
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	claims, _ := agentClaimsFromContext(request.Context())
	action, err := s.channels.PreviewAgentAction(request.Context(), claims, payload)
	if err != nil {
		writeAgentChannelError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "action": action})
}

func (s *Server) createAgentChannelHandoff(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusForbidden, "channel_context_unavailable")
		return
	}
	var payload struct {
		Summary string `json:"summary"`
		Details string `json:"details"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	claims, _ := agentClaimsFromContext(request.Context())
	handoff, err := s.channels.CreateAgentHandoff(request.Context(), claims, payload.Summary, payload.Details)
	if err != nil {
		writeAgentChannelError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "handoff": handoff})
}

func writeAgentChannelError(writer http.ResponseWriter, err error) {
	if errors.Is(err, app.ErrAgentCallForbidden) || errors.Is(err, app.ErrAgentTurnInactive) {
		writeError(writer, http.StatusForbidden, "channel_operation_forbidden")
		return
	}
	writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "channel_operation_failed", "message": err.Error()})
}
