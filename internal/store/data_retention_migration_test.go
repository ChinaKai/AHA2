package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func migrationVersionCount(t *testing.T, database *Store, version int) int {
	t.Helper()
	var count int
	if err := database.db.QueryRow(
		`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func retentionIndexExists(t *testing.T, database *Store) bool {
	t.Helper()
	var exists bool
	if err := database.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='index' AND name='idx_conversation_items_cleanup')`).
		Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func assertConversationRetentionSchema(t *testing.T, database *Store) {
	t.Helper()
	for _, column := range []string{
		"conversation_retention_days", "conversation_purge_tool", "conversation_purge_update",
	} {
		if !database.tableHasColumn(context.Background(), "data_retention_settings", column) {
			t.Fatalf("missing column %s", column)
		}
	}
	if !retentionIndexExists(t, database) {
		t.Fatal("missing idx_conversation_items_cleanup")
	}
	if count := migrationVersionCount(t, database, 145); count != 1 {
		t.Fatalf("schema version 145 rows=%d, want 1", count)
	}
}

// A brand-new database runs main's v142/v144 knowledge migrations and then the
// conversation-retention migration as v145.
func TestConversationRetentionMigrationFreshDatabase(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	assertConversationRetentionSchema(t, database)
	if count := migrationVersionCount(t, database, 142); count != 1 {
		t.Fatalf("schema version 142 rows=%d, want 1", count)
	}
	if count := migrationVersionCount(t, database, 144); count != 1 {
		t.Fatalf("schema version 144 rows=%d, want 1", count)
	}
	settings, err := database.ConversationRetentionSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.RetentionDays != 30 || !settings.PurgeTool || settings.PurgeUpdate {
		t.Fatalf("default settings=%#v", settings)
	}
}

// A v144 database without the new columns upgrades to v145. Dropping the
// columns and version row is the closest fixture we can build in-package; the
// v142/v144 rows stay in place.
func TestConversationRetentionMigrationFromV144(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP INDEX IF EXISTS idx_conversation_items_cleanup`,
		`ALTER TABLE data_retention_settings DROP COLUMN conversation_purge_update`,
		`ALTER TABLE data_retention_settings DROP COLUMN conversation_purge_tool`,
		`ALTER TABLE data_retention_settings DROP COLUMN conversation_retention_days`,
		`DELETE FROM schema_migrations WHERE version=145`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			raw.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	var v144 int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=144`).Scan(&v144); err != nil || v144 != 1 {
		raw.Close()
		t.Fatalf("fixture v144=%d err=%v", v144, err)
	}
	raw.Close()

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	assertConversationRetentionSchema(t, upgraded)
	if count := migrationVersionCount(t, upgraded, 144); count != 1 {
		t.Fatalf("v144 row lost during upgrade: %d", count)
	}
	settings, err := upgraded.ConversationRetentionSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.RetentionDays != 30 || !settings.PurgeTool || settings.PurgeUpdate {
		t.Fatalf("upgraded defaults=%#v", settings)
	}

	// Reopening an already-migrated database is a no-op: the version row is
	// still unique and the migration can repair a missing column even when the
	// version row survived.
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`ALTER TABLE data_retention_settings DROP COLUMN conversation_purge_tool`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertConversationRetentionSchema(t, reopened)
	if count := migrationVersionCount(t, reopened, 145); count != 1 {
		t.Fatalf("idempotent reopen version rows=%d", count)
	}
}
