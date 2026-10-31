package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

type PetRemoteTaskBrief struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	ProjectID     string `json:"project_id"`
	WorkspaceID   string `json:"workspace_id"`
	OwnerDeviceID string `json:"owner_device_id,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

// PetRemoteTaskMirrors lists read-only Task mirrors from other devices so the
// pet can inspect or take them over. It uses the same view-tasks permission as
// local task reads.
func (s *Service) PetRemoteTaskMirrors(ctx context.Context, projectID string) ([]PetRemoteTaskBrief, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return nil, err
	}
	if !assistant.PetAllowViewTasks {
		return nil, fmt.Errorf("主人未开启查看其他任务信息权限")
	}
	mirrors, err := s.store.RemoteTaskMirrors(ctx, strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	result := make([]PetRemoteTaskBrief, 0, len(mirrors))
	for _, mirror := range mirrors {
		result = append(result, PetRemoteTaskBrief{
			ID: mirror.Task.ID, Title: mirror.Task.Title, Status: string(mirror.Task.Status),
			ProjectID: mirror.Task.ProjectID, WorkspaceID: mirror.Task.WorkspaceID,
			OwnerDeviceID: mirror.Task.OwnerDeviceID, UpdatedAt: mirror.Task.UpdatedAt.Format(time.RFC3339),
		})
		if len(result) == 40 {
			break
		}
	}
	return result, nil
}

type PetRemoteMirrorResult struct {
	ID            string `json:"id"`
	OwnerDeviceID string `json:"owner_device_id,omitempty"`
	SourceID      string `json:"source_id,omitempty"`
	Synchronized  bool   `json:"synchronized"`
}

func (s *Service) PetRetireRemoteTaskMirror(ctx context.Context, taskID string) (PetRemoteMirrorResult, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return PetRemoteMirrorResult{}, err
	}
	if !assistant.PetAllowCreateTasks {
		return PetRemoteMirrorResult{}, fmt.Errorf("主人未开启主动管理任务权限")
	}
	mirror, err := s.store.RetireRemoteTaskMirror(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PetRemoteMirrorResult{}, fmt.Errorf("远程 Task 镜像不存在")
		}
		return PetRemoteMirrorResult{}, err
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
	return PetRemoteMirrorResult{
		ID: taskID, OwnerDeviceID: mirror.Task.OwnerDeviceID,
		SourceID: mirror.SourceTaskID, Synchronized: true,
	}, nil
}

func (s *Service) PetRetireRemoteWorkspaceMirror(ctx context.Context, workspaceID string) (PetRemoteMirrorResult, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return PetRemoteMirrorResult{}, err
	}
	if !assistant.PetAllowCreateWorkspaces {
		return PetRemoteMirrorResult{}, fmt.Errorf("主人未开启主动管理项目/Workspace 权限")
	}
	workspace, synchronized, err := s.store.RetireRemoteWorkspaceMirror(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PetRemoteMirrorResult{}, fmt.Errorf("远程 Workspace 镜像不存在")
		}
		return PetRemoteMirrorResult{}, err
	}
	if s.secrets != nil {
		_ = s.secrets.DeleteMany([]string{syncer.MirrorWorkspaceSecretRef(workspace.ID)})
	}
	return PetRemoteMirrorResult{
		ID: workspaceID, OwnerDeviceID: workspace.OwnerDeviceID,
		SourceID: workspace.SourceWorkspaceID, Synchronized: synchronized,
	}, nil
}
