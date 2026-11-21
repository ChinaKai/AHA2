package browser_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func grantWithCaps(t *testing.T, f *fixture, capabilities []string) (browser.GrantView, domain.BrowserTab) {
	t.Helper()
	profile := createProfile(t, f)
	grant, err := f.service.CreateGrant(context.Background(), "owner", f.task.ID, profile.ID, capabilities, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.host.SetCurrentPages(profile.ID, []browser.HostTab{
		{ID: "host-tab-interaction", URL: "https://example.com/login", Title: "Login", Active: true},
	})
	tabs, err := f.service.SyncTabs(context.Background(), grant.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("sync tabs=%#v err=%v", tabs, err)
	}
	return grant, tabs[0]
}

func TestSnapshotPassesThroughStructuredElements(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	grant, tab := grantWithCaps(t, f, []string{domain.BrowserCapabilitySnapshot})
	f.host.SnapshotElements = []browser.HostElement{{
		Ref: "e1-2", Role: "textbox", Name: "Email",
		Bounds:  browser.HostBounds{X: 1, Y: 2, Width: 30, Height: 40},
		Actions: []string{"click", "type"},
	}}
	snapshot, err := f.service.SnapshotTab(ctx, "agent", grant.ID, tab.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Elements) != 1 || snapshot.Elements[0].Ref != "e1-2" ||
		snapshot.Elements[0].Name != "Email" || len(snapshot.Elements[0].Actions) != 2 {
		t.Fatalf("snapshot elements=%#v", snapshot.Elements)
	}
}

func TestPageActionsRequireInteractAndKeepPayloadsOutOfAudit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	grant, tab := grantWithCaps(t, f, []string{
		domain.BrowserCapabilityOpenTab, domain.BrowserCapabilityNavigate,
		domain.BrowserCapabilitySnapshot, domain.BrowserCapabilityInteract,
	})

	result, err := f.service.Act(ctx, "agent:a@turn", grant.ID, tab.ID, browser.HostAction{
		Kind: "type", Ref: "e1-2", Text: "owner-supplied-2FA-123456",
	})
	if err != nil {
		t.Fatalf("type action: %v", err)
	}
	if result.Kind != "type" || result.Ref != "e1-2" {
		t.Fatalf("action result=%#v", result)
	}
	if len(f.host.ActionCalls) != 1 || f.host.ActionCalls[0].Text != "owner-supplied-2FA-123456" {
		t.Fatalf("host action calls=%#v", f.host.ActionCalls)
	}
	events, err := f.store.BrowserAuditEvents(ctx, "browser_tab.action", 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("action audit=%#v err=%v", events, err)
	}
	encoded, _ := json.Marshal(events[0].Data)
	if strings.Contains(string(encoded), "owner-supplied-2FA-123456") {
		t.Fatalf("audit leaked typed text: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"kind":"type"`) {
		t.Fatalf("audit lost the action kind: %s", encoded)
	}

	// Key names are payload too; only their count is audited.
	if _, err := f.service.Act(ctx, "agent:a@turn", grant.ID, tab.ID, browser.HostAction{
		Kind: "key", Keys: []string{"CTRL", "SHIFT", "L"},
	}); err != nil {
		t.Fatalf("key action: %v", err)
	}
	events, _ = f.store.BrowserAuditEvents(ctx, "browser_tab.action", 10)
	encoded, _ = json.Marshal(events[0].Data)
	if strings.Contains(string(encoded), "CTRL") || strings.Contains(string(encoded), "SHIFT") {
		t.Fatalf("audit leaked key names: %s", encoded)
	}
}

func TestPageActionValidationAndStaleRefsFailClosed(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	grant, tab := grantWithCaps(t, f, []string{
		domain.BrowserCapabilitySnapshot, domain.BrowserCapabilityInteract,
	})
	for name, action := range map[string]browser.HostAction{
		"unknown kind":    {Kind: "evaluate"},
		"missing target":  {Kind: "click"},
		"half coords":     {Kind: "click", X: intPtr(10)},
		"both targets":    {Kind: "click", Ref: "e1-1", X: intPtr(1), Y: intPtr(1)},
		"bad ref":         {Kind: "click", Ref: "../x"},
		"empty text":      {Kind: "type", Ref: "e1-1"},
		"oversize text":   {Kind: "type", Ref: "e1-1", Text: strings.Repeat("a", browser.MaxActionTextBytes+1)},
		"too many keys":   {Kind: "key", Keys: []string{"A", "B", "C", "D", "E"}},
		"zero scroll":     {Kind: "scroll"},
		"oversize scroll": {Kind: "scroll", DeltaY: browser.MaxScrollDelta + 1},
	} {
		if _, err := f.service.Act(ctx, "agent", grant.ID, tab.ID, action); browser.Code(err) != "browser_action_invalid" {
			t.Fatalf("%s: expected browser_action_invalid, got %v", name, err)
		}
	}
	if len(f.host.ActionCalls) != 0 {
		t.Fatalf("invalid actions reached the host: %#v", f.host.ActionCalls)
	}

	f.host.FailAction = &browser.Error{Code: "browser_element_stale", Status: http.StatusConflict, Message: "stale"}
	if _, err := f.service.Act(ctx, "agent", grant.ID, tab.ID, browser.HostAction{Kind: "click", Ref: "e1-1"}); browser.Code(err) != "browser_element_stale" {
		t.Fatalf("stale ref: %v", err)
	}
}

func TestOwnerActionsUseOwnerSetAndKeepPayloadsOutOfAudit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	grant, tab := grantWithCaps(t, f, []string{
		domain.BrowserCapabilitySnapshot, domain.BrowserCapabilityInteract,
	})

	// The Agent set is closed: Owner-only kinds must not become Agent powers.
	for name, action := range map[string]browser.HostAction{
		"context click": {Kind: "context_click", X: intPtr(10), Y: intPtr(20)},
		"text":          {Kind: "text", Text: "owner-only-2FA-654321"},
		"drag":          {Kind: "drag", X: intPtr(1), Y: intPtr(2), ToX: intPtr(3), ToY: intPtr(4)},
	} {
		if _, err := f.service.Act(ctx, "agent:a@turn", grant.ID, tab.ID, action); browser.Code(err) != "browser_action_invalid" {
			t.Fatalf("agent %s: expected browser_action_invalid, got %v", name, err)
		}
	}
	if len(f.host.ActionCalls) != 0 {
		t.Fatalf("owner-only actions reached the host through the agent path: %#v", f.host.ActionCalls)
	}

	if _, err := f.service.ActOwner(ctx, "owner", grant.ID, tab.ID, browser.HostAction{
		Kind: "text", Text: "owner-only-2FA-654321",
	}); err != nil {
		t.Fatalf("owner text: %v", err)
	}
	if len(f.host.ActionCalls) != 1 || f.host.ActionCalls[0].Kind != "text" {
		t.Fatalf("owner text host calls=%#v", f.host.ActionCalls)
	}
	if _, err := f.service.ActOwner(ctx, "owner", grant.ID, tab.ID, browser.HostAction{
		Kind: "context_click", X: intPtr(40), Y: intPtr(50),
	}); err != nil {
		t.Fatalf("owner right click: %v", err)
	}
	result, err := f.service.ActOwner(ctx, "owner", grant.ID, tab.ID, browser.HostAction{
		Kind: "drag", X: intPtr(1), Y: intPtr(2), ToX: intPtr(30), ToY: intPtr(40),
	})
	if err != nil {
		t.Fatalf("owner drag: %v", err)
	}
	if result.Kind != "drag" || result.ToX == nil || *result.ToX != 30 {
		t.Fatalf("owner drag result=%#v", result)
	}

	events, err := f.store.BrowserAuditEvents(ctx, "browser_tab.action", 10)
	if err != nil || len(events) != 3 {
		t.Fatalf("owner action audit=%#v err=%v", events, err)
	}
	for _, event := range events {
		if event.OwnerID != "owner" {
			t.Fatalf("owner action actor=%q", event.OwnerID)
		}
		encoded, _ := json.Marshal(event.Data)
		text := string(encoded)
		if !strings.Contains(text, `"source":"owner"`) ||
			!strings.Contains(text, `"grant_id":"`+grant.ID+`"`) ||
			!strings.Contains(text, `"tab_id":"`+tab.ID+`"`) {
			t.Fatalf("owner action audit lost provenance: %s", text)
		}
		if strings.Contains(text, "owner-only-2FA-654321") {
			t.Fatalf("owner action audit leaked typed text: %s", text)
		}
	}

	// Owner input still needs the interact capability; it never rides on the
	// snapshot capability that the preview uses. A Task can hold only one
	// Profile lease, so this uses its own fixture.
	f2 := newFixture(t)
	snapshotOnly, snapshotTab := grantWithCaps(t, f2, []string{domain.BrowserCapabilitySnapshot})
	if _, err := f2.service.ActOwner(ctx, "owner", snapshotOnly.ID, snapshotTab.ID, browser.HostAction{
		Kind: "click", X: intPtr(1), Y: intPtr(1),
	}); browser.Code(err) != "browser_capability_denied" {
		t.Fatalf("snapshot-only owner action: %v", err)
	}

	// Validation is shared with the Agent path.
	for name, action := range map[string]browser.HostAction{
		"text with coords":  {Kind: "text", Text: "x", X: intPtr(1), Y: intPtr(2)},
		"text without text": {Kind: "text"},
		"drag without end":  {Kind: "drag", X: intPtr(1), Y: intPtr(2)},
		"drag end out of range": {Kind: "drag", X: intPtr(1), Y: intPtr(2),
			ToX: intPtr(browser.MaxCoordinate + 1), ToY: intPtr(2)},
		"click with end": {Kind: "click", X: intPtr(1), Y: intPtr(2), ToX: intPtr(3), ToY: intPtr(4)},
	} {
		if _, err := f.service.ActOwner(ctx, "owner", grant.ID, tab.ID, action); browser.Code(err) != "browser_action_invalid" {
			t.Fatalf("%s: expected browser_action_invalid, got %v", name, err)
		}
	}
}

func TestOldGrantDoesNotSilentlyAcquireNewCapabilities(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	grant, tab := grantWithCaps(t, f, []string{
		domain.BrowserCapabilityOpenTab, domain.BrowserCapabilityNavigate, domain.BrowserCapabilitySnapshot,
	})
	if _, err := f.service.Act(ctx, "agent", grant.ID, tab.ID, browser.HostAction{Kind: "click", Ref: "e1-1"}); browser.Code(err) != "browser_capability_denied" {
		t.Fatalf("old grant action: %v", err)
	}
	if _, err := f.service.Downloads(ctx, "agent", grant.ID); browser.Code(err) != "browser_capability_denied" {
		t.Fatalf("old grant downloads: %v", err)
	}
	if _, _, err := f.service.ExportCookies(ctx, "agent", grant.ID, "https://example.com/", ""); browser.Code(err) != "browser_capability_denied" {
		t.Fatalf("old grant cookies: %v", err)
	}
	stored, err := f.service.Grant(ctx, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range []string{domain.BrowserCapabilityInteract, domain.BrowserCapabilityFiles, domain.BrowserCapabilityCookies} {
		if stored.Has(capability) {
			t.Fatalf("old grant was upgraded with %q: %#v", capability, stored.Capabilities)
		}
	}
	// A capability-less new request keeps the legacy default, never the full set.
	if _, err := f.service.CreateGrant(ctx, "owner", f.task.ID, grant.ProfileID, nil, time.Hour); err != nil {
		t.Fatalf("refresh grant: %v", err)
	}
	refreshed, _ := f.service.Grant(ctx, grant.ID)
	if refreshed.Has(domain.BrowserCapabilityInteract) || refreshed.Has(domain.BrowserCapabilityFiles) || refreshed.Has(domain.BrowserCapabilityCookies) {
		t.Fatalf("capability-less refresh upgraded the grant: %#v", refreshed.Capabilities)
	}
}

func TestFileUploadAndDownloadLimits(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	grant, tab := grantWithCaps(t, f, []string{
		domain.BrowserCapabilitySnapshot, domain.BrowserCapabilityFiles,
	})
	if f.host.ClearDownload != 1 {
		t.Fatalf("a new lease must clear the previous holder's downloads: %d", f.host.ClearDownload)
	}
	upload, err := f.service.Upload(ctx, "agent", grant.ID, tab.ID, "e1-4", nil, nil, []browser.UploadFile{
		{Filename: "resume.pdf", Data: []byte("hello")},
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if upload.FileCount != 1 || len(f.host.UploadCalls) != 1 || f.host.UploadCalls[0].Ref != "e1-4" {
		t.Fatalf("upload=%#v calls=%#v", upload, f.host.UploadCalls)
	}
	events, err := f.store.BrowserAuditEvents(ctx, "browser_tab.upload", 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("upload audit=%#v err=%v", events, err)
	}
	encoded, _ := json.Marshal(events[0].Data)
	if !strings.Contains(string(encoded), "resume.pdf") || strings.Contains(string(encoded), "aGVsbG8=") {
		t.Fatalf("upload audit should keep name/size but not bytes: %s", encoded)
	}

	if _, err := f.service.Upload(ctx, "agent", grant.ID, tab.ID, "", nil, nil, []browser.UploadFile{{Filename: "a.txt", Data: []byte("x")}}); browser.Code(err) != "browser_upload_invalid" {
		t.Fatalf("missing target: %v", err)
	}
	if _, err := f.service.Upload(ctx, "agent", grant.ID, tab.ID, "e1-4", nil, nil, []browser.UploadFile{{Filename: "../escape.txt", Data: []byte("x")}}); browser.Code(err) != "browser_upload_invalid" {
		t.Fatalf("path filename: %v", err)
	}
	if _, err := f.service.Upload(ctx, "agent", grant.ID, tab.ID, "e1-4", nil, nil, []browser.UploadFile{{Filename: "big.bin", Data: make([]byte, browser.MaxUploadFileBytes+1)}}); browser.Code(err) != "browser_upload_invalid" {
		t.Fatalf("oversize upload: %v", err)
	}
	if len(f.host.UploadCalls) != 1 {
		t.Fatalf("invalid uploads reached the host: %d", len(f.host.UploadCalls))
	}

	f.host.DownloadsList = []browser.HostDownload{{ID: "download-0123456789abcdef01234567", Filename: "report.csv", Size: 12}}
	downloads, err := f.service.Downloads(ctx, "agent", grant.ID)
	if err != nil || len(downloads) != 1 {
		t.Fatalf("downloads=%#v err=%v", downloads, err)
	}
	f.host.DownloadFiles = map[string]browser.HostDownloadFile{
		"download-0123456789abcdef01234567": {
			Download: browser.HostDownload{Filename: "../../report.csv", Size: int64(len("a,b\n1,2\n"))},
			Body:     io.NopCloser(bytes.NewReader([]byte("a,b\n1,2\n"))),
		},
	}
	file, err := f.service.Download(ctx, "agent", grant.ID, "download-0123456789abcdef01234567")
	if err != nil || strings.Contains(file.Download.Filename, "/") {
		t.Fatalf("download=%#v err=%v", file, err)
	}
	body, readErr := io.ReadAll(file.Body)
	_ = file.Body.Close()
	if readErr != nil || string(body) != "a,b\n1,2\n" {
		t.Fatalf("download body=%q err=%v", body, readErr)
	}
	// Downloads have no business size cap; a file past the old 10 MiB limit
	// must stream through intact rather than be truncated or rejected. The
	// service must not read the body to measure it.
	f.host.DownloadFiles["download-0123456789abcdef01234567"] = browser.HostDownloadFile{
		Download: browser.HostDownload{Filename: "large.bin", Size: 11 << 20},
		Body:     io.NopCloser(bytes.NewReader(make([]byte, 11<<20))),
	}
	large, err := f.service.Download(ctx, "agent", grant.ID, "download-0123456789abcdef01234567")
	if err != nil || large.Download.Size != 11<<20 {
		t.Fatalf("large download size=%d err=%v", large.Download.Size, err)
	}
	_ = large.Body.Close()
	for _, id := range []string{"../../etc/passwd", "download/../../x", "", strings.Repeat("a", 65)} {
		if _, err := f.service.Download(ctx, "agent", grant.ID, id); browser.Code(err) != "browser_download_not_found" {
			t.Fatalf("download id %q: %v", id, err)
		}
	}
	clearedBefore := f.host.ClearDownload
	if _, err := f.service.RevokeGrant(ctx, "owner", grant.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if f.host.ClearDownload != clearedBefore+1 {
		t.Fatalf("release did not clear downloads: before=%d after=%d", clearedBefore, f.host.ClearDownload)
	}
}

func TestCookieExportImportValidationAndNoValuesInAudit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	grant, _ := grantWithCaps(t, f, []string{
		domain.BrowserCapabilitySnapshot, domain.BrowserCapabilityCookies,
	})
	f.host.CookieList = []browser.HostCookie{{
		Name: "session", Value: "super-secret-cookie", Domain: "example.com", Path: "/",
		Expires: -1, HTTPOnly: true, Secure: true, SameSite: "Lax",
	}}
	cookies, truncated, err := f.service.ExportCookies(ctx, "agent", grant.ID, "https://example.com/login", "")
	if err != nil || truncated || len(cookies) != 1 || cookies[0].Value != "super-secret-cookie" {
		t.Fatalf("export=%#v truncated=%v err=%v", cookies, truncated, err)
	}
	events, err := f.store.BrowserAuditEvents(ctx, "browser_cookie.export", 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("export audit=%#v err=%v", events, err)
	}
	encoded, _ := json.Marshal(events[0].Data)
	if strings.Contains(string(encoded), "super-secret-cookie") {
		t.Fatalf("audit leaked a cookie value: %s", encoded)
	}

	imported, err := f.service.ImportCookies(ctx, "agent", grant.ID, []browser.HostCookie{{
		Name: "session", Value: "imported-secret", Domain: ".example.com", Path: "/app",
		Expires: f.now.Add(time.Hour).Unix(), HTTPOnly: true, Secure: true, SameSite: "none",
	}})
	if err != nil || imported != 1 || len(f.host.ImportedCookies) != 1 {
		t.Fatalf("import=%d host=%#v err=%v", imported, f.host.ImportedCookies, err)
	}
	events, err = f.store.BrowserAuditEvents(ctx, "browser_cookie.import", 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("import audit=%#v err=%v", events, err)
	}
	encoded, _ = json.Marshal(events[0].Data)
	if strings.Contains(string(encoded), "imported-secret") {
		t.Fatalf("import audit leaked a cookie value: %s", encoded)
	}

	for name, cookie := range map[string]browser.HostCookie{
		"same-site none without secure": {Name: "a", Value: "b", Domain: "example.com", Path: "/", Expires: -1, SameSite: "None"},
		"domain with path":              {Name: "a", Value: "b", Domain: "example.com/x", Path: "/", Expires: -1},
		"expired":                       {Name: "a", Value: "b", Domain: "example.com", Path: "/", Expires: f.now.Add(-time.Hour).Unix()},
		"bad name":                      {Name: "a;b", Value: "b", Domain: "example.com", Path: "/", Expires: -1},
		"bad same-site":                 {Name: "a", Value: "b", Domain: "example.com", Path: "/", Expires: -1, SameSite: "Sometimes"},
	} {
		if _, err := f.service.ImportCookies(ctx, "agent", grant.ID, []browser.HostCookie{cookie}); browser.Code(err) != "browser_cookie_invalid" {
			t.Fatalf("%s: expected browser_cookie_invalid, got %v", name, err)
		}
	}
	if _, _, err := f.service.ExportCookies(ctx, "agent", grant.ID, "", ""); browser.Code(err) != "browser_cookie_invalid" {
		t.Fatalf("unscoped export: %v", err)
	}
	if len(f.host.ImportedCookies) != 1 {
		t.Fatalf("invalid imports reached the host: %#v", f.host.ImportedCookies)
	}
}

func intPtr(value int) *int { return &value }
