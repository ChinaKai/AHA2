package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) channelKnowledgeBindings(writer http.ResponseWriter, request *http.Request) {
	if s.app == nil {
		writeError(writer, http.StatusServiceUnavailable, "app_unavailable")
		return
	}
	endpoint := strings.TrimSpace(request.URL.Query().Get("endpoint"))
	selection, err := s.app.ChannelKnowledgeBindingSelection(request.Context(), request.PathValue("id"), endpoint)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_knowledge_bindings_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "endpoint": endpoint, "bindings": selection.Bindings, "include_global": selection.IncludeGlobal,
	})
}

func (s *Server) updateChannelKnowledgeBindings(writer http.ResponseWriter, request *http.Request) {
	if s.app == nil {
		writeError(writer, http.StatusServiceUnavailable, "app_unavailable")
		return
	}
	var payload struct {
		Endpoint      string `json:"endpoint"`
		IncludeGlobal *bool  `json:"include_global"`
		Bindings      []struct {
			SpaceProject string `json:"space_project_id"`
			AccessMode   string `json:"access_mode"`
			ScopeMode    string `json:"scope_mode"`
		} `json:"bindings"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	bindings := make([]domain.KnowledgeBinding, 0, len(payload.Bindings))
	for _, item := range payload.Bindings {
		bindings = append(bindings, domain.KnowledgeBinding{
			SpaceProject: strings.TrimSpace(item.SpaceProject),
			AccessMode:   strings.TrimSpace(item.AccessMode),
			ScopeMode:    strings.TrimSpace(item.ScopeMode),
		})
	}
	endpoint := strings.TrimSpace(payload.Endpoint)
	updated, err := s.app.ReplaceChannelKnowledgeBindings(request.Context(), request.PathValue("id"), endpoint, bindings, payload.IncludeGlobal)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, app.ErrKnowledgeSpaceNotReady) {
			status = http.StatusConflict
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": "knowledge_binding_invalid", "message": err.Error()})
		return
	}
	// A nil include_global preserves the existing row; report the effective
	// value instead of echoing the missing field as false.
	selection, err := s.app.ChannelKnowledgeBindingSelection(request.Context(), request.PathValue("id"), endpoint)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_knowledge_bindings_failed")
		return
	}
	s.audit(request, "channel.knowledge_bindings.update", "channel_instance", request.PathValue("id"), map[string]any{
		"endpoint": endpoint, "count": len(updated), "include_global": selection.IncludeGlobal,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "endpoint": endpoint, "bindings": updated, "include_global": selection.IncludeGlobal,
	})
}

// updateChannelGroupKnowledgeBindings is the atomic save the Web settings use:
// it writes the group endpoint selection and removes the instance defaults in
// one transaction. An empty selection therefore cannot fall back to old rows.
func (s *Server) updateChannelGroupKnowledgeBindings(writer http.ResponseWriter, request *http.Request) {
	if s.app == nil {
		writeError(writer, http.StatusServiceUnavailable, "app_unavailable")
		return
	}
	var payload struct {
		IncludeGlobal bool `json:"include_global"`
		Bindings      []struct {
			SpaceProject string `json:"space_project_id"`
			AccessMode   string `json:"access_mode"`
			ScopeMode    string `json:"scope_mode"`
		} `json:"bindings"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	bindings := make([]domain.KnowledgeBinding, 0, len(payload.Bindings))
	for _, item := range payload.Bindings {
		bindings = append(bindings, domain.KnowledgeBinding{
			SpaceProject: strings.TrimSpace(item.SpaceProject),
			AccessMode:   strings.TrimSpace(item.AccessMode),
			ScopeMode:    strings.TrimSpace(item.ScopeMode),
		})
	}
	updated, err := s.app.ReplaceChannelGroupKnowledgeBindings(request.Context(), request.PathValue("id"), bindings, payload.IncludeGlobal)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, app.ErrKnowledgeSpaceNotReady) {
			status = http.StatusConflict
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": "knowledge_binding_invalid", "message": err.Error()})
		return
	}
	s.audit(request, "channel.knowledge_bindings.update", "channel_instance", request.PathValue("id"), map[string]any{
		"endpoint": domain.ChannelEndpointGroupDigitalHuman, "count": len(updated), "include_global": payload.IncludeGlobal,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "endpoint": domain.ChannelEndpointGroupDigitalHuman, "bindings": updated, "include_global": payload.IncludeGlobal,
	})
}
