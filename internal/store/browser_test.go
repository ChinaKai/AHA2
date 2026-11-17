package store

import (
	"context"
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
	// The superseding transaction is the supported path: it revokes the old
	// grant and inserts the new one atomically.
	replacement := grant
	replacement.ID = domain.NewID("browser_grant")
	revoked, err := database.CreateBrowserTaskGrantSuperseding(ctx, replacement, now)
	if err != nil || len(revoked) != 1 || revoked[0].ID != grant.ID {
		t.Fatalf("superseding insert: revoked=%#v err=%v", revoked, err)
	}
	grant = replacement
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
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=148`); err != nil {
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
