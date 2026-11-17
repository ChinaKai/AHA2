package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/browser/browsertest"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type browserAPIFixture struct {
	server  *httptest.Server
	client  *http.Client
	csrf    string
	store   *store.Store
	host    *browsertest.FakeHost
	service *browser.Service
	task    domain.Task
}

func newBrowserAPIFixture(t *testing.T, host *browsertest.FakeHost) *browserAPIFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	service := browser.NewService(database, host, "local")
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, nil, app.StubExecutor{})
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: appService, Browser: service,
	}).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	fixture := &browserAPIFixture{
		server: server, client: client, store: database, host: host, service: service,
		task: createHardwareAPITask(t, database),
	}
	fixture.csrf = registerOwner(t, client, server.URL)
	return fixture
}

func (f *browserAPIFixture) do(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	response := requestJSON(t, f.client, method, f.server.URL+path, body, f.csrf)
	var payload map[string]any
	decodeResponse(t, response, &payload)
	return response, payload
}

func TestBrowserProfileAPIConfirmationAndInUseGuard(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)

	response, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create profile: %d %#v", response.StatusCode, payload)
	}
	profile := payload["profile"].(map[string]any)
	profileID := profile["id"].(string)
	if profile["browser_kind"] != "edge" || profile["host_id"] != "local" {
		t.Fatalf("profile=%#v", profile)
	}

	response, payload = f.do(t, http.MethodDelete, "/api/v1/browser/profiles/"+profileID, nil)
	if response.StatusCode != http.StatusBadRequest || payload["error"] != "browser_profile_delete_confirmation_required" {
		t.Fatalf("delete without confirm: %d %#v", response.StatusCode, payload)
	}

	response, payload = f.do(t, http.MethodPost, "/api/v1/browser/profiles/"+profileID+"/login-window", map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login window: %d %#v", response.StatusCode, payload)
	}
	if payload["session"].(map[string]any)["state"] != "ready" {
		t.Fatalf("session=%#v", payload["session"])
	}

	response, payload = f.do(t, http.MethodDelete, "/api/v1/browser/profiles/"+profileID+"?confirm=1", nil)
	if response.StatusCode != http.StatusConflict || payload["error"] != "browser_profile_in_use" {
		t.Fatalf("delete in-use profile: %d %#v", response.StatusCode, payload)
	}

	response, payload = f.do(t, http.MethodDelete, "/api/v1/browser/profiles/"+profileID+"/session", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("close session: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.do(t, http.MethodDelete, "/api/v1/browser/profiles/"+profileID+"?confirm=1", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete profile: %d %#v", response.StatusCode, payload)
	}
}

func TestBrowserHostHealthReportsAvailabilityForTheUI(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	response, payload := f.do(t, http.MethodGet, "/api/v1/browser/host", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("host health: %d %#v", response.StatusCode, payload)
	}
	view := payload["host"].(map[string]any)
	if view["available"] != true || view["edge_available"] != true || view["host_id"] != "local" {
		t.Fatalf("host view=%#v", view)
	}
	host.Unavailable = true
	response, payload = f.do(t, http.MethodGet, "/api/v1/browser/host", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unavailable host health should still describe why: %d %#v", response.StatusCode, payload)
	}
	view = payload["host"].(map[string]any)
	if view["available"] != false || view["code"] != "browser_host_unavailable" {
		t.Fatalf("unavailable host view=%#v", view)
	}
}

func TestBrowserHostFailuresReturnActionableStatus(t *testing.T) {
	host := browsertest.New()
	host.EdgeAvailable = false
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles/"+profileID+"/login-window", map[string]any{})
	if response.StatusCode != http.StatusServiceUnavailable || payload["error"] != "browser_edge_unavailable" {
		t.Fatalf("edge missing: %d %#v", response.StatusCode, payload)
	}
	host.EdgeAvailable = true
	host.Unavailable = true
	response, payload = f.do(t, http.MethodPost, "/api/v1/browser/profiles/"+profileID+"/login-window", map[string]any{})
	if response.StatusCode != http.StatusServiceUnavailable || payload["error"] != "browser_host_unavailable" {
		t.Fatalf("host down: %d %#v", response.StatusCode, payload)
	}
}

func TestBrowserGrantExpiryAndCrossTaskTabOverHTTP(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	now := time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC)
	f.service.SetNowForTest(func() time.Time { return now })

	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-sessions", map[string]any{
		"profile_id": profileID, "expires_in_seconds": 60,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create grant: %d %#v", response.StatusCode, payload)
	}
	grant := payload["grant"].(map[string]any)
	grantID := grant["id"].(string)

	response, payload = f.do(t, http.MethodPost, "/api/v1/browser-grants/"+grantID+"/tabs", map[string]any{
		"url": "https://example.com/a",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("open tab: %d %#v", response.StatusCode, payload)
	}
	tabID := payload["tab"].(map[string]any)["id"].(string)

	// A second Task attaches to the same session but may not touch Task A's tab.
	secondTask := createHardwareAPITask(t, f.store)
	response, payload = f.do(t, http.MethodPost, "/api/v1/tasks/"+secondTask.ID+"/browser-sessions", map[string]any{
		"profile_id": profileID,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("second grant: %d %#v", response.StatusCode, payload)
	}
	secondGrantID := payload["grant"].(map[string]any)["id"].(string)
	response, payload = f.do(t, http.MethodPost,
		"/api/v1/browser-grants/"+secondGrantID+"/tabs/"+tabID+"/navigate", map[string]any{"url": "https://example.com/b"})
	if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_tab_forbidden" {
		t.Fatalf("cross-task navigate: %d %#v", response.StatusCode, payload)
	}

	now = now.Add(2 * time.Minute)
	response, payload = f.do(t, http.MethodGet,
		"/api/v1/browser-grants/"+grantID+"/tabs/"+tabID+"/snapshot", nil)
	if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_grant_expired" {
		t.Fatalf("expired snapshot: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.do(t, http.MethodPost,
		"/api/v1/browser-grants/"+grantID+"/tabs/"+tabID+"/navigate", map[string]any{"url": "https://example.com/c"})
	if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_grant_expired" {
		t.Fatalf("expired navigate: %d %#v", response.StatusCode, payload)
	}
}

func TestTaskBrowserStateAndGrantRevocationAPI(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{
		"profile_id": profileID, "capabilities": []string{"snapshot"},
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("grant: %d %#v", response.StatusCode, payload)
	}
	grantID := payload["grant"].(map[string]any)["id"].(string)
	response, payload = f.do(t, http.MethodGet, "/api/v1/tasks/"+f.task.ID+"/browser", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("task browser state: %d %#v", response.StatusCode, payload)
	}
	state := payload["browser"].(map[string]any)
	if len(state["profiles"].([]any)) != 1 || len(state["grants"].([]any)) != 1 {
		t.Fatalf("state=%#v", state)
	}
	response, payload = f.do(t, http.MethodDelete, "/api/v1/browser-grants/"+grantID, nil)
	if response.StatusCode != http.StatusOK || payload["grant"].(map[string]any)["status"] != "revoked" {
		t.Fatalf("revoke: %d %#v", response.StatusCode, payload)
	}
}

func TestConcurrentBrowserGrantCreationOverHTTPKeepsOneActive(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)

	const workers = 24
	statuses := make(chan int, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			body, _ := json.Marshal(map[string]any{"profile_id": profileID})
			request, err := http.NewRequest(http.MethodPost,
				f.server.URL+"/api/v1/tasks/"+f.task.ID+"/browser-sessions", bytes.NewReader(body))
			if err != nil {
				statuses <- 0
				return
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-CSRF-Token", f.csrf)
			response, err := f.client.Do(request)
			if err != nil {
				statuses <- 0
				return
			}
			response.Body.Close()
			statuses <- response.StatusCode
		}()
	}
	wait.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusCreated {
			t.Fatalf("concurrent create status=%d", status)
		}
	}
	active, err := f.store.BrowserTaskGrantsForProfileActive(context.Background(), profileID, time.Now().UTC())
	if err != nil || len(active) != 1 {
		t.Fatalf("active grants=%d err=%v", len(active), err)
	}
}

func TestBrowserAPINeverReturnsHostDiagnosticPaths(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	host.FailOpen = &browser.Error{
		Code: "browser_launch_failed", Status: http.StatusInternalServerError,
		Message: `C:\Users\owner\AppData\Local\AHA2\browser-profiles\browser_profile_1`,
	}
	response, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles/"+profileID+"/login-window", map[string]any{})
	if response.StatusCode == http.StatusOK {
		t.Fatalf("launch should fail: %#v", payload)
	}
	message, _ := payload["message"].(string)
	if strings.Contains(message, "browser-profiles") || strings.Contains(message, `C:\`) {
		t.Fatalf("host diagnostic leaked through API: %q", message)
	}
}
