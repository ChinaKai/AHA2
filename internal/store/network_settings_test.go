package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func domainNetworkSettingsForTest(address string) domain.NetworkSettings {
	return domain.NetworkSettings{ListenAddress: address, UpdatedAt: time.Now().UTC()}
}

func TestNetworkSettingsDefaultsToNoOverride(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var migrated bool
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=68)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v68 missing: migrated=%t err=%v", migrated, err)
	}
	// A fresh install stores nothing, so it keeps exactly the address it was
	// installed with rather than being moved onto some built-in default.
	item, err := database.NetworkSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if item.ListenAddress != "" {
		t.Fatalf("fresh install listen address = %q, want empty", item.ListenAddress)
	}

	if _, err := database.UpdateNetworkSettings(ctx, domainNetworkSettingsForTest("0.0.0.0:9000")); err != nil {
		t.Fatal(err)
	}
	item, err = database.NetworkSettings(ctx)
	if err != nil || item.ListenAddress != "0.0.0.0:9000" {
		t.Fatalf("stored listen address = %#v err=%v", item, err)
	}
}

func TestAccessScopeSurvivesUpgradeAsLAN(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	// v68 adds the column with a 'lan' default, so an install that upgrades keeps
	// answering the LAN instead of silently losing its remote clients.
	settings, err := database.SecuritySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.AccessScope != "lan" {
		t.Fatalf("access scope after migration = %q, want lan", settings.AccessScope)
	}
	if _, err := database.UpdateSecuritySettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	settings.AccessScope = "local"
	if _, err := database.UpdateSecuritySettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	settings, err = database.SecuritySettings(ctx)
	if err != nil || settings.AccessScope != "local" {
		t.Fatalf("access scope after update = %q err=%v", settings.AccessScope, err)
	}
	// An unknown value must never be stored as a scope that does nothing.
	settings.AccessScope = "bogus"
	if _, err := database.UpdateSecuritySettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	settings, err = database.SecuritySettings(ctx)
	if err != nil || settings.AccessScope != "lan" {
		t.Fatalf("unknown scope resolved to %q err=%v, want lan", settings.AccessScope, err)
	}
}

func TestValidateListenAddress(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{"0.0.0.0:8766", "127.0.0.1:1", "192.168.1.5:65535", "[::]:8766"} {
		if err := ValidateListenAddress(valid); err != nil {
			t.Fatalf("ValidateListenAddress(%q) = %v, want nil", valid, err)
		}
	}
	// A hostname or wildcard would need quoting in a scheduled task argument list,
	// and a bad port cannot be bound, so all of these are refused.
	for _, invalid := range []string{"", "localhost:8766", "0.0.0.0", "0.0.0.0:0", "0.0.0.0:65536", ":8766", "1.2.3.4:abc", "0.0.0.0:8766; rm -rf /"} {
		if err := ValidateListenAddress(invalid); err == nil {
			t.Fatalf("ValidateListenAddress(%q) = nil, want an error", invalid)
		}
	}
}
