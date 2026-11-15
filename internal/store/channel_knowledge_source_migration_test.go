package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestChannelKnowledgeGrantMigrationCopiesRowsAndKeepsOtherForeignKeys rewinds
// a live database to the pre-v140 constraint shape, then verifies the upgrade:
// existing grants survive, the knowledge_entries reference is gone, the
// policy/owner references remain, and a second migrate is a no-op.
func TestChannelKnowledgeGrantMigrationCopiesRowsAndKeepsOtherForeignKeys(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	conn, err := database.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	now := timeString(time.Now().UTC())
	for _, statement := range []string{
		`DELETE FROM schema_migrations WHERE version=140`,
		`CREATE TABLE channel_knowledge_grants_v139 (
			id TEXT PRIMARY KEY,
			policy_id TEXT NOT NULL REFERENCES channel_knowledge_policies(id) ON DELETE CASCADE,
			knowledge_entry_id TEXT NOT NULL REFERENCES knowledge_entries(id) ON DELETE RESTRICT,
			grant_scope TEXT NOT NULL CHECK(grant_scope IN ('node','subtree')),
			granted_by_owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE RESTRICT,
			created_at TEXT NOT NULL,
			revoked_at TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO channel_knowledge_grants_v139(id,policy_id,knowledge_entry_id,grant_scope,granted_by_owner_id,created_at,revoked_at)
		 VALUES('grant-existing','policy-existing','knowledge-entry-existing','subtree','owner-existing','` + now + `','')`,
		`DROP TABLE channel_knowledge_grants`,
		`ALTER TABLE channel_knowledge_grants_v139 RENAME TO channel_knowledge_grants`,
		`CREATE UNIQUE INDEX idx_channel_knowledge_grants_active
		 ON channel_knowledge_grants(policy_id,knowledge_entry_id,grant_scope) WHERE revoked_at=''`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}

	if err := database.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_knowledge_grants`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration did not copy the existing grant: count=%d", count)
	}
	var id, policyID, entryID string
	if err := database.db.QueryRowContext(ctx,
		`SELECT id,policy_id,knowledge_entry_id FROM channel_knowledge_grants`).Scan(&id, &policyID, &entryID); err != nil {
		t.Fatal(err)
	}
	if id != "grant-existing" || policyID != "policy-existing" || entryID != "knowledge-entry-existing" {
		t.Fatalf("migration changed the grant row: %s/%s/%s", id, policyID, entryID)
	}

	references := map[string]int{}
	rows, err := database.db.QueryContext(ctx, `SELECT "table" FROM pragma_foreign_key_list('channel_knowledge_grants')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		references[table]++
	}
	rows.Close()
	if references["knowledge_entries"] != 0 {
		t.Fatalf("legacy knowledge_entries reference survived: %#v", references)
	}
	if references["channel_knowledge_policies"] == 0 || references["owners"] == 0 {
		t.Fatalf("policy/owner references were dropped: %#v", references)
	}
	var version140 int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=140`).Scan(&version140); err != nil || version140 != 1 {
		t.Fatalf("schema v140 record=%d err=%v", version140, err)
	}

	// A second startup must not rebuild or duplicate rows again.
	if err := database.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_knowledge_grants`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("second migration duplicated grants: count=%d", count)
	}
}
