package browser_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/browser/browsertest"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type fixture struct {
	store   *store.Store
	host    *browsertest.FakeHost
	service *browser.Service
	task    domain.Task
	now     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	host := browsertest.New()
	service := browser.NewService(database, host, "local")
	now := time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC)
	service.SetNowForTest(func() time.Time { return now })
	return &fixture{store: database, host: host, service: service, task: createTask(t, database), now: now}
}

func (f *fixture) setNow(value time.Time) {
	f.now = value
	// The clock is captured by value above; reinstall it so tests can move time.
	f.service.SetNowForTest(func() time.Time { return value })
}

func createTask(t *testing.T, database *store.Store) domain.Task {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	project := domain.Project{ID: domain.NewID("project"), Name: "Browser", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: project.ID, Name: "Local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	env := domain.EnvGroup{
		ID: domain.NewID("env"), Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: domain.NewID("model"), DisplayName: "Stub", ProviderID: "stub", Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: domain.NewID("runtime"), WorkspaceID: workspace.ID, Backend: "stub",
		ModelID: model.ID, WireModel: "stub", EnvGroupID: env.ID, EnvGroupRevision: 1, CreatedAt: now,
	}
	task := domain.Task{
		ID: domain.NewID("task"), ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Browser", OriginalRequest: "use browser", CurrentGoal: "use browser",
		Status: domain.TaskActive, RuntimeConfigSnapshotID: snapshot.ID,
		CollaborationMode: "single", MaxAgents: 1, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, env) },
		func() error { return database.UpsertModel(ctx, model) },
		func() error {
			_, err := database.CreateTaskWithSnapshot(ctx, snapshot, task)
			return err
		},
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func createProfile(t *testing.T, f *fixture) browser.ProfileView {
	t.Helper()
	profile, err := f.service.CreateProfile(context.Background(), "owner", "Work", "")
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", code)
	}
	if got := browser.Code(err); got != code {
		t.Fatalf("expected error %q, got %q (%v)", code, got, err)
	}
}

func TestProfileLifecycleAndDeleteGuards(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	if profile.Name != "Work" || profile.BrowserKind != domain.BrowserKindEdge || profile.HostID != "local" {
		t.Fatalf("profile=%#v", profile)
	}
	profiles, err := f.service.Profiles(ctx)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles=%#v err=%v", profiles, err)
	}
	renamed, err := f.service.RenameProfile(ctx, "owner", profile.ID, "Work 2")
	if err != nil || renamed.Name != "Work 2" {
		t.Fatalf("rename=%#v err=%v", renamed, err)
	}
	if _, _, err := f.service.StartSession(ctx, "owner", profile.ID); err != nil {
		t.Fatal(err)
	}
	requireCode(t, f.service.DeleteProfile(ctx, "owner", profile.ID), "browser_profile_in_use")
	session, err := f.service.CloseProfileSession(ctx, "owner", profile.ID)
	if err != nil || session == nil || session.State != domain.BrowserSessionStateClosed {
		t.Fatalf("close=%#v err=%v", session, err)
	}
	if err := f.service.DeleteProfile(ctx, "owner", profile.ID); err != nil {
		t.Fatalf("delete after close: %v", err)
	}
	requireCode(t, f.service.DeleteProfile(ctx, "owner", profile.ID), "browser_profile_not_found")
	if _, err := f.service.CreateProfile(ctx, "owner", "Other", "chrome"); browser.Code(err) != "browser_profile_invalid" {
		t.Fatalf("non-Edge profile kind must be rejected, err=%v", err)
	}
}

func TestSingleSessionIsReusedThenReplaced(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	first, reused, err := f.service.StartSession(ctx, "owner", profile.ID)
	if err != nil || reused {
		t.Fatalf("first start=%#v reused=%v err=%v", first, reused, err)
	}
	second, reused, err := f.service.StartSession(ctx, "owner", profile.ID)
	if err != nil || !reused || second.ID != first.ID {
		t.Fatalf("second start=%#v reused=%v err=%v", second, reused, err)
	}
	if f.host.OpenCalls != 1 {
		t.Fatalf("host Open calls=%d, want 1", f.host.OpenCalls)
	}
	if _, err := f.service.CloseSession(ctx, "owner", first.ID); err != nil {
		t.Fatal(err)
	}
	third, reused, err := f.service.StartSession(ctx, "owner", profile.ID)
	if err != nil || reused || third.ID == first.ID {
		t.Fatalf("third start=%#v reused=%v err=%v", third, reused, err)
	}
	if f.host.OpenCalls != 2 {
		t.Fatalf("host Open calls=%d, want 2", f.host.OpenCalls)
	}
}

func TestHostFailuresAreTypedAndRecoverable(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)

	f.host.Unavailable = true
	_, _, err := f.service.StartSession(ctx, "owner", profile.ID)
	requireCode(t, err, "browser_host_unavailable")
	f.host.Unavailable = false

	f.host.EdgeAvailable = false
	_, _, err = f.service.StartSession(ctx, "owner", profile.ID)
	requireCode(t, err, "browser_edge_unavailable")
	f.host.EdgeAvailable = true

	f.host.LockProfile(profile.ID, true)
	_, _, err = f.service.StartSession(ctx, "owner", profile.ID)
	requireCode(t, err, "browser_profile_locked")
	f.host.LockProfile(profile.ID, false)

	// A failed attempt leaves an error session; the next start reconciles it
	// with the host and creates a fresh instance instead of reusing the error.
	session, _, err := f.service.StartSession(ctx, "owner", profile.ID)
	if err != nil || session.State != domain.BrowserSessionStateReady {
		t.Fatalf("recovery start=%#v err=%v", session, err)
	}
}

func TestGrantExpiryAndRevocationFailClosed(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID,
		[]string{domain.BrowserCapabilityOpenTab, domain.BrowserCapabilityNavigate, domain.BrowserCapabilitySnapshot},
		time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if grant.Status != domain.BrowserGrantStatusActive || grant.Session == nil {
		t.Fatalf("grant=%#v", grant)
	}
	tab, err := f.service.OpenTab(ctx, "owner", grant.ID, "https://example.com/login")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.NavigateTab(ctx, "owner", grant.ID, tab.ID, "https://example.com/home"); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := f.service.SnapshotTab(ctx, "owner", grant.ID, tab.ID); err != nil || snapshot.Text == "" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}

	f.setNow(f.now.Add(2 * time.Minute))
	_, err = f.service.NavigateTab(ctx, "owner", grant.ID, tab.ID, "https://example.com/late")
	requireCode(t, err, "browser_grant_expired")
	views, err := f.service.Grants(ctx, f.task.ID)
	if err != nil || len(views) != 1 || views[0].Status != domain.BrowserGrantStatusExpired {
		t.Fatalf("expired grant view=%#v err=%v", views, err)
	}

	f.setNow(f.now.Add(-2 * time.Minute))
	fresh, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := f.service.RevokeGrant(ctx, "owner", fresh.ID)
	if err != nil || revoked.Status != domain.BrowserGrantStatusRevoked {
		t.Fatalf("revoke=%#v err=%v", revoked, err)
	}
	_, err = f.service.OpenTab(ctx, "owner", fresh.ID, "https://example.com")
	requireCode(t, err, "browser_grant_revoked")
}

func TestCrossTaskTabAccessIsForbidden(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	secondTask := createTask(t, f.store)
	profile := createProfile(t, f)
	grantA, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tabA, err := f.service.OpenTab(ctx, "owner", grantA.ID, "https://example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	grantB, err := f.service.CreateGrant(ctx, "owner", secondTask.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if grantB.Session == nil || grantB.Session.ID != grantA.Session.ID {
		t.Fatal("the second Task should attach to the same profile session")
	}
	_, err = f.service.NavigateTab(ctx, "owner", grantB.ID, tabA.ID, "https://example.com/b")
	requireCode(t, err, "browser_tab_forbidden")
	_, err = f.service.SnapshotTab(ctx, "owner", grantB.ID, tabA.ID)
	requireCode(t, err, "browser_tab_forbidden")
	// Task B can still open and read its own tab.
	tabB, err := f.service.OpenTab(ctx, "owner", grantB.ID, "https://example.com/b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SnapshotTab(ctx, "owner", grantB.ID, tabB.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityAndURLValidation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID,
		[]string{domain.BrowserCapabilitySnapshot}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.OpenTab(ctx, "owner", grant.ID, "https://example.com")
	requireCode(t, err, "browser_capability_denied")
	full, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"javascript:alert(1)", "file:///etc/passwd", "example.com", ""} {
		_, err = f.service.OpenTab(ctx, "owner", full.ID, target)
		if browser.Code(err) != "browser_url_invalid" {
			t.Fatalf("url %q: err=%v", target, err)
		}
	}
}

func TestClosingSessionRevokesGrantsAndHostCloseRefreshes(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tab, err := f.service.OpenTab(ctx, "owner", grant.ID, "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	closed, err := f.service.CloseSession(ctx, "owner", grant.Session.ID)
	if err != nil || closed.State != domain.BrowserSessionStateClosed {
		t.Fatalf("closed=%#v err=%v", closed, err)
	}
	if f.host.CloseCalls != 1 {
		t.Fatalf("host close calls=%d", f.host.CloseCalls)
	}
	updated, err := f.service.Grant(ctx, grant.ID)
	if err != nil || updated.Status != domain.BrowserGrantStatusRevoked {
		t.Fatalf("grant after close=%#v err=%v", updated, err)
	}
	_, err = f.service.NavigateTab(ctx, "owner", grant.ID, tab.ID, "https://example.com/after")
	requireCode(t, err, "browser_grant_revoked")
	if tabs, err := f.service.Tabs(ctx, grant.ID); err != nil || len(tabs) != 0 {
		t.Fatalf("tabs after close=%#v err=%v", tabs, err)
	}

	// Owner closes the visible window: the host reports closed, and the next
	// refresh drops the session and revokes its grants without inventing state.
	profile2 := createProfile(t, f)
	grant2, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile2.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.MarkSessionClosed(profile2.ID)
	session, err := f.service.RefreshSession(ctx, profile2.ID)
	if err != nil || session != nil {
		t.Fatalf("refresh after host close=%#v err=%v", session, err)
	}
	updated2, _ := f.service.Grant(ctx, grant2.ID)
	if updated2.Status != domain.BrowserGrantStatusRevoked {
		t.Fatalf("grant after host close=%#v", updated2)
	}
	storedProfile, err := f.store.BrowserProfile(ctx, profile2.ID)
	if err != nil || storedProfile.State != domain.BrowserProfileStateIdle {
		t.Fatalf("profile after close=%#v err=%v", storedProfile, err)
	}
}

func TestAuditTrailOmitsPageContent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tab, err := f.service.OpenTab(ctx, "owner", grant.ID, "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SnapshotTab(ctx, "owner", grant.ID, tab.ID); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{
		"browser_profile.create", "browser_session.start", "browser_grant.create",
		"browser_tab.open", "browser_tab.snapshot",
	} {
		events, err := f.store.BrowserAuditEvents(ctx, action, 10)
		if err != nil || len(events) == 0 {
			t.Fatalf("audit %s: %#v %v", action, events, err)
		}
	}
	events, err := f.store.BrowserAuditEvents(ctx, "browser_tab.snapshot", 10)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range events[0].Data {
		if text, ok := value.(string); ok && text == "page text" {
			t.Fatalf("audit data %q leaked snapshot content", key)
		}
	}
}

func TestTaskBrowserStateReportsProfilesGrantsAndTabs(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.OpenTab(ctx, "owner", grant.ID, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	state, err := f.service.TaskBrowserState(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Profiles) != 1 || len(state.Grants) != 1 || len(state.Tabs) != 1 || state.Session == nil {
		t.Fatalf("task browser state=%#v", state)
	}
	if !state.HostAvailable || !state.Host.Available {
		t.Fatalf("host view=%#v", state.Host)
	}
}

func TestHostProfileMismatch(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	stored, err := f.store.BrowserProfile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.HostID = "other-host"
	if err := f.store.UpsertBrowserProfile(ctx, stored); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.service.StartSession(ctx, "owner", profile.ID)
	requireCode(t, err, "browser_profile_host_mismatch")
	_, err = f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	requireCode(t, err, "browser_profile_host_mismatch")
}

func TestServiceWithoutHostFailsClosed(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := browser.NewService(database, nil, "local")
	profile, err := service.CreateProfile(ctx, "owner", "Work", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.StartSession(ctx, "owner", profile.ID)
	requireCode(t, err, "browser_host_unavailable")
	var typed *browser.Error
	if !errors.As(err, &typed) || typed.Status != http.StatusServiceUnavailable {
		t.Fatalf("unexpected error type: %#v", err)
	}
}

func TestProfilesRefreshAWindowClosedInTheHost(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.MarkSessionClosed(profile.ID)
	profiles, err := f.service.Profiles(ctx)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles=%#v err=%v", profiles, err)
	}
	if profiles[0].Session != nil || profiles[0].State != domain.BrowserProfileStateIdle {
		t.Fatalf("profile after host close=%#v", profiles[0])
	}
	updated, err := f.service.Grant(ctx, grant.ID)
	if err != nil || updated.Status != domain.BrowserGrantStatusRevoked {
		t.Fatalf("grant after host close=%#v err=%v", updated, err)
	}
}

func TestHostDiagnosticTextIsNeverPersistedOrReturned(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	f.host.FailOpen = &browser.Error{
		Code: "browser_launch_failed", Status: http.StatusInternalServerError,
		Message: `C:\Users\owner\AppData\Local\AHA2\browser-profiles\browser_profile_1`,
	}
	_, _, err := f.service.StartSession(ctx, "owner", profile.ID)
	requireCode(t, err, "browser_launch_failed")
	if strings.Contains(err.Error(), "browser-profiles") || strings.Contains(err.Error(), `C:\`) {
		t.Fatalf("host diagnostic leaked from service error: %v", err)
	}
	session, lookupErr := f.store.ActiveBrowserSession(ctx, profile.ID)
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	if strings.Contains(session.Error, "browser-profiles") || strings.Contains(session.Error, `C:\`) {
		t.Fatalf("host diagnostic persisted: %q", session.Error)
	}
}

func TestOpenTabHostTimeoutLeavesNoTabRecord(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.FailOpenTab = &browser.Error{
		Code: "browser_host_timeout", Status: http.StatusGatewayTimeout,
		Message: "浏览器宿主操作超时",
	}
	_, err = f.service.OpenTab(ctx, "owner", grant.ID, "https://example.com")
	requireCode(t, err, "browser_host_timeout")
	tabs, err := f.store.BrowserTabsForGrant(ctx, grant.ID)
	if err != nil || len(tabs) != 0 {
		t.Fatalf("tabs after host timeout=%#v err=%v", tabs, err)
	}
}

func TestConcurrentGrantCreationKeepsOneActiveGrant(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	const workers = 24
	var wait sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent grant create: %v", err)
		}
	}
	active, err := f.store.BrowserTaskGrantsForProfileActive(ctx, profile.ID, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("active grants=%d, want 1", len(active))
	}
}

func TestConcurrentGrantRevocationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 24
	var wait sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := f.service.RevokeGrant(ctx, "owner", grant.ID)
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent grant revoke: %v", err)
		}
	}
	updated, err := f.service.Grant(ctx, grant.ID)
	if err != nil || updated.Status != domain.BrowserGrantStatusRevoked {
		t.Fatalf("grant after concurrent revoke=%#v err=%v", updated, err)
	}
}

func TestSuccessfulHostSessionPayloadCannotPersistDiagnosticPaths(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	f.host.OpenError = `C:\Users\owner\AppData\Local\AHA2\browser-profiles\browser_profile_1`
	session, _, err := f.service.StartSession(ctx, "owner", profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(session.Error, "browser-profiles") || strings.Contains(session.Error, `C:\`) {
		t.Fatalf("session view leaked: %q", session.Error)
	}
	stored, err := f.store.ActiveBrowserSession(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.Error, "browser-profiles") || strings.Contains(stored.Error, `C:\`) {
		t.Fatalf("persisted session leaked: %q", stored.Error)
	}
}

func blockNextOpenTab(f *fixture) (started <-chan struct{}, release chan<- struct{}) {
	startedCh := make(chan struct{}, 1)
	releaseCh := make(chan struct{})
	f.host.OpenTabStarted = startedCh
	f.host.OpenTabRelease = releaseCh
	return startedCh, releaseCh
}

func TestOpenTabInFlightCloseDoesNotResurrectSessionOrInsertTab(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	started, release := blockNextOpenTab(f)
	result := make(chan error, 1)
	go func() {
		_, openErr := f.service.OpenTab(ctx, "owner", grant.ID, "https://example.com")
		result <- openErr
	}()
	<-started
	if _, err := f.service.CloseSession(ctx, "owner", grant.Session.ID); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	err = <-result
	if err == nil {
		t.Fatal("open tab succeeded after the session was closed")
	}
	if code := browser.Code(err); code != "browser_grant_revoked" && code != "browser_session_closed" {
		t.Fatalf("unexpected error after close: %v", err)
	}
	session, err := f.store.BrowserSession(ctx, grant.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != domain.BrowserSessionStateClosed || session.ClosedAt.IsZero() {
		t.Fatalf("session state was overwritten: %#v", session)
	}
	tabs, err := f.store.BrowserTabsForGrant(ctx, grant.ID)
	if err != nil || len(tabs) != 0 {
		t.Fatalf("tabs after in-flight close=%#v err=%v", tabs, err)
	}
}

func TestOpenTabInFlightRevokeDoesNotInsertTab(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	started, release := blockNextOpenTab(f)
	result := make(chan error, 1)
	go func() {
		_, openErr := f.service.OpenTab(ctx, "owner", grant.ID, "https://example.com")
		result <- openErr
	}()
	<-started
	if _, err := f.service.RevokeGrant(ctx, "owner", grant.ID); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	err = <-result
	requireCode(t, err, "browser_grant_revoked")
	tabs, err := f.store.BrowserTabsForGrant(ctx, grant.ID)
	if err != nil || len(tabs) != 0 {
		t.Fatalf("tabs after in-flight revoke=%#v err=%v", tabs, err)
	}
}
