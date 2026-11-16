package httpapi

import (
	"context"
	"net/http"
	"time"
)

// databaseCompactionPlan reports whether a manual VACUUM can run. It never
// changes the database.
func (s *Server) databaseCompactionPlan(writer http.ResponseWriter, request *http.Request) {
	plan, err := s.store.DatabaseCompactionPlan(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"ok": false, "error": "compaction_plan_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "plan": plan})
}

// compactDatabase performs one confirmed compaction. Blocked prechecks return
// 409 with the plan so the dialog can explain what must finish first.
func (s *Server) compactDatabase(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Confirm bool `json:"confirm"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if !payload.Confirm {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "confirmation_required", "message": "压缩数据库需要明确确认"})
		return
	}
	result, compactErr := s.store.CompactDatabase(request.Context())
	auditRequest := request.WithContext(context.WithoutCancel(request.Context()))
	auditData := map[string]any{
		"before_bytes": result.BeforeBytes, "after_bytes": result.AfterBytes,
		"freed_bytes": result.FreedBytes, "backup_path": result.BackupPath,
		"integrity_check": result.IntegrityCheck, "restored": result.Restored,
		"restore_error": result.RestoreError, "skip_reason": result.SkipReason,
		"duration_ms": result.DurationMS,
	}
	if compactErr != nil {
		auditData["error"] = compactErr.Error()
	}
	occurredAt := time.Now().UTC()
	s.audit(auditRequest, "data.database.compact", "database_file", "aha2.db", auditData)
	audit := map[string]any{
		"action": "data.database.compact", "resource_type": "database_file",
		"resource_id": "aha2.db", "occurred_at": occurredAt,
	}
	if compactErr != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "database_compaction_failed", "message": compactErr.Error(),
			"result": result, "audit": audit,
		})
		return
	}
	if result.SkipReason != "" {
		// Every preflight refusal (active tasks/turns, pending approvals, low
		// disk, active writes, another compaction) is a conflict, not a server
		// error. The reasons are comma-separated in result.SkipReason.
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "database_compaction_blocked", "message": result.SkipReason,
			"result": result, "audit": audit,
		})
		return
	}
	response := map[string]any{"ok": true, "result": result, "audit": audit}
	if stats, err := s.store.DatabaseStats(auditRequest.Context(), true); err == nil {
		response["database"] = stats
	}
	writeJSON(writer, http.StatusOK, response)
}
