package app

import (
	"context"
	"database/sql"
	"errors"
	"regexp"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

var (
	ErrAgentAttachmentInvalidID = errors.New("invalid attachment ID")
	ErrAgentAttachmentNotFound  = errors.New("attachment not found in this Task")
	ErrAgentAttachmentBlob      = errors.New("attachment content is unavailable")
	agentAttachmentID           = regexp.MustCompile(`^attachment_(?:[0-9a-f]{20}|[0-9a-f]{64})$`)
)

type AgentAttachmentMaterialization struct {
	workspace.ScratchAttachment
	MediaType string `json:"media_type"`
}

func (s *Service) MaterializeAgentAttachment(ctx context.Context, call AgentCallContext, attachmentID string, limit int64) (AgentAttachmentMaterialization, error) {
	if !agentAttachmentID.MatchString(attachmentID) {
		return AgentAttachmentMaterialization{}, ErrAgentAttachmentInvalidID
	}
	item, err := s.store.Attachment(ctx, call.Task.ID, attachmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentAttachmentMaterialization{}, ErrAgentAttachmentNotFound
	}
	if err != nil {
		return AgentAttachmentMaterialization{}, err
	}
	if item.Size > limit {
		return AgentAttachmentMaterialization{ScratchAttachment: workspace.ScratchAttachment{Size: item.Size}}, workspace.ErrScratchAttachmentTooLarge
	}
	file, err := s.store.OpenAttachmentContent(item)
	if err != nil {
		return AgentAttachmentMaterialization{}, ErrAgentAttachmentBlob
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return AgentAttachmentMaterialization{}, ErrAgentAttachmentBlob
	}
	if info.Size() > limit {
		return AgentAttachmentMaterialization{ScratchAttachment: workspace.ScratchAttachment{Size: info.Size()}}, workspace.ErrScratchAttachmentTooLarge
	}

	boundWorkspace, err := s.store.Workspace(ctx, call.Task.WorkspaceID)
	if err != nil {
		return AgentAttachmentMaterialization{}, workspace.ErrScratchAttachmentDestination
	}
	if boundWorkspace.SSHCredentialRef != "" && s.secrets != nil {
		password, ok := s.secrets.Get(boundWorkspace.SSHCredentialRef)
		if !ok {
			return AgentAttachmentMaterialization{}, workspace.ErrScratchAttachmentDestination
		}
		boundWorkspace.SSHPassword = password
	}
	workDir := call.Task.TaskWorkspacePath
	if workDir == "" {
		workDir = boundWorkspace.RootPath
	}
	result, err := workspace.MaterializeScratchAttachment(ctx, boundWorkspace, workDir, call.Task.ID, call.Turn.AgentID, file, item.Size, item.SHA256, limit)
	return AgentAttachmentMaterialization{ScratchAttachment: result, MediaType: item.MediaType}, err
}
