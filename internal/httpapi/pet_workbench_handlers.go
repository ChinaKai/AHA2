package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) petWorkbench(writer http.ResponseWriter, request *http.Request) {
	// A workbench read can wait behind a busy database. Bound it so the page
	// gets a retryable error instead of an indefinite "reading" state.
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	query := request.URL.Query()
	view, err := s.app.PetWorkbenchWithOptions(ctx, app.PetWorkbenchOptions{
		ApprovalLimit: petWorkbenchQueryLimit(query.Get("approval_limit"), 50),
		WorklogLimit:  petWorkbenchQueryLimit(query.Get("worklog_limit"), 30),
	})
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pet_workbench_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok":   true,
		"core": view.Core, "worklog": view.Worklog, "worklog_total": view.WorklogTotal,
		"approvals": view.Approvals, "approval_total": view.ApprovalTotal,
		"approval_counts": view.ApprovalCounts,
		"watches":         view.Watches, "task": view.Task, "updated_at": view.UpdatedAt,
		"channels": view.Channels, "channel_total": view.ChannelTotal, "channel_note": view.ChannelNote,
		"inspection": view.Inspection, "desk": view.Desk,
	})
}

func petWorkbenchQueryLimit(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func (s *Server) followPetWatch(writer http.ResponseWriter, request *http.Request) {
	taskID := strings.TrimSpace(request.PathValue("task"))
	watch, err := s.app.FollowPetTask(request.Context(), taskID)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "pet_watch_follow_failed", "message": err.Error(),
		})
		return
	}
	s.audit(request, "pet.watch.follow", "task", taskID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "watch": watch})
}

func (s *Server) deletePetWatch(writer http.ResponseWriter, request *http.Request) {
	taskID := strings.TrimSpace(request.PathValue("task"))
	if err := s.app.RemovePetTaskWatch(request.Context(), taskID); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "pet_watch_remove_failed", "message": err.Error(),
		})
		return
	}
	s.audit(request, "pet.watch.remove", "task", taskID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) updatePetWatch(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Ignored *bool `json:"ignored"`
	}
	if err := decodeJSON(request, &input); err != nil || input.Ignored == nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	taskID := strings.TrimSpace(request.PathValue("task"))
	watch, err := s.app.SetPetTaskWatchIgnored(request.Context(), taskID, *input.Ignored)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "pet_watch_update_failed", "message": err.Error(),
		})
		return
	}
	s.audit(request, "pet.watch.update", "task", taskID, map[string]any{"ignored": watch.Ignored})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "watch": watch})
}

func (s *Server) savePetWorkbenchDocument(section string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		var input struct {
			Content string `json:"content"`
		}
		if err := decodeJSON(request, &input); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_json")
			return
		}
		name := request.PathValue("name")
		if err := s.app.PetSaveWorkbenchDocument(request.Context(), section, name, input.Content); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "save_pet_document_failed", "message": err.Error()})
			return
		}
		s.audit(request, "pet.workbench.document.save", "pet_workspace", section+"/"+name, nil)
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
	}
}

func (s *Server) resolvePetApproval(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Decision       string `json:"decision"`
		Note           string `json:"note"`
		DecisionReason string `json:"decision_reason"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	reason := strings.TrimSpace(input.DecisionReason)
	if reason == "" {
		reason = input.Note
	}
	audited := false
	item, err := s.app.ResolvePetApprovalBeforeExecution(request.Context(), request.PathValue("id"), input.Decision, reason, func(item domain.PetApproval) {
		audited = true
		s.audit(request, "pet.approval.resolve", "pet_approval", item.ID, map[string]any{"status": item.Status})
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "已经处理过") {
			status = http.StatusConflict
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": "pet_approval_resolve_failed", "message": err.Error()})
		return
	}
	if !audited {
		s.audit(request, "pet.approval.resolve", "pet_approval", item.ID, map[string]any{"status": item.Status})
	}
	// A heavy delete was claimed and is running in the background; report the
	// acceptance instead of holding this connection open for the transaction.
	if item.Status == "executing" {
		writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "status": "executing"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "approval": item})
}

func (s *Server) petPendingApprovalCount(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	pending, err := s.app.PetPendingApprovalCount(ctx)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pet_pending_approval_count_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "pending": pending})
}

// petInsights serves the workbench "last 7 days" block. It is a separate
// request so the desk renders first and a slow or failed count only affects
// this block.
func (s *Server) petInsights(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	insights, err := s.app.PetInsights(ctx, s.petInsightTokens)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pet_insights_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "insights": insights})
}

// petInsightTokens totals the pet's tokens with the Task list's algorithm
// (taskTotalTokens). That algorithm treats usage differently per backend:
//   - claude and dsh: the session total is the sum of its Turns' usage, so
//     the Turns that started in the window can be summed on their own;
//   - codex (and anything else): the session row carries the provider's
//     running session total and a Turn only holds its delta against that
//     baseline. Without the session row the algorithm keeps only the latest
//     Turn, and with it the whole session is counted, so a window cannot be
//     cut out of it.
//
// When every window Turn ran on a summable backend, the window number is
// returned. Otherwise the pet Task's cumulative total (the number the Task
// list shows) is returned and labelled as such.
func (s *Server) petInsightTokens(ctx context.Context, taskID string, windowTurns []domain.Turn) (int64, string, error) {
	backends, err := s.turnBackends(ctx, windowTurns)
	if err != nil {
		return 0, "", err
	}
	windowed := true
	for _, turn := range windowTurns {
		if backend := backends[turn.RuntimeConfigSnapshotID]; backend != "claude" && backend != "dsh" {
			windowed = false
			break
		}
	}
	if windowed {
		// No session rows: each session's Turns are then summed by the
		// per-session branch, and a session's stored running total (which
		// covers Turns outside the window) is never added.
		return int64(taskTotalTokens(windowTurns, nil, backends)), app.PetTokenScopeWindow, nil
	}
	turnsByTask, err := s.store.ListTurnTokenUsageForTasks(ctx, []string{taskID})
	if err != nil {
		return 0, "", err
	}
	sessionsByTask, err := s.store.ListBackendSessionsForTasks(ctx, []string{taskID})
	if err != nil {
		return 0, "", err
	}
	all := turnsByTask[taskID]
	if backends, err = s.turnBackends(ctx, all); err != nil {
		return 0, "", err
	}
	return int64(taskTotalTokens(all, sessionsByTask[taskID], backends)), app.PetTokenScopeCumulative, nil
}

// turnBackends maps each Turn's runtime snapshot to its backend name.
func (s *Server) turnBackends(ctx context.Context, turns []domain.Turn) (map[string]string, error) {
	ids := []string{}
	seen := map[string]bool{}
	for _, turn := range turns {
		if turn.RuntimeConfigSnapshotID != "" && !seen[turn.RuntimeConfigSnapshotID] {
			seen[turn.RuntimeConfigSnapshotID] = true
			ids = append(ids, turn.RuntimeConfigSnapshotID)
		}
	}
	snapshots, err := s.store.RuntimeSnapshotsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	backends := make(map[string]string, len(snapshots))
	for id, snapshot := range snapshots {
		backends[id] = snapshot.Backend
	}
	return backends, nil
}

// petApprovalDetail returns one pet approval. The overview opens approvals
// that may be older than the loaded approvals page.
func (s *Server) petApprovalDetail(writer http.ResponseWriter, request *http.Request) {
	item, err := s.app.PetApproval(request.Context(), request.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "pet_approval_not_found")
			return
		}
		writeError(writer, http.StatusInternalServerError, "pet_approval_read_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "approval": item})
}
