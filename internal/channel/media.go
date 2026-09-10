package channel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const MaxMediaFileBytes int64 = 25 << 20
const MaxMediaImageBytes int64 = 10 * 1000 * 1000
const MaxMediaResources = 8

var errMediaRouteChanged = errors.New("media_route_changed")

func mediaImageType(mediaType string) bool {
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func (s *Service) inboundAttachments(ctx context.Context, receipt domain.ChannelInboxReceipt, conversation domain.ChannelConversation, taskID string, envelope domain.ChannelInboundEnvelope) ([]string, string, bool, error) {
	if len(envelope.Resources) == 0 {
		return nil, "", false, nil
	}
	if len(envelope.Resources) > MaxMediaResources {
		return nil, "每条消息最多支持 8 个图片或文件，请分批发送。", false, nil
	}
	if envelope.ExternalMessageID == "" {
		return nil, "媒体消息缺少来源，无法读取附件，请重新发送。", false, nil
	}
	var ids []string
	warning := ""
	pending := false
	for index, resource := range envelope.Resources {
		kind, key := stringField(resource, "type"), stringField(resource, "file_key")
		if (kind != "image" && kind != "file") || key == "" || len(key) > 1024 {
			warning = "本期仅支持图片和文件，其他媒体未读取。"
			continue
		}
		commandID := stableID("channel_media", receipt.ID, strconv.Itoa(index))
		command, err := s.store.ChannelCommand(ctx, receipt.InstanceID, commandID)
		if err != nil {
			command, err = s.store.EnqueueChannelCommand(ctx, domain.ChannelPluginCommand{
				ID: commandID, InstanceID: receipt.InstanceID, Kind: "download_resource", IdempotencyKey: commandID,
				Payload: map[string]any{"receipt_id": receipt.ID, "conversation_id": conversation.ID, "task_id": taskID, "message_id": envelope.ExternalMessageID, "resource_type": kind, "resource_key": key, "file_name": stringField(resource, "file_name")},
				State:   "pending", AvailableAt: s.now().UTC(), CreatedAt: s.now().UTC(),
			})
		}
		if err != nil {
			return nil, "", false, err
		}
		if stringField(command.Payload, "task_id") != taskID {
			return nil, "", false, errMediaRouteChanged
		}
		switch command.State {
		case "completed":
			attachmentID := stringField(command.Result, "attachment_id")
			item, err := s.store.Attachment(ctx, taskID, attachmentID)
			if err != nil || item.MessageID != "" {
				return nil, "", false, fmt.Errorf("media_attachment_unavailable")
			}
			ids = append(ids, attachmentID)
		case "failed":
			warning = "部分附件读取失败（诊断码：" + store.SafeChannelMediaErrorCode(command.LastErrorCode) + "）。请根据诊断码排查；通用失败不代表文件过大或缺少权限。"
		default:
			if s.now().UTC().Sub(command.CreatedAt) > 10*time.Minute {
				warning = "附件读取超时，请检查渠道连接后重新发送。"
			} else {
				pending = true
			}
		}
	}
	return ids, warning, pending, nil
}

func (s *Service) MediaCommand(ctx context.Context, claims RuntimeClaims, id, leaseID string) (domain.ChannelPluginCommand, error) {
	if !claims.Scopes["channel.media.upload"] {
		return domain.ChannelPluginCommand{}, fmt.Errorf("capability_scope_denied")
	}
	command, err := s.store.ChannelCommand(ctx, claims.InstanceID, id)
	if err != nil || command.Kind != "download_resource" {
		return domain.ChannelPluginCommand{}, fmt.Errorf("capability_scope_denied")
	}
	instance, err := s.store.ChannelInstance(ctx, claims.InstanceID)
	if err != nil || (instance.Status != "ready" && instance.Status != "degraded") {
		return domain.ChannelPluginCommand{}, fmt.Errorf("capability_scope_denied")
	}
	receipt, err := s.store.ChannelInboxReceipt(ctx, claims.InstanceID, stringField(command.Payload, "receipt_id"))
	if err != nil || (receipt.State != "processing" && receipt.State != "received" && command.State != "completed") {
		return domain.ChannelPluginCommand{}, fmt.Errorf("capability_scope_denied")
	}
	if command.State != "completed" && (command.State != "leased" || command.LeaseID != leaseID || !command.LeaseUntil.After(s.now().UTC())) {
		return domain.ChannelPluginCommand{}, store.ErrChannelRevision
	}
	conversation, err := s.store.ChannelConversation(ctx, stringField(command.Payload, "conversation_id"))
	if err != nil || conversation.InstanceID != claims.InstanceID {
		return domain.ChannelPluginCommand{}, fmt.Errorf("capability_scope_denied")
	}
	targetID := conversation.HostTaskID
	if route, routeErr := s.store.ActiveChannelTaskRoute(ctx, conversation.ID); routeErr == nil {
		targetID = route.TargetTaskID
	}
	if targetID != stringField(command.Payload, "task_id") {
		return domain.ChannelPluginCommand{}, fmt.Errorf("capability_scope_denied")
	}
	task, err := s.store.Task(ctx, targetID)
	if err != nil || task.ReadOnly {
		return domain.ChannelPluginCommand{}, fmt.Errorf("capability_scope_denied")
	}
	return command, nil
}

func (s *Service) ReceiveMediaAttachment(ctx context.Context, claims RuntimeClaims, id, leaseID, name string, content []byte) (domain.Attachment, error) {
	command, err := s.MediaCommand(ctx, claims, id, leaseID)
	if err != nil {
		return domain.Attachment{}, err
	}
	kind := stringField(command.Payload, "resource_type")
	limit := MaxMediaFileBytes
	mediaType := http.DetectContentType(content)
	if kind == "image" {
		limit = MaxMediaImageBytes
		if !mediaImageType(mediaType) {
			return domain.Attachment{}, fmt.Errorf("resource_type_unsupported")
		}
		if strings.TrimSpace(name) == "" {
			name = "image." + strings.TrimPrefix(mediaType, "image/")
		}
	} else if original := stringField(command.Payload, "file_name"); original != "" {
		name = original
	}
	if len(content) == 0 || int64(len(content)) > limit {
		return domain.Attachment{}, fmt.Errorf("resource_size_invalid")
	}
	item, err := s.store.CreateChannelAttachment(ctx, command.ID, stringField(command.Payload, "task_id"), name, mediaType, content, s.now().UTC())
	if err != nil {
		return domain.Attachment{}, err
	}
	result := map[string]any{"attachment_id": item.ID}
	if err := s.store.CompleteChannelCommand(ctx, claims.InstanceID, id, leaseID, true, result, "", s.now().UTC()); err != nil {
		return domain.Attachment{}, err
	}
	return item, nil
}

func (s *Service) MediaDelivery(ctx context.Context, claims RuntimeClaims, id, leaseID string) (domain.ChannelDelivery, error) {
	if !claims.Scopes["channel.media.read"] {
		return domain.ChannelDelivery{}, fmt.Errorf("capability_scope_denied")
	}
	item, err := s.store.ChannelDelivery(ctx, id)
	if err != nil || item.InstanceID != claims.InstanceID || item.SemanticPayload["kind"] != "attachment" {
		return domain.ChannelDelivery{}, fmt.Errorf("capability_scope_denied")
	}
	instance, err := s.store.ChannelInstance(ctx, claims.InstanceID)
	if err != nil || (instance.Status != "ready" && instance.Status != "degraded") {
		return domain.ChannelDelivery{}, fmt.Errorf("capability_scope_denied")
	}
	if item.State != "leased" || item.LeaseID != leaseID || !item.LeaseUntil.After(s.now().UTC()) {
		return domain.ChannelDelivery{}, store.ErrChannelRevision
	}
	return item, nil
}

func (s *Service) DeliveryAttachment(ctx context.Context, claims RuntimeClaims, id, leaseID string) (domain.Attachment, []byte, error) {
	delivery, err := s.MediaDelivery(ctx, claims, id, leaseID)
	if err != nil {
		return domain.Attachment{}, nil, err
	}
	item, err := s.store.Attachment(ctx, stringField(delivery.SemanticPayload, "task_id"), stringField(delivery.SemanticPayload, "attachment_id"))
	if err != nil || item.MessageID == "" {
		return domain.Attachment{}, nil, fmt.Errorf("capability_scope_denied")
	}
	if item.Size > MaxMediaFileBytes {
		return domain.Attachment{}, nil, fmt.Errorf("resource_size_invalid")
	}
	content, err := s.store.AttachmentContent(item)
	return item, content, err
}

func (s *Service) RecordMediaUpload(ctx context.Context, claims RuntimeClaims, id, leaseID, kind, key string) error {
	if _, err := s.MediaDelivery(ctx, claims, id, leaseID); err != nil {
		return err
	}
	if key != "" && (len(key) > 1024 || (kind != "image" && kind != "file")) {
		return fmt.Errorf("invalid_envelope")
	}
	return s.store.SaveChannelMediaUpload(ctx, claims.InstanceID, id, leaseID, kind, key, s.now().UTC())
}
