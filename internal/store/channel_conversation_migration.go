package store

import (
	"context"
	"fmt"
)

// schemaV132 rebuilds channel_conversations so host_task_id can be NULL.
//
// Deleting a Task must not delete the channel conversations it hosts: the
// channel instance still owns their inbox and handoff history, and
// channel_inbox_dedup/channel_handoffs reference them with ON DELETE RESTRICT.
// A detached host is therefore the only lossless way to stop the RESTRICT
// foreign key from blocking the Task delete. SQLite cannot drop NOT NULL with
// ALTER TABLE, so rebuild the table with the documented foreign_keys=OFF
// procedure instead. The column order matches the original table plus the
// columns added by later migrations.
const schemaV132 = `
CREATE TABLE channel_conversations_v132 (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL REFERENCES channel_instances(id) ON DELETE CASCADE,
    endpoint_id TEXT NOT NULL REFERENCES channel_endpoints(id) ON DELETE RESTRICT,
    scope_key_version INTEGER NOT NULL DEFAULT 1 CHECK(scope_key_version >= 1),
    scope_key TEXT NOT NULL,
    external_chat_id TEXT NOT NULL DEFAULT '',
    external_sender_id TEXT NOT NULL DEFAULT '',
    owner_identity_link_id TEXT REFERENCES channel_identity_links(id) ON DELETE RESTRICT,
    host_task_id TEXT UNIQUE REFERENCES tasks(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK(status IN ('active','closed')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    members_synced_at TEXT NOT NULL DEFAULT '',
    UNIQUE(endpoint_id,scope_key_version,scope_key)
);
INSERT INTO channel_conversations_v132(
    id,instance_id,endpoint_id,scope_key_version,scope_key,external_chat_id,external_sender_id,
    owner_identity_link_id,host_task_id,status,created_at,updated_at,display_name,members_synced_at)
SELECT id,instance_id,endpoint_id,scope_key_version,scope_key,external_chat_id,external_sender_id,
    owner_identity_link_id,host_task_id,status,created_at,updated_at,display_name,members_synced_at
FROM channel_conversations;
DROP TABLE channel_conversations;
ALTER TABLE channel_conversations_v132 RENAME TO channel_conversations;
CREATE INDEX IF NOT EXISTS idx_channel_conversations_instance ON channel_conversations(instance_id,status);
`

// migrateChannelConversationNullableHost runs the table rebuild on one
// dedicated connection: PRAGMA foreign_keys is per connection and is ignored
// inside a transaction, so it must be switched off before the transaction and
// switched back on before the connection returns to the pool.
func (s *Store) migrateChannelConversationNullableHost(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		schemaV132,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(132, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	// The rebuilt table must reference rows that exist. Check only the table
	// itself so a legacy violation elsewhere in the database cannot block
	// startup.
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check(channel_conversations)`)
	if err != nil {
		return err
	}
	violations := 0
	for rows.Next() {
		violations++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if violations != 0 {
		return fmt.Errorf("channel conversation rebuild left %d foreign key violations", violations)
	}
	return tx.Commit()
}
