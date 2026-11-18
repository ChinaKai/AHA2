package browser_test

import (
	"context"
	"errors"
	"fmt"
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
	host.Now = func() time.Time { return now }
	return &fixture{store: database, host: host, service: service, task: createTask(t, database), now: now}
}

func (f *fixture) setNow(value time.Time) {
	f.now = value
	// The clock is captured by value above; reinstall it so tests can move time.
	f.service.SetNowForTest(func() time.Time { return value })
	f.host.Now = func() time.Time { return value }
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

func TestTabFrameFollowsGrantAndTabOwnership(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID,
		[]string{domain.BrowserCapabilityOpenTab, domain.BrowserCapabilitySnapshot}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tab, err := f.service.OpenTab(ctx, "owner", grant.ID, "https://example.com/login")
	if err != nil {
		t.Fatal(err)
	}
	frame, err := f.service.TabFrame(ctx, "owner", grant.ID, tab.ID)
	if err != nil || frame.ContentType != "image/jpeg" || len(frame.Data) == 0 {
		t.Fatalf("frame=%#v err=%v", frame, err)
	}
	if f.host.FrameCalls != 1 {
		t.Fatalf("host frame calls=%d, want 1", f.host.FrameCalls)
	}
	// Preview reads must not be audited per frame.
	events, err := f.store.BrowserAuditEvents(ctx, "browser_tab.frame", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("frame polling wrote %d audit rows", len(events))
	}
	if _, err := f.service.RevokeGrant(ctx, "owner", grant.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.TabFrame(ctx, "owner", grant.ID, tab.ID)
	requireCode(t, err, "browser_grant_revoked")
	if f.host.FrameCalls != 1 {
		t.Fatalf("host was called after revocation: %d", f.host.FrameCalls)
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
	if tabs, err := f.store.BrowserTabsForGrant(ctx, grant.ID); err != nil || len(tabs) != 0 {
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
	all, err := f.store.BrowserTaskGrants(ctx, f.task.ID)
	if err != nil || len(all) != 1 {
		t.Fatalf("idempotent acquire created %d grant rows: %v", len(all), err)
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
	if code := browser.Code(err); code != "browser_grant_revoked" && code != "browser_fence_stale" {
		t.Fatalf("in-flight open after revoke: %v", err)
	}
	tabs, err := f.store.BrowserTabsForGrant(ctx, grant.ID)
	if err != nil || len(tabs) != 0 {
		t.Fatalf("tabs after in-flight revoke=%#v err=%v", tabs, err)
	}
}

func TestProfileExclusiveLeaseConflictsThenReleases(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	otherTask := createTask(t, f.store)
	grantA, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.CreateGrant(ctx, "owner", otherTask.ID, profile.ID, nil, time.Hour)
	requireCode(t, err, "browser_profile_in_use")
	state, err := f.service.TaskBrowserState(ctx, otherTask.ID)
	if err != nil || len(state.Profiles) != 1 || state.Profiles[0].LeaseTaskID != f.task.ID || state.Profiles[0].LeaseTaskTitle == "" {
		t.Fatalf("owner-facing lease view=%#v err=%v", state, err)
	}
	active, err := f.store.BrowserTaskGrantsForProfileActive(ctx, profile.ID, f.now)
	if err != nil || len(active) != 1 || active[0].ID != grantA.ID {
		t.Fatalf("active lease after conflict=%#v err=%v", active, err)
	}
	if _, err := f.service.RevokeGrant(ctx, "owner", grantA.ID); err != nil {
		t.Fatal(err)
	}
	grantB, err := f.service.CreateGrant(ctx, "owner", otherTask.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if grantB.TaskID != otherTask.ID {
		t.Fatalf("lease owner=%q", grantB.TaskID)
	}
	if events, err := f.store.BrowserAuditEvents(ctx, "browser_grant.release", 10); err != nil || len(events) == 0 {
		t.Fatalf("release audit=%#v err=%v", events, err)
	}
}

func TestConcurrentDifferentTasksOnlyOneLeaseWins(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	tasks := make([]domain.Task, 0, 12)
	for index := 0; index < 12; index++ {
		tasks = append(tasks, createTask(t, f.store))
	}
	var wait sync.WaitGroup
	results := make(chan error, len(tasks))
	for _, task := range tasks {
		wait.Add(1)
		go func(task domain.Task) {
			defer wait.Done()
			_, err := f.service.CreateGrant(ctx, "owner", task.ID, profile.ID, nil, time.Hour)
			results <- err
		}(task)
	}
	wait.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
			continue
		}
		if browser.Code(err) != "browser_profile_in_use" {
			t.Fatalf("unexpected concurrent acquire error: %v", err)
		}
		conflicts++
	}
	if winners != 1 || conflicts != len(tasks)-1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
	active, err := f.store.BrowserTaskGrantsForProfileActive(ctx, profile.ID, f.now)
	if err != nil || len(active) != 1 {
		t.Fatalf("active leases=%#v err=%v", active, err)
	}
}

func TestExclusiveTaskControlsOwnerOpenedTabs(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-1", URL: "https://owner.example/one", Title: "One", Active: true},
		{ID: "host-page-2", URL: "https://owner.example/two", Title: "Two"},
	})
	tabs, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 2 {
		t.Fatalf("tabs=%#v", tabs)
	}
	byURL := map[string]domain.BrowserTab{}
	for _, tab := range tabs {
		byURL[tab.URL] = tab
		if tab.Origin != domain.BrowserTabOriginOwner || tab.TaskID != f.task.ID {
			t.Fatalf("owner tab=%#v", tab)
		}
	}
	// Activate the second owner-opened page and operate on both without any
	// per-tab grant binding.
	activated, err := f.service.ActivateTab(ctx, "owner", grant.ID, byURL["https://owner.example/two"].ID)
	if err != nil || !activated.Active {
		t.Fatalf("activate=%#v err=%v", activated, err)
	}
	if f.host.ActivateCalls != 1 {
		t.Fatalf("activate calls=%d", f.host.ActivateCalls)
	}
	if _, err := f.service.NavigateTab(ctx, "owner", grant.ID, byURL["https://owner.example/one"].ID, "https://owner.example/next"); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := f.service.SnapshotTab(ctx, "owner", grant.ID, byURL["https://owner.example/two"].ID); err != nil || snapshot.URL != "https://owner.example/two" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	if err := f.service.CloseTab(ctx, "owner", grant.ID, byURL["https://owner.example/one"].ID); err != nil {
		t.Fatal(err)
	}
	if f.host.CloseTabCalls != 1 {
		t.Fatalf("owner tab was not closed by the exclusive holder: %d", f.host.CloseTabCalls)
	}
	remaining, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil || len(remaining) != 1 || remaining[0].URL != "https://owner.example/two" {
		t.Fatalf("tabs after close=%#v err=%v", remaining, err)
	}
	for _, action := range []string{"browser_tab.activate", "browser_tab.navigate", "browser_tab.close"} {
		if events, err := f.store.BrowserAuditEvents(ctx, action, 10); err != nil || len(events) == 0 {
			t.Fatalf("audit %s=%#v err=%v", action, events, err)
		}
	}
}

func TestProfileExclusiveTabOperationsRejectAnotherProfileSession(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profileA := createProfile(t, f)
	profileB, err := f.service.CreateProfile(ctx, "owner", "Second", "")
	if err != nil {
		t.Fatal(err)
	}
	grantA, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profileA.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tabA, err := f.service.OpenTab(ctx, "owner", grantA.ID, "https://a.example/page")
	if err != nil {
		t.Fatal(err)
	}
	otherTask := createTask(t, f.store)
	grantB, err := f.service.CreateGrant(ctx, "owner", otherTask.ID, profileB.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SnapshotTab(ctx, "owner", grantB.ID, tabA.ID); browser.Code(err) != "browser_tab_forbidden" {
		t.Fatalf("cross-profile snapshot: %v", err)
	}
	if err := f.service.CloseTab(ctx, "owner", grantB.ID, tabA.ID); browser.Code(err) != "browser_tab_forbidden" {
		t.Fatalf("cross-profile close: %v", err)
	}
	if _, err := f.store.BrowserTab(ctx, tabA.ID); err != nil {
		t.Fatalf("profile A tab changed: %v", err)
	}
}

func TestSameTaskMustReleaseBeforeAnotherProfile(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profileA := createProfile(t, f)
	profileB, err := f.service.CreateProfile(ctx, "owner", "Second", "")
	if err != nil {
		t.Fatal(err)
	}
	grantA, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profileA.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.CreateGrant(ctx, "owner", f.task.ID, profileB.ID, nil, time.Hour)
	requireCode(t, err, "browser_task_profile_limit")
	still, err := f.service.Grant(ctx, grantA.ID)
	if err != nil || still.Status != domain.BrowserGrantStatusActive {
		t.Fatalf("old lease after limit: %#v err=%v", still, err)
	}
	if _, err := f.service.RevokeGrant(ctx, "owner", grantA.ID); err != nil {
		t.Fatal(err)
	}
	grantB, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profileB.ID, nil, time.Hour)
	if err != nil || grantB.TaskID != f.task.ID {
		t.Fatalf("acquire after release: %#v err=%v", grantB, err)
	}
}

func TestReleaseKeepsTabsForTheNextTask(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grantA, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	tabs, err := f.service.SyncTabs(ctx, grantA.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	originalID := tabs[0].ID
	if _, err := f.service.RevokeGrant(ctx, "owner", grantA.ID); err != nil {
		t.Fatal(err)
	}
	if f.host.CloseTabCalls != 0 {
		t.Fatalf("releasing the lease closed visible tabs: %d", f.host.CloseTabCalls)
	}
	if stored, err := f.store.BrowserTab(ctx, originalID); err != nil || stored.HostTabID != "host-page-owner" {
		t.Fatalf("tab row should survive release: %#v err=%v", stored, err)
	}
	_, err = f.service.SnapshotTab(ctx, "owner", grantA.ID, originalID)
	requireCode(t, err, "browser_grant_revoked")

	otherTask := createTask(t, f.store)
	grantB, err := f.service.CreateGrant(ctx, "owner", otherTask.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	next, err := f.service.SyncTabs(ctx, grantB.ID)
	if err != nil || len(next) != 1 || next[0].ID != originalID || next[0].TaskID != otherTask.ID {
		t.Fatalf("next tenant tabs=%#v err=%v", next, err)
	}
	if snapshot, err := f.service.SnapshotTab(ctx, "owner", grantB.ID, originalID); err != nil || snapshot.URL != "https://owner.example/work" {
		t.Fatalf("next tenant snapshot=%#v err=%v", snapshot, err)
	}
}

func TestTransferProfileControlIsOwnerActionAndMovesLease(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	otherTask := createTask(t, f.store)
	grantA, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	synced, err := f.service.SyncTabs(ctx, grantA.ID)
	if err != nil || len(synced) != 1 {
		t.Fatalf("initial tabs=%#v err=%v", synced, err)
	}
	grantB, err := f.service.TransferProfileControl(ctx, "owner", profile.ID, otherTask.ID, nil, time.Hour)
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if grantB.TaskID != otherTask.ID || grantB.Status != domain.BrowserGrantStatusActive {
		t.Fatalf("transferred grant=%#v", grantB)
	}
	if old, err := f.service.Grant(ctx, grantA.ID); err != nil || old.Status != domain.BrowserGrantStatusRevoked {
		t.Fatalf("old grant after transfer=%#v err=%v", old, err)
	}
	if _, err := f.service.SnapshotTab(ctx, "owner", grantA.ID, synced[0].ID); browser.Code(err) != "browser_grant_revoked" {
		t.Fatalf("old tenant still read after transfer: %v", err)
	}
	tabs, err := f.service.SyncTabs(ctx, grantB.ID)
	if err != nil || len(tabs) != 1 || tabs[0].TaskID != otherTask.ID {
		t.Fatalf("transferred tabs=%#v err=%v", tabs, err)
	}
	if events, err := f.store.BrowserAuditEvents(ctx, "browser_grant.transfer", 10); err != nil || len(events) != 1 {
		t.Fatalf("transfer audit=%#v err=%v", events, err)
	}
}

func TestBookmarksReadIsAuditedAndPathFree(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.BookmarkList = []browser.HostBookmark{
		{Title: "Work", URL: "https://work.example/", Folder: "Bookmarks bar"},
		{Title: "Mail", URL: "https://mail.example/", Folder: "Bookmarks bar"},
	}
	bookmarks, err := f.service.Bookmarks(ctx, "owner", grant.ID)
	if err != nil || len(bookmarks) != 2 || f.host.BookmarkCalls != 1 {
		t.Fatalf("bookmarks=%#v err=%v calls=%d", bookmarks, err, f.host.BookmarkCalls)
	}
	events, err := f.store.BrowserAuditEvents(ctx, "browser_bookmark.read", 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("bookmark audit=%#v err=%v", events, err)
	}
	for key, value := range events[0].Data {
		if text, ok := value.(string); ok && strings.Contains(text, "AppData") {
			t.Fatalf("bookmark audit %q leaked a path: %q", key, text)
		}
	}
}

func TestExpiredLeaseCanBeTakenOver(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	otherTask := createTask(t, f.store)
	grantA, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.setNow(f.now.Add(2 * time.Minute))
	grantB, err := f.service.CreateGrant(ctx, "owner", otherTask.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatalf("expired lease takeover: %v", err)
	}
	if grantB.TaskID != otherTask.ID {
		t.Fatalf("new lease task=%q", grantB.TaskID)
	}
	old, err := f.service.Grant(ctx, grantA.ID)
	if err != nil || old.Status != domain.BrowserGrantStatusRevoked {
		t.Fatalf("expired lease should be revoked: %#v err=%v", old, err)
	}
}

func TestSyncTabsKeepsEveryVisiblePage(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pages := make([]browser.HostTab, 0, 61)
	for index := 0; index < 61; index++ {
		pages = append(pages, browser.HostTab{
			ID: fmt.Sprintf("host-page-%d", index), URL: fmt.Sprintf("https://example.com/%d", index),
			Title: fmt.Sprintf("Page %d", index), Active: index == 60,
		})
	}
	f.host.SetCurrentPages(profile.ID, pages)
	tabs, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil || len(tabs) != len(pages) {
		t.Fatalf("tabs=%d err=%v", len(tabs), err)
	}
	activeFound := false
	for _, tab := range tabs {
		if tab.Active && tab.URL == "https://example.com/60" {
			activeFound = true
		}
	}
	if !activeFound {
		t.Fatal("active page was lost")
	}
	stored, err := f.store.BrowserTabsForSession(ctx, grant.Session.ID)
	if err != nil || len(stored) != len(pages) {
		t.Fatalf("stored tabs=%d err=%v", len(stored), err)
	}
}

func TestTransferFencesInFlightClose(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grantA, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	tabs, err := f.service.SyncTabs(ctx, grantA.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	closeStarted := make(chan struct{}, 1)
	closeRelease := make(chan struct{})
	f.host.CloseTabStarted = closeStarted
	f.host.CloseTabRelease = closeRelease
	f.host.CloseTabGateTabID = tabs[0].HostTabID
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- f.service.CloseTab(ctx, "owner", grantA.ID, tabs[0].ID)
	}()
	<-closeStarted

	otherTask := createTask(t, f.store)
	grantB, err := f.service.TransferProfileControl(ctx, "owner", profile.ID, otherTask.ID, nil, time.Hour)
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if _, err := f.service.SyncTabs(ctx, grantB.ID); err != nil {
		t.Fatalf("new tenant sync: %v", err)
	}
	if snapshot, err := f.service.SnapshotTab(ctx, "owner", grantB.ID, tabs[0].ID); err != nil || snapshot.URL != "https://owner.example/work" {
		t.Fatalf("new tenant snapshot while old close is paused=%#v err=%v", snapshot, err)
	}
	close(closeRelease)
	if err := <-closeResult; browser.Code(err) != "browser_fence_stale" {
		t.Fatalf("stale close returned %v", err)
	}
	if f.host.CloseTabCalls != 1 {
		// CloseTabCalls counts the attempted close; the important part is that
		// the tab still exists.
	}
	pages, err := f.host.ListTabs(ctx, profile.ID, grantB.Session.HostSessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, page := range pages {
		if page.ID == "host-page-owner" {
			found = true
		}
	}
	if !found {
		t.Fatal("stale close removed the page after transfer")
	}
	if snapshot, err := f.service.SnapshotTab(ctx, "owner", grantB.ID, tabs[0].ID); err != nil || snapshot.URL != "https://owner.example/work" {
		t.Fatalf("new tenant snapshot=%#v err=%v", snapshot, err)
	}
}

func TestTransferFencesInFlightOpen(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grantA, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	if _, err := f.service.SyncTabs(ctx, grantA.ID); err != nil {
		t.Fatal(err)
	}
	started, release := blockNextOpenTab(f)
	openResult := make(chan error, 1)
	go func() {
		_, openErr := f.service.OpenTab(ctx, "owner", grantA.ID, "https://stale.example/")
		openResult <- openErr
	}()
	<-started

	otherTask := createTask(t, f.store)
	grantB, err := f.service.TransferProfileControl(ctx, "owner", profile.ID, otherTask.ID, nil, time.Hour)
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	release <- struct{}{}
	if err := <-openResult; browser.Code(err) != "browser_fence_stale" {
		t.Fatalf("stale open returned %v", err)
	}
	tabs, err := f.service.SyncTabs(ctx, grantB.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tab := range tabs {
		if tab.URL == "https://stale.example/" {
			t.Fatal("stale open created a page after transfer")
		}
	}
}

func TestReleaseFencesInFlightClose(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	tabs, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	closeStarted := make(chan struct{}, 1)
	closeRelease := make(chan struct{})
	f.host.CloseTabStarted = closeStarted
	f.host.CloseTabRelease = closeRelease
	f.host.CloseTabGateTabID = tabs[0].HostTabID
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- f.service.CloseTab(ctx, "owner", grant.ID, tabs[0].ID)
	}()
	<-closeStarted
	if _, err := f.service.RevokeGrant(ctx, "owner", grant.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	close(closeRelease)
	if err := <-closeResult; browser.Code(err) != "browser_fence_stale" {
		t.Fatalf("stale close after release returned %v", err)
	}
	pages, err := f.host.ListTabs(ctx, profile.ID, grant.Session.HostSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("released page disappeared: %#v", pages)
	}
}

func TestTransferRejectsTargetHoldingAnotherProfile(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profileA := createProfile(t, f)
	profileB, err := f.service.CreateProfile(ctx, "owner", "Second", "")
	if err != nil {
		t.Fatal(err)
	}
	otherTask := createTask(t, f.store)
	grantA, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profileA.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	grantB, err := f.service.CreateGrant(ctx, "owner", otherTask.ID, profileB.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profileA.ID, []browser.HostTab{
		{ID: "host-page-a", URL: "https://a.example/", Title: "A", Active: true},
	})
	if _, err := f.service.SyncTabs(ctx, grantA.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.TransferProfileControl(ctx, "owner", profileA.ID, otherTask.ID, nil, time.Hour)
	requireCode(t, err, "browser_task_profile_limit")
	if tabs, err := f.service.SyncTabs(ctx, grantA.ID); err != nil || len(tabs) != 1 {
		t.Fatalf("profile A fence was not restored: %#v err=%v", tabs, err)
	}
	stillA, err := f.service.Grant(ctx, grantA.ID)
	if err != nil || stillA.Status != domain.BrowserGrantStatusActive {
		t.Fatalf("profile A lease changed: %#v err=%v", stillA, err)
	}
	stillB, err := f.service.Grant(ctx, grantB.ID)
	if err != nil || stillB.Status != domain.BrowserGrantStatusActive {
		t.Fatalf("target profile lease changed: %#v err=%v", stillB, err)
	}
}

func TestInFlightCloseHoldsLeasePastOriginalExpiry(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	tabs, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	closeStarted := make(chan struct{}, 1)
	closeRelease := make(chan struct{})
	f.host.CloseTabStarted = closeStarted
	f.host.CloseTabRelease = closeRelease
	f.host.CloseTabGateTabID = tabs[0].HostTabID
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- f.service.CloseTab(ctx, "owner", grant.ID, tabs[0].ID)
	}()
	<-closeStarted
	f.setNow(f.now.Add(2 * time.Minute))
	// The active operation extended only this lease's expiry. An operation
	// that starts now would be rejected, but the admitted close may finish.
	during, err := f.service.Grant(ctx, grant.ID)
	if err != nil || during.Status != domain.BrowserGrantStatusActive {
		t.Fatalf("hold did not keep the admitted lease active: %#v err=%v", during, err)
	}
	close(closeRelease)
	if err := <-closeResult; err != nil {
		t.Fatalf("held in-flight close returned %v", err)
	}
	pages, err := f.host.ListTabs(ctx, profile.ID, grant.Session.HostSessionID)
	if err != nil || len(pages) != 0 {
		t.Fatalf("held close did not remove page: %#v err=%v", pages, err)
	}
	after, err := f.service.Grant(ctx, grant.ID)
	if err != nil || after.Status != domain.BrowserGrantStatusExpired {
		t.Fatalf("hold was not released after the operation: %#v err=%v", after, err)
	}
	if _, err := f.service.OpenTab(ctx, "owner", grant.ID, "https://late.example/"); browser.Code(err) != "browser_grant_expired" {
		t.Fatalf("operation after hold returned %v", err)
	}
	if pages, listErr := f.host.ListTabs(ctx, profile.ID, grant.Session.HostSessionID); listErr != nil || len(pages) != 0 {
		t.Fatalf("host was called after the lease expired: %#v err=%v", pages, listErr)
	}
}

func TestHoldRenewalFailureRejectsBeforeHostSideEffect(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	tabs, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	f.service.SetHoldForTest(func() error {
		return &browser.Error{Code: "browser_lease_hold_failed", Message: "hold unavailable", Status: http.StatusConflict}
	})
	if err := f.service.CloseTab(ctx, "owner", grant.ID, tabs[0].ID); browser.Code(err) != "browser_lease_hold_failed" {
		t.Fatalf("close without a hold returned %v", err)
	}
	if f.host.CloseTabCalls != 0 {
		t.Fatalf("host close was called without a hold: %d", f.host.CloseTabCalls)
	}
	if pages, listErr := f.host.ListTabs(ctx, profile.ID, grant.Session.HostSessionID); listErr != nil || len(pages) != 1 {
		t.Fatalf("page changed before the hold: %#v err=%v", pages, listErr)
	}
}

func TestConcurrentOperationsShareStableLeaseFence(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	tabs, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	closeStarted := make(chan struct{}, 1)
	closeRelease := make(chan struct{})
	f.host.CloseTabStarted = closeStarted
	f.host.CloseTabRelease = closeRelease
	f.host.CloseTabGateTabID = tabs[0].HostTabID
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- f.service.CloseTab(ctx, "owner", grant.ID, tabs[0].ID)
	}()
	<-closeStarted
	// A same-lease request joins the active hold. Both calls must use the
	// stable lease fence even though the temporary deadline is present.
	opened, err := f.service.OpenTab(ctx, "owner", grant.ID, "https://concurrent.example/")
	if err != nil {
		t.Fatalf("concurrent open under hold: %v", err)
	}
	if opened.URL != "https://concurrent.example/" {
		t.Fatalf("opened=%#v", opened)
	}
	close(closeRelease)
	if err := <-closeResult; err != nil {
		t.Fatalf("close under concurrent open: %v", err)
	}
	if got := f.host.Fences[profile.ID]; got != grant.ID {
		t.Fatalf("fence identity changed during hold: %q, want %q", got, grant.ID)
	}
	if len(f.host.FenceCalls) < 2 {
		t.Fatalf("fence calls=%v, want close and open", f.host.FenceCalls)
	}
	closeFence := f.host.FenceCalls[0]
	openFence := f.host.FenceCalls[1]
	if closeFence != openFence {
		t.Fatalf("concurrent operations used different tokens: %q vs %q", closeFence, openFence)
	}
	if !strings.HasPrefix(closeFence, grant.ID+"|") {
		t.Fatalf("operation token %q does not carry the stable lease id", closeFence)
	}
}

func TestHoldRestoreFailureIsBoundedAndDoesNotRoll(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	tabs, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	closeStarted := make(chan struct{}, 1)
	closeRelease := make(chan struct{})
	f.host.CloseTabStarted = closeStarted
	f.host.CloseTabRelease = closeRelease
	f.host.CloseTabGateTabID = tabs[0].HostTabID
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- f.service.CloseTab(ctx, "owner", grant.ID, tabs[0].ID)
	}()
	<-closeStarted
	original, err := f.store.BrowserTaskGrant(ctx, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.service.SetReleaseHoldForTest(func() error { return errors.New("injected restore failure") })
	close(closeRelease)
	if err := <-closeResult; browser.Code(err) != "browser_lease_hold_release_failed" {
		t.Fatalf("restore failure was not surfaced: %v", err)
	}
	held, err := f.store.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || held.HoldID == "" {
		t.Fatalf("hold was not left for bounded recovery: %#v err=%v", held, err)
	}
	fixedDeadline := held.ExpiresAt
	if !fixedDeadline.Equal(f.now.Add(5 * time.Minute)) {
		t.Fatalf("hold deadline=%s, want %s", fixedDeadline, f.now.Add(5*time.Minute))
	}

	f.setNow(fixedDeadline.Add(-time.Minute))
	if _, err := f.service.OpenTab(ctx, "owner", grant.ID, "https://after-failure.example/"); browser.Code(err) != "browser_lease_hold_failed" {
		t.Fatalf("operation under unreleased hold returned %v", err)
	}
	after, err := f.store.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || !after.ExpiresAt.Equal(fixedDeadline) {
		t.Fatalf("hold deadline rolled: %#v err=%v", after, err)
	}

	// Once the fixed deadline passes the lease is expired; another Task can
	// acquire it instead of waiting for an ever-growing deadline.
	f.setNow(original.ExpiresAt.Add(6 * time.Minute))
	otherTask := createTask(t, f.store)
	if _, err := f.service.CreateGrant(ctx, "owner", otherTask.ID, profile.ID, nil, time.Minute); err != nil {
		t.Fatalf("acquire after bounded hold: %v", err)
	}
}

func TestNewOperationRebuildsHoldAfterOwnerRenewal(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	tabs, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	closeStarted := make(chan struct{}, 1)
	closeRelease := make(chan struct{})
	f.host.CloseTabStarted = closeStarted
	f.host.CloseTabRelease = closeRelease
	f.host.CloseTabGateTabID = tabs[0].HostTabID
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- f.service.CloseTab(ctx, "owner", grant.ID, tabs[0].ID)
	}()
	<-closeStarted
	oldHold, err := f.store.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || oldHold.HoldID == "" {
		t.Fatalf("old hold=%#v err=%v", oldHold, err)
	}

	// Owner renewal clears the persisted hold generation while the old close
	// is still running.
	renewedExpiry := f.now.Add(2 * time.Minute)
	if _, err := f.store.RefreshBrowserTaskGrantLease(ctx, grant.ID, oldHold.Capabilities, renewedExpiry, f.now); err != nil {
		t.Fatal(err)
	}
	openStarted := make(chan struct{}, 1)
	openRelease := make(chan struct{})
	f.host.OpenTabStarted = openStarted
	f.host.OpenTabRelease = openRelease
	openResult := make(chan error, 1)
	go func() {
		_, err := f.service.OpenTab(ctx, "owner", grant.ID, "https://after-renew.example/")
		openResult <- err
	}()
	<-openStarted
	newHold, err := f.store.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || newHold.HoldID == "" || newHold.HoldID == oldHold.HoldID {
		t.Fatalf("new operation reused old hold generation: %#v err=%v", newHold, err)
	}

	// Finishing the old generation must not delete the new hold cache/row.
	close(closeRelease)
	if err := <-closeResult; err != nil {
		t.Fatalf("old close after owner renewal: %v", err)
	}
	stillHeld, err := f.store.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || stillHeld.HoldID != newHold.HoldID {
		t.Fatalf("old generation cleanup removed new hold: %#v err=%v", stillHeld, err)
	}

	close(openRelease)
	if err := <-openResult; err != nil {
		t.Fatalf("open after owner renewal: %v", err)
	}
	finalGrant, err := f.store.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || finalGrant.HoldID != "" || !finalGrant.ExpiresAt.Equal(renewedExpiry) {
		t.Fatalf("final hold/expiry=%#v err=%v", finalGrant, err)
	}
}

func TestOwnerRenewalWinsOverOldHoldRestore(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	tabs, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	closeStarted := make(chan struct{}, 1)
	closeRelease := make(chan struct{})
	f.host.CloseTabStarted = closeStarted
	f.host.CloseTabRelease = closeRelease
	f.host.CloseTabGateTabID = tabs[0].HostTabID
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- f.service.CloseTab(ctx, "owner", grant.ID, tabs[0].ID)
	}()
	<-closeStarted
	held, err := f.store.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || held.HoldID == "" {
		t.Fatalf("hold=%#v err=%v", held, err)
	}
	// Owner renewal writes the same expiry value as the temporary hold on a
	// fixed clock. The hold ID, not the value, must decide whether restore wins.
	if _, err := f.store.RefreshBrowserTaskGrantLease(ctx, held.ID, held.Capabilities, held.ExpiresAt, f.now); err != nil {
		t.Fatal(err)
	}
	close(closeRelease)
	if err := <-closeResult; err != nil {
		t.Fatalf("close after owner renewal: %v", err)
	}
	stored, err := f.store.BrowserTaskGrant(ctx, grant.ID)
	if err != nil || !stored.ExpiresAt.Equal(held.ExpiresAt) || stored.HoldID != "" {
		t.Fatalf("old hold overwrote owner renewal: %#v err=%v", stored, err)
	}
}

func TestReleaseFailsClosedWhenFenceCannotBeInstalled(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	tabs, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	closeStarted := make(chan struct{}, 1)
	closeRelease := make(chan struct{})
	f.host.CloseTabStarted = closeStarted
	f.host.CloseTabRelease = closeRelease
	f.host.CloseTabGateTabID = tabs[0].HostTabID
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- f.service.CloseTab(ctx, "owner", grant.ID, tabs[0].ID)
	}()
	<-closeStarted
	f.host.FailSetFence = &browser.Error{Code: "browser_fence_timeout", Message: "fence blocked", Status: http.StatusConflict}
	_, err = f.service.RevokeGrant(ctx, "owner", grant.ID)
	if err == nil {
		t.Fatal("release succeeded while the fence could not be installed")
	}
	still, grantErr := f.service.Grant(ctx, grant.ID)
	if grantErr != nil || still.Status != domain.BrowserGrantStatusActive {
		t.Fatalf("lease was revoked after failed release: %#v err=%v", still, grantErr)
	}
	if pages, _ := f.host.ListTabs(ctx, profile.ID, grant.Session.HostSessionID); len(pages) != 1 {
		t.Fatalf("page disappeared before the in-flight close was released: %#v", pages)
	}
	close(closeRelease)
	<-closeResult
}

func TestReleaseFailsClosedWhenHostUnreachableOrTimesOut(t *testing.T) {
	for _, hostCode := range []string{"browser_host_unavailable", "browser_host_timeout"} {
		t.Run(hostCode, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			profile := createProfile(t, f)
			grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			status := http.StatusServiceUnavailable
			if hostCode == "browser_host_timeout" {
				status = http.StatusGatewayTimeout
			}
			f.host.FailSession = &browser.Error{Code: hostCode, Message: "host cannot be reached", Status: status}
			if _, err := f.service.RevokeGrant(ctx, "owner", grant.ID); err == nil {
				t.Fatal("release reported success while the host could not be reached")
			} else if browser.Code(err) != hostCode {
				t.Fatalf("release error=%v, want %s", err, hostCode)
			}
			still, err := f.service.Grant(ctx, grant.ID)
			if err != nil || still.Status != domain.BrowserGrantStatusActive {
				t.Fatalf("lease must remain active after a failed release: %#v err=%v", still, err)
			}
			// The Owner can retry once the host is reachable again.
			f.host.FailSession = nil
			released, err := f.service.RevokeGrant(ctx, "owner", grant.ID)
			if err != nil || released.Status != domain.BrowserGrantStatusRevoked {
				t.Fatalf("retry release=%#v err=%v", released, err)
			}
		})
	}
}

func TestReleaseAfterHostRecoveryStillFencesInFlightClose(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	tabs, err := f.service.SyncTabs(ctx, grant.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("tabs=%#v err=%v", tabs, err)
	}
	closeStarted := make(chan struct{}, 1)
	closeRelease := make(chan struct{})
	f.host.CloseTabStarted = closeStarted
	f.host.CloseTabRelease = closeRelease
	f.host.CloseTabGateTabID = tabs[0].HostTabID
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- f.service.CloseTab(ctx, "owner", grant.ID, tabs[0].ID)
	}()
	<-closeStarted
	f.host.FailSession = &browser.Error{Code: "browser_host_unavailable", Message: "down", Status: http.StatusServiceUnavailable}
	if _, err := f.service.RevokeGrant(ctx, "owner", grant.ID); err == nil {
		t.Fatal("release succeeded while the host was unreachable")
	}
	f.host.FailSession = nil
	released := make(chan error, 1)
	go func() {
		_, releaseErr := f.service.RevokeGrant(ctx, "owner", grant.ID)
		released <- releaseErr
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, grantErr := f.service.Grant(ctx, grant.ID)
		if grantErr == nil && current.Status == domain.BrowserGrantStatusRevoked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("release did not install the released fence")
		}
		time.Sleep(time.Millisecond)
	}
	close(closeRelease)
	if err := <-closeResult; browser.Code(err) != "browser_fence_stale" {
		t.Fatalf("old close after recovered release returned %v", err)
	}
	if err := <-released; err != nil {
		t.Fatalf("retry release: %v", err)
	}
	if pages, err := f.host.ListTabs(ctx, profile.ID, grant.Session.HostSessionID); err != nil || len(pages) != 1 {
		t.Fatalf("released page disappeared: %#v err=%v", pages, err)
	}
}

func TestExplicitMissingHostSessionAllowsDatabaseOnlyRelease(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// An explicit 404 proves there is no execution endpoint left to fence.
	f.host.FailSession = &browser.Error{Code: "browser_session_not_found", Message: "no session", Status: http.StatusNotFound}
	released, err := f.service.RevokeGrant(ctx, "owner", grant.ID)
	if err != nil {
		t.Fatalf("explicit missing session must allow DB-only release: %v", err)
	}
	if released.Status != domain.BrowserGrantStatusRevoked {
		t.Fatalf("grant status=%s, want revoked", released.Status)
	}
}

func TestIdempotentAcquireRefreshesFenceExpiry(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/", Title: "Owner", Active: true},
	})
	if _, err := f.service.SyncTabs(ctx, grant.ID); err != nil {
		t.Fatal(err)
	}
	refreshed, err := f.service.CreateGrant(ctx, "owner", f.task.ID, profile.ID, nil, time.Hour)
	if err != nil || refreshed.ID != grant.ID {
		t.Fatalf("refresh=%#v err=%v", refreshed, err)
	}
	f.setNow(f.now.Add(2 * time.Minute))
	if _, err := f.service.SyncTabs(ctx, grant.ID); err != nil {
		t.Fatalf("fence did not follow the refreshed expiry: %v", err)
	}
}
