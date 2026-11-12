package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Production hit v132 with legacy channel_conversations rows whose
// host_task_id pointed at a Task deleted before SQLite enforced the foreign
// key. The rebuild must detach those rows instead of aborting startup, and a
// violation that already exists in another table must not block it either.
func TestChannelConversationHostMigrationNullsDanglingHost(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	hostTask := createStoreTestTask(t, database)
	fixture := createChannelHistoryFixture(t, database, hostTask.ID, hostTask.ID)

	danglingHostID := domain.NewID("task") // deliberately never inserted into tasks
	danglingConversationID := domain.NewID("conversation")

	conn := rewindChannelConversationsToV131(t, database)
	insertLegacyConversation(t, conn, danglingConversationID, fixture.instance.ID,
		fixture.conversation.EndpointID, domain.NewID("scope"), danglingHostID, "Legacy dangling")
	// Another table already carries a legacy violation. The migration checks
	// only the invariant it owns, so this must not block startup.
	if _, err := conn.ExecContext(ctx, `UPDATE channel_handoffs SET created_task_id=? WHERE id=?`,
		domain.NewID("task"), fixture.handoffID); err != nil {
		conn.Close()
		t.Fatalf("plant unrelated legacy violation: %v", err)
	}
	finishLegacyConnection(t, conn)

	reopened := reopenAfterMigration(t, database, path)
	defer reopened.Close()

	var (
		normalHost string
		normalName string
		dangling   sql.NullString
		danglingNm string
	)
	if err := reopened.db.QueryRowContext(ctx,
		`SELECT host_task_id, display_name FROM channel_conversations WHERE id=?`, fixture.conversation.ID).
		Scan(&normalHost, &normalName); err != nil {
		t.Fatalf("normal conversation was lost by the rebuild: %v", err)
	}
	if normalHost != hostTask.ID || normalName != "Group" {
		t.Fatalf("normal conversation changed during rebuild: host=%q name=%q", normalHost, normalName)
	}
	if err := reopened.db.QueryRowContext(ctx,
		`SELECT host_task_id, display_name FROM channel_conversations WHERE id=?`, danglingConversationID).
		Scan(&dangling, &danglingNm); err != nil {
		t.Fatalf("dangling conversation was dropped instead of detached: %v", err)
	}
	if dangling.Valid {
		t.Fatalf("dangling host was copied as %q, want NULL", dangling.String)
	}
	if danglingNm != "Legacy dangling" {
		t.Fatalf("dangling conversation display_name=%q", danglingNm)
	}
	var rows int
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_conversations`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("rebuilt conversation count=%d err=%v, want 2", rows, err)
	}

	assertScopedForeignKeyCheckClean(t, reopened, "channel_conversations")
	// Prove the unrelated violation is real, so the clean scoped check is the
	// reason startup succeeded rather than the fixture failing to plant one.
	if !foreignKeyCheckHasTable(t, reopened, "channel_handoffs") {
		t.Fatalf("planted violation in channel_handoffs is missing; scoped-check assertion would be vacuous")
	}
	var applied int
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=132`).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("schema v132 record=%d err=%v", applied, err)
	}
}

// The old column was NOT NULL, so ” was its "no host" sentinel. The rebuild
// keeps it instead of turning it into NULL, and the narrowed check does not
// treat it as a dangling reference that would block startup.
func TestChannelConversationHostMigrationKeepsEmptyHostSentinel(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	hostTask := createStoreTestTask(t, database)
	fixture := createChannelHistoryFixture(t, database, hostTask.ID, hostTask.ID)
	emptyHostConversationID := domain.NewID("conversation")

	conn := rewindChannelConversationsToV131(t, database)
	insertLegacyConversation(t, conn, emptyHostConversationID, fixture.instance.ID,
		fixture.conversation.EndpointID, domain.NewID("scope"), "", "Legacy empty host")
	finishLegacyConnection(t, conn)

	reopened := reopenAfterMigration(t, database, path)
	defer reopened.Close()

	var host sql.NullString
	var name string
	if err := reopened.db.QueryRowContext(ctx,
		`SELECT host_task_id, display_name FROM channel_conversations WHERE id=?`, emptyHostConversationID).
		Scan(&host, &name); err != nil {
		t.Fatalf("empty-host conversation was dropped: %v", err)
	}
	if !host.Valid || host.String != "" || name != "Legacy empty host" {
		t.Fatalf("empty-string host must stay '', got valid=%v host=%q name=%q", host.Valid, host.String, name)
	}
}

// rewindChannelConversationsToV131 restores the pre-v132 table shape and
// forgets the v132 record. The returned connection has foreign_keys OFF, so
// callers can write the legacy rows the old build accepted.
func rewindChannelConversationsToV131(t *testing.T, database *Store) *sql.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := database.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE channel_conversations_v131 (
			id TEXT PRIMARY KEY,
			instance_id TEXT NOT NULL REFERENCES channel_instances(id) ON DELETE CASCADE,
			endpoint_id TEXT NOT NULL REFERENCES channel_endpoints(id) ON DELETE RESTRICT,
			scope_key_version INTEGER NOT NULL DEFAULT 1 CHECK(scope_key_version >= 1),
			scope_key TEXT NOT NULL,
			external_chat_id TEXT NOT NULL DEFAULT '',
			external_sender_id TEXT NOT NULL DEFAULT '',
			owner_identity_link_id TEXT REFERENCES channel_identity_links(id) ON DELETE RESTRICT,
			host_task_id TEXT NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE RESTRICT,
			status TEXT NOT NULL CHECK(status IN ('active','closed')),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			members_synced_at TEXT NOT NULL DEFAULT '',
			UNIQUE(endpoint_id,scope_key_version,scope_key)
		)`,
		`INSERT INTO channel_conversations_v131(
			id,instance_id,endpoint_id,scope_key_version,scope_key,external_chat_id,external_sender_id,
			owner_identity_link_id,host_task_id,status,created_at,updated_at,display_name,members_synced_at)
		SELECT id,instance_id,endpoint_id,scope_key_version,scope_key,external_chat_id,external_sender_id,
			owner_identity_link_id,host_task_id,status,created_at,updated_at,display_name,members_synced_at
		FROM channel_conversations`,
		`DROP TABLE channel_conversations`,
		`ALTER TABLE channel_conversations_v131 RENAME TO channel_conversations`,
		`CREATE INDEX IF NOT EXISTS idx_channel_conversations_instance ON channel_conversations(instance_id,status)`,
		`DELETE FROM schema_migrations WHERE version=132`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			conn.Close()
			t.Fatalf("roll back to v131 shape: %v", err)
		}
	}
	return conn
}

func insertLegacyConversation(t *testing.T, conn *sql.Conn, id, instanceID, endpointID, scopeKey, hostTaskID, displayName string) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(), `INSERT INTO channel_conversations(
		id,instance_id,endpoint_id,scope_key_version,scope_key,external_chat_id,external_sender_id,
		owner_identity_link_id,host_task_id,status,created_at,updated_at,display_name,members_synced_at)
	VALUES(?,?,?,?,?,'','',NULL,?,'active','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',?,'')`,
		id, instanceID, endpointID, 2, scopeKey, hostTaskID, displayName); err != nil {
		t.Fatalf("insert legacy conversation %s: %v", id, err)
	}
}

func finishLegacyConnection(t *testing.T, conn *sql.Conn) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func reopenAfterMigration(t *testing.T, database *Store, path string) *Store {
	t.Helper()
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen with v132 migration: %v", err)
	}
	return reopened
}

func assertScopedForeignKeyCheckClean(t *testing.T, database *Store, table string) {
	t.Helper()
	rows, err := database.db.QueryContext(context.Background(), `PRAGMA foreign_key_check(`+table+`)`)
	if err != nil {
		t.Fatalf("foreign_key_check(%s): %v", table, err)
	}
	defer rows.Close()
	if rows.Next() {
		var found, parent string
		var rowID, fkID sql.NullInt64
		if err := rows.Scan(&found, &rowID, &parent, &fkID); err != nil {
			t.Fatalf("scan foreign_key_check(%s): %v", table, err)
		}
		t.Fatalf("foreign_key_check(%s) reported %s rowid=%v parent=%s fk=%v", table, found, rowID, parent, fkID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign_key_check(%s): %v", table, err)
	}
}

func foreignKeyCheckHasTable(t *testing.T, database *Store, want string) bool {
	t.Helper()
	rows, err := database.db.QueryContext(context.Background(), `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var table, parent string
		var rowID, fkID sql.NullInt64
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			t.Fatal(err)
		}
		if table == want {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}
