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
    owner_identity_link_id,
    -- Legacy rows may point at a Task deleted before foreign keys were
    -- enforced. Detach them instead of copying a reference that the rebuilt
    -- table's own foreign key check would reject. The old column was NOT NULL,
    -- so '' was its "no host" value and is kept as-is; NULL stays NULL.
    CASE WHEN host_task_id='' OR EXISTS(SELECT 1 FROM tasks WHERE id=host_task_id)
         THEN host_task_id ELSE NULL END,
    status,created_at,updated_at,display_name,members_synced_at
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
	// The rebuild only transforms host_task_id (dangling -> NULL). Check that
	// exact invariant instead of running a table-wide foreign_key_check: a
	// production database already carries legacy violations in other tables
	// and in other columns of this table, and the legacy '' sentinel would
	// itself show up as one. None of that is introduced by this migration, and
	// none of it may block startup.
	var danglingHosts int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM channel_conversations conversation
		WHERE conversation.host_task_id IS NOT NULL
		  AND conversation.host_task_id <> ''
		  AND NOT EXISTS(SELECT 1 FROM tasks WHERE id=conversation.host_task_id)`).Scan(&danglingHosts); err != nil {
		return err
	}
	if danglingHosts != 0 {
		return fmt.Errorf("channel conversation rebuild left %d dangling host references", danglingHosts)
	}
	return tx.Commit()
}
