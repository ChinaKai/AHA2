package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/proxyconfig"
)

const testLegacyProxyTime = "2025-01-02T03:04:05.678Z"

func TestProxyDefaultOffOnFreshDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.ProxySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "off" {
		t.Fatalf("fresh database proxy mode = %q, want off", settings.Mode)
	}
	if settings.HTTPProxy != legacyProxyAddress || settings.HTTPSProxy != legacyProxyAddress {
		t.Fatalf("fresh database lost the historical address seed: %#v", settings)
	}
	// Follow the Turn path's composition: read the stored row, normalize it,
	// then apply it to the environment. A fresh database must leave no
	// HTTP_PROXY/HTTPS_PROXY behind even though the inert addresses remain.
	normalized, err := proxyconfig.Normalize(settings)
	if err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		"HTTP_PROXY": "http://stale.example:7897", "HTTPS_PROXY": "http://stale.example:7897",
	}
	proxyconfig.ApplyEnvironment(environment, normalized)
	if _, ok := environment["HTTP_PROXY"]; ok {
		t.Fatalf("fresh database injected HTTP_PROXY: %#v", environment)
	}
	if _, ok := environment["HTTPS_PROXY"]; ok {
		t.Fatalf("fresh database injected HTTPS_PROXY: %#v", environment)
	}
	assertProxyDefaultOffVersion(t, ctx, database)

	// Reopening is a no-op: the version row prevents the migration from
	// re-evaluating (and overwriting) whatever the owner set later.
	if _, err := database.UpdateProxySettings(ctx, domain.ProxySettings{
		Mode: "external", HTTPProxy: "http://192.0.2.10:3128", HTTPSProxy: "http://192.0.2.10:3128",
		NoProxy: "localhost", ManagedRefreshIntervalMins: 1440, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	settings, err = database.ProxySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "external" || settings.HTTPProxy != "http://192.0.2.10:3128" {
		t.Fatalf("reopen overwrote an explicit proxy setting: %#v", settings)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatalf("repeat migration failed: %v", err)
	}
}

// TestProxyDefaultOffMigrationCoversLegacyModeColumnPath rewinds a real
// database to the shape it had before schema v53 added proxy_settings.mode.
// Opening it then exercises the actual historical path: v53 adds the mode
// column with its 'external' default on the untouched v18 seed row, and v157
// turns that seed off.
func TestProxyDefaultOffMigrationCoversLegacyModeColumnPath(t *testing.T) {
	t.Parallel()
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
		`DELETE FROM schema_migrations WHERE version IN (53,54,157)`,
		`UPDATE schema_migrations SET applied_at='` + testLegacyProxyTime + `' WHERE version=18`,
		`CREATE TABLE proxy_settings_legacy (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			http_proxy TEXT NOT NULL DEFAULT '',
			https_proxy TEXT NOT NULL DEFAULT '',
			no_proxy TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO proxy_settings_legacy(id,http_proxy,https_proxy,no_proxy,updated_at)
		 VALUES(1,'` + legacyProxyAddress + `','` + legacyProxyAddress + `','` + legacyProxyNoProxy + `','` + testLegacyProxyTime + `')`,
		`DROP TABLE proxy_settings`,
		`ALTER TABLE proxy_settings_legacy RENAME TO proxy_settings`,
	} {
		if _, err := raw.ExecContext(ctx, statement); err != nil {
			raw.Close()
			t.Fatalf("prepare legacy database: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	settings, err := database.ProxySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "off" {
		t.Fatalf("legacy v18+v53 path proxy mode = %q, want off", settings.Mode)
	}
	if !settings.UpdatedAt.Equal(mustParseProxyTestTime(t, testLegacyProxyTime)) {
		t.Fatalf("migration rewrote the seed timestamp: %v", settings.UpdatedAt)
	}
	assertProxyDefaultOffVersion(t, ctx, database)
}

func TestProxyDefaultOffMigrationPreservesExplicitSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	later := "2026-02-03T04:05:06.789123456Z"
	cases := []struct {
		name     string
		settings domain.ProxySettings
	}{
		{
			name: "explicit save of the old default",
			settings: domain.ProxySettings{
				Mode: "external", HTTPProxy: legacyProxyAddress, HTTPSProxy: legacyProxyAddress,
				NoProxy: legacyProxyNoProxy, ManagedRefreshIntervalMins: 1440,
			},
		},
		{
			name: "custom external",
			settings: domain.ProxySettings{
				Mode: "external", HTTPProxy: "http://198.51.100.7:8080", HTTPSProxy: "https://proxy.example:8443",
				NoProxy: "localhost,.corp.example", ManagedRefreshIntervalMins: 1440,
			},
		},
		{
			name: "off",
			settings: domain.ProxySettings{
				Mode: "off", HTTPProxy: "http://198.51.100.7:8080", HTTPSProxy: "http://198.51.100.7:8080",
				NoProxy: "localhost", ManagedRefreshIntervalMins: 1440,
			},
		},
		{
			name: "managed",
			settings: domain.ProxySettings{
				Mode: "managed_hysteria2", HTTPProxy: legacyProxyAddress, HTTPSProxy: legacyProxyAddress,
				NoProxy: legacyProxyNoProxy, ManagedProfileID: "profile-1", ManagedNodeID: "node-2",
				ManagedRefreshIntervalMins: 120, ManagedSubscriptionAt: mustParseProxyTestTime(t, later),
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			database, path := prepareProxyUpgradeRow(t, ctx, testCase.settings, later)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			database, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			stored, err := database.ProxySettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Mode != testCase.settings.Mode ||
				stored.HTTPProxy != testCase.settings.HTTPProxy ||
				stored.HTTPSProxy != testCase.settings.HTTPSProxy ||
				stored.NoProxy != testCase.settings.NoProxy ||
				stored.ManagedProfileID != testCase.settings.ManagedProfileID ||
				stored.ManagedNodeID != testCase.settings.ManagedNodeID ||
				stored.ManagedRefreshIntervalMins != testCase.settings.ManagedRefreshIntervalMins ||
				!stored.ManagedSubscriptionAt.Equal(testCase.settings.ManagedSubscriptionAt) ||
				!stored.UpdatedAt.Equal(mustParseProxyTestTime(t, later)) {
				t.Fatalf("migration changed an explicit setting:\n got %#v\nwant %#v", stored, testCase.settings)
			}
			assertProxyDefaultOffVersion(t, ctx, database)
		})
	}
}

func TestProxyDefaultOffMigrationRerunKeepsLaterUserSave(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateProxySettings(ctx, domain.ProxySettings{
		Mode: "external", HTTPProxy: "http://203.0.113.9:8080", HTTPSProxy: "http://203.0.113.9:8080",
		NoProxy: "localhost", ManagedRefreshIntervalMins: 1440,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, proxyDefaultOffVersion); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	settings, err := database.ProxySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "external" || settings.HTTPProxy != "http://203.0.113.9:8080" {
		t.Fatalf("rerun overwrote a later user save: %#v", settings)
	}
	assertProxyDefaultOffVersion(t, ctx, database)
}

func TestProxyDefaultOffMigrationPreservesUnprovableRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(t *testing.T, database *Store)
	}{
		{
			name: "empty timestamp",
			mutate: func(t *testing.T, database *Store) {
				execProxyTestSQL(t, database, `UPDATE proxy_settings SET updated_at='' WHERE id=1`)
			},
		},
		{
			name: "timestamp far before migration record",
			mutate: func(t *testing.T, database *Store) {
				execProxyTestSQL(t, database,
					`UPDATE proxy_settings SET updated_at='2024-12-31T00:00:00.000Z' WHERE id=1`)
			},
		},
		{
			name: "custom values under a seed timestamp",
			mutate: func(t *testing.T, database *Store) {
				execProxyTestSQL(t, database,
					`UPDATE proxy_settings SET http_proxy='http://198.51.100.7:8080' WHERE id=1`)
			},
		},
		{
			name: "unreadable seed migration time",
			mutate: func(t *testing.T, database *Store) {
				execProxyTestSQL(t, database,
					`UPDATE schema_migrations SET applied_at='' WHERE version=18`)
			},
		},
		{
			// The legacy managed-profile import path writes
			// managed_profile_id without advancing updated_at. A non-empty
			// managed field means the row is not the untouched seed.
			name: "managed metadata under a seed timestamp",
			mutate: func(t *testing.T, database *Store) {
				execProxyTestSQL(t, database,
					`UPDATE proxy_settings SET managed_profile_id='legacy-profile' WHERE id=1`)
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "aha2.db")
			database, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(ctx,
				`UPDATE proxy_settings SET mode='external' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			testCase.mutate(t, database)
			if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, proxyDefaultOffVersion); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			database, err = Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			settings, err := database.ProxySettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if settings.Mode != "external" {
				t.Fatalf("unprovable row was rewritten to %q", settings.Mode)
			}
			assertProxyDefaultOffVersion(t, ctx, database)
		})
	}
}

func TestProxyDefaultOffMigrationPreservesRowWithoutSeedMarker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	execProxyTestSQL(t, database,
		`UPDATE proxy_settings SET mode='external',updated_at='`+testLegacyProxyTime+`' WHERE id=1`)
	if _, err := database.db.ExecContext(ctx,
		`DELETE FROM schema_migrations WHERE version IN (18,?)`, proxyDefaultOffVersion); err != nil {
		t.Fatal(err)
	}
	if err := database.migrateProxyDefaultOff(ctx); err != nil {
		t.Fatal(err)
	}
	settings, err := database.ProxySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "external" {
		t.Fatalf("row without a v18 marker was rewritten to %q", settings.Mode)
	}
	assertProxyDefaultOffVersion(t, ctx, database)
}

// TestProxyDefaultOffMigrationPreservesSaveAfterCandidateRead pins the
// read-then-rewrite race: after the migration has read a row that still looks
// like the v18 seed, an explicit save (including one that stores exactly the
// old default addresses) must survive both the conditional rewrite and the
// version bookkeeping.
func TestProxyDefaultOffMigrationPreservesSaveAfterCandidateRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name  string
		saved domain.ProxySettings
	}{
		{
			name: "same legacy addresses",
			saved: domain.ProxySettings{
				Mode: "external", HTTPProxy: legacyProxyAddress, HTTPSProxy: legacyProxyAddress,
				NoProxy: legacyProxyNoProxy, ManagedRefreshIntervalMins: 1440,
			},
		},
		{
			name: "custom external",
			saved: domain.ProxySettings{
				Mode: "external", HTTPProxy: "http://198.51.100.9:8080", HTTPSProxy: "https://proxy.example:8443",
				NoProxy: "localhost,.corp.example", ManagedRefreshIntervalMins: 1440,
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			database, _ := prepareProxySeedRow(t, ctx)
			defer database.Close()
			candidate, isSeed, err := database.proxyDefaultOffCandidate(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !isSeed {
				t.Fatal("prepared row was not classified as the v18 seed")
			}
			saved, err := database.UpdateProxySettings(ctx, testCase.saved)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := database.applyProxyDefaultOffCandidate(ctx, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Fatal("conditional rewrite overwrote a save made after the candidate read")
			}
			assertProxySavedValue(t, ctx, database, saved)
			// Bookkeeping must still finish, and must not revisit the row.
			if err := database.migrateProxyDefaultOff(ctx); err != nil {
				t.Fatal(err)
			}
			assertProxySavedValue(t, ctx, database, saved)
			assertProxyDefaultOffVersion(t, ctx, database)
		})
	}
}

func TestProxyDefaultOffConditionalRewriteSurvivesRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, _ := prepareProxySeedRow(t, ctx)
	defer database.Close()
	candidate, isSeed, err := database.proxyDefaultOffCandidate(ctx)
	if err != nil || !isSeed {
		t.Fatalf("prepared row was not classified as the v18 seed: %v", err)
	}
	changed, err := database.applyProxyDefaultOffCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("conditional rewrite did not update the unchanged seed row")
	}
	settings, err := database.ProxySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "off" {
		t.Fatalf("conditional rewrite left mode %q, want off", settings.Mode)
	}
	if !settings.UpdatedAt.Equal(mustParseProxyTestTime(t, testLegacyProxyTime)) {
		t.Fatalf("conditional rewrite changed updated_at: %v", settings.UpdatedAt)
	}
	// Simulate a failure between the rewrite and the version insert: the
	// retry sees mode=off, skips the rewrite and records the version.
	if err := database.migrateProxyDefaultOff(ctx); err != nil {
		t.Fatal(err)
	}
	settings, err = database.ProxySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "off" {
		t.Fatalf("retry changed mode to %q, want off", settings.Mode)
	}
	assertProxyDefaultOffVersion(t, ctx, database)
}

// TestProxyDefaultOffMigrationTreatsSameMillisecondSeedAsSeed documents the
// historical boundary: v18 writes both timestamps with millisecond precision,
// so a real seed commonly has updated_at == applied_at. That row is the seed
// and is turned off.
func TestProxyDefaultOffMigrationTreatsSameMillisecondSeedAsSeed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, _ := prepareProxySeedRow(t, ctx)
	defer database.Close()
	if err := database.migrateProxyDefaultOff(ctx); err != nil {
		t.Fatal(err)
	}
	settings, err := database.ProxySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "off" {
		t.Fatalf("same-millisecond seed row mode = %q, want off", settings.Mode)
	}
	assertProxyDefaultOffVersion(t, ctx, database)
}

// TestProxyDefaultOffMigrationPreservesGoTimestampBeforeAppliedAt covers a
// settings write whose nanosecond timestamp happens to parse before v18's
// applied_at (for example after a clock rollback). Its format is not the v18
// seed shape, so it is preserved even though the time comparison alone would
// classify it as a seed.
func TestProxyDefaultOffMigrationPreservesGoTimestampBeforeAppliedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, _ := prepareProxySeedRow(t, ctx)
	defer database.Close()
	execProxyTestSQL(t, database,
		`UPDATE schema_migrations SET applied_at='2025-01-02T03:04:05.679Z' WHERE version=18`)
	execProxyTestSQL(t, database,
		`UPDATE proxy_settings SET updated_at='2025-01-02T03:04:05.678123456Z' WHERE id=1`)
	if err := database.migrateProxyDefaultOff(ctx); err != nil {
		t.Fatal(err)
	}
	settings, err := database.ProxySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "external" {
		t.Fatalf("nanosecond settings write was rewritten to %q", settings.Mode)
	}
	assertProxyDefaultOffVersion(t, ctx, database)
}

func prepareProxySeedRow(t *testing.T, ctx context.Context) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE proxy_settings
		SET mode='external', http_proxy=?, https_proxy=?, no_proxy=?,
		    managed_profile_id='', managed_node_id='', managed_refresh_interval_minutes=1440,
		    managed_subscription_at='', updated_at=?
		WHERE id=1`,
		legacyProxyAddress, legacyProxyAddress, legacyProxyNoProxy, testLegacyProxyTime,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx,
		`UPDATE schema_migrations SET applied_at=? WHERE version=18`, testLegacyProxyTime); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx,
		`DELETE FROM schema_migrations WHERE version=?`, proxyDefaultOffVersion); err != nil {
		t.Fatal(err)
	}
	return database, path
}

func assertProxySavedValue(t *testing.T, ctx context.Context, database *Store, want domain.ProxySettings) {
	t.Helper()
	stored, err := database.ProxySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Mode != want.Mode ||
		stored.HTTPProxy != want.HTTPProxy ||
		stored.HTTPSProxy != want.HTTPSProxy ||
		stored.NoProxy != want.NoProxy ||
		stored.ManagedProfileID != want.ManagedProfileID ||
		stored.ManagedNodeID != want.ManagedNodeID ||
		stored.ManagedRefreshIntervalMins != want.ManagedRefreshIntervalMins ||
		!stored.ManagedSubscriptionAt.Equal(want.ManagedSubscriptionAt) ||
		!stored.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("saved proxy value changed:\n got %#v\nwant %#v", stored, want)
	}
}

func prepareProxyUpgradeRow(
	t *testing.T, ctx context.Context, settings domain.ProxySettings, updatedAt string,
) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE proxy_settings
		SET mode=?, http_proxy=?, https_proxy=?, no_proxy=?, managed_profile_id=?,
		    managed_node_id=?, managed_refresh_interval_minutes=?, managed_subscription_at=?, updated_at=?
		WHERE id=1`,
		settings.Mode, settings.HTTPProxy, settings.HTTPSProxy, settings.NoProxy,
		settings.ManagedProfileID, settings.ManagedNodeID, settings.ManagedRefreshIntervalMins,
		timeString(settings.ManagedSubscriptionAt), updatedAt,
	); err != nil {
		t.Fatal(err)
	}
	// Put the seed record in the past so an explicit save can be strictly
	// later than it, exactly like an upgraded installation.
	if _, err := database.db.ExecContext(ctx,
		`UPDATE schema_migrations SET applied_at=? WHERE version=18`, testLegacyProxyTime); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, proxyDefaultOffVersion); err != nil {
		t.Fatal(err)
	}
	return database, path
}

func execProxyTestSQL(t *testing.T, database *Store, statement string) {
	t.Helper()
	if _, err := database.db.ExecContext(context.Background(), statement); err != nil {
		t.Fatal(err)
	}
}

func assertProxyDefaultOffVersion(t *testing.T, ctx context.Context, database *Store) {
	t.Helper()
	var count int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, proxyDefaultOffVersion).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("schema v%d recorded %d times, want 1", proxyDefaultOffVersion, count)
	}
}

func mustParseProxyTestTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
