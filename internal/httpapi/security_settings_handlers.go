package httpapi

import (
	"net/http"
	"strings"
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
		ValidateOrigin bool   `json:"validate_origin"`
		AccessScope    string `json:"access_scope"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	// Refuse an unknown scope rather than quietly storing the default: a typo
	// would otherwise look saved while leaving the previous scope in force.
	if scope := strings.ToLower(strings.TrimSpace(payload.AccessScope)); scope != "" &&
		scope != domain.AccessScopeLocal && scope != domain.AccessScopeLAN {
		writeError(writer, http.StatusBadRequest, "invalid_access_scope")
		return
	}
	item, err := s.store.UpdateSecuritySettings(request.Context(), domain.SecuritySettings{
		ValidateOrigin: payload.ValidateOrigin,
		AccessScope:    payload.AccessScope,
		UpdatedAt:      time.Now().UTC(),
	})
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "update_security_settings_failed")
		return
	}
	s.setOriginValidation(item.ValidateOrigin)
	// Applied immediately: the gate is read per request, so switching to
	// local-only does not need a restart to start refusing remote callers.
	if s.accessScope != nil {
		s.accessScope.set(item.AccessScope)
	}
	item.ValidateOrigin = s.originValidationEnabled()
	item.StartupOverride = s.originStartupOverride
	s.audit(request, "security.settings.update", "settings", "security", map[string]any{
		"validate_origin": item.ValidateOrigin,
		"access_scope":    item.AccessScope,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "security": item})
}
