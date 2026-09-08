package httpapi

import (
	"net/http"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	minimumBackendTimeoutSeconds = 60
	maximumBackendIdleSeconds    = 24 * 60 * 60
	maximumBackendTurnSeconds    = 7 * 24 * 60 * 60
)

func (s *Server) backendSettings(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.BackendSettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "backend_settings_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "backend": item})
}

func (s *Server) updateBackendSettings(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		IdleTimeoutSeconds int `json:"idle_timeout_seconds"`
		TurnTimeoutSeconds int `json:"turn_timeout_seconds"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if payload.IdleTimeoutSeconds < minimumBackendTimeoutSeconds || payload.IdleTimeoutSeconds > maximumBackendIdleSeconds {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_backend_idle_timeout", "message": "Backend 无活动超时必须在 1 分钟到 24 小时之间"})
		return
	}
	if payload.TurnTimeoutSeconds < minimumBackendTimeoutSeconds || payload.TurnTimeoutSeconds > maximumBackendTurnSeconds {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_backend_turn_timeout", "message": "Backend 单次执行总时限必须在 1 分钟到 7 天之间"})
		return
	}
	item, err := s.store.UpdateBackendSettings(request.Context(), domain.BackendSettings{
		IdleTimeoutSeconds: payload.IdleTimeoutSeconds,
		TurnTimeoutSeconds: payload.TurnTimeoutSeconds,
		UpdatedAt:          time.Now().UTC(),
	})
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "update_backend_settings_failed")
		return
	}
	s.audit(request, "backend.timeout.update", "settings", "backend", map[string]any{
		"idle_timeout_seconds": item.IdleTimeoutSeconds,
		"turn_timeout_seconds": item.TurnTimeoutSeconds,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "backend": item})
}
