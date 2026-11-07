package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// hardwareIORetentionPayload carries optional overrides of the stored
// hardware_io retention settings. A missing field uses the stored value.
type hardwareIORetentionPayload struct {
	RetentionDays *int `json:"retention_days"`
	MaxDeleteRows *int `json:"max_delete_rows"`
	Confirm       bool `json:"confirm"`
}

func (payload hardwareIORetentionPayload) apply(base domain.HardwareIORetentionSettings) domain.HardwareIORetentionSettings {
	if payload.RetentionDays != nil {
		base.RetentionDays = *payload.RetentionDays
	}
	if payload.MaxDeleteRows != nil {
		base.MaxDeleteRows = *payload.MaxDeleteRows
	}
	return base
}

func (s *Server) databaseStats(writer http.ResponseWriter, request *http.Request) {
	stats, err := s.store.DatabaseStats(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"ok": false, "error": "database_stats_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "database": stats})
}

func (s *Server) updateHardwareIORetention(writer http.ResponseWriter, request *http.Request) {
	var payload hardwareIORetentionPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	current, err := s.store.HardwareIORetentionSettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "retention_settings_failed")
		return
	}
	next := payload.apply(current)
	next.UpdatedAt = time.Now().UTC()
	if err := next.Validate(); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_retention_settings", "message": err.Error()})
		return
	}
	saved, err := s.store.UpdateHardwareIORetentionSettings(request.Context(), next)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "update_retention_settings_failed")
		return
	}
	s.audit(request, "data.hardware_io.retention.update", "data_retention_settings", "hardware_io", map[string]any{
		"retention_days": saved.RetentionDays, "max_delete_rows": saved.MaxDeleteRows,
		"previous_retention_days": current.RetentionDays, "previous_max_delete_rows": current.MaxDeleteRows,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "retention": saved})
}

// retentionRequest decodes the payload and resolves the settings to use,
// writing the error response itself when it returns false.
func (s *Server) retentionRequest(writer http.ResponseWriter, request *http.Request) (hardwareIORetentionPayload, domain.HardwareIORetentionSettings, bool) {
	var payload hardwareIORetentionPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return payload, domain.HardwareIORetentionSettings{}, false
	}
	stored, err := s.store.HardwareIORetentionSettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "retention_settings_failed")
		return payload, domain.HardwareIORetentionSettings{}, false
	}
	settings := payload.apply(stored)
	if err := settings.Validate(); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_retention_settings", "message": err.Error()})
		return payload, domain.HardwareIORetentionSettings{}, false
	}
	return payload, settings, true
}

// previewHardwareIOPurge never deletes. It is a POST only so the settings being
// previewed travel in the same body shape the purge uses.
func (s *Server) previewHardwareIOPurge(writer http.ResponseWriter, request *http.Request) {
	_, settings, ok := s.retentionRequest(writer, request)
	if !ok {
		return
	}
	plan, err := s.store.HardwareIOPurgePlan(request.Context(), settings, time.Now().UTC())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"ok": false, "error": "hardware_io_preview_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "plan": plan})
}

func (s *Server) purgeHardwareIO(writer http.ResponseWriter, request *http.Request) {
	payload, settings, ok := s.retentionRequest(writer, request)
	if !ok {
		return
	}
	if !payload.Confirm {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "confirmation_required", "message": "清理需要明确确认"})
		return
	}
	result, purgeErr := s.store.PurgeHardwareIO(request.Context(), settings, time.Now().UTC())
	// Audit even a failed or interrupted run, on a context that survives the
	// client disconnecting, so every confirmed deletion leaves a record.
	auditRequest := request.WithContext(context.WithoutCancel(request.Context()))
	auditData := map[string]any{
		"retention_days": settings.RetentionDays, "max_delete_rows": settings.MaxDeleteRows,
		"cutoff": result.Plan.Cutoff, "run_start_sequence": result.Plan.RunStartSequence,
		"run_end_sequence": result.Plan.RunEndSequence, "planned_rows": result.Plan.RunRows,
		"deleted_rows": result.DeletedRows, "batches": result.Batches, "partial": result.Partial,
		"skip_reason": result.SkipReason, "duration_ms": result.DurationMS,
	}
	if purgeErr != nil {
		auditData["error"] = purgeErr.Error()
	}
	occurredAt := time.Now().UTC()
	s.audit(auditRequest, "data.hardware_io.purge", "database_table", "hardware_io", auditData)
	audit := map[string]any{
		"action": "data.hardware_io.purge", "resource_type": "database_table", "resource_id": "hardware_io",
		"occurred_at": occurredAt,
	}
	if purgeErr != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "hardware_io_purge_failed", "message": purgeErr.Error(), "result": result, "audit": audit,
		})
		return
	}
	response := map[string]any{"ok": true, "result": result, "audit": audit}
	if stats, err := s.store.DatabaseStats(auditRequest.Context()); err == nil {
		response["database"] = stats
	}
	writeJSON(writer, http.StatusOK, response)
}
