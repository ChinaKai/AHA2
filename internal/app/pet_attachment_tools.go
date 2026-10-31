package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const petAttachmentLimit = 10 << 20

type PetAttachmentBrief struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at"`
}

func (s *Service) PetTaskAttachments(ctx context.Context, taskID string) ([]PetAttachmentBrief, error) {
	task, err := s.petViewableTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListTaskAttachmentsIncludingDrafts(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	result := make([]PetAttachmentBrief, 0, len(items))
	for _, item := range items {
		result = append(result, PetAttachmentBrief{
			ID: item.ID, Name: item.Name, MediaType: item.MediaType,
			Size: item.Size, CreatedAt: item.CreatedAt.Format(time.RFC3339),
		})
	}
	return result, nil
}

type PetUploadAttachmentInput struct {
	TaskID        string `json:"task_id"`
	Name          string `json:"name"`
	MediaType     string `json:"media_type"`
	ContentBase64 string `json:"content_base64"`
}

func (s *Service) PetUploadTaskAttachment(ctx context.Context, input PetUploadAttachmentInput) (PetAttachmentBrief, error) {
	task, _, err := s.petManageableTask(ctx, input.TaskID)
	if err != nil {
		return PetAttachmentBrief{}, err
	}
	if task.Status == domain.TaskCompleted || task.Status == domain.TaskCancelled || task.Status == domain.TaskBlocked {
		return PetAttachmentBrief{}, fmt.Errorf("Task 已处于 %s，不能添加附件", task.Status)
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = "attachment"
	}
	encoded := strings.TrimSpace(input.ContentBase64)
	if encoded == "" {
		return PetAttachmentBrief{}, fmt.Errorf("附件内容不能为空")
	}
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return PetAttachmentBrief{}, fmt.Errorf("附件内容不是合法 base64")
	}
	if len(content) == 0 || len(content) > petAttachmentLimit {
		return PetAttachmentBrief{}, fmt.Errorf("附件大小必须在 1 字节到 10MB 之间")
	}
	mediaType := strings.TrimSpace(input.MediaType)
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = http.DetectContentType(content)
	}
	item, err := s.store.CreateAttachment(ctx, task.ID, name, mediaType, content, s.now().UTC())
	if err != nil {
		return PetAttachmentBrief{}, err
	}
	return PetAttachmentBrief{
		ID: item.ID, Name: item.Name, MediaType: item.MediaType,
		Size: item.Size, CreatedAt: item.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (s *Service) PetDeleteTaskAttachment(ctx context.Context, taskID, attachmentID string) error {
	task, _, err := s.petManageableTask(ctx, taskID)
	if err != nil {
		return err
	}
	return s.store.DeleteDraftAttachment(ctx, task.ID, strings.TrimSpace(attachmentID))
}
