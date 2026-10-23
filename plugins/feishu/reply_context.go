package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/channel/normalize"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	replyContextTimeout      = 1500 * time.Millisecond
	maxReplyContextRunes     = 16000
	maxForwardedReplyEntries = 20
)

func inboundReplyContext(ctx context.Context, client *lark.Client, message *channeltypes.NormalizedMessage) map[string]any {
	if client == nil || message == nil {
		return nil
	}
	event, ok := message.RawEvent.(*larkim.P2MessageReceiveV1)
	if !ok || event.Event == nil || event.Event.Message == nil || event.Event.Message.ParentId == nil {
		return nil
	}
	parentID := strings.TrimSpace(*event.Event.Message.ParentId)
	if parentID == "" {
		return nil
	}
	result := map[string]any{
		"parent_message_id": parentID,
		"status":            "unavailable",
	}
	if event.Event.Message.RootId != nil {
		result["root_message_id"] = strings.TrimSpace(*event.Event.Message.RootId)
	}
	if event.Event.Message.ThreadId != nil {
		result["thread_id"] = strings.TrimSpace(*event.Event.Message.ThreadId)
	}

	lookupCtx, cancel := context.WithTimeout(ctx, replyContextTimeout)
	defer cancel()
	request := larkim.NewGetMessageReqBuilder().MessageId(parentID).WithSenderName(true).Build()
	response, err := client.Im.V1.Message.Get(lookupCtx, request)
	if err != nil {
		result["error_code"] = "reply_parent_lookup_failed"
		return result
	}
	if !response.Success() {
		result["error_code"] = fmt.Sprintf("reply_parent_provider_%d", response.Code)
		return result
	}
	if response.Data == nil || len(response.Data.Items) == 0 {
		result["error_code"] = "reply_parent_not_returned"
		return result
	}
	parent := response.Data.Items[0]
	for _, item := range response.Data.Items {
		if item != nil && item.MessageId != nil && strings.TrimSpace(*item.MessageId) == parentID {
			parent = item
			break
		}
	}
	if parent == nil {
		result["error_code"] = "reply_parent_not_returned"
		return result
	}
	if parent.Deleted != nil && *parent.Deleted {
		result["error_code"] = "reply_parent_deleted"
		return result
	}
	messageType := optionalString(parent.MsgType)
	rawContent := ""
	if parent.Body != nil && parent.Body.Content != nil {
		rawContent = *parent.Body.Content
	}
	normalizedContent, resources := normalizedReplyContent(messageType, rawContent)
	if messageType == "merge_forward" && len(response.Data.Items) > 1 {
		normalizedContent = forwardedReplyContent(response.Data.Items, parentID)
	}
	normalizedContent = strings.TrimSpace(normalizedContent)
	if normalizedContent == "" {
		if messageType == "" {
			messageType = "unknown"
		}
		normalizedContent = "[" + messageType + " message]"
	}
	senderName, senderIsBot := replySender(parent.Sender)
	result["status"] = "available"
	delete(result, "error_code")
	result["message_type"] = messageType
	result["sender"] = map[string]any{"display_name": senderName, "is_bot": senderIsBot}
	result["content"] = truncateReplyRunes(normalizedContent, maxReplyContextRunes)
	result["resources"] = resources
	if parent.RootId != nil && strings.TrimSpace(*parent.RootId) != "" {
		result["root_message_id"] = strings.TrimSpace(*parent.RootId)
	}
	if parent.ThreadId != nil && strings.TrimSpace(*parent.ThreadId) != "" {
		result["thread_id"] = strings.TrimSpace(*parent.ThreadId)
	}
	return result
}

func normalizedReplyContent(messageType, raw string) (string, []map[string]any) {
	messageType = strings.TrimSpace(messageType)
	if messageType == "post" {
		var body map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &body) == nil && (len(body["content"]) > 0 || len(body["content_v2"]) > 0) {
			if wrapped, err := json.Marshal(map[string]json.RawMessage{"zh_cn": json.RawMessage(raw)}); err == nil {
				raw = string(wrapped)
			}
		}
	}
	content, sourceResources := normalize.ParseContent(messageType, raw)
	result := make([]map[string]any, 0, len(sourceResources))
	for _, resource := range sourceResources {
		placeholder := replyResourcePlaceholder(resource.Type)
		if resource.FileKey != "" {
			tagPattern := regexp.MustCompile(`<[^>]*\bkey\s*=\s*"` + regexp.QuoteMeta(resource.FileKey) + `"[^>]*/>`)
			content = tagPattern.ReplaceAllString(content, placeholder)
			imagePattern := regexp.MustCompile(`!\[[^\]\r\n]*\]\(` + regexp.QuoteMeta(resource.FileKey) + `\)`)
			content = imagePattern.ReplaceAllString(content, placeholder)
			content = strings.ReplaceAll(content, resource.FileKey, placeholder)
		}
		if len(result) >= 8 {
			continue
		}
		item := map[string]any{"type": resource.Type}
		if strings.TrimSpace(resource.FileName) != "" {
			item["file_name"] = strings.TrimSpace(resource.FileName)
		}
		result = append(result, item)
	}
	return strings.TrimSpace(content), result
}

func forwardedReplyContent(items []*larkim.Message, parentID string) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if len(parts) >= maxForwardedReplyEntries || item == nil || item.MessageId == nil || strings.TrimSpace(*item.MessageId) == parentID {
			continue
		}
		messageType := optionalString(item.MsgType)
		raw := ""
		if item.Body != nil && item.Body.Content != nil {
			raw = *item.Body.Content
		}
		content, _ := normalizedReplyContent(messageType, raw)
		sender, _ := replySender(item.Sender)
		content = strings.TrimSpace(content)
		if content == "" {
			if messageType == "" {
				messageType = "unknown"
			}
			content = "[" + messageType + " message]"
		}
		parts = append(parts, fmt.Sprintf("%s: %s", sender, content))
	}
	return strings.Join(parts, "\n")
}

func replyResourcePlaceholder(resourceType string) string {
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case "image":
		return "[图片附件]"
	case "file":
		return "[文件附件]"
	case "audio":
		return "[语音]"
	case "video", "media":
		return "[视频]"
	case "sticker":
		return "[表情]"
	default:
		return "[附件]"
	}
}

func replySender(sender *larkim.Sender) (string, bool) {
	isBot := false
	name := ""
	if sender != nil {
		if sender.SenderType != nil {
			kind := strings.ToLower(strings.TrimSpace(*sender.SenderType))
			isBot = kind == "app" || kind == "bot"
		}
		if sender.SenderName != nil {
			name = strings.TrimSpace(*sender.SenderName)
		}
	}
	if name == "" {
		if isBot {
			name = "其他机器人"
		} else {
			name = "引用消息发送者"
		}
	}
	return name, isBot
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func truncateReplyRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "\n[引用内容已截断]"
}
