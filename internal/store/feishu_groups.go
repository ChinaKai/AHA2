package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const migrateFeishuGroupsSQL = `
CREATE TABLE IF NOT EXISTS channel_feishu_groups (
    instance_id TEXT NOT NULL REFERENCES channel_instances(id) ON DELETE CASCADE,
    chat_id TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    provider_listed INTEGER NOT NULL DEFAULT 0,
    receive_all_messages INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(instance_id, chat_id)
);
INSERT OR IGNORE INTO channel_feishu_groups(instance_id,chat_id,receive_all_messages,updated_at)
SELECT DISTINCT i.id,j.value,1,strftime('%Y-%m-%dT%H:%M:%fZ','now')
FROM channel_instances i
JOIN channel_plugins p ON p.id=i.plugin_id AND p.provider_key='feishu'
JOIN json_each(i.config_json,'$.feishu_group_message_allowlist') j
WHERE json_type(i.config_json,'$.feishu_group_message_allowlist')='array'
  AND j.type='text' AND j.value<>'';
UPDATE channel_instances SET config_json=json_remove(config_json,'$.feishu_group_message_allowlist')
WHERE json_type(config_json,'$.feishu_group_message_allowlist') IS NOT NULL
  AND plugin_id IN (SELECT id FROM channel_plugins WHERE provider_key='feishu');

CREATE TABLE channel_group_listening_v165 (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES channel_conversations(id) ON DELETE CASCADE,
    route_id TEXT REFERENCES channel_task_routes(id) ON DELETE CASCADE,
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
INSERT INTO channel_group_listening_v165 SELECT * FROM channel_group_listening;
DROP TABLE channel_group_listening;
ALTER TABLE channel_group_listening_v165 RENAME TO channel_group_listening;
CREATE INDEX idx_channel_group_listening_group
ON channel_group_listening(conversation_id,received_ms DESC,id DESC);
CREATE INDEX idx_channel_group_listening_expiry ON channel_group_listening(received_ms);

CREATE TABLE channel_group_receipts_v165 (
    receipt_id TEXT PRIMARY KEY REFERENCES channel_inbox_dedup(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL REFERENCES channel_conversations(id) ON DELETE CASCADE,
    route_id TEXT REFERENCES channel_task_routes(id) ON DELETE CASCADE,
    target_task_id TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    batch_id TEXT NOT NULL,
    ready_ms INTEGER NOT NULL
);
INSERT INTO channel_group_receipts_v165 SELECT * FROM channel_group_receipts;
DROP TABLE channel_group_receipts;
ALTER TABLE channel_group_receipts_v165 RENAME TO channel_group_receipts;
CREATE INDEX idx_channel_group_receipts_open
ON channel_group_receipts(route_id,target_task_id,thread_id,ready_ms);
CREATE INDEX idx_channel_group_receipts_batch ON channel_group_receipts(batch_id);

INSERT INTO schema_migrations(version,applied_at) VALUES(165,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
`

func (s *Store) migrateFeishuGroups(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var applied bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=165)`).Scan(&applied); err != nil || applied {
		return err
	}
	if _, err := tx.ExecContext(ctx, migrateFeishuGroupsSQL); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FeishuGroupMessageAllowed(ctx context.Context, instance domain.ChannelInstance, chatID string, mentioned bool) (bool, error) {
	return feishuGroupMessageAllowed(ctx, s.db, instance.ID, instance.Config, chatID, mentioned)
}

func feishuGroupMessageAllowed(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, instanceID string, config map[string]any, chatID string, mentioned bool) (bool, error) {
	if mentioned || !domain.FeishuFullGroupMessagesEnabled(config) {
		return mentioned, nil
	}
	var enabled bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_feishu_groups
		WHERE instance_id=? AND chat_id=? AND receive_all_messages=1)`, instanceID, chatID).Scan(&enabled)
	return enabled, err
}

func (s *Store) FeishuGroupMessageAllowlist(ctx context.Context, instance domain.ChannelInstance) ([]string, error) {
	if instance.ProviderKey != "feishu" || !domain.FeishuFullGroupMessagesEnabled(instance.Config) {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT chat_id FROM channel_feishu_groups
		WHERE instance_id=? AND receive_all_messages=1 ORDER BY chat_id`, instance.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func scanFeishuGroup(scanner interface{ Scan(...any) error }) (domain.FeishuGroup, error) {
	var group domain.FeishuGroup
	err := scanner.Scan(&group.ChatID, &group.DisplayName, &group.ReceiveAllMessages, &group.TopicWakeMode, &group.UpdatedAt, &group.UpdatedBy)
	return group, err
}

const feishuGroupColumns = `chat_id,display_name,receive_all_messages,topic_wake_mode,updated_at,updated_by`

const FeishuGroupRefreshCommand = "sync_feishu_groups"

func (s *Store) CompleteFeishuGroupRefresh(ctx context.Context, instanceID, id, leaseID string, groups []domain.FeishuGroup, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := timeString(now)
	result, err := tx.ExecContext(ctx, `
		UPDATE channel_plugin_commands SET state='completed',lease_id='',lease_until='',completed_at=?
		WHERE instance_id=? AND id=? AND kind=? AND state='leased' AND lease_id=? AND lease_until>?
		  AND EXISTS(SELECT 1 FROM channel_instances WHERE id=? AND retired_at='' AND status IN ('ready','degraded'))`,
		at, instanceID, id, FeishuGroupRefreshCommand, leaseID, at, instanceID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrChannelRevision
	}
	var newer bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_plugin_commands
		WHERE instance_id=? AND kind=? AND state='completed'
		  AND rowid>(SELECT rowid FROM channel_plugin_commands WHERE id=?))`,
		instanceID, FeishuGroupRefreshCommand, id).Scan(&newer); err != nil {
		return err
	}
	if !newer {
		if _, err := tx.ExecContext(ctx, `UPDATE channel_feishu_groups SET provider_listed=0 WHERE instance_id=? AND provider_listed=1`, instanceID); err != nil {
			return err
		}
		for _, group := range groups {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO channel_feishu_groups(instance_id,chat_id,display_name,provider_listed)
				VALUES(?,?,?,1) ON CONFLICT(instance_id,chat_id) DO UPDATE
				SET display_name=excluded.display_name,provider_listed=1`, instanceID, group.ChatID, group.DisplayName); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) feishuConversationGroups(ctx context.Context, instanceID string) (map[string]domain.FeishuGroup, bool, error) {
	var refreshed bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_plugin_commands
		WHERE instance_id=? AND kind=? AND state='completed')`, instanceID, FeishuGroupRefreshCommand).Scan(&refreshed); err != nil {
		return nil, false, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+feishuGroupColumns+` FROM channel_feishu_groups
		WHERE instance_id=? AND (provider_listed=1 OR NOT ?)`, instanceID, refreshed)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	groups := make(map[string]domain.FeishuGroup)
	for rows.Next() {
		group, err := scanFeishuGroup(rows)
		if err != nil {
			return nil, false, err
		}
		groups[group.ChatID] = group
	}
	return groups, refreshed, rows.Err()
}

func (s *Store) SetFeishuGroupReception(ctx context.Context, instanceID, ownerID, chatID string, enabled bool, at time.Time) (domain.FeishuGroup, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.FeishuGroup{}, false, err
	}
	defer tx.Rollback()
	// Groups may already have a setting (including migrated IDs), or be known
	// through an active group conversation before the first provider refresh.
	result, err := tx.ExecContext(ctx, `
		INSERT INTO channel_feishu_groups(instance_id,chat_id,receive_all_messages,updated_at,updated_by)
		SELECT i.id,?,?,?,? FROM channel_instances i
		JOIN channel_plugins p ON p.id=i.plugin_id AND p.provider_key='feishu'
		WHERE i.id=? AND i.owner_id=? AND i.retired_at=''
		  AND (EXISTS(SELECT 1 FROM channel_feishu_groups g WHERE g.instance_id=i.id AND g.chat_id=?)
		       OR EXISTS(SELECT 1 FROM channel_conversations c JOIN channel_endpoints e ON e.id=c.endpoint_id
		                 WHERE c.instance_id=i.id AND c.external_chat_id=? AND c.status='active' AND e.kind='group_digital_human'))
		ON CONFLICT(instance_id,chat_id) DO UPDATE SET receive_all_messages=excluded.receive_all_messages,
		    updated_at=excluded.updated_at,updated_by=excluded.updated_by
		WHERE channel_feishu_groups.receive_all_messages<>excluded.receive_all_messages`,
		chatID, enabled, timeString(at), ownerID, instanceID, ownerID, chatID, chatID)
	if err != nil {
		return domain.FeishuGroup{}, false, err
	}
	group, err := scanFeishuGroup(tx.QueryRowContext(ctx, `
		SELECT `+feishuGroupColumns+` FROM channel_feishu_groups
		WHERE instance_id=? AND chat_id=? AND EXISTS (
		    SELECT 1 FROM channel_instances WHERE id=? AND owner_id=? AND retired_at=''
		)`, instanceID, chatID, instanceID, ownerID))
	if err != nil {
		return domain.FeishuGroup{}, false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return domain.FeishuGroup{}, false, err
	}
	return group, count > 0, tx.Commit()
}

func (s *Store) SetFeishuGroupTopicWakeMode(ctx context.Context, instanceID, ownerID, chatID, mode string) (domain.FeishuGroup, error) {
	return scanFeishuGroup(s.db.QueryRowContext(ctx, `
		UPDATE channel_feishu_groups
		SET topic_wake_mode=?
		WHERE instance_id=? AND chat_id=? AND receive_all_messages=1
		  AND EXISTS (
		      SELECT 1 FROM channel_instances i JOIN channel_plugins p ON p.id=i.plugin_id AND p.provider_key='feishu'
		      WHERE i.id=? AND i.owner_id=? AND i.retired_at=''
		  )
		RETURNING `+feishuGroupColumns, mode, instanceID, chatID, instanceID, ownerID))
}
