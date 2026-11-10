package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrationV129UpgradesV128Database simulates a database created before
// the local ingest columns existed. Reopening it must add the columns and the
// idempotency index without losing existing rows.
func TestMigrationV129UpgradesV128Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP INDEX IF EXISTS idx_messages_client_request_id`,
		`ALTER TABLE messages DROP COLUMN source`,
		`ALTER TABLE messages DROP COLUMN client_request_id`,
		`DELETE FROM schema_migrations WHERE version=129`,
	} {
		if _, err := database.db.ExecContext(ctx, statement); err != nil {
			database.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var migrated bool
	if err := reopened.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=129)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v129 migrated=%t err=%v", migrated, err)
	}
	columns := map[string]bool{}
	rows, err := reopened.db.QueryContext(ctx, `PRAGMA table_info(messages)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		columns[name] = true
	}
	rows.Close()
	if !columns["source"] || !columns["client_request_id"] {
		t.Fatalf("messages columns=%v", columns)
	}
	var indexSQL string
	if err := reopened.db.QueryRowContext(ctx, `
		SELECT sql FROM sqlite_master
		WHERE type='index' AND name='idx_messages_client_request_id'`).Scan(&indexSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexSQL, "client_request_id") {
		t.Fatalf("index sql=%q", indexSQL)
	}
}
