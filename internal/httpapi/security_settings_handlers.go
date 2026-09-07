package httpapi

import (
	"net/http"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) securitySettings(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.SecuritySettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "security_settings_failed")
		return
	}
	item.ValidateOrigin = s.originValidationEnabled()
	item.StartupOverride = s.originStartupOverride
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "security": item})
}

func (s *Server) updateSecuritySettings(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ValidateOrigin bool `json:"validate_origin"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	item, err := s.store.UpdateSecuritySettings(request.Context(), domain.SecuritySettings{
		ValidateOrigin: payload.ValidateOrigin,
		UpdatedAt:      time.Now().UTC(),
	})
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "update_security_settings_failed")
		return
	}
	s.setOriginValidation(item.ValidateOrigin)
	item.ValidateOrigin = s.originValidationEnabled()
	item.StartupOverride = s.originStartupOverride
	s.audit(request, "security.origin_validation.update", "settings", "security", map[string]any{
		"validate_origin": item.ValidateOrigin,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "security": item})
}
