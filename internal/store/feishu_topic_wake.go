package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const migrateFeishuTopicWakeSQL = `
ALTER TABLE channel_feishu_groups ADD COLUMN topic_wake_mode TEXT NOT NULL DEFAULT 'full'
    CHECK(topic_wake_mode IN ('full','mention'));
ALTER TABLE channel_delivery_attempts ADD COLUMN feishu_thread_id TEXT NOT NULL DEFAULT '';

CREATE TABLE channel_feishu_topics (
    instance_id TEXT NOT NULL REFERENCES channel_instances(id) ON DELETE CASCADE,
    chat_id TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    participated_at TEXT NOT NULL,
    PRIMARY KEY(instance_id,chat_id,thread_id)
);
CREATE TABLE channel_feishu_topic_revocations (
    instance_id TEXT NOT NULL REFERENCES channel_instances(id) ON DELETE CASCADE,
    chat_id TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    receipt_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    revoked_at TEXT NOT NULL,
    restored_at TEXT NOT NULL DEFAULT '',
    restored_by TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(turn_id,receipt_id)
);
CREATE UNIQUE INDEX idx_feishu_topic_revocation_active
ON channel_feishu_topic_revocations(instance_id,chat_id,thread_id) WHERE restored_at='';

INSERT INTO schema_migrations(version,applied_at) VALUES(166,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
`

func (s *Store) migrateFeishuTopicWake(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var applied bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=166)`).Scan(&applied); err != nil || applied {
		return err
	}
	if _, err := tx.ExecContext(ctx, migrateFeishuTopicWakeSQL); err != nil {
		return err
	}
	return tx.Commit()
}

func recordFeishuTopicParticipation(ctx context.Context, tx *sql.Tx, instanceID, conversationID, deliveryID, leaseID string, providerThreadID *string, at time.Time) error {
	// Prefer Feishu's response. When it omits the thread, use the target given
	// to the plugin for this attempt, since a later inbound can change it.
	_, err := tx.ExecContext(ctx, `
		INSERT INTO channel_feishu_topics(instance_id,chat_id,thread_id,participated_at)
		SELECT i.id,c.external_chat_id,COALESCE(?,a.feishu_thread_id),?
		FROM channel_delivery_attempts a
		JOIN channel_conversations c ON c.id=?
		JOIN channel_endpoints e ON e.id=c.endpoint_id AND e.kind='group_digital_human'
		JOIN channel_instances i ON i.id=c.instance_id
		JOIN channel_plugins p ON p.id=i.plugin_id AND p.provider_key='feishu'
		WHERE i.id=? AND a.delivery_id=? AND a.lease_id=? AND COALESCE(?,a.feishu_thread_id)<>''
		ON CONFLICT(instance_id,chat_id,thread_id) DO NOTHING
	`, providerThreadID, timeString(at), conversationID, instanceID, deliveryID, leaseID, providerThreadID)
	return err
}

func feishuTopicWakes(ctx context.Context, tx *sql.Tx, instanceID, chatID, threadID string) (bool, error) {
	if threadID == "" {
		return false, nil
	}
	var wakes bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
		    SELECT 1 FROM channel_feishu_topics t
		    JOIN channel_feishu_groups g USING(instance_id,chat_id)
		    WHERE t.instance_id=? AND t.chat_id=? AND t.thread_id=? AND g.topic_wake_mode=?
		      AND NOT EXISTS (
		          SELECT 1 FROM channel_feishu_topic_revocations r
		          WHERE r.instance_id=t.instance_id AND r.chat_id=t.chat_id AND r.thread_id=t.thread_id AND r.restored_at=''
		      )
		)`, instanceID, chatID, threadID, domain.FeishuTopicWakeFull).Scan(&wakes)
	return wakes, err
}

func isFeishuTopicMention(receipt domain.ChannelInboxReceipt) bool {
	payload := receipt.NormalizedPayload
	return receipt.EventType == "message" && payload["chat_type"] == "group" &&
		payload["mentioned_bot"] == true && stringValue(payload["thread_id"]) != ""
}

func restoreFeishuTopicOnMention(ctx context.Context, tx *sql.Tx, receipt domain.ChannelInboxReceipt) error {
	if !isFeishuTopicMention(receipt) {
		return nil
	}
	payload := receipt.NormalizedPayload
	chatID, messageID := stringValue(payload["external_chat_id"]), stringValue(payload["external_message_id"])
	_, err := tx.ExecContext(ctx, `
		UPDATE channel_feishu_topic_revocations SET restored_at=?,restored_by=?
		WHERE instance_id=? AND chat_id=? AND thread_id=? AND restored_at=''
		  AND (?='' OR NOT EXISTS (
		      SELECT 1 FROM channel_inbox_dedup
		      WHERE instance_id=? AND event_type='message' AND id<>?
		        AND json_extract(normalized_payload_json,'$.external_chat_id')=?
		        AND json_extract(normalized_payload_json,'$.external_message_id')=?
		  ))
	`, timeString(receipt.ReceivedAt), stringValue(payload["external_sender_id"]), receipt.InstanceID,
		chatID, stringValue(payload["thread_id"]), messageID, receipt.InstanceID, receipt.ID, chatID, messageID)
	return err
}
