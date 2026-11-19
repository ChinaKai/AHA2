package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ChinaKai/AHA2/internal/prompt"
)

func (s *Server) promptTemplates(writer http.ResponseWriter, request *http.Request) {
	items, err := s.app.PromptTemplates(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_prompt_templates_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "templates": items})
}

func (s *Server) updatePromptTemplate(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	id := request.PathValue("id")
	if err := s.app.UpdatePromptTemplate(request.Context(), id, payload.Content); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "update_prompt_template_failed", "message": err.Error()})
		return
	}
	s.audit(request, "prompt.template.update", "prompt_template", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) resetPromptTemplate(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if err := s.app.ResetPromptTemplate(request.Context(), id); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "reset_prompt_template_failed", "message": err.Error()})
		return
	}
	s.audit(request, "prompt.template.reset", "prompt_template", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

// promptEntryCatalog is the two-entry, read-only view of the template registry.
// The legacy template list above stays the only list with edit affordances.
func (s *Server) promptEntryCatalog(writer http.ResponseWriter, request *http.Request) {
	catalog, err := s.app.PromptEntryCatalog(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_prompt_entries_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "entries": catalog.Entries})
}

// promptEntryAnchorPreview renders one anchor. Unknown or blank identifiers are
// rejected explicitly instead of falling back to the first entry or template.
func (s *Server) promptEntryAnchorPreview(writer http.ResponseWriter, request *http.Request) {
	entryID := strings.TrimSpace(request.PathValue("entry"))
	key := strings.TrimSpace(request.PathValue("anchor"))
	if entryID == "" || key == "" {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_prompt_entry_anchor", "entry and anchor are required")
		return
	}
	if _, ok := prompt.LookupEntryAnchor(entryID, key); !ok {
		writeErrorDetail(writer, http.StatusNotFound, "prompt_entry_anchor_not_found", "prompt entry anchor was not found")
		return
	}
	preview, err := s.app.PreviewPromptEntryAnchor(request.Context(), entryID, key)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "preview_prompt_entry_anchor_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "preview": preview})
}

// promptMigrationPreview reports how existing overrides map onto entry anchors.
// It only reads the override table; no route below this path accepts a write.
func (s *Server) promptMigrationPreview(writer http.ResponseWriter, request *http.Request) {
	migration, err := s.app.PreviewPromptTemplateMigration(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "preview_prompt_migration_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "migration": migration})
}

// promptScenarioPreview renders a complete demo Prompt Snapshot through the
// real builder. The route only accepts GET and never receives draft content.
func (s *Server) promptScenarioPreview(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	scenarioID := strings.TrimSpace(query.Get("scenario"))
	if scenarioID == "" {
		writeErrorDetail(writer, http.StatusBadRequest, "invalid_prompt_scenario", "scenario is required")
		return
	}
	preview, err := s.app.PreviewPromptScenario(request.Context(), scenarioID, query.Get("message"), query.Get("source"))
	if err != nil {
		switch {
		case errors.Is(err, prompt.ErrUnknownScenario):
			writeErrorDetail(writer, http.StatusNotFound, "prompt_scenario_not_found", "prompt preview scenario was not found")
		case errors.Is(err, prompt.ErrScenarioMessageTooLong):
			writeErrorDetail(writer, http.StatusBadRequest, "prompt_preview_message_too_long",
				"test message exceeds the preview limit; shorten it and retry")
		case errors.Is(err, prompt.ErrInvalidMessageSource):
			writeErrorDetail(writer, http.StatusBadRequest, "invalid_message_source", "message source must be owner or pet")
		default:
			writeError(writer, http.StatusInternalServerError, "preview_prompt_scenario_failed")
		}
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "preview": preview})
}

// rejectPromptReadOnly makes the read-only boundary explicit instead of
// letting a write-shaped request fall through to a generic 404.
func (s *Server) rejectPromptReadOnly(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Allow", "GET, HEAD")
	writeErrorDetail(writer, http.StatusMethodNotAllowed, "read_only_prompt_view", "entry, anchor, migration, and scenario previews do not accept writes")
}
