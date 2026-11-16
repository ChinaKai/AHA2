package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const petAttachmentLimit = 200 << 20

// validatePetAttachmentSize is split out so boundary tests can cover the
// 200MiB cap without constructing a 200MiB payload.
func validatePetAttachmentSize(size int) error {
	if size < 1 || size > petAttachmentLimit {
		return fmt.Errorf("附件大小必须在 1 字节到 200MiB 之间")
	}
	return nil
}

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
	if err := validatePetAttachmentSize(len(content)); err != nil {
		return PetAttachmentBrief{}, err
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

type PetCopyAttachmentInput struct {
	TaskID       string `json:"task_id"`
	AttachmentID string `json:"attachment_id"`
}

// PetAttachmentCopy is returned by the copy tool. It carries the same
// identity fields as the source attachment plus the sha256 that proves the
// clone reuses the existing blob instead of uploading new bytes.
type PetAttachmentCopy struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	CreatedAt string `json:"created_at"`
}

// PetCopyTaskAttachment clones a bound or draft attachment of the same Task
// into a new unbound attachment so it can be sent again after a failed Turn
// consumed the original binding.
func (s *Service) PetCopyTaskAttachment(ctx context.Context, input PetCopyAttachmentInput) (PetAttachmentCopy, error) {
	task, _, err := s.petManageableTask(ctx, input.TaskID)
	if err != nil {
		return PetAttachmentCopy{}, err
	}
	if task.Status == domain.TaskCompleted || task.Status == domain.TaskCancelled || task.Status == domain.TaskBlocked {
		return PetAttachmentCopy{}, fmt.Errorf("Task 已处于 %s，不能复制附件", task.Status)
	}
	attachmentID := strings.TrimSpace(input.AttachmentID)
	if attachmentID == "" {
		return PetAttachmentCopy{}, fmt.Errorf("attachment_id 不能为空")
	}
	item, err := s.store.CloneAttachment(ctx, task.ID, attachmentID, s.now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return PetAttachmentCopy{}, fmt.Errorf("附件不存在或不属于该 Task")
	}
	if err != nil {
		return PetAttachmentCopy{}, err
	}
	return PetAttachmentCopy{
		ID: item.ID, Name: item.Name, MediaType: item.MediaType,
		Size: item.Size, SHA256: item.SHA256, CreatedAt: item.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (s *Service) PetDeleteTaskAttachment(ctx context.Context, taskID, attachmentID string) error {
	task, _, err := s.petManageableTask(ctx, taskID)
	if err != nil {
		return err
	}
	return s.store.DeleteDraftAttachment(ctx, task.ID, strings.TrimSpace(attachmentID))
}
