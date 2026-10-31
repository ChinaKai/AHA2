package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) generalSettings(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.GeneralSettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "general_settings_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "general": item})
}

func (s *Server) updateGeneralSettings(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Language string `json:"language"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	language := strings.TrimSpace(payload.Language)
	if language != "" && language != domain.LanguageAuto &&
		language != domain.LanguageSimplified && language != domain.LanguageEnglish {
		writeError(writer, http.StatusBadRequest, "invalid_language")
		return
	}
	item, err := s.store.UpdateGeneralSettings(request.Context(), domain.GeneralSettings{
		Language:  language,
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "update_general_settings_failed")
		return
	}
	s.audit(request, "general_settings.update", "general_settings", "1", map[string]any{"language": item.Language})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "general": item})
}
