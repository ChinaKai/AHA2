package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentAPISecurityMigrationDisablesLegacyPlaintextAddresses pins what happens
// to values written before the https/loopback boundary existed.
//
// The migration must do three different things to three kinds of row: drop a
// plaintext address that is now forbidden (and say why), keep a still-permitted
// manual address as the hidden override, and fold the removed "global" mode. It
// must not touch mirrors owned by another device — rewriting those would make
// this device fight the sync loop over a row it does not own.
func TestAgentAPISecurityMigrationDisablesLegacyPlaintextAddresses(t *testing.T) {
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
	rows := []struct {
		id, mode, url, resolved, status string
		readOnly                        int
	}{
		{"ws-http", "manual", "http://192.168.1.10:8766", "", "ready", 0},
		{"ws-https", "manual", "https://aha.example.test", "https://aha.example.test", "ready", 0},
		{"ws-loopback", "manual", "http://127.0.0.1:8766", "http://127.0.0.1:8766", "ready", 0},
		// Detection used to persist whatever it probed. An automatic row has no
		// manual address, so only this column can reveal the old plaintext value.
		{"ws-resolved-plaintext", "auto", "", "http://192.168.1.30:8766", "ready", 0},
		{"ws-global", "global", "", "", "ready", 0},
		{"ws-mirror", "manual", "http://192.168.1.11:8766", "", "ready", 1},
		{"ws-mirror-global", "global", "", "", "ready", 1},
	}
	for _, row := range rows {
		if _, err := database.db.ExecContext(ctx,
			`INSERT INTO workspaces(id,project_id,name,locality,transport,root_path,created_at,updated_at,agent_api_mode,agent_api_url,agent_api_resolved_url,agent_api_status,read_only)
			 VALUES(?, 'p1', 'W', 'remote', 'ssh', '/tmp', ?, ?, ?, ?, ?, ?, ?)`,
			row.id, now, now, row.mode, row.url, row.resolved, row.status, row.readOnly); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.db.ExecContext(ctx,
		`UPDATE agent_api_settings SET url='http://10.0.0.5:8766',allow_insecure=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}

	if err := database.migrateAgentAPISecurityConvergence(ctx); err != nil {
		t.Fatal(err)
	}

	var overrideURL, note string
	var allowInsecure int
	if err := database.db.QueryRowContext(ctx,
		`SELECT url,migration_note,allow_insecure FROM agent_api_settings WHERE id=1`).
		Scan(&overrideURL, &note, &allowInsecure); err != nil {
		t.Fatal(err)
	}
	if overrideURL != "" || allowInsecure != 0 {
		t.Fatalf("legacy override survived: url=%q allow_insecure=%d", overrideURL, allowInsecure)
	}
	if !strings.Contains(note, "已禁用") {
		t.Fatalf("migration note does not explain the change: %q", note)
	}

	assertWorkspace := func(id, wantMode, wantURL, wantResolved, wantStatus, wantNote string) {
		t.Helper()
		var mode, url, resolved, note, status string
		if err := database.db.QueryRowContext(ctx,
			`SELECT agent_api_mode,agent_api_url,agent_api_resolved_url,agent_api_migration_note,agent_api_status FROM workspaces WHERE id=?`, id).
			Scan(&mode, &url, &resolved, &note, &status); err != nil {
			t.Fatal(err)
		}
		if mode != wantMode || url != wantURL || resolved != wantResolved || status != wantStatus {
			t.Errorf("%s: mode=%q url=%q resolved=%q status=%q, want mode=%q url=%q resolved=%q status=%q",
				id, mode, url, resolved, status, wantMode, wantURL, wantResolved, wantStatus)
		}
		if wantNote == "" && note != "" {
			t.Errorf("%s: unexpected note %q", id, note)
		}
		if wantNote != "" && !strings.Contains(note, wantNote) {
			t.Errorf("%s: note=%q, want it to mention %q", id, note, wantNote)
		}
	}
	assertWorkspace("ws-http", "auto", "", "", "unknown", "已禁用")
	assertWorkspace("ws-https", "manual", "https://aha.example.test", "https://aha.example.test", "ready", "")
	assertWorkspace("ws-loopback", "manual", "http://127.0.0.1:8766", "http://127.0.0.1:8766", "ready", "")
	assertWorkspace("ws-resolved-plaintext", "auto", "", "", "unknown", "已清除")
	assertWorkspace("ws-global", "auto", "", "", "ready", "")
	// Read-only mirrors belong to another device; leave them alone.
	assertWorkspace("ws-mirror", "manual", "http://192.168.1.11:8766", "", "ready", "")
	assertWorkspace("ws-mirror-global", "global", "", "", "ready", "")
}

// TestAgentAPISecurityMigrationKeepsAllowedOverride pins the other half: a safe
// stored value is preserved, and the removed insecure flag is cleared regardless.
func TestAgentAPISecurityMigrationKeepsAllowedOverride(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.db.ExecContext(ctx,
		`UPDATE agent_api_settings SET url='https://aha.example.test',allow_insecure=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrateAgentAPISecurityConvergence(ctx); err != nil {
		t.Fatal(err)
	}
	var url, note string
	var allowInsecure int
	if err := database.db.QueryRowContext(ctx,
		`SELECT url,migration_note,allow_insecure FROM agent_api_settings WHERE id=1`).
		Scan(&url, &note, &allowInsecure); err != nil {
		t.Fatal(err)
	}
	if url != "https://aha.example.test" || note != "" || allowInsecure != 0 {
		t.Fatalf("url=%q note=%q allow_insecure=%d", url, note, allowInsecure)
	}
}
