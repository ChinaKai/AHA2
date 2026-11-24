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
		if _, err := upgraded.UpdateWorkspaceTerminalSettings(ctx, &enabled, nil); err != nil {
			t.Fatal(err)
		}
		settings, err := upgraded.SecuritySettings(ctx)
		if err != nil || settings.WorkspaceTerminalLAN != enabled || settings.ValidateOrigin || settings.AccessScope != domain.AccessScopeLocal {
			t.Fatalf("persisted LAN setting after writing %t = %+v, %v", enabled, settings, err)
		}
	}
}

func TestSecuritySettingsV163UpgradesV162Database(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "aha2.db")
	legacy, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.db.ExecContext(ctx, `
		ALTER TABLE security_settings DROP COLUMN workspace_terminal_external_origin;
		DELETE FROM schema_migrations WHERE version=163;
		UPDATE security_settings SET access_scope='lan', validate_origin=0, workspace_terminal_lan=1 WHERE id=1;
	`)
	legacy.Close()
	if err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if count := migrationVersionCount(t, upgraded, 163); count != 1 {
		t.Fatalf("v163 migration rows = %d, want 1", count)
	}
	settings, err := upgraded.SecuritySettings(ctx)
	if err != nil || settings.WorkspaceTerminalExternalOrigin != "" || !settings.WorkspaceTerminalLAN ||
		settings.ValidateOrigin || settings.AccessScope != domain.AccessScopeLAN {
		t.Fatalf("migrated security settings = %+v, %v", settings, err)
	}
	origin := "https://proxy.example"
	settings, err = upgraded.UpdateWorkspaceTerminalSettings(ctx, nil, &origin)
	if err != nil || settings.WorkspaceTerminalExternalOrigin != origin || !settings.WorkspaceTerminalLAN {
		t.Fatalf("origin-only update = %+v, %v", settings, err)
	}
	disabled := false
	settings, err = upgraded.UpdateWorkspaceTerminalSettings(ctx, &disabled, nil)
	if err != nil || settings.WorkspaceTerminalExternalOrigin != origin || settings.WorkspaceTerminalLAN {
		t.Fatalf("LAN-only update = %+v, %v", settings, err)
	}
	settings, err = upgraded.UpdateSecuritySettings(ctx, domain.SecuritySettings{ValidateOrigin: true, AccessScope: domain.AccessScopeLocal})
	if err != nil || settings.WorkspaceTerminalExternalOrigin != origin || settings.WorkspaceTerminalLAN {
		t.Fatalf("general security update = %+v, %v", settings, err)
	}
}
