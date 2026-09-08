package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func (s *Server) deleteTask(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if s.store.IsManagedChannelTask(request.Context(), id) {
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "managed_channel_resource", "message": "渠道宿主 Task 只能在渠道管理流程中删除"})
		return
	}
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
	s.cleanupTaskHardware(request.Context(), id)
	if err := s.store.DeleteTaskWithSyncTombstone(request.Context(), id); err != nil {
		writeError(writer, http.StatusInternalServerError, "delete_task_failed")
		return
	}
	if task.RuntimeConfigSnapshotID != "" {
		_ = s.store.DeleteRuntimeSnapshot(request.Context(), task.RuntimeConfigSnapshotID)
	}
	s.audit(request, "task.delete", "task", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) retireRemoteTaskMirror(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	mirror, err := s.store.RetireRemoteTaskMirror(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "remote_task_not_found")
		return
	}
	if s.secrets != nil {
		refs := make([]string, 0, len(mirror.Hardware))
		for _, group := range mirror.Hardware {
			if group.PasswordConfigured {
				refs = append(refs, syncer.MirrorHardwareSecretRef(mirror.Task.OwnerDeviceID, mirror.SourceTaskID, group.ID))
			}
		}
		if len(refs) > 0 {
			_ = s.secrets.DeleteMany(refs)
		}
	}
	s.audit(request, "task.remote_mirror.retire", "task", id, map[string]any{
		"source_task_id": mirror.SourceTaskID, "owner_device_id": mirror.Task.OwnerDeviceID,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "source_task_id": mirror.SourceTaskID, "owner_device_id": mirror.Task.OwnerDeviceID,
	})
}

func (s *Server) retireRemoteWorkspaceMirror(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	workspace, synchronized, err := s.store.RetireRemoteWorkspaceMirror(request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeError(writer, http.StatusNotFound, "remote_workspace_not_found")
		case errors.Is(err, store.ErrWorkspaceMirrorHasTasks):
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "workspace_mirror_has_tasks", "message": "该 Workspace 仍有只读任务，请先移除对应的孤立任务"})
		case errors.Is(err, store.ErrReadOnlySyncMirror):
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "workspace_not_remote", "message": "只能移除其他设备的只读 Workspace 镜像"})
		default:
			writeError(writer, http.StatusInternalServerError, "retire_remote_workspace_failed")
		}
		return
	}
	if s.secrets != nil {
		_ = s.secrets.DeleteMany([]string{syncer.MirrorWorkspaceSecretRef(workspace.ID)})
	}
	s.audit(request, "workspace.remote_mirror.retire", "workspace", id, map[string]any{
		"owner_device_id": workspace.OwnerDeviceID, "synchronized": synchronized,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "owner_device_id": workspace.OwnerDeviceID, "synchronized": synchronized,
	})
}

func (s *Server) deleteWorkspace(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if s.store.IsManagedChannelWorkspace(request.Context(), id) {
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "managed_channel_resource", "message": "渠道宿主 Workspace 只能在渠道管理流程中删除"})
		return
	}
	item, err := s.store.Workspace(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "workspace_not_found")
		return
	}
	if item.ReadOnly {
		writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "workspace_read_only", "message": "该 Workspace 属于其他设备，不能在本机删除"})
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
		s.cleanupTaskHardware(request.Context(), task.ID)
		if err := s.store.DeleteTaskWithSyncTombstone(request.Context(), task.ID); err != nil {
			writeError(writer, http.StatusInternalServerError, "delete_workspace_task_failed")
			return
		}
		if task.RuntimeConfigSnapshotID != "" {
			if err := s.store.DeleteRuntimeSnapshot(request.Context(), task.RuntimeConfigSnapshotID); err != nil {
				writeError(writer, http.StatusInternalServerError, "delete_workspace_snapshot_failed")
				return
			}
		}
	}
	if err := s.store.DeleteWorkspaceWithSyncTombstone(request.Context(), id); err != nil {
		if errors.Is(err, store.ErrWorkspaceInUse) {
			writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "workspace_in_use", "message": "Workspace 仍被任务、运行记录或 Backend Session 引用"})
			return
		}
		writeError(writer, http.StatusInternalServerError, "delete_workspace_failed")
		return
	}
	s.cleanupWorkspaceSecret(item)
	s.audit(request, "workspace.delete", "workspace", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) deleteProject(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if s.store.IsManagedChannelProject(request.Context(), id) {
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "managed_channel_resource", "message": "渠道宿主 Project 只能在渠道管理流程中删除"})
		return
	}
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
		s.cleanupTaskHardware(request.Context(), task.ID)
		_ = s.store.DeleteTaskWithSyncTombstone(request.Context(), task.ID)
		if task.RuntimeConfigSnapshotID != "" {
			_ = s.store.DeleteRuntimeSnapshot(request.Context(), task.RuntimeConfigSnapshotID)
		}
	}
	workspaces, _ := s.store.ListWorkspaces(request.Context(), "")
	for _, workspace := range workspaces {
		if workspace.ProjectID == id {
			if workspace.ReadOnly {
				_ = s.store.DeleteWorkspace(request.Context(), workspace.ID)
			} else {
				_ = s.store.DeleteWorkspaceWithSyncTombstone(request.Context(), workspace.ID)
			}
			s.cleanupWorkspaceSecret(workspace)
		}
	}
	if err := s.store.DeleteProject(request.Context(), id); err != nil {
		writeError(writer, http.StatusInternalServerError, "delete_project_failed")
		return
	}
	s.audit(request, "project.delete", "project", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) cleanupWorkspaceSecret(workspace domain.Workspace) {
	if s.secrets != nil && workspace.SSHCredentialRef != "" {
		_ = s.secrets.DeleteMany([]string{workspace.SSHCredentialRef})
	}
}

func (s *Server) cleanupTaskHardware(ctx context.Context, taskID string) {
	if s.hardware != nil {
		s.hardware.DisconnectTask(taskID)
	}
	if s.secrets == nil {
		return
	}
	groups, _ := s.store.HardwareGroups(ctx, taskID)
	var refs []string
	for _, group := range groups {
		if group.CredentialRef != "" {
			refs = append(refs, group.CredentialRef)
		}
	}
	if len(refs) > 0 {
		_ = s.secrets.DeleteMany(refs)
	}
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
