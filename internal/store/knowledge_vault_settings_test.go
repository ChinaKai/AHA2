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
