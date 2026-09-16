package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) AppendChannelSourceAndProject(ctx context.Context, source domain.ChannelSourceEvent, coalesceKey string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if source.SourceRevision < 1 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(source_revision),0)+1 FROM channel_source_events WHERE source_key=?`, source.SourceKey).Scan(&source.SourceRevision); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO channel_source_events(id,source_key,source_revision,task_id,round_id,turn_id,conversation_item_id,event_class,event_type,semantic_payload_json,occurred_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		source.ID, source.SourceKey, source.SourceRevision, source.TaskID, source.RoundID, source.TurnID, source.ConversationItemID,
		source.EventClass, source.EventType, encodeJSON(source.SemanticPayload), timeString(source.OccurredAt))
	if err != nil {
		return err
	}
	inserted, _ := result.RowsAffected()
	if inserted == 0 {
		return tx.Commit()
	}
	if err := tx.QueryRowContext(ctx, `SELECT sequence FROM channel_source_events WHERE id=?`, source.ID).Scan(&source.Sequence); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT sub.id,sub.instance_id,sub.conversation_id,sub.kind,endpoint.kind
		FROM channel_subscriptions sub
		JOIN channel_instances instance ON instance.id=sub.instance_id
		JOIN channel_conversations conversation ON conversation.id=sub.conversation_id
		JOIN channel_endpoints endpoint ON endpoint.id=conversation.endpoint_id
		WHERE sub.state='active' AND instance.status IN ('ready','degraded')
		  AND ((sub.kind IN ('task_route','conversation_host') AND sub.source_task_id=?)
		       OR (sub.kind='owner_global' AND ? AND COALESCE(json_extract(instance.config_json,'$.notify_task_status'),0)=1))`, source.TaskID, source.EventClass == "status")
	if err != nil {
		return err
	}
	type target struct{ subscriptionID, instanceID, conversationID, kind, endpointKind string }
	targets := []target{}
	for rows.Next() {
		var item target
		if err := rows.Scan(&item.subscriptionID, &item.instanceID, &item.conversationID, &item.kind, &item.endpointKind); err != nil {
			rows.Close()
			return err
		}
		if !channelSubscriptionAllows(item.endpointKind, item.kind, source.EventClass, source.EventType) {
			continue
		}
		if item.endpointKind == domain.ChannelEndpointGroupDigitalHuman && source.EventType == "agent_reply" {
			matches, matchErr := channelGroupReplyMatchesRoundTx(ctx, tx, source.TaskID, source.RoundID, item.conversationID)
			if matchErr != nil {
				rows.Close()
				return matchErr
			}
			if !matches {
				continue
			}
		}
		targets = append(targets, item)
	}
	rows.Close()
	for _, target := range targets {
		if target.kind == "owner_global" {
			var managed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_conversations WHERE host_task_id=?)`, source.TaskID).Scan(&managed); err != nil {
				return err
			}
			if managed {
				continue
			}
			var routed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_task_routes WHERE instance_id=? AND target_task_id=? AND state='active')`, target.instanceID, source.TaskID).Scan(&routed); err != nil {
				return err
			}
			if routed {
				continue
			}
		}
		deliveryState := "pending"
		suppressionReason := ""
		isBotDialogue := false
		replyDecision := ""
		botTurnCount := 0
		botMaxTurns := 0
		if source.EventType == "agent_reply" {
			if target.endpointKind == domain.ChannelEndpointGroupDigitalHuman {
				state, stateErr := channelGroupReplyStateTx(ctx, tx, source.TaskID, source.RoundID, source.TurnID, target.conversationID)
				if stateErr != nil {
					return stateErr
				}
				if !state.matches {
					continue
				}
				isBotDialogue, replyDecision, botTurnCount, botMaxTurns = state.isBot, state.decision, state.turnCount, state.maxTurns
				if state.isBot && state.decision != "continue" {
					deliveryState = "suppressed"
					suppressionReason = "bot_dialogue_ended"
				} else if state.isBot && state.turnCount > state.maxTurns {
					deliveryState = "suppressed"
					suppressionReason = "bot_dialogue_max_turns"
				}
			}
			if err := insertChannelRouteCardTx(ctx, tx, source, target, deliveryState, suppressionReason, isBotDialogue, replyDecision, botTurnCount, botMaxTurns); err != nil {
				return err
			}
		}
		if deliveryState == "suppressed" {
			if _, err := tx.ExecContext(ctx, `UPDATE channel_subscriptions SET source_cursor=?,updated_at=? WHERE id=?`, source.Sequence, timeString(source.OccurredAt), target.subscriptionID); err != nil {
				return err
			}
			continue
		}
		for partIndex, part := range channelDeliveryParts(source.SemanticPayload) {
			partCoalesceKey := coalesceKey
			idempotencyKey := "channel-delivery:" + source.ID + ":" + target.subscriptionID
			if partIndex > 0 {
				partCoalesceKey = ""
				idempotencyKey = "channel-attachment:" + source.TaskID + ":" + fmt.Sprint(part["attachment_id"]) + ":" + target.subscriptionID
			}
			if partCoalesceKey != "" {
				var existingID string
				err := tx.QueryRowContext(ctx, `SELECT id FROM channel_delivery_outbox WHERE conversation_id=? AND coalesce_key=? AND state='pending' ORDER BY stream_sequence DESC LIMIT 1`, target.conversationID, partCoalesceKey).Scan(&existingID)
				if err == nil {
					if _, err := tx.ExecContext(ctx, `UPDATE channel_delivery_outbox SET source_event_sequence=?,semantic_payload_json=json_patch(semantic_payload_json,?),updated_at=? WHERE id=?`, source.Sequence, encodeJSON(part), timeString(source.OccurredAt), existingID); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, `UPDATE channel_subscriptions SET source_cursor=?,updated_at=? WHERE id=?`, source.Sequence, timeString(source.OccurredAt), target.subscriptionID); err != nil {
						return err
					}
					continue
				}
				if err != nil && err != sql.ErrNoRows {
					return err
				}
			}
			var streamSequence int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(stream_sequence),0)+1 FROM channel_delivery_outbox WHERE conversation_id=?`, target.conversationID).Scan(&streamSequence); err != nil {
				return err
			}
			deliveryID := domain.NewID("channel_delivery")
			if _, err := tx.ExecContext(ctx, `INSERT INTO channel_delivery_outbox(id,instance_id,conversation_id,subscription_id,source_event_sequence,stream_sequence,replay_generation,replay_of_id,idempotency_key,coalesce_key,payload_version,semantic_payload_json,state,attempts,first_attempt_at,available_at,lease_id,lease_until,provider_message_id,last_error_code,outcome_certainty,created_at,updated_at,delivered_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`,
				deliveryID, target.instanceID, target.conversationID, target.subscriptionID, source.Sequence, streamSequence, 0, nil,
				idempotencyKey, partCoalesceKey, 1, encodeJSON(part), "pending", 0, "", timeString(source.OccurredAt), "", "", "", "", "", timeString(source.OccurredAt), timeString(source.OccurredAt), ""); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE channel_subscriptions SET source_cursor=?,updated_at=? WHERE id=?`, source.Sequence, timeString(source.OccurredAt), target.subscriptionID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func channelDeliveryParts(payload map[string]any) []map[string]any {
	text := map[string]any{}
	for key, value := range payload {
		if key != "attachments" {
			text[key] = value
		}
	}
	parts := []map[string]any{text}
	raw, _ := json.Marshal(payload["attachments"])
	var attachments []domain.Attachment
	if json.Unmarshal(raw, &attachments) != nil {
		return parts
	}
	if strings.TrimSpace(fmt.Sprint(payload["kind"])) == "agent_outreach" {
		images := make([]map[string]any, 0, len(attachments))
		files := make([]map[string]any, 0, len(attachments))
		for _, item := range attachments {
			if item.ID == "" || item.TaskID != payload["task_id"] {
				continue
			}
			part := map[string]any{"kind": "attachment", "task_id": item.TaskID, "attachment_id": item.ID, "name": item.Name, "media_type": item.MediaType, "size": item.Size}
			switch item.MediaType {
			case "image/png", "image/jpeg", "image/gif", "image/webp":
				images = append(images, part)
			default:
				files = append(files, part)
			}
		}
		if len(images) > 0 {
			text["image_attachments"] = images
		}
		return append([]map[string]any{text}, files...)
	}
	for _, item := range attachments {
		if item.ID == "" || item.TaskID != payload["task_id"] {
			continue
		}
		parts = append(parts, map[string]any{"kind": "attachment", "task_id": item.TaskID, "attachment_id": item.ID, "name": item.Name, "media_type": item.MediaType, "size": item.Size})
	}
	return parts
}

func channelSubscriptionAllows(endpointKind, subscriptionKind, eventClass, eventType string) bool {
	if subscriptionKind == "owner_global" {
		return eventClass == "status"
	}
	if endpointKind == domain.ChannelEndpointGroupDigitalHuman {
		return eventType == "agent_reply"
	}
	return true
}

func (s *Store) ChannelBotDialogueTurnCount(ctx context.Context, taskID, conversationID string) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT payload_json
		FROM conversation_items
		WHERE task_id=? AND kind='user_message'
		  AND json_extract(payload_json,'$.channel_context.conversation_id')=?
		ORDER BY sequence DESC`,
		taskID, conversationID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var payloadJSON string
		if err := rows.Scan(&payloadJSON); err != nil {
			return 0, err
		}
		payload := decodeJSON(payloadJSON, map[string]any{})
		channelContext, _ := payload["channel_context"].(map[string]any)
		actor, _ := channelContext["actor"].(map[string]any)
		isBot, _ := actor["is_bot"].(bool)
		if !isBot {
			break
		}
		count++
	}
	return count, rows.Err()
}

type channelBotReplyState struct {
	matches   bool
	isBot     bool
	decision  string
	turnCount int
	maxTurns  int
}

func channelGroupReplyStateTx(ctx context.Context, tx *sql.Tx, taskID, roundID, turnID, conversationID string) (channelBotReplyState, error) {
	state := channelBotReplyState{maxTurns: domain.DefaultChannelBotDialogueMaxTurns}
	var payloadJSON string
	if err := tx.QueryRowContext(ctx, `
		SELECT item.payload_json
		FROM conversation_items item
		JOIN channel_inbox_dedup receipt ON receipt.id=json_extract(item.payload_json,'$.channel_receipt_id')
		WHERE item.task_id=? AND item.round_id=? AND item.kind='user_message'
		  AND json_extract(item.payload_json,'$.channel_context.endpoint')=?
		  AND json_extract(item.payload_json,'$.channel_context.conversation_id')=?
		  AND receipt.conversation_id=? AND receipt.state='processed'
		  AND COALESCE(json_extract(receipt.normalized_payload_json,'$.mentioned_bot'),0)=1
		ORDER BY item.sequence DESC LIMIT 1`,
		taskID, roundID, domain.ChannelEndpointGroupDigitalHuman, conversationID, conversationID,
	).Scan(&payloadJSON); err != nil {
		if err == sql.ErrNoRows {
			return state, nil
		}
		return state, err
	}
	state.matches = true
	payload := decodeJSON(payloadJSON, map[string]any{})
	channelContext, _ := payload["channel_context"].(map[string]any)
	actor, _ := channelContext["actor"].(map[string]any)
	state.isBot, _ = actor["is_bot"].(bool)
	if turnID != "" {
		_ = tx.QueryRowContext(ctx, `SELECT channel_reply_decision FROM turns WHERE id=? AND task_id=?`, turnID, taskID).Scan(&state.decision)
	}
	_ = tx.QueryRowContext(ctx, `
		SELECT COALESCE(json_extract(instance.config_json,'$.bot_dialogue_max_turns'),?)
		FROM channel_conversations conversation
		JOIN channel_instances instance ON instance.id=conversation.instance_id
		WHERE conversation.id=?`,
		domain.DefaultChannelBotDialogueMaxTurns, conversationID).Scan(&state.maxTurns)
	if state.maxTurns < 1 || state.maxTurns > 50 {
		state.maxTurns = domain.DefaultChannelBotDialogueMaxTurns
	}
	if state.isBot {
		rows, err := tx.QueryContext(ctx, `
			SELECT item.payload_json
			FROM conversation_items item
			WHERE item.task_id=? AND item.kind='user_message'
			  AND json_extract(item.payload_json,'$.channel_context.conversation_id')=?
			ORDER BY item.sequence DESC`,
			taskID, conversationID)
		if err != nil {
			return state, err
		}
		for rows.Next() {
			var itemPayload string
			if err := rows.Scan(&itemPayload); err != nil {
				rows.Close()
				return state, err
			}
			item := decodeJSON(itemPayload, map[string]any{})
			itemContext, _ := item["channel_context"].(map[string]any)
			itemActor, _ := itemContext["actor"].(map[string]any)
			itemIsBot, _ := itemActor["is_bot"].(bool)
			if !itemIsBot {
				break
			}
			state.turnCount++
		}
		if err := rows.Close(); err != nil {
			return state, err
		}
	}
	return state, nil
}

func insertChannelRouteCardTx(ctx context.Context, tx *sql.Tx, source domain.ChannelSourceEvent, target struct {
	subscriptionID, instanceID, conversationID, kind, endpointKind string
}, deliveryState, suppressionReason string, isBotDialogue bool, replyDecision string, botTurnCount, botMaxTurns int) error {
	var conversationName, instanceName string
	if err := tx.QueryRowContext(ctx, `
		SELECT conversation.display_name,instance.name
		FROM channel_conversations conversation
		JOIN channel_instances instance ON instance.id=conversation.instance_id
		WHERE conversation.id=? AND instance.id=?`,
		target.conversationID, target.instanceID).Scan(&conversationName, &instanceName); err != nil {
		return err
	}
	payload := map[string]any{
		"message":            strings.TrimSpace(fmt.Sprint(source.SemanticPayload["text"])),
		"delivery_state":     deliveryState,
		"suppression_reason": suppressionReason,
		"reply_decision":     replyDecision,
		"is_bot_dialogue":    isBotDialogue,
		"bot_turn":           botTurnCount,
		"bot_max_turns":      botMaxTurns,
		"channel": map[string]any{
			"conversation_id": target.conversationID,
			"display_name":    conversationName,
			"instance_name":   instanceName,
			"endpoint_kind":   target.endpointKind,
		},
	}
	item := domain.ConversationItem{
		ID:     stableStoreID("conversation_channel_route", source.ID, target.subscriptionID),
		TaskID: source.TaskID, RoundID: source.RoundID, TurnID: source.TurnID,
		AgentID: "aha", StreamAgentID: "main", FromAgentID: "aha", ToAgentID: "main",
		RouteKind: "channel_route", Category: "update", Kind: "agent_channel_route",
		Summary: "AHA 外部渠道路由", Payload: payload, CreatedAt: source.OccurredAt,
	}
	_, err := insertConversationItem(ctx, tx, item)
	return err
}

func channelGroupReplyMatchesRoundTx(ctx context.Context, tx *sql.Tx, taskID, roundID, conversationID string) (bool, error) {
	if taskID == "" || roundID == "" || conversationID == "" {
		return false, nil
	}
	var matches bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM conversation_items item
			JOIN channel_inbox_dedup receipt
			  ON receipt.id=json_extract(item.payload_json,'$.channel_receipt_id')
			WHERE item.task_id=? AND item.round_id=? AND item.kind='user_message'
			  AND json_extract(item.payload_json,'$.channel_context.endpoint')=?
			  AND json_extract(item.payload_json,'$.channel_context.conversation_id')=?
			  AND json_extract(item.payload_json,'$.channel_context.inbound_receipt_id')=receipt.id
			  AND receipt.conversation_id=? AND receipt.state='processed'
			  AND COALESCE(json_extract(receipt.normalized_payload_json,'$.mentioned_bot'),0)=1
		)`,
		taskID, roundID, domain.ChannelEndpointGroupDigitalHuman, conversationID, conversationID,
	).Scan(&matches)
	return matches, err
}

func (s *Store) EnsureOwnerGlobalSubscription(ctx context.Context, instanceID, conversationID string, at time.Time) error {
	var cursor int64
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM channel_source_events`).Scan(&cursor)
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO channel_subscriptions(id,instance_id,conversation_id,route_id,source_task_id,kind,filter_version,filter_json,source_cursor,state,created_at,updated_at) VALUES(?,?,?,NULL,NULL,'owner_global',1,'{}',?,'active',?,?)`,
		domain.NewID("channel_subscription"), instanceID, conversationID, cursor, timeString(at), timeString(at))
	return err
}

func (s *Store) EnsureConversationHostSubscription(ctx context.Context, instanceID, conversationID, taskID string, at time.Time) error {
	var cursor int64
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM channel_source_events`).Scan(&cursor)
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO channel_subscriptions(id,instance_id,conversation_id,route_id,source_task_id,kind,filter_version,filter_json,source_cursor,state,created_at,updated_at) VALUES(?,?,?,NULL,?,'conversation_host',1,'{}',?,'active',?,?)`,
		domain.NewID("channel_subscription"), instanceID, conversationID, taskID, cursor, timeString(at), timeString(at))
	return err
}

func ChannelCoalesceKey(taskID, roundID, eventType string) string {
	if roundID == "" {
		return ""
	}
	switch eventType {
	case "agent_reply", "waiting_user", "terminal":
		return fmt.Sprintf("task/%s/round/%s/final", taskID, roundID)
	case "agent_message_update":
		return fmt.Sprintf("task/%s/round/%s/stream", taskID, roundID)
	default:
		return ""
	}
}
