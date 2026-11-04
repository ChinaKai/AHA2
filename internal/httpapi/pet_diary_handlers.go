package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
)

func (s *Server) listPetDiary(writer http.ResponseWriter, request *http.Request) {
	items, err := s.app.PetDiary(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pet_diary_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "entries": items})
}

func (s *Server) petWorkbench(writer http.ResponseWriter, request *http.Request) {
	// A workbench read can wait behind a busy database. Bound it so the page
	// gets a retryable error instead of an indefinite "reading" state.
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	query := request.URL.Query()
	view, err := s.app.PetWorkbenchWithOptions(ctx, app.PetWorkbenchOptions{
		ApprovalLimit: petWorkbenchQueryLimit(query.Get("approval_limit"), 50),
		DiaryLimit:    petWorkbenchQueryLimit(query.Get("diary_limit"), 30),
		WorklogLimit:  petWorkbenchQueryLimit(query.Get("worklog_limit"), 30),
	})
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pet_workbench_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "diary": view.Diary, "diary_total": view.DiaryTotal,
		"core": view.Core, "worklog": view.Worklog, "worklog_total": view.WorklogTotal,
		"approvals": view.Approvals, "approval_total": view.ApprovalTotal,
		"watches": view.Watches, "task": view.Task, "updated_at": view.UpdatedAt,
		"channels": view.Channels, "channel_note": view.ChannelNote,
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

func (s *Server) savePetWorkbenchDocument(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	section := request.PathValue("section")
	name := request.PathValue("name")
	if err := s.app.PetSaveWorkbenchDocument(request.Context(), section, name, input.Content); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "save_pet_document_failed", "message": err.Error()})
		return
	}
	s.audit(request, "pet.workbench.document.save", "pet_workspace", section+"/"+name, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
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
	item, err := s.app.ResolvePetApproval(request.Context(), request.PathValue("id"), input.Decision, reason)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "已经处理过") {
			status = http.StatusConflict
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": "pet_approval_resolve_failed", "message": err.Error()})
		return
	}
	s.audit(request, "pet.approval.resolve", "pet_approval", item.ID, map[string]any{"status": item.Status})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "approval": item})
}
