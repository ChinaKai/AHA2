package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestSecuritySettingsDefaultAndPersistence(t *testing.T) {
	t.Parallel()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	settings, err := database.SecuritySettings(context.Background())
	if err != nil || !settings.ValidateOrigin {
		t.Fatalf("default security settings = %#v, %v", settings, err)
	}
	settings, err = database.UpdateSecuritySettings(context.Background(), domain.SecuritySettings{ValidateOrigin: false})
	if err != nil || settings.ValidateOrigin {
		t.Fatalf("update security settings = %#v, %v", settings, err)
	}
	stored, err := database.SecuritySettings(context.Background())
	if err != nil || stored.ValidateOrigin || stored.UpdatedAt.IsZero() {
		t.Fatalf("stored security settings = %#v, %v", stored, err)
	}
}

func TestSecuritySettingsV161UpgradesV160Database(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "aha2.db")
	legacy, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the v160 schema on disk, keeping a non-default security row.
	err = func() error {
		defer legacy.Close()
		_, err := legacy.db.ExecContext(ctx, `
			ALTER TABLE security_settings DROP COLUMN workspace_terminal_lan;
			DELETE FROM schema_migrations WHERE version=161;
			UPDATE security_settings SET access_scope='local', validate_origin=0 WHERE id=1;
		`)
		return err
	}()
	if err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if count := migrationVersionCount(t, upgraded, 161); count != 1 {
		t.Fatalf("v161 migration rows = %d, want 1", count)
	}
	settings, err := upgraded.SecuritySettings(ctx)
	if err != nil || settings.WorkspaceTerminalLAN || settings.ValidateOrigin || settings.AccessScope != domain.AccessScopeLocal {
		t.Fatalf("migrated security settings = %+v, %v", settings, err)
	}
	for _, enabled := range []bool{true, false} {
		if _, err := upgraded.UpdateWorkspaceTerminalLAN(ctx, enabled); err != nil {
			t.Fatal(err)
		}
		settings, err := upgraded.SecuritySettings(ctx)
		if err != nil || settings.WorkspaceTerminalLAN != enabled || settings.ValidateOrigin || settings.AccessScope != domain.AccessScopeLocal {
			t.Fatalf("persisted LAN setting after writing %t = %+v, %v", enabled, settings, err)
		}
	}
}
