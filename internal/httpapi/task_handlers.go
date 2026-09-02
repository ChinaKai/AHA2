package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func (s *Server) listTasks(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListTasks(request.Context(), request.URL.Query().Get("project_id"))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_tasks_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "tasks": items})
}

func (s *Server) createTask(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ProjectID       string `json:"project_id"`
		WorkspaceID     string `json:"workspace_id"`
		Title           string `json:"title"`
		Request         string `json:"request"`
		TargetBranch    string `json:"target_branch"`
		BaseCommit      string `json:"base_commit"`
		TaskBranch      string `json:"task_branch"`
		WorkspacePath   string `json:"task_workspace_path"`
		ModelID         string `json:"model_id"`
		ReasoningEffort string `json:"reasoning_effort"`
		Filesystem      string `json:"filesystem"`
		Approval        string `json:"approval"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	filesystem := strings.TrimSpace(payload.Filesystem)
	if filesystem == "" {
		filesystem = "workspace-write"
	}
	switch filesystem {
	case "read-only", "workspace-write", "danger-full-access":
	default:
		writeError(writer, http.StatusBadRequest, "invalid_filesystem")
		return
	}
	approval := strings.TrimSpace(payload.Approval)
	if approval == "" {
		approval = "never"
	}
	switch approval {
	case "never", "auto":
	default:
		writeError(writer, http.StatusBadRequest, "invalid_approval")
		return
	}
	item, err := s.app.CreateTask(request.Context(), app.CreateTaskInput{
		ProjectID: payload.ProjectID, WorkspaceID: payload.WorkspaceID, Title: payload.Title, Request: payload.Request,
		TargetBranch: payload.TargetBranch, BaseCommit: payload.BaseCommit, TaskBranch: payload.TaskBranch,
		WorkspacePath: payload.WorkspacePath, ModelID: payload.ModelID, ReasoningEffort: payload.ReasoningEffort,
		Filesystem: filesystem, Approval: approval,
	})
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "create_task_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.create", "task", item.ID, map[string]any{"project_id": item.ProjectID})
	turn, startErr := s.app.SubmitMessage(request.Context(), item.ID, item.OriginalRequest)
	if startErr != nil {
		writeJSON(writer, http.StatusCreated, map[string]any{
			"ok": true, "task": item, "start_error": startErr.Error(),
		})
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "task": item, "turn": turn})
}

func (s *Server) taskDetail(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	task, err := s.store.Task(request.Context(), taskID)
	if err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	messages, _ := s.store.ListMessages(request.Context(), taskID)
	turns, _ := s.store.ListTurns(request.Context(), taskID)
	memory, _ := s.store.TaskMemory(request.Context(), taskID)
	projectKB, _ := s.store.ListKnowledge(request.Context(), "project", task.ProjectID, nil)
	if messages == nil {
		messages = []domain.Message{}
	}
	if turns == nil {
		turns = []domain.Turn{}
	}
	if projectKB == nil {
		projectKB = []domain.KnowledgeEntry{}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "task": task, "messages": messages, "turns": turns,
		"memory": memory, "knowledge_candidates": projectKB,
	})
}

func (s *Server) submitMessage(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	turn, err := s.app.SubmitMessage(request.Context(), request.PathValue("id"), payload.Content)
	if err != nil {
		status := http.StatusBadRequest
		code := "submit_message_failed"
		if errors.Is(err, store.ErrActiveTurn) {
			status = http.StatusConflict
			code = "active_turn_exists"
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": code, "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "turn": turn})
}

func (s *Server) updateTaskTitle(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	var payload struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	title := strings.TrimSpace(payload.Title)
	if title == "" {
		writeError(writer, http.StatusBadRequest, "task_title_required")
		return
	}
	if err := s.store.UpdateTaskTitle(request.Context(), id, title, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		writeError(writer, http.StatusInternalServerError, "update_task_title_failed")
		return
	}
	task, err := s.store.Task(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	s.audit(request, "task.update_title", "task", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "task": task})
}

func (s *Server) completeTask(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if err := s.app.CompleteTask(request.Context(), taskID); err != nil {
		status := http.StatusBadRequest
		code := "complete_task_failed"
		if errors.Is(err, store.ErrActiveTurn) {
			status = http.StatusConflict
			code = "active_turn_exists"
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": code, "message": err.Error()})
		return
	}
	s.audit(request, "task.complete", "task", taskID, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) interruptTurn(writer http.ResponseWriter, request *http.Request) {
	turnID := request.PathValue("id")
	if err := s.app.InterruptTurn(request.Context(), turnID); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "interrupt_turn_failed", "message": err.Error()})
		return
	}
	s.audit(request, "turn.interrupt", "turn", turnID, nil)
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) allEvents(writer http.ResponseWriter, request *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusInternalServerError, "streaming_unsupported")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	channel, unsubscribe := s.app.Hub().SubscribeAll()
	defer unsubscribe()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event := <-channel:
			writeSSE(writer, event)
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(writer, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) taskEvents(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if _, err := s.store.Task(request.Context(), taskID); err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusInternalServerError, "streaming_unsupported")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	after := queryInt(request, "after", 0)
	history, _ := s.store.EventsAfter(request.Context(), "task", taskID, after, 500)
	for _, event := range history {
		writeSSE(writer, event)
	}
	flusher.Flush()
	channel, unsubscribe := s.app.Hub().Subscribe(taskID)
	defer unsubscribe()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event := <-channel:
			writeSSE(writer, event)
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(writer, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(writer http.ResponseWriter, event any) {
	data, _ := json.Marshal(event)
	_, _ = fmt.Fprintf(writer, "event: update\ndata: %s\n\n", data)
}
