package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigrationFoldsGlobalAgentAPIModeIntoAuto pins that a workspace carrying the
// removed "global" mode is converted rather than left behind.
//
// "global" and "auto" resolve to the same effective URL, so this changes no
// behaviour. It matters because the mode picker no longer offers "global": a row
// still holding it would be a value the UI cannot display or set back.
func TestMigrationFoldsGlobalAgentAPIModeIntoAuto(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := "2026-01-01T00:00:00Z"
	if _, err := database.db.ExecContext(ctx,
		`INSERT INTO projects(id,name,project_type,created_at,updated_at) VALUES('p1','P','folder',?,?)`,
		now, now); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, mode string }{
		{"ws-global", "global"},
		{"ws-auto", "auto"},
		{"ws-manual", "manual"},
	} {
		if _, err := database.db.ExecContext(ctx,
			`INSERT INTO workspaces(id,project_id,name,locality,transport,root_path,created_at,updated_at,agent_api_mode)
			 VALUES(?, 'p1', 'W', 'remote', 'ssh', '/tmp', ?, ?, ?)`,
			row.id, now, now, row.mode); err != nil {
			t.Fatal(err)
		}
	}

	// Re-run the migration the way a real upgrade would: the version is already
	// recorded by Open, so apply the statement directly to check its effect.
	if _, err := database.db.ExecContext(ctx, schemaV69); err != nil {
		t.Fatal(err)
	}

	for _, want := range []struct{ id, mode string }{
		{"ws-global", "auto"},
		{"ws-auto", "auto"},
		{"ws-manual", "manual"},
	} {
		var got string
		if err := database.db.QueryRowContext(ctx,
			`SELECT agent_api_mode FROM workspaces WHERE id=?`, want.id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want.mode {
			t.Errorf("%s: agent_api_mode = %q, want %q", want.id, got, want.mode)
		}
	}
}
