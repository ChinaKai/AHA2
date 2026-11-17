package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestKnowledgeVaultSettingsMigrationDefaultsAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	var version int
	if err := database.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 136 {
		t.Fatalf("schema version = %d, want at least 136", version)
	}
	settings, err := database.KnowledgeVaultSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Enabled || settings.Configured {
		t.Fatalf("fresh knowledge vault settings = %+v, want disabled and unconfigured", settings)
	}

	if _, err := database.UpdateKnowledgeVaultSettings(ctx, domain.KnowledgeVaultSettings{
		Enabled: true, Configured: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	settings, err = reopened.KnowledgeVaultSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled || !settings.Configured {
		t.Fatalf("settings after restart = %+v, want enabled and configured", settings)
	}

	// Re-applying v136 must keep the row (CREATE IF NOT EXISTS + INSERT OR
	// IGNORE) and record the version exactly once.
	if _, err := reopened.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=136`); err != nil {
		t.Fatal(err)
	}
	if err := reopened.migrate(ctx); err != nil {
		t.Fatalf("re-applying schema v136: %v", err)
	}
	if err := reopened.migrate(ctx); err != nil {
		t.Fatalf("applying schema v136 a third time: %v", err)
	}
	settings, err = reopened.KnowledgeVaultSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled || !settings.Configured {
		t.Fatalf("re-migration lost the settings row: %+v", settings)
	}
	var recorded int
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=136`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Fatalf("schema v136 recorded %d times, want 1", recorded)
	}
}

func TestKnowledgeVaultAutoSyncMigrationFromV146(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// Rebuild the settings row without the v147 column to reproduce an
	// upgraded v146 database. CREATE TABLE AS avoids SQLite's refusal to drop a
	// column referenced by its own CHECK constraint.
	for _, statement := range []string{
		`CREATE TABLE kv_v146 AS SELECT id,enabled,configured,import_source,migration_verified_at,import_verified_at,last_backup_path,updated_at FROM knowledge_vault_settings`,
		`DROP TABLE knowledge_vault_settings`,
		`ALTER TABLE kv_v146 RENAME TO knowledge_vault_settings`,
		`DELETE FROM schema_migrations WHERE version=147`,
	} {
		if _, err := database.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := upgraded.KnowledgeVaultSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.AutoSync {
		t.Fatalf("v147 upgrade did not default auto_sync to on: %+v", settings)
	}
	if count := migrationCount(t, ctx, upgraded, 147); count != 1 {
		t.Fatalf("v147 recorded %d times after upgrade", count)
	}

	// A partial v147 that already has the column with the owner's explicit 0
	// must keep that value and record the version exactly once.
	if _, err := upgraded.db.ExecContext(ctx, `UPDATE knowledge_vault_settings SET auto_sync=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := upgraded.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=147`); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	settings, err = resumed.KnowledgeVaultSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.AutoSync {
		t.Fatalf("v147 re-run overwrote the stored auto_sync=0: %+v", settings)
	}
	if count := migrationCount(t, ctx, resumed, 147); count != 1 {
		t.Fatalf("v147 recorded %d times after re-run", count)
	}
}

func migrationCount(t *testing.T, ctx context.Context, database *Store, version int) int {
	t.Helper()
	var count int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
