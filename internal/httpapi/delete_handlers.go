package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

func (s *Server) deleteTask(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	task, err := s.store.Task(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	active, err := s.taskHasActiveTurn(request.Context(), task.ID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "delete_task_failed")
		return
	}
	if active {
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "active_turn_exists", "message": "任务有执行中的 Turn，请先中断或等待完成后再删除",
		})
		return
	}
	if err := s.store.DeleteTask(request.Context(), id); err != nil {
		writeError(writer, http.StatusInternalServerError, "delete_task_failed")
		return
	}
	if task.RuntimeConfigSnapshotID != "" {
		_ = s.store.DeleteRuntimeSnapshot(request.Context(), task.RuntimeConfigSnapshotID)
	}
	s.audit(request, "task.delete", "task", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) deleteWorkspace(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if _, err := s.store.Workspace(request.Context(), id); err != nil {
		writeError(writer, http.StatusNotFound, "workspace_not_found")
		return
	}
	tasks, _ := s.store.ListTasks(request.Context(), "")
	var active []string
	for _, task := range tasks {
		if task.WorkspaceID != id {
			continue
		}
		ok, err := s.taskHasActiveTurn(request.Context(), task.ID)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "delete_workspace_failed")
			return
		}
		if ok {
			active = append(active, task.ID)
		}
	}
	if len(active) > 0 {
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "active_turn_exists", "message": "Workspace 下有执行中的任务，请先中断或等待完成后再删除",
		})
		return
	}
	for _, task := range tasks {
		if task.WorkspaceID != id {
			continue
		}
		_ = s.store.DeleteTask(request.Context(), task.ID)
		if task.RuntimeConfigSnapshotID != "" {
			_ = s.store.DeleteRuntimeSnapshot(request.Context(), task.RuntimeConfigSnapshotID)
		}
	}
	if err := s.store.DeleteWorkspace(request.Context(), id); err != nil {
		writeError(writer, http.StatusInternalServerError, "delete_workspace_failed")
		return
	}
	s.audit(request, "workspace.delete", "workspace", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) deleteProject(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if _, err := s.store.Project(request.Context(), id); err != nil {
		writeError(writer, http.StatusNotFound, "project_not_found")
		return
	}
	tasks, _ := s.store.ListTasks(request.Context(), "")
	var active []string
	for _, task := range tasks {
		if task.ProjectID != id {
			continue
		}
		ok, err := s.taskHasActiveTurn(request.Context(), task.ID)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "delete_project_failed")
			return
		}
		if ok {
			active = append(active, task.ID)
		}
	}
	if len(active) > 0 {
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "active_turn_exists", "message": "项目下有执行中的任务，请先中断或等待完成后再删除",
		})
		return
	}
	for _, task := range tasks {
		if task.ProjectID != id {
			continue
		}
		_ = s.store.DeleteTask(request.Context(), task.ID)
		if task.RuntimeConfigSnapshotID != "" {
			_ = s.store.DeleteRuntimeSnapshot(request.Context(), task.RuntimeConfigSnapshotID)
		}
	}
	workspaces, _ := s.store.ListWorkspaces(request.Context(), "")
	for _, workspace := range workspaces {
		if workspace.ProjectID == id {
			_ = s.store.DeleteWorkspace(request.Context(), workspace.ID)
		}
	}
	if err := s.store.DeleteProject(request.Context(), id); err != nil {
		writeError(writer, http.StatusInternalServerError, "delete_project_failed")
		return
	}
	s.audit(request, "project.delete", "project", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) taskHasActiveTurn(ctx context.Context, taskID string) (bool, error) {
	_, err := s.store.ActiveTurn(ctx, taskID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}
