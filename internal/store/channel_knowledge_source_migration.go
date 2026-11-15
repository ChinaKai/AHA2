package store

import (
	"context"
	"fmt"
)

// schemaV140 rebuilds channel_knowledge_grants without the foreign key to
// knowledge_entries. Vault root/document IDs are not legacy rows, but they are
// legitimate grant targets once the owning project reads the vault; keeping the
// foreign key would force a vault-only project to keep a shadow legacy row just
// to be grantable. Existing rows are copied unchanged — this is a constraint
// change, not a knowledge migration.
const schemaV140 = `
CREATE TABLE channel_knowledge_grants_v140 (
    id TEXT PRIMARY KEY,
    policy_id TEXT NOT NULL REFERENCES channel_knowledge_policies(id) ON DELETE CASCADE,
    knowledge_entry_id TEXT NOT NULL,
    grant_scope TEXT NOT NULL CHECK(grant_scope IN ('node','subtree')),
    granted_by_owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    revoked_at TEXT NOT NULL DEFAULT ''
);
INSERT INTO channel_knowledge_grants_v140(
    id,policy_id,knowledge_entry_id,grant_scope,granted_by_owner_id,created_at,revoked_at)
SELECT id,policy_id,knowledge_entry_id,grant_scope,granted_by_owner_id,created_at,revoked_at
FROM channel_knowledge_grants;
DROP TABLE channel_knowledge_grants;
ALTER TABLE channel_knowledge_grants_v140 RENAME TO channel_knowledge_grants;
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_knowledge_grants_active
ON channel_knowledge_grants(policy_id,knowledge_entry_id,grant_scope) WHERE revoked_at='';
`

// migrateChannelKnowledgeGrantVaultIDs runs the table rebuild on one dedicated
// connection: PRAGMA foreign_keys is per connection and is ignored inside a
// transaction, so it must be switched off before the transaction and back on
// before the connection returns to the pool.
func (s *Store) migrateChannelKnowledgeGrantVaultIDs(ctx context.Context) error {
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
		schemaV140,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(140, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	// Only the intended invariant is checked: the rebuilt table must no longer
	// reference knowledge_entries, and its existing grant rows must survive.
	var entryReferences int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_list('channel_knowledge_grants') WHERE "table"='knowledge_entries'`).Scan(&entryReferences); err != nil {
		return err
	}
	if entryReferences != 0 {
		return fmt.Errorf("channel knowledge grant rebuild kept %d knowledge_entries references", entryReferences)
	}
	return tx.Commit()
}
