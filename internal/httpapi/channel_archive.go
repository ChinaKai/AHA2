package httpapi

import (
	"context"
	"net/http"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) decorateChannelProject(ctx context.Context, item *domain.Project) {
	instance, err := s.store.ChannelInstanceForHostProject(ctx, item.ID)
	if err != nil {
		return
	}
	item.ChannelInstanceID = instance.ID
	item.ChannelRetired = instance.Retired
	if instance.Retired {
		item.ReadOnlyReason = "channel_retired"
	}
}

func (s *Server) decorateChannelProjects(ctx context.Context, items []domain.Project) {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	instances, err := s.store.ChannelInstancesForHostProjects(ctx, ids)
	if err != nil {
		return
	}
	for index := range items {
		instance, ok := instances[items[index].ID]
		if !ok {
			continue
		}
		items[index].ChannelInstanceID = instance.ID
		items[index].ChannelRetired = instance.Retired
		if instance.Retired {
			items[index].ReadOnlyReason = "channel_retired"
		}
	}
}

func (s *Server) decorateChannelWorkspace(ctx context.Context, item *domain.Workspace) {
	instance, err := s.store.RetiredChannelInstanceForWorkspace(ctx, item.ID)
	if err != nil {
		return
	}
	item.ReadOnly = true
	item.ReadOnlyReason = "channel_retired"
	item.ChannelInstanceID = instance.ID
	item.ChannelRetired = true
}

func (s *Server) decorateChannelWorkspaces(ctx context.Context, items []domain.Workspace) {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	instances, err := s.store.RetiredChannelInstancesForHostWorkspaces(ctx, ids)
	if err != nil {
		return
	}
	for index := range items {
		instance, ok := instances[items[index].ID]
		if !ok {
			continue
		}
		items[index].ReadOnly = true
		items[index].ReadOnlyReason = "channel_retired"
		items[index].ChannelInstanceID = instance.ID
		items[index].ChannelRetired = true
	}
}

func (s *Server) decorateChannelTask(ctx context.Context, item *domain.Task) {
	instance, err := s.store.RetiredChannelInstanceForTask(ctx, item.ID)
	if err != nil {
		return
	}
	item.ReadOnly = true
	item.ReadOnlyReason = "channel_retired"
	item.ChannelInstanceID = instance.ID
	item.ChannelRetired = true
}

func (s *Server) decorateChannelTasks(ctx context.Context, items []domain.Task) {
	projectIDs := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		if item.OwnerDeviceID == "" && !seen[item.ProjectID] {
			seen[item.ProjectID] = true
			projectIDs = append(projectIDs, item.ProjectID)
		}
	}
	instances, err := s.store.RetiredChannelInstancesForHostProjects(ctx, projectIDs)
	if err != nil {
		return
	}
	for index := range items {
		if items[index].OwnerDeviceID != "" {
			continue
		}
		instance, ok := instances[items[index].ProjectID]
		if !ok {
			continue
		}
		items[index].ReadOnly = true
		items[index].ReadOnlyReason = "channel_retired"
		items[index].ChannelInstanceID = instance.ID
		items[index].ChannelRetired = true
	}
}

func (s *Server) rejectRetiredChannelTaskWrite(writer http.ResponseWriter, request *http.Request, taskID string) bool {
	if _, err := s.store.RetiredChannelInstanceForTask(request.Context(), taskID); err != nil {
		return false
	}
	writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "channel_archive_read_only", "message": "渠道实例已归档，宿主 Task 只允许查看和导出"})
	return true
}

func (s *Server) rejectRetiredChannelWorkspaceWrite(writer http.ResponseWriter, request *http.Request, workspaceID string) bool {
	if _, err := s.store.RetiredChannelInstanceForWorkspace(request.Context(), workspaceID); err != nil {
		return false
	}
	writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "channel_archive_read_only", "message": "渠道实例已归档，宿主 Workspace 只允许查看和导出"})
	return true
}

func (s *Server) rejectRetiredChannelProjectWrite(writer http.ResponseWriter, request *http.Request, projectID string) bool {
	if _, err := s.store.RetiredChannelInstanceForProject(request.Context(), projectID); err != nil {
		return false
	}
	writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "channel_archive_read_only", "message": "渠道实例已归档，宿主 Project 只允许查看和导出"})
	return true
}
