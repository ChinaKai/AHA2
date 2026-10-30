package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func (s *Server) ownerPetState(writer http.ResponseWriter, request *http.Request) {
	state, err := s.app.OwnerPetState(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pet_state_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "state": state})
}

func (s *Server) updateOwnerPetState(writer http.ResponseWriter, request *http.Request) {
	var patch domain.PetStatePatch
	if err := decodeJSON(request, &patch); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	state, err := s.app.UpdateOwnerPetState(request.Context(), patch)
	if err != nil {
		if storeIsConflict(err) {
			current, _ := s.app.OwnerPetState(request.Context())
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "pet_state_conflict", "state": current})
			return
		}
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "pet_state_update_failed", "message": err.Error()})
		return
	}
	s.audit(request, "pet.update", "pet", state.ID, map[string]any{"action": state.Action, "mood": state.Mood, "zone": state.Zone})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "state": state})
}

func (s *Server) controlOwnerPet(writer http.ResponseWriter, request *http.Request) {
	var input app.PetControlRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	result, err := s.app.ControlOwnerPet(request.Context(), input)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "pet_control_failed", "message": err.Error()})
		return
	}
	s.audit(request, "pet.control", "pet", "owner_pet", map[string]any{"say": result.Say, "actions": result.Actions})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "say": result.Say, "actions": result.Actions})
}

func (s *Server) ownerPetPrompts(writer http.ResponseWriter, request *http.Request) {
	prompts, err := s.app.OwnerPetPrompts(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pet_prompts_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "chat": prompts.Chat, "brain": prompts.Brain})
}

func (s *Server) ownerPetActivity(writer http.ResponseWriter, request *http.Request) {
	limit := queryInt(request, "limit", 20)
	events, err := s.app.OwnerPetActivity(request.Context(), int(limit))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pet_activity_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "events": events})
}

func (s *Server) updateOwnerPetSettings(writer http.ResponseWriter, request *http.Request) {
	var input app.PetSettingsInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	view, err := s.app.UpdateOwnerPetSettings(request.Context(), input)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "pet_settings_update_failed", "message": err.Error()})
		return
	}
	s.audit(request, "pet.settings", "assistant", view.Assistant.ID, map[string]any{
		"enabled": view.Assistant.PetEnabled, "character": view.Assistant.Character,
		"brain_model_id": view.Assistant.BrainModelID, "actions": view.Assistant.PetActions,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "assistant": view.Assistant, "models": view.Models,
		"model_name": view.ModelName, "brain_model_name": view.BrainModelName,
	})
}

func (s *Server) ownerPetBrain(writer http.ResponseWriter, request *http.Request) {
	var input app.PetBrainContext
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	result, err := s.app.AskOwnerPetBrain(request.Context(), input)
	if err != nil {
		if app.IsPetBrainSkip(err) {
			writeJSON(writer, http.StatusOK, map[string]any{"ok": false, "skipped": true, "message": err.Error()})
			return
		}
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "宠物 Task") {
			status = http.StatusConflict
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": "pet_brain_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "say": result.Say, "actions": result.Actions})
}

func (s *Server) ownerPetEvents(writer http.ResponseWriter, request *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusInternalServerError, "streaming_unsupported")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	channel, unsubscribe := s.app.Hub().SubscribeAll()
	defer unsubscribe()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event := <-channel:
			switch event.Type {
			case "pet_state":
				data, _ := json.Marshal(event.Data["state"])
				_, _ = fmt.Fprintf(writer, "event: pet_state\ndata: %s\n\n", data)
				flusher.Flush()
			case "pet_action":
				data, _ := json.Marshal(event.Data)
				_, _ = fmt.Fprintf(writer, "event: pet_action\ndata: %s\n\n", data)
				flusher.Flush()
			}
		case <-heartbeat.C:
			_, _ = fmt.Fprint(writer, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		}
	}
}

func storeIsConflict(err error) bool {
	return err != nil && err == store.ErrPetStateConflict
}
