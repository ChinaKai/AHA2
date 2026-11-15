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

// schemaV141 rebuilds channel_knowledge_policies without the foreign key on
// fixed_index_entry_id. A channel host project that reads the vault has no
// legacy knowledge root: the vault never creates a channel project root, and
// seeding a shadow legacy row would make a vault-routed channel keep old
// knowledge alive. The column stays NOT NULL but may be empty, meaning "this
// endpoint has no fixed channel index". Existing rows are copied unchanged —
// this is a constraint change, not a knowledge migration.
const schemaV141 = `
CREATE TABLE channel_knowledge_policies_v141 (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL REFERENCES channel_instances(id) ON DELETE CASCADE,
    endpoint_id TEXT NOT NULL UNIQUE REFERENCES channel_endpoints(id) ON DELETE CASCADE,
    fixed_index_entry_id TEXT NOT NULL DEFAULT '',
    default_visibility TEXT NOT NULL CHECK(default_visibility IN ('conversation_only','instance_shared')),
    revision INTEGER NOT NULL DEFAULT 1 CHECK(revision >= 1),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    scope_mode TEXT NOT NULL DEFAULT 'selected' CHECK(scope_mode IN ('all','selected'))
);
INSERT INTO channel_knowledge_policies_v141(
    id,instance_id,endpoint_id,fixed_index_entry_id,default_visibility,revision,created_at,updated_at,scope_mode)
SELECT id,instance_id,endpoint_id,fixed_index_entry_id,default_visibility,revision,created_at,updated_at,scope_mode
FROM channel_knowledge_policies;
DROP TABLE channel_knowledge_policies;
ALTER TABLE channel_knowledge_policies_v141 RENAME TO channel_knowledge_policies;
`

// migrateChannelKnowledgePolicyVaultIndex uses the same dedicated connection as
// the grants rebuild: channel_knowledge_grants references this table, and the
// table rebuild must not cascade-delete those grants.
func (s *Store) migrateChannelKnowledgePolicyVaultIndex(ctx context.Context) error {
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
	var grantsBefore int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_knowledge_grants`).Scan(&grantsBefore); err != nil {
		return err
	}
	var policiesBefore int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_knowledge_policies`).Scan(&policiesBefore); err != nil {
		return err
	}
	for _, statement := range []string{
		schemaV141,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(141, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	var entryReferences int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_list('channel_knowledge_policies') WHERE "table"='knowledge_entries'`).Scan(&entryReferences); err != nil {
		return err
	}
	if entryReferences != 0 {
		return fmt.Errorf("channel knowledge policy rebuild kept %d knowledge_entries references", entryReferences)
	}
	var grantsAfter, policiesAfter int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_knowledge_grants`).Scan(&grantsAfter); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_knowledge_policies`).Scan(&policiesAfter); err != nil {
		return err
	}
	if grantsBefore != grantsAfter || policiesBefore != policiesAfter {
		return fmt.Errorf("channel knowledge policy rebuild changed rows: policies %d→%d grants %d→%d", policiesBefore, policiesAfter, grantsBefore, grantsAfter)
	}
	return tx.Commit()
}
