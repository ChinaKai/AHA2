package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) taskLifecycle(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	task, err := s.store.Task(ctx, request.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "task_lifecycle_failed")
		return
	}
	lifecycle, err := s.app.TaskLifecycle(ctx, task)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "task_lifecycle_failed")
		return
	}
	tasks := []domain.Task{task}
	s.applyTaskTokenTotals(ctx, tasks)
	lifecycle.Stats.Tokens = tasks[0].TotalTokens
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "lifecycle": lifecycle})
}
