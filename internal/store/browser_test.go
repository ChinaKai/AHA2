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
