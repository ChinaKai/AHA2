package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type channelInboundStatus struct {
	receiptID string
	content   string
	payload   map[string]any
}

// ReconcileChannelMessageStatuses creates or updates one Web-visible status card
// for every channel receipt delivered to the current main Turn.
func (s *Store) ReconcileChannelMessageStatuses(
	ctx context.Context,
	taskID, turnID, receiptFilter, terminalState, reason, reasonText string,
	at time.Time,
) ([]domain.ConversationItem, error) {
	if taskID == "" || turnID == "" {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var roundID, inboxBatchID, replyDecision string
	if err := tx.QueryRowContext(ctx, `
		SELECT round_id,inbox_batch_id,channel_reply_decision
		FROM turns WHERE id=? AND task_id=?`,
		turnID, taskID,
	).Scan(&roundID, &inboxBatchID, &replyDecision); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT content,payload_json
		FROM agent_inbox
		WHERE task_id=? AND target_agent_id='main'
		  AND (delivered_turn_id=? OR batch_id=?)
		  AND json_extract(payload_json,'$.channel_receipt_id') IS NOT NULL
		ORDER BY sequence`,
		taskID, turnID, inboxBatchID,
	)
	if err != nil {
		return nil, err
	}
	ordered := []channelInboundStatus{}
	seen := map[string]bool{}
	for rows.Next() {
		var content, payloadJSON string
		if err := rows.Scan(&content, &payloadJSON); err != nil {
			rows.Close()
			return nil, err
		}
		payload := decodeJSON(payloadJSON, map[string]any{})
		receiptID := strings.TrimSpace(fmt.Sprint(payload["channel_receipt_id"]))
		if receiptID == "" || receiptID == "<nil>" || seen[receiptID] ||
			(receiptFilter != "" && receiptID != receiptFilter) {
			continue
		}
		seen[receiptID] = true
		ordered = append(ordered, channelInboundStatus{receiptID: receiptID, content: content, payload: payload})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	items := make([]domain.ConversationItem, 0, len(ordered))
	for _, inbound := range ordered {
		channelContext, _ := inbound.payload["channel_context"].(map[string]any)
		route, _ := channelContext["route"].(map[string]any)
		if strings.TrimSpace(fmt.Sprint(route["mode"])) != "task_route" {
			continue
		}
		replyPayload, replyItemID, replyFound, err := channelReplyPayloadTx(ctx, tx, taskID, inbound.receiptID)
		if err != nil {
			return nil, err
		}
		messageStatus, statusReason := channelStatusOutcome(
			terminalState, reason, replyDecision, replyFound,
		)
		if messageStatus == "" {
			continue
		}
		statusReasonText := ""
		if messageStatus == "not_replied" && statusReason == "reply_decision_end" {
			statusReasonText = strings.TrimSpace(reasonText)
			if statusReasonText == "" {
				statusReasonText, err = channelStatusReasonTextTx(ctx, tx, taskID, inbound.receiptID)
				if err != nil {
					return nil, err
				}
			}
		}
		actor, _ := channelContext["actor"].(map[string]any)
		channelName := strings.TrimSpace(fmt.Sprint(channelContext["chat_display_name"]))
		if channelName == "" || channelName == "<nil>" {
			channelName = strings.TrimSpace(fmt.Sprint(actor["display_name"]))
		}
		inboundMessage := compactChannelReply(inbound.content)
		replyMessage := strings.TrimSpace(stringValue(replyPayload["message"]))
		message := inboundMessage
		if messageStatus == "replied" && replyMessage != "" {
			message = replyMessage
		}
		itemID := stableStoreID("conversation_channel_status", taskID, inbound.receiptID)
		if replyFound && replyItemID != "" {
			itemID = replyItemID
		}
		replyChannel, _ := replyPayload["channel"].(map[string]any)
		channelPayload := map[string]any{
			"display_name":  channelName,
			"endpoint_kind": stringValue(channelContext["endpoint"]),
		}
		for _, key := range []string{"conversation_id", "instance_name", "endpoint_kind"} {
			if value := stringValue(replyChannel[key]); value != "" {
				channelPayload[key] = value
			}
		}
		recipientPayload := map[string]any{
			"identity_link_id": stringValue(actor["identity_link_id"]),
			"display_name":     stringValue(actor["display_name"]),
			"is_bot":           boolValue(actor["is_bot"]),
		}
		if replyRecipient, ok := replyPayload["recipient"].(map[string]any); ok {
			for key, value := range replyRecipient {
				recipientPayload[key] = value
			}
		}
		item := normalizeConversationItem(domain.ConversationItem{
			ID:     itemID,
			TaskID: taskID, RoundID: roundID, TurnID: turnID,
			AgentID: "aha", StreamAgentID: "main", FromAgentID: "aha", ToAgentID: "main",
			RouteKind: "channel_route", Category: "update", Kind: "agent_channel_route",
			Summary: channelStatusSummary(messageStatus, statusReason),
			Payload: map[string]any{
				"status_card":        true,
				"message_status":     messageStatus,
				"status_reason":      statusReason,
				"status_reason_text": statusReasonText,
				"reply_decision":     replyDecision,
				"is_bot_dialogue":    boolValue(actor["is_bot"]),
				"message":            message,
				"inbound_message":    inboundMessage,
				"reply_message":      replyMessage,
				"inbound_receipt_id": inbound.receiptID,
				"delivery_state":     stringValue(replyPayload["delivery_state"]),
				"delivery_id":        stringValue(replyPayload["delivery_id"]),
				"channel":            channelPayload,
				"recipient":          recipientPayload,
				"actor": map[string]any{
					"display_name": stringValue(actor["display_name"]),
					"is_bot":       boolValue(actor["is_bot"]),
				},
			},
			CreatedAt: at,
		})
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO conversation_items(
				id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,
				route_kind,category,kind,summary,payload_json,created_at
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
				turn_id=excluded.turn_id,route_kind=excluded.route_kind,
				category=excluded.category,kind=excluded.kind,summary=excluded.summary,
				payload_json=excluded.payload_json,created_at=excluded.created_at`,
			item.ID, item.TaskID, item.RoundID, item.TurnID, item.AgentID, item.StreamAgentID,
			item.FromAgentID, item.ToAgentID, item.RouteKind, item.Category, item.Kind,
			item.Summary, encodeJSON(item.Payload), timeString(item.CreatedAt),
		); err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT sequence FROM conversation_items WHERE id=?`, item.ID).Scan(&item.Sequence); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) ChannelReceiptDeliveredToTurn(
	ctx context.Context,
	taskID, inboxBatchID, turnID, receiptID string,
) (bool, error) {
	if taskID == "" || receiptID == "" || (inboxBatchID == "" && turnID == "") {
		return false, nil
	}
	var delivered bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM agent_inbox
			WHERE task_id=? AND target_agent_id='main'
			  AND (delivered_turn_id=? OR (?<>'' AND batch_id=?))
			  AND json_extract(payload_json,'$.channel_receipt_id')=?
		)`,
		taskID, turnID, inboxBatchID, inboxBatchID, receiptID,
	).Scan(&delivered)
	return delivered, err
}

func (s *Store) ChannelContextForReceipt(
	ctx context.Context,
	taskID, turnID, receiptID string,
) (map[string]any, error) {
	if taskID == "" || turnID == "" || receiptID == "" {
		return nil, sql.ErrNoRows
	}
	var payloadJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT payload_json FROM agent_inbox
		WHERE task_id=? AND target_agent_id='main'
		  AND (delivered_turn_id=? OR batch_id=(
			SELECT inbox_batch_id FROM turns WHERE id=? AND task_id=?
		  ))
		  AND json_extract(payload_json,'$.channel_receipt_id')=?
		ORDER BY sequence DESC LIMIT 1`,
		taskID, turnID, turnID, taskID, receiptID,
	).Scan(&payloadJSON)
	if err != nil {
		return nil, err
	}
	payload := decodeJSON(payloadJSON, map[string]any{})
	channelContext, ok := payload["channel_context"].(map[string]any)
	if !ok || len(channelContext) == 0 {
		return nil, sql.ErrNoRows
	}
	return channelContext, nil
}

func (s *Store) HasActiveTaskGroupRoute(ctx context.Context, taskID string) (bool, error) {
	if strings.TrimSpace(taskID) == "" {
		return false, nil
	}
	var active bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM channel_task_routes route
			JOIN channel_conversations conversation ON conversation.id=route.conversation_id
			JOIN channel_endpoints endpoint ON endpoint.id=conversation.endpoint_id
			WHERE route.target_task_id=? AND route.state='active'
			  AND conversation.status='active'
			  AND endpoint.kind=?
		)`,
		taskID, domain.ChannelEndpointGroupDigitalHuman,
	).Scan(&active)
	return active, err
}

func channelStatusReasonTextTx(ctx context.Context, tx *sql.Tx, taskID, receiptID string) (string, error) {
	var reasonText string
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(json_extract(payload_json,'$.status_reason_text'),'')
		FROM conversation_items
		WHERE task_id=?
		  AND json_extract(payload_json,'$.status_card')=1
		  AND json_extract(payload_json,'$.inbound_receipt_id')=?
		ORDER BY sequence DESC LIMIT 1`,
		taskID, receiptID,
	).Scan(&reasonText)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return strings.TrimSpace(reasonText), err
}

func channelReplyPayloadTx(ctx context.Context, tx *sql.Tx, taskID, receiptID string) (map[string]any, string, bool, error) {
	var itemID, payloadJSON string
	err := tx.QueryRowContext(ctx, `
		SELECT id,payload_json FROM conversation_items
		WHERE task_id=?
		  AND (kind='agent_channel_reply' OR json_extract(payload_json,'$.message_status')='replied')
		  AND json_extract(payload_json,'$.inbound_receipt_id')=?
		ORDER BY sequence DESC LIMIT 1`,
		taskID, receiptID,
	).Scan(&itemID, &payloadJSON)
	if err == sql.ErrNoRows {
		return map[string]any{}, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	return decodeJSON(payloadJSON, map[string]any{}), itemID, true, nil
}

func channelStatusOutcome(terminalState, overrideReason, replyDecision string, replied bool) (string, string) {
	if replied {
		return "replied", ""
	}
	if replyDecision == "end" {
		return "not_replied", "reply_decision_end"
	}
	switch terminalState {
	case "decided_continue":
		return "pending_reply", "reply_decision_continue"
	case "reply_rejected":
		return "not_replied", overrideReason
	case "succeeded":
		return "not_replied", "no_channel_reply"
	case "failed":
		return "failed", "turn_failed"
	case "interrupted":
		return "interrupted", "turn_interrupted"
	default:
		return "", ""
	}
}

func channelStatusSummary(status, reason string) string {
	switch status {
	case "replied":
		return "AHA 渠道回复已生成"
	case "pending_reply":
		return "AHA 渠道回复待生成"
	case "failed":
		return "AHA 渠道消息处理失败"
	case "interrupted":
		return "AHA 渠道消息处理已中断"
	default:
		if reason == "reply_decision_end" {
			return "AHA 已处理并选择不回复"
		}
		if reason == "no_channel_reply" {
			return "AHA 未提交渠道回复或无需回复决策"
		}
		return "AHA 已处理但未生成渠道回复"
	}
}

func stringValue(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}
