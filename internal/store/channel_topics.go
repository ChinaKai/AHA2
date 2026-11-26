package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	channelListeningRetention = 7 * 24 * time.Hour
	channelListeningPerGroup  = 200
	channelListeningBytes     = 4 * 1024
)

const schemaChannelTopics = `
CREATE TABLE IF NOT EXISTS channel_group_receipts (
    receipt_id TEXT PRIMARY KEY REFERENCES channel_inbox_dedup(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL REFERENCES channel_conversations(id) ON DELETE CASCADE,
    route_id TEXT NOT NULL REFERENCES channel_task_routes(id) ON DELETE CASCADE,
    target_task_id TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    batch_id TEXT NOT NULL,
    ready_ms INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_channel_task_routes_conversation ON channel_task_routes(conversation_id);
CREATE INDEX IF NOT EXISTS idx_channel_group_receipts_open
ON channel_group_receipts(route_id,target_task_id,thread_id,ready_ms);
CREATE INDEX IF NOT EXISTS idx_channel_group_receipts_batch ON channel_group_receipts(batch_id);
CREATE INDEX IF NOT EXISTS idx_channel_inbox_provider_message
ON channel_inbox_dedup(conversation_id,json_extract(normalized_payload_json,'$.external_message_id'))
WHERE event_type='message' AND conversation_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS channel_group_listening (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES channel_conversations(id) ON DELETE CASCADE,
    route_id TEXT NOT NULL REFERENCES channel_task_routes(id) ON DELETE CASCADE,
    target_task_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    sender_name TEXT NOT NULL,
    sender_type TEXT NOT NULL,
    content TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    received_ms INTEGER NOT NULL,
    UNIQUE(conversation_id,message_id)
);
CREATE INDEX IF NOT EXISTS idx_channel_group_listening_group
ON channel_group_listening(conversation_id,received_ms DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_channel_group_listening_expiry ON channel_group_listening(received_ms);

CREATE TABLE IF NOT EXISTS channel_topic_turns (
    turn_id TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL REFERENCES channel_conversations(id) ON DELETE CASCADE,
    thread_id TEXT NOT NULL,
    queued_ms INTEGER NOT NULL DEFAULT 0,
    bot_only INTEGER NOT NULL DEFAULT 0,
    decision TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(turn_id,conversation_id,thread_id)
);
CREATE INDEX IF NOT EXISTS idx_channel_topic_turns_recent
ON channel_topic_turns(conversation_id,thread_id,queued_ms DESC);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES(164, strftime('%Y-%m-%dT%H:%M:%fZ','now'));
`

var ErrChannelGroupUnbound = errors.New("group_task_route_required")
var ErrChannelGroupNotAllowed = errors.New("group_not_allowlisted")

// The route is rechecked under the same write lock as the content insert.
func checkGroupRouteTx(ctx context.Context, tx *sql.Tx, route domain.ChannelTaskRoute, chatID string, mentioned bool) error {
	var configJSON string
	var err error
	if route.ID == "" {
		err = tx.QueryRowContext(ctx, `
			SELECT i.config_json FROM channel_conversations c
			JOIN channel_endpoints e ON e.id=c.endpoint_id AND e.enabled=1
			JOIN channel_instances i ON i.id=c.instance_id
			WHERE c.id=? AND c.instance_id=? AND c.external_chat_id=? AND c.status='active'
			  AND e.kind='group_digital_human' AND i.retired_at='' AND i.status IN ('ready','degraded')
			  AND (?='' OR c.host_task_id=?)
			  AND NOT EXISTS (
			      SELECT 1 FROM channel_task_routes r
			      JOIN channel_conversations bound ON bound.id=r.conversation_id
			      WHERE r.instance_id=i.id AND r.state='active' AND bound.endpoint_id=c.endpoint_id
			        AND bound.external_chat_id=c.external_chat_id AND bound.status='active'
			  )`, route.ConversationID, route.InstanceID, chatID, route.TargetTaskID, route.TargetTaskID).Scan(&configJSON)
	} else {
		err = tx.QueryRowContext(ctx, `
		SELECT i.config_json FROM channel_task_routes r
		JOIN channel_conversations c ON c.id=r.conversation_id AND c.status='active'
		JOIN channel_endpoints e ON e.id=c.endpoint_id AND e.enabled=1
		JOIN channel_instances i ON i.id=r.instance_id
		WHERE r.id=? AND r.state='active' AND r.target_task_id=?
		  AND r.conversation_id=? AND r.instance_id=?
		  AND c.external_chat_id=? AND i.retired_at='' AND i.status IN ('ready','degraded')`,
			route.ID, route.TargetTaskID, route.ConversationID, route.InstanceID, chatID).Scan(&configJSON)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrChannelGroupUnbound
	}
	if err != nil {
		return err
	}
	allowed, err := feishuGroupMessageAllowed(ctx, tx, route.InstanceID, decodeJSON(configJSON, map[string]any{}), chatID, mentioned)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrChannelGroupNotAllowed
	}
	return nil
}

func (s *Store) ReceiveChannelGroupMessage(ctx context.Context, receipt domain.ChannelInboxReceipt, route domain.ChannelTaskRoute, envelope domain.ChannelInboundEnvelope) (domain.ChannelInboxReceipt, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelInboxReceipt{}, false, err
	}
	defer tx.Rollback()
	if err := checkGroupRouteTx(ctx, tx, route, envelope.ExternalChatID, envelope.MentionedBot); err != nil {
		return domain.ChannelInboxReceipt{}, false, err
	}
	receipt.ConversationID = route.ConversationID
	if envelope.ExternalMessageID != "" {
		// Feishu can redeliver the same message using another event ID.
		existing, err := scanChannelInbox(tx.QueryRowContext(ctx, `
			SELECT `+channelInboxColumns+` FROM channel_inbox_dedup
			WHERE conversation_id=? AND event_type='message'
			  AND json_extract(normalized_payload_json,'$.external_message_id')=?
			ORDER BY received_at LIMIT 1`, route.ConversationID, envelope.ExternalMessageID))
		if err == nil {
			return existing, true, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return domain.ChannelInboxReceipt{}, false, err
		}
		var listeningID string
		err = tx.QueryRowContext(ctx, `SELECT id FROM channel_group_listening WHERE conversation_id=? AND message_id=?`,
			route.ConversationID, envelope.ExternalMessageID).Scan(&listeningID)
		if err == nil {
			receipt.ID, receipt.State, receipt.Outcome, receipt.NormalizedPayload = listeningID, "listening", "listening_only", nil
			return receipt, true, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return domain.ChannelInboxReceipt{}, false, err
		}
	}
	if !envelope.MentionedBot && envelope.ThreadID == "" {
		return storeListeningMessage(ctx, tx, receipt, route, envelope)
	}
	stored, duplicate, err := receiveChannelInbox(ctx, tx, receipt)
	if err != nil || duplicate {
		return stored, duplicate, err
	}
	// Keep the receipt metadata compatible with pending messages from older cores.
	// Each new receipt is independently eligible for scheduling.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO channel_group_receipts(receipt_id,conversation_id,route_id,target_task_id,thread_id,batch_id,ready_ms)
		VALUES(?,?,?,?,?,?,?)`, receipt.ID, route.ConversationID, nullableString(route.ID), route.TargetTaskID, envelope.ThreadID, receipt.ID, receipt.ReceivedAt.UnixMilli()); err != nil {
		return domain.ChannelInboxReceipt{}, false, err
	}
	return stored, false, tx.Commit()
}

func storeListeningMessage(ctx context.Context, tx *sql.Tx, receipt domain.ChannelInboxReceipt, route domain.ChannelTaskRoute, envelope domain.ChannelInboundEnvelope) (domain.ChannelInboxReceipt, bool, error) {
	receipt.State, receipt.Outcome, receipt.NormalizedPayload = "listening", "listening_only", nil
	messageID := envelope.ExternalMessageID
	if messageID == "" {
		messageID = envelope.ExternalEventID
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO channel_group_listening(id,conversation_id,route_id,target_task_id,message_id,thread_id,sender_name,sender_type,content,occurred_at,received_ms)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(conversation_id,message_id) DO NOTHING`,
		receipt.ID, route.ConversationID, nullableString(route.ID), route.TargetTaskID, messageID, envelope.ThreadID,
		envelope.SenderDisplayName, envelope.SenderType, limitChannelText(envelope.Content, channelListeningBytes),
		timeString(receipt.OccurredAt), receipt.ReceivedAt.UnixMilli())
	if err != nil {
		return domain.ChannelInboxReceipt{}, false, err
	}
	inserted, _ := result.RowsAffected()
	if inserted == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM channel_group_listening WHERE conversation_id=? AND message_id=?`,
			route.ConversationID, messageID).Scan(&receipt.ID); err != nil {
			return domain.ChannelInboxReceipt{}, false, err
		}
		return receipt, true, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM channel_group_listening WHERE id IN (
		    SELECT id FROM channel_group_listening WHERE conversation_id=?
		    ORDER BY received_ms DESC,id DESC LIMIT -1 OFFSET ?
		)`, route.ConversationID, channelListeningPerGroup); err != nil {
		return domain.ChannelInboxReceipt{}, false, err
	}
	return receipt, false, tx.Commit()
}

func (s *Store) PruneChannelListening(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM channel_group_listening WHERE received_ms<=?`, now.Add(-channelListeningRetention).UnixMilli())
	return err
}

func limitChannelText(text string, max int) string {
	if len(text) > max {
		text = text[:max]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text
}

func (s *Store) ChannelListeningContext(ctx context.Context, taskID, conversationID, threadID, rootID string, now time.Time) (string, error) {
	if taskID == "" || conversationID == "" {
		return "", nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.sender_name,l.sender_type,l.occurred_at,l.content
		FROM channel_group_listening l
		JOIN channel_conversations c ON c.id=l.conversation_id AND c.status='active'
		JOIN channel_instances i ON i.id=c.instance_id
		JOIN channel_feishu_groups g ON g.instance_id=i.id AND g.chat_id=c.external_chat_id AND g.receive_all_messages=1
		LEFT JOIN channel_task_routes r ON r.id=l.route_id AND r.state='active' AND r.target_task_id=l.target_task_id
		WHERE l.conversation_id=? AND l.received_ms>?
		  AND ((r.id IS NOT NULL AND l.target_task_id=?)
		       OR (l.route_id IS NULL AND c.host_task_id=? AND l.target_task_id IN ('',c.host_task_id)
		           AND NOT EXISTS(SELECT 1 FROM channel_task_routes active WHERE active.conversation_id=c.id AND active.state='active')))
		  AND 'group_message_read' IN (SELECT value FROM json_each(i.config_json,'$.channel_permission_ids'))
		  AND (l.thread_id=? OR (?<>'' AND l.message_id=?))
		ORDER BY l.received_ms DESC,l.id DESC LIMIT ?`,
		conversationID, now.Add(-channelListeningRetention).UnixMilli(), taskID, taskID, threadID, rootID, rootID, channelListeningPerGroup)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	remaining := channelListeningBytes
	for rows.Next() && remaining > 0 {
		var name, kind, occurredAt, content string
		if err := rows.Scan(&name, &kind, &occurredAt, &content); err != nil {
			return "", err
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		prefix := "[" + occurredAt + " | " + name + " | " + kind
		if threadID != "" {
			prefix += " | " + threadID
		}
		prefix += "] "
		if remaining <= len(prefix) {
			break
		}
		content = strings.NewReplacer("\r", " ", "\n", " ").Replace(content)
		line := limitChannelText(prefix+content, remaining)
		lines = append(lines, line)
		remaining -= len(line) + 1
	}
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	return strings.Join(lines, "\n"), rows.Err()
}

type ChannelGroupReceipt struct {
	BatchID, RouteID, TargetTaskID, ConversationID string
}

func (s *Store) ChannelGroupReceipt(ctx context.Context, receiptID string) (ChannelGroupReceipt, error) {
	var group ChannelGroupReceipt
	err := s.db.QueryRowContext(ctx, `SELECT batch_id,COALESCE(route_id,''),target_task_id,conversation_id FROM channel_group_receipts WHERE receipt_id=?`, receiptID).
		Scan(&group.BatchID, &group.RouteID, &group.TargetTaskID, &group.ConversationID)
	return group, err
}

func (s *Store) PendingChannelGroupTasks(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT task_id FROM agent_inbox
		WHERE status='pending' AND source_kind='channel_group_message'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var taskIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			return nil, err
		}
		taskIDs = append(taskIDs, taskID)
	}
	return taskIDs, rows.Err()
}

var ErrChannelGroupSelf = errors.New("group_self_message")

type channelTopicTurn struct {
	conversationID, threadID string
	botOnly                  bool
}

func channelTopicForInbox(items []domain.AgentInboxItem) channelTopicTurn {
	channelContext, _ := items[0].Payload["channel_context"].(map[string]any)
	topic := channelTopicTurn{
		conversationID: stringValue(channelContext["conversation_id"]),
		threadID:       stringValue(channelContext["thread_id"]),
		botOnly:        true,
	}
	for _, item := range items {
		context, _ := item.Payload["channel_context"].(map[string]any)
		actor, _ := context["actor"].(map[string]any)
		topic.botOnly = topic.botOnly && boolValue(actor["is_bot"])
	}
	return topic
}

func channelTopicBotTurns(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, conversationID, threadID string) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM channel_topic_turns
		WHERE conversation_id=? AND thread_id=? AND bot_only=1 AND queued_ms>0
		  AND rowid>COALESCE((
		    SELECT MAX(rowid) FROM channel_topic_turns
		    WHERE conversation_id=? AND thread_id=? AND bot_only=0 AND queued_ms>0
		  ),0)`, conversationID, threadID, conversationID, threadID).Scan(&count)
	return count, err
}

func (s *Store) ChannelTopicBotTurns(ctx context.Context, conversationID, threadID string) (int, error) {
	return channelTopicBotTurns(ctx, s.db, conversationID, threadID)
}

func checkGroupInboxRoute(ctx context.Context, tx *sql.Tx, receiptID, taskID string) error {
	var route domain.ChannelTaskRoute
	var chatID string
	var mentioned bool
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(g.route_id,''),g.target_task_id,g.conversation_id,r.instance_id,
		       json_extract(r.normalized_payload_json,'$.external_chat_id'),
		       COALESCE(json_extract(r.normalized_payload_json,'$.mentioned_bot'),0)
		FROM channel_group_receipts g
		JOIN channel_inbox_dedup r ON r.id=g.receipt_id
		WHERE g.receipt_id=?`, receiptID).Scan(&route.ID, &route.TargetTaskID, &route.ConversationID, &route.InstanceID, &chatID, &mentioned)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && route.TargetTaskID != taskID) {
		return ErrChannelGroupUnbound
	}
	if err != nil {
		return err
	}
	return checkGroupRouteTx(ctx, tx, route, chatID, mentioned)
}

// Both live inbox paths match the pending receipt to the Turn's original
// binding and topic. After end, new messages stay pending for the next Turn.
const liveChannelGroupMatchesTurn = `(
	source_kind='channel_group_message' AND EXISTS (
		SELECT 1
		FROM turns active
		JOIN agent_inbox origin ON origin.batch_id=active.inbox_batch_id
		    AND origin.task_id=active.task_id AND origin.target_agent_id=active.agent_id
		    AND origin.source_kind='channel_group_message'
		JOIN channel_group_receipts initial ON initial.receipt_id=json_extract(origin.payload_json,'$.channel_receipt_id')
		JOIN channel_group_receipts incoming ON incoming.receipt_id=json_extract(agent_inbox.payload_json,'$.channel_receipt_id')
		    AND incoming.route_id IS initial.route_id AND incoming.thread_id=initial.thread_id
		    AND incoming.conversation_id=initial.conversation_id AND incoming.target_task_id=initial.target_task_id
		LEFT JOIN channel_topic_turns topic ON topic.turn_id=active.id
		    AND topic.conversation_id=initial.conversation_id AND topic.thread_id=initial.thread_id
		WHERE active.id=? AND active.inbox_batch_id<>'' AND active.status IN ('starting','running')
		  AND active.task_id=agent_inbox.task_id AND active.round_id=agent_inbox.round_id
		  AND active.agent_id=agent_inbox.target_agent_id
		  AND COALESCE(topic.decision,'')<>'end'
	)
)`

func markChannelTopicLiveHuman(ctx context.Context, tx *sql.Tx, turnID string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE channel_topic_turns SET bot_only=0
		WHERE turn_id=? AND bot_only=1 AND EXISTS (
			SELECT 1 FROM agent_inbox
			WHERE delivered_turn_id=channel_topic_turns.turn_id
			  AND status='delivered_in_turn' AND source_kind='channel_group_message'
			  AND COALESCE(json_extract(payload_json,'$.channel_context.actor.is_bot'),0)=0
		)`, turnID)
	return err
}

func suppressChannelInboxItem(ctx context.Context, tx *sql.Tx, item domain.AgentInboxItem, outcome string, now time.Time) error {
	at := timeString(now)
	if _, err := tx.ExecContext(ctx, `UPDATE agent_inbox SET status='cancelled',processed_at=? WHERE id=? AND status='pending'`, at, item.ID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE channel_inbox_dedup SET state='rejected',outcome=?,processed_at=? WHERE id=?`,
		outcome, at, stringValue(item.Payload["channel_receipt_id"]))
	return err
}

func prepareChannelTopicTurn(ctx context.Context, tx *sql.Tx, items []domain.AgentInboxItem, turn domain.Turn) ([]domain.AgentInboxItem, channelTopicTurn, error) {
	if items[0].SourceKind != "channel_group_message" {
		return items, channelTopicTurn{}, nil
	}
	eligible, err := recheckChannelGroupInbox(ctx, tx, turn.TaskID, items, turn.QueuedAt)
	if err != nil || len(eligible) == 0 {
		return nil, channelTopicTurn{}, err
	}
	return eligible, channelTopicForInbox(eligible), nil
}

func recheckChannelGroupInbox(ctx context.Context, tx *sql.Tx, taskID string, items []domain.AgentInboxItem, now time.Time) ([]domain.AgentInboxItem, error) {
	eligible := items[:0]
	for _, item := range items {
		if item.SourceKind == "channel_group_message" {
			err := checkGroupInboxRoute(ctx, tx, stringValue(item.Payload["channel_receipt_id"]), taskID)
			if errors.Is(err, ErrChannelGroupUnbound) || errors.Is(err, ErrChannelGroupNotAllowed) {
				if err := suppressChannelInboxItem(ctx, tx, item, err.Error(), now); err != nil {
					return nil, err
				}
				continue
			}
			if err != nil {
				return nil, err
			}
		}
		eligible = append(eligible, item)
	}
	return eligible, nil
}

func (s *Store) SetChannelTopicReplyDecision(ctx context.Context, turnID, conversationID, threadID, decision, reason string) error {
	return setChannelTopicReplyDecision(ctx, s.db, turnID, conversationID, threadID, decision, reason)
}

func setChannelTopicReplyDecision(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, turnID, conversationID, threadID, decision, reason string) error {
	result, err := db.ExecContext(ctx, `
		INSERT INTO channel_topic_turns(turn_id,conversation_id,thread_id,decision,reason)
		VALUES(?,?,?,?,?)
		ON CONFLICT(turn_id,conversation_id,thread_id) DO UPDATE SET decision=excluded.decision,reason=excluded.reason
		WHERE channel_topic_turns.decision<>'end' OR excluded.decision='end'`,
		turnID, conversationID, threadID, decision, reason)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrChannelBotDialogueEnded
	}
	return nil
}

func channelTopicDecisionTx(ctx context.Context, tx *sql.Tx, turnID, conversationID, threadID string) (string, string, error) {
	var decision, reason string
	err := tx.QueryRowContext(ctx, `
		SELECT decision,reason FROM channel_topic_turns WHERE turn_id=? AND conversation_id=? AND thread_id=?`,
		turnID, conversationID, threadID).Scan(&decision, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return decision, reason, err
}

func (s *Store) ChannelTopicReplyDecision(ctx context.Context, turnID, conversationID, threadID string) (string, error) {
	var decision string
	err := s.db.QueryRowContext(ctx, `
		SELECT decision FROM channel_topic_turns WHERE turn_id=? AND conversation_id=? AND thread_id=?`,
		turnID, conversationID, threadID).Scan(&decision)
	return decision, err
}

func copyChannelTopicForRetry(ctx context.Context, tx *sql.Tx, turn domain.Turn) error {
	if turn.Attempt <= 1 || turn.ParentTurnID == "" {
		return nil
	}
	// Retrying the same input preserves an explicit choice to stay silent.
	// New messages start with an independent decision.
	_, err := tx.ExecContext(ctx, `
		INSERT INTO channel_topic_turns(turn_id,conversation_id,thread_id,queued_ms,bot_only,decision,reason)
		SELECT ?,topic.conversation_id,topic.thread_id,?,topic.bot_only,
		       CASE WHEN topic.decision='end' THEN 'end' ELSE '' END,
		       CASE WHEN topic.decision='end' THEN topic.reason ELSE '' END
		FROM channel_topic_turns topic JOIN turns previous ON previous.id=topic.turn_id
		WHERE topic.turn_id=? AND topic.queued_ms>0 AND previous.task_id=? AND previous.inbox_batch_id=?`,
		turn.ID, turn.QueuedAt.UnixMilli(), turn.ParentTurnID, turn.TaskID, turn.InboxBatchID)
	return err
}

func insertChannelTopicTurn(ctx context.Context, tx *sql.Tx, turn domain.Turn, topic channelTopicTurn) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO channel_topic_turns(turn_id,conversation_id,thread_id,queued_ms,bot_only)
		VALUES(?,?,?,?,?)`, turn.ID, topic.conversationID, topic.threadID, turn.QueuedAt.UnixMilli(), topic.botOnly)
	return err
}
