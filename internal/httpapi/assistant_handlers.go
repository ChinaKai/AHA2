package httpapi

import (
	"net/http"

	"github.com/ChinaKai/AHA2/internal/app"
)

func (s *Server) ownerAssistant(writer http.ResponseWriter, request *http.Request) {
	view, err := s.app.OwnerAssistant(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "assistant_load_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "assistant": view.Assistant, "messages": view.Messages,
		"models": view.Models, "model_name": view.ModelName, "brain_model_name": view.BrainModelName,
		"tools": view.Tools,
	})
}

func (s *Server) updateOwnerAssistant(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ModelID   string `json:"model_id"`
		Name      string `json:"name"`
		Character string `json:"character"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	view, err := s.app.UpdateOwnerAssistant(request.Context(), payload.ModelID, payload.Name, payload.Character)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "assistant_update_failed", "message": err.Error()})
		return
	}
	s.audit(request, "assistant.update", "assistant", view.Assistant.ID, map[string]any{"model_id": view.Assistant.ModelID, "character": view.Assistant.Character})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "assistant": view.Assistant, "models": view.Models, "model_name": view.ModelName,
	})
}

func (s *Server) askOwnerAssistant(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Content string        `json:"content"`
		Layout  app.PetLayout `json:"layout"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	reply, err := s.app.AskOwnerAssistantWithLayout(request.Context(), payload.Content, payload.Layout)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "assistant_ask_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "message": reply.Message, "reply": reply.Reply, "tool_events": reply.ToolEvents, "actions": reply.Actions,
	})
}

func (s *Server) ownerAssistantHints(writer http.ResponseWriter, request *http.Request) {
	hints, err := s.app.OwnerAssistantHints(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "assistant_hints_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "hints": hints})
}

func (s *Server) dismissOwnerAssistantHint(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Kind     string `json:"kind"`
		TargetID string `json:"target_id"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := s.app.DismissOwnerAssistantHint(request.Context(), payload.Kind, payload.TargetID); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "assistant_dismiss_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}
