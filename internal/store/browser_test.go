package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestBrowserSchemaAndSingleActiveSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var hasV147 bool
	if err := database.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=147)`).Scan(&hasV147); err != nil || !hasV147 {
		t.Fatalf("schema v147=%v err=%v", hasV147, err)
	}
	var hasV149 bool
	if err := database.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=149)`).Scan(&hasV149); err != nil || !hasV149 {
		t.Fatalf("schema v149=%v err=%v", hasV149, err)
	}
	var originColumn int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('browser_tabs') WHERE name='origin'`).Scan(&originColumn); err != nil || originColumn != 1 {
		t.Fatalf("browser_tabs.origin=%d err=%v", originColumn, err)
	}
	var hasV150 bool
	if err := database.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=150)`).Scan(&hasV150); err != nil || !hasV150 {
		t.Fatalf("schema v150=%v err=%v", hasV150, err)
	}
	var hasV151 bool
	if err := database.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=151)`).Scan(&hasV151); err != nil || !hasV151 {
		t.Fatalf("schema v151=%v err=%v", hasV151, err)
	}
	var hasV152 bool
	_ = database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=152)`).Scan(&hasV152)
	var holdColumns int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('browser_task_grants')
		 WHERE name IN ('hold_id','hold_original_expires_at','hold_expires_at')`).Scan(&holdColumns); err != nil ||
		!hasV152 || holdColumns != 3 {
		t.Fatalf("schema v152=%v hold columns=%d err=%v", hasV152, holdColumns, err)
	}
	var hasOldPerTaskIndex int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_browser_task_grants_one_active'`).
		Scan(&hasOldPerTaskIndex); err != nil || hasOldPerTaskIndex != 0 {
		t.Fatalf("obsolete per-task index=%d err=%v", hasOldPerTaskIndex, err)
	}
	now := time.Now().UTC()
	profile := domain.BrowserProfile{
		ID: domain.NewID("browser_profile"), Name: "Work", BrowserKind: domain.BrowserKindEdge,
		HostID: "local", State: domain.BrowserProfileStateIdle, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertBrowserProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	stored, err := database.BrowserProfile(ctx, profile.ID)
	if err != nil || stored.Name != "Work" || stored.BrowserKind != domain.BrowserKindEdge {
		t.Fatalf("profile=%#v err=%v", stored, err)
	}
	first := domain.BrowserSession{
		ID: domain.NewID("browser_session"), ProfileID: profile.ID, HostID: "local",
		State: domain.BrowserSessionStateReady, StartedAt: now, CreatedAt: now,
	}
	if err := database.CreateBrowserSession(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID = domain.NewID("browser_session")
	if err := database.CreateBrowserSession(ctx, second); err == nil {
		t.Fatal("a second active session for the same profile was accepted")
	}
	first.State = domain.BrowserSessionStateClosed
	first.ClosedAt = now
	if err := database.UpdateBrowserSession(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateBrowserSession(ctx, second); err != nil {
		t.Fatalf("new session after closing the old one: %v", err)
	}
	active, err := database.ActiveBrowserSession(ctx, profile.ID)
	if err != nil || active.ID != second.ID {
		t.Fatalf("active session=%#v err=%v", active, err)
	}
}

func TestBrowserGrantsTabsAndCascade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	profile := domain.BrowserProfile{
		ID: domain.NewID("browser_profile"), Name: "Work", BrowserKind: domain.BrowserKindEdge,
		HostID: "local", State: domain.BrowserProfileStateReady, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertBrowserProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	session := domain.BrowserSession{
		ID: domain.NewID("browser_session"), ProfileID: profile.ID, HostID: "local",
		State: domain.BrowserSessionStateReady, StartedAt: now, CreatedAt: now,
	}
	if err := database.CreateBrowserSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	grant := domain.BrowserTaskGrant{
		ID: domain.NewID("browser_grant"), TaskID: task.ID, ProfileID: profile.ID,
		SessionID: session.ID, Capabilities: []string{domain.BrowserCapabilityOpenTab, domain.BrowserCapabilitySnapshot},
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateBrowserTaskGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	duplicate := grant
	duplicate.ID = domain.NewID("browser_grant")
	if err := database.CreateBrowserTaskGrant(ctx, duplicate); err == nil {
		t.Fatal("the unique active-grant index accepted a second active grant")
	}
	// The same Task/Profile acquire is idempotent: the existing lease is
	// returned and nothing is revoked or rebuilt.
	replacement := grant
	replacement.ID = domain.NewID("browser_grant")
	created, revoked, reused, err := database.AcquireBrowserTaskGrantExclusive(ctx, replacement, now)
	if err != nil || !reused || created.ID != grant.ID || len(revoked) != 0 {
		t.Fatalf("idempotent acquire: created=%#v revoked=%#v reused=%v err=%v", created, revoked, reused, err)
	}
	grant = created
	loaded, err := database.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || len(loaded.Capabilities) != 2 || !loaded.Has(domain.BrowserCapabilitySnapshot) {
		t.Fatalf("grant=%#v err=%v", loaded, err)
	}
	activeGrants, err := database.BrowserTaskGrantsForProfileActive(ctx, profile.ID, now)
	if err != nil || len(activeGrants) != 1 {
		t.Fatalf("active grants=%#v err=%v", activeGrants, err)
	}
	tab := domain.BrowserTab{
		ID: domain.NewID("browser_tab"), SessionID: session.ID, GrantID: grant.ID, TaskID: task.ID,
		HostTabID: "host-tab-1", URL: "https://example.com", Title: "Example", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateBrowserTab(ctx, tab); err != nil {
		t.Fatal(err)
	}
	tabs, err := database.BrowserTabsForTask(ctx, task.ID)
	if err != nil || len(tabs) != 1 || tabs[0].HostTabID != "host-tab-1" {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	if tabs[0].Origin != domain.BrowserTabOriginAgent {
		t.Fatalf("default tab origin=%q", tabs[0].Origin)
	}
	// Deleting the profile cascades sessions, grants and tabs.
	if err := database.DeleteBrowserProfile(ctx, profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.BrowserTaskGrant(ctx, grant.ID); err == nil {
		t.Fatal("grant survived profile deletion")
	}
	if tabs, err := database.BrowserTabsForTask(ctx, task.ID); err != nil || len(tabs) != 0 {
		t.Fatalf("tabs after cascade=%#v err=%v", tabs, err)
	}
}

func TestBrowserGrantMigrationDedupeUsesRealTimeAndCleansTabs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	profile := domain.BrowserProfile{
		ID: domain.NewID("browser_profile"), Name: "Work", BrowserKind: domain.BrowserKindEdge,
		HostID: "local", State: domain.BrowserProfileStateReady, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertBrowserProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	session := domain.BrowserSession{
		ID: domain.NewID("browser_session"), ProfileID: profile.ID, HostID: "local",
		State: domain.BrowserSessionStateReady, StartedAt: now, CreatedAt: now,
	}
	if err := database.CreateBrowserSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_browser_task_grants_one_active`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_browser_task_grants_profile_active`); err != nil {
		t.Fatal(err)
	}
	// Lexical order of these RFC3339Nano strings disagrees with real time:
	// ".12345678Z" sorts after ".123456789Z" even though it is earlier.
	older := "2026-11-17T06:00:00.12345678Z"
	newer := "2026-11-17T06:00:00.123456789Z"
	insert := func(id, created string) {
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO browser_task_grants(id,task_id,profile_id,session_id,capabilities_json,
				expires_at,revoked_at,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			id, task.ID, profile.ID, session.ID, `["snapshot"]`,
			"2027-11-17T06:00:00Z", "", created, created); err != nil {
			t.Fatal(err)
		}
	}
	oldID := domain.NewID("browser_grant")
	newID := domain.NewID("browser_grant")
	insert(oldID, older)
	insert(newID, newer)
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO browser_tabs(id,session_id,grant_id,task_id,host_tab_id,url,title,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		domain.NewID("browser_tab"), session.ID, oldID, task.ID, "host-tab-old",
		"https://example.com", "Old", older, older); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version IN (148,151)`); err != nil {
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
	oldGrant, err := database.BrowserTaskGrant(ctx, oldID)
	if err != nil || oldGrant.RevokedAt.IsZero() {
		t.Fatalf("older grant should be revoked: %#v err=%v", oldGrant, err)
	}
	newGrant, err := database.BrowserTaskGrant(ctx, newID)
	if err != nil || !newGrant.RevokedAt.IsZero() {
		t.Fatalf("newer grant should remain active: %#v err=%v", newGrant, err)
	}
	tabs, err := database.BrowserTabsForTask(ctx, task.ID)
	if err != nil || len(tabs) != 0 {
		t.Fatalf("revoked grant tabs should be removed: %#v err=%v", tabs, err)
	}
}

func TestBrowserTabOriginMigrationKeepsExistingTabsAsAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	profile := domain.BrowserProfile{
		ID: domain.NewID("browser_profile"), Name: "Work", BrowserKind: domain.BrowserKindEdge,
		HostID: "local", State: domain.BrowserProfileStateReady, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertBrowserProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	session := domain.BrowserSession{
		ID: domain.NewID("browser_session"), ProfileID: profile.ID, HostID: "local",
		State: domain.BrowserSessionStateReady, StartedAt: now, CreatedAt: now,
	}
	if err := database.CreateBrowserSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	grant := domain.BrowserTaskGrant{
		ID: domain.NewID("browser_grant"), TaskID: task.ID, ProfileID: profile.ID,
		SessionID: session.ID, Capabilities: []string{domain.BrowserCapabilitySnapshot},
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateBrowserTaskGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	tab := domain.BrowserTab{
		ID: domain.NewID("browser_tab"), SessionID: session.ID, GrantID: grant.ID, TaskID: task.ID,
		HostTabID: "host-tab-legacy", URL: "https://example.com/legacy", Title: "Legacy",
		Origin: domain.BrowserTabOriginAgent, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateBrowserTab(ctx, tab); err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-v149 shape: the column and version row are absent, but
	// the tab row predates the upgrade.
	if _, err := database.db.ExecContext(ctx, `ALTER TABLE browser_tabs DROP COLUMN origin`); err != nil {
		t.Fatalf("drop origin for the upgrade fixture: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=149`); err != nil {
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
	stored, err := database.BrowserTab(ctx, tab.ID)
	if err != nil || stored.Origin != domain.BrowserTabOriginAgent || stored.URL != "https://example.com/legacy" {
		t.Fatalf("upgraded tab=%#v err=%v", stored, err)
	}
}

func TestBrowserTabHostUniqueMigrationDeduplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	profile := domain.BrowserProfile{
		ID: domain.NewID("browser_profile"), Name: "Work", BrowserKind: domain.BrowserKindEdge,
		HostID: "local", State: domain.BrowserProfileStateReady, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertBrowserProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	session := domain.BrowserSession{
		ID: domain.NewID("browser_session"), ProfileID: profile.ID, HostID: "local",
		State: domain.BrowserSessionStateReady, StartedAt: now, CreatedAt: now,
	}
	if err := database.CreateBrowserSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	grant := domain.BrowserTaskGrant{
		ID: domain.NewID("browser_grant"), TaskID: task.ID, ProfileID: profile.ID,
		SessionID: session.ID, Capabilities: []string{domain.BrowserCapabilitySnapshot},
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateBrowserTaskGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-v150 shape and two rows that share a host page.
	if _, err := database.db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_browser_tabs_session_host`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=150`); err != nil {
		t.Fatal(err)
	}
	insert := func(id string) {
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO browser_tabs(id,session_id,grant_id,task_id,host_tab_id,url,title,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			id, session.ID, grant.ID, task.ID, "host-tab-shared",
			"https://example.com", "Shared", timeString(now), timeString(now)); err != nil {
			t.Fatal(err)
		}
	}
	firstID := domain.NewID("browser_tab")
	insert(firstID)
	insert(domain.NewID("browser_tab"))
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM browser_tabs WHERE session_id=? AND host_tab_id='host-tab-shared'`, session.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate host tabs survived migration: %d", count)
	}
	stored, err := database.BrowserTab(ctx, firstID)
	if err != nil || stored.HostTabID != "host-tab-shared" {
		t.Fatalf("oldest row should survive: %#v err=%v", stored, err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO browser_tabs(id,session_id,grant_id,task_id,host_tab_id,url,title,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		domain.NewID("browser_tab"), session.ID, grant.ID, task.ID, "host-tab-shared",
		"https://example.com/again", "Again", timeString(now), timeString(now)); err == nil {
		t.Fatal("unique index accepted a second row for the same host tab")
	}
}

func TestBrowserProfileExclusiveMigrationDeduplicatesGrants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	taskA := createStoreTestTask(t, database)
	taskB := createStoreTestTask(t, database)
	now := time.Now().UTC()
	profile := domain.BrowserProfile{
		ID: domain.NewID("browser_profile"), Name: "Work", BrowserKind: domain.BrowserKindEdge,
		HostID: "local", State: domain.BrowserProfileStateReady, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertBrowserProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	session := domain.BrowserSession{
		ID: domain.NewID("browser_session"), ProfileID: profile.ID, HostID: "local",
		State: domain.BrowserSessionStateReady, StartedAt: now, CreatedAt: now,
	}
	if err := database.CreateBrowserSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_browser_task_grants_profile_active`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=151`); err != nil {
		t.Fatal(err)
	}
	insert := func(id, taskID, created string) {
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO browser_task_grants(id,task_id,profile_id,session_id,capabilities_json,
				expires_at,revoked_at,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			id, taskID, profile.ID, session.ID, `["snapshot"]`,
			"2027-11-17T06:00:00Z", "", created, created); err != nil {
			t.Fatal(err)
		}
	}
	oldID := domain.NewID("browser_grant")
	newID := domain.NewID("browser_grant")
	insert(oldID, taskA.ID, "2026-11-17T06:00:00Z")
	insert(newID, taskB.ID, "2026-11-17T07:00:00Z")
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	old, err := database.BrowserTaskGrant(ctx, oldID)
	if err != nil || old.RevokedAt.IsZero() {
		t.Fatalf("older lease should be revoked: %#v err=%v", old, err)
	}
	newer, err := database.BrowserTaskGrant(ctx, newID)
	if err != nil || !newer.RevokedAt.IsZero() {
		t.Fatalf("newer lease should remain active: %#v err=%v", newer, err)
	}
	var hasIndex int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_browser_task_grants_profile_active'`).
		Scan(&hasIndex); err != nil || hasIndex != 1 {
		t.Fatalf("profile lease index=%d err=%v", hasIndex, err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO browser_task_grants(id,task_id,profile_id,session_id,capabilities_json,
			expires_at,revoked_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		domain.NewID("browser_grant"), taskA.ID, profile.ID, session.ID, `["snapshot"]`,
		"2027-11-17T06:00:00Z", "", timeString(now), timeString(now)); err == nil {
		t.Fatal("profile lease index accepted a second active grant")
	}
}

func TestUpsertBrowserTabReassignsLeaseAndKeepsID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	taskA := createStoreTestTask(t, database)
	taskB := createStoreTestTask(t, database)
	now := time.Now().UTC()
	profile := domain.BrowserProfile{
		ID: domain.NewID("browser_profile"), Name: "Work", BrowserKind: domain.BrowserKindEdge,
		HostID: "local", State: domain.BrowserProfileStateReady, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertBrowserProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	session := domain.BrowserSession{
		ID: domain.NewID("browser_session"), ProfileID: profile.ID, HostID: "local",
		State: domain.BrowserSessionStateReady, StartedAt: now, CreatedAt: now,
	}
	if err := database.CreateBrowserSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	grantA := domain.BrowserTaskGrant{
		ID: domain.NewID("browser_grant"), TaskID: taskA.ID, ProfileID: profile.ID,
		SessionID: session.ID, Capabilities: []string{domain.BrowserCapabilitySnapshot},
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateBrowserTaskGrant(ctx, grantA); err != nil {
		t.Fatal(err)
	}
	tab := domain.BrowserTab{
		ID: domain.NewID("browser_tab"), SessionID: session.ID, GrantID: grantA.ID, TaskID: taskA.ID,
		HostTabID: "host-page-1", URL: "https://example.com/one", Title: "One",
		Origin: domain.BrowserTabOriginOwner, CreatedAt: now, UpdatedAt: now,
	}
	stored, err := database.UpsertBrowserTabForActiveGrant(ctx, tab, now)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != tab.ID || stored.Origin != domain.BrowserTabOriginOwner {
		t.Fatalf("first upsert=%#v", stored)
	}
	if err := database.RevokeBrowserTaskGrant(ctx, grantA.ID, now); err != nil {
		t.Fatal(err)
	}
	grantB := domain.BrowserTaskGrant{
		ID: domain.NewID("browser_grant"), TaskID: taskB.ID, ProfileID: profile.ID,
		SessionID: session.ID, Capabilities: []string{domain.BrowserCapabilitySnapshot},
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateBrowserTaskGrant(ctx, grantB); err != nil {
		t.Fatal(err)
	}
	next := domain.BrowserTab{
		ID: domain.NewID("browser_tab"), SessionID: session.ID, GrantID: grantB.ID, TaskID: taskB.ID,
		HostTabID: "host-page-1", URL: "https://example.com/two", Title: "Two",
		Origin: domain.BrowserTabOriginOwner, CreatedAt: now, UpdatedAt: now,
	}
	stored, err = database.UpsertBrowserTabForActiveGrant(ctx, next, now)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != tab.ID || stored.TaskID != taskB.ID || stored.GrantID != grantB.ID || stored.URL != "https://example.com/two" {
		t.Fatalf("reassigned tab=%#v", stored)
	}
	if count, err := database.BrowserTabsForSession(ctx, session.ID); err != nil || len(count) != 1 {
		t.Fatalf("session tabs=%#v err=%v", count, err)
	}
}

func TestBrowserGrantHoldCASRejectsExpiredRowAndOwnerRenewal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	profile := domain.BrowserProfile{
		ID: domain.NewID("browser_profile"), Name: "Work", BrowserKind: domain.BrowserKindEdge,
		HostID: "local", State: domain.BrowserProfileStateReady, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertBrowserProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	session := domain.BrowserSession{
		ID: domain.NewID("browser_session"), ProfileID: profile.ID, HostID: "local",
		State: domain.BrowserSessionStateReady, StartedAt: now, CreatedAt: now,
	}
	if err := database.CreateBrowserSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	grant := domain.BrowserTaskGrant{
		ID: domain.NewID("browser_grant"), TaskID: task.ID, ProfileID: profile.ID, SessionID: session.ID,
		Capabilities: []string{domain.BrowserCapabilityOpenTab}, ExpiresAt: now.Add(time.Minute),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateBrowserTaskGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if _, err := database.BeginBrowserTaskGrantHold(ctx, grant.ID, grant.ExpiresAt, "hold-1", now.Add(5*time.Minute), now.Add(2*time.Minute)); !errors.Is(err, ErrBrowserGrantHoldActive) {
		t.Fatalf("expired grant hold error=%v, want ErrBrowserGrantHoldActive", err)
	}
	unchanged, err := database.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || unchanged.HoldID != "" || !unchanged.ExpiresAt.Equal(grant.ExpiresAt) {
		t.Fatalf("expired hold changed row: %#v err=%v", unchanged, err)
	}

	holdUntil := now.Add(5 * time.Minute)
	if _, err := database.BeginBrowserTaskGrantHold(ctx, grant.ID, grant.ExpiresAt, "hold-1", holdUntil, now); err != nil {
		t.Fatal(err)
	}
	held, err := database.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || held.HoldID != "hold-1" || !held.ExpiresAt.Equal(holdUntil) {
		t.Fatalf("held grant=%#v err=%v", held, err)
	}
	// The Owner renews to the same numeric deadline on a fixed clock. Owner
	// renewal clears the hold identity so the old restore must not win.
	if _, err := database.RefreshBrowserTaskGrantLease(ctx, grant.ID, grant.Capabilities, holdUntil, now); err != nil {
		t.Fatal(err)
	}
	released, err := database.ReleaseBrowserTaskGrantHold(ctx, grant.ID, "hold-1", now)
	if err != nil || released {
		t.Fatalf("old hold release=%v err=%v, want superseded", released, err)
	}
	renewed, err := database.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || !renewed.ExpiresAt.Equal(holdUntil) || renewed.HoldID != "" {
		t.Fatalf("owner renewal was overwritten: %#v err=%v", renewed, err)
	}
}
