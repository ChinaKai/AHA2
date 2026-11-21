package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
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
	agents  *agentapi.Capabilities
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
	agents := agentapi.NewCapabilities()
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: appService, Browser: service,
		AgentCapabilities: agents,
	}).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	fixture := &browserAPIFixture{
		server: server, client: client, store: database, host: host, service: service,
		task: createHardwareAPITask(t, database), agents: agents,
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

func TestBrowserTabFrameReturnsAuthorizedImageAndStopsAfterRevoke(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-sessions", map[string]any{
		"profile_id": profileID,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create grant: %d %#v", response.StatusCode, payload)
	}
	grantID := payload["grant"].(map[string]any)["id"].(string)
	response, payload = f.do(t, http.MethodPost, "/api/v1/browser-grants/"+grantID+"/tabs", map[string]any{
		"url": "https://example.com/login",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("open tab: %d %#v", response.StatusCode, payload)
	}
	tabID := payload["tab"].(map[string]any)["id"].(string)

	frameResponse, err := f.client.Get(f.server.URL + "/api/v1/browser-grants/" + grantID + "/tabs/" + tabID + "/frame")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(frameResponse.Body)
	frameResponse.Body.Close()
	if frameResponse.StatusCode != http.StatusOK {
		t.Fatalf("frame: %d %s", frameResponse.StatusCode, body)
	}
	if frameResponse.Header.Get("Content-Type") != "image/jpeg" || frameResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("frame headers: %#v", frameResponse.Header)
	}
	if len(body) != 4 || body[0] != 0xff {
		t.Fatalf("frame body=%v", body)
	}

	if response, payload := f.do(t, http.MethodDelete, "/api/v1/browser-grants/"+grantID, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("revoke: %d %#v", response.StatusCode, payload)
	}
	revokedResponse, err := f.client.Get(f.server.URL + "/api/v1/browser-grants/" + grantID + "/tabs/" + tabID + "/frame")
	if err != nil {
		t.Fatal(err)
	}
	defer revokedResponse.Body.Close()
	if revokedResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("frame after revoke: %d", revokedResponse.StatusCode)
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

func (f *browserAPIFixture) agentToken(t *testing.T, taskID string) string {
	t.Helper()
	turn, err := f.store.ActiveTurnForAgent(context.Background(), taskID, "main")
	if errors.Is(err, sql.ErrNoRows) {
		task, readErr := f.store.Task(context.Background(), taskID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		turn = createCoreTurn(t, f.store, task, "main", domain.NewID("inbox_batch"))
	} else if err != nil {
		t.Fatal(err)
	}
	token, err := f.agents.Issue(taskID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (f *browserAPIFixture) agentDo(t *testing.T, method, path, token string, body any) (*http.Response, map[string]any) {
	t.Helper()
	response := agentRequest(t, f.server.URL+path, method, token, body)
	var payload map[string]any
	decodeResponse(t, response, &payload)
	return response, payload
}

func TestBindCurrentPageWithoutAWebPageIsAnExplicitEmptyState(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{"profile_id": profileID})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create grant: %d %#v", response.StatusCode, payload)
	}
	grantID := payload["grant"].(map[string]any)["id"].(string)
	host.SetCurrentPages(profileID, []browser.HostTab{{ID: "host-tab-blank", URL: "about:blank"}})
	response, payload = f.do(t, http.MethodPost, "/api/v1/browser-grants/"+grantID+"/tabs/current", nil)
	if response.StatusCode != http.StatusNotFound || payload["error"] != "browser_no_current_page" {
		t.Fatalf("expected browser_no_current_page, got %d %#v", response.StatusCode, payload)
	}
	if message, _ := payload["message"].(string); !strings.Contains(message, "打开页面") {
		t.Fatalf("empty-state message should tell the Owner how to open a page: %q", message)
	}
}

func TestAgentBrowserAPIControlsItsOwnGrantAndFailsClosed(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{
		"profile_id": profileID, "capabilities": []string{"navigate", "open_tab", "snapshot"},
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create grant: %d %#v", response.StatusCode, payload)
	}
	grantID := payload["grant"].(map[string]any)["id"].(string)
	host.SetCurrentPages(profileID, []browser.HostTab{
		{ID: "host-tab-owner", URL: "https://example.com/login", Title: "Login", Active: true},
	})
	token := f.agentToken(t, f.task.ID)

	// The Agent only sees profiles it is actually granted.
	response, payload = f.agentDo(t, http.MethodGet, "/api/v1/agent/browser", token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("agent browser state: %d %#v", response.StatusCode, payload)
	}
	state := payload["browser"].(map[string]any)
	if profiles := state["profiles"].([]any); len(profiles) != 1 {
		t.Fatalf("agent profiles=%#v", profiles)
	}

	// Binding, opening, navigating, snapshot and frame all work with the grant.
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/current", token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("agent bind: %d %#v", response.StatusCode, payload)
	}
	tabID := payload["tab"].(map[string]any)["id"].(string)
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs", token, map[string]any{"url": "https://example.com/agent"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("agent open: %d %#v", response.StatusCode, payload)
	}
	openedTabID := payload["tab"].(map[string]any)["id"].(string)
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+openedTabID+"/navigate", token,
		map[string]any{"url": "https://example.com/next"})
	if response.StatusCode != http.StatusOK || payload["tab"].(map[string]any)["url"] != "https://example.com/next" {
		t.Fatalf("agent navigate: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.agentDo(t, http.MethodGet,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/snapshot", token, nil)
	if response.StatusCode != http.StatusOK || payload["snapshot"].(map[string]any)["text"] != "page text" {
		t.Fatalf("agent snapshot: %d %#v", response.StatusCode, payload)
	}
	frameRequest, err := http.NewRequest(http.MethodGet,
		f.server.URL+"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/frame", nil)
	if err != nil {
		t.Fatal(err)
	}
	frameRequest.Header.Set("Authorization", "Bearer "+token)
	frameResponse, err := http.DefaultClient.Do(frameRequest)
	if err != nil {
		t.Fatal(err)
	}
	frameBody, _ := io.ReadAll(frameResponse.Body)
	frameResponse.Body.Close()
	if frameResponse.StatusCode != http.StatusOK || len(frameBody) == 0 || frameResponse.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("agent frame: %d %q %d bytes", frameResponse.StatusCode, frameResponse.Header.Get("Content-Type"), len(frameBody))
	}

	// A capability from another Task can neither list this grant nor use it.
	otherTask := createHardwareAPITask(t, f.store)
	otherToken := f.agentToken(t, otherTask.ID)
	response, payload = f.agentDo(t, http.MethodGet, "/api/v1/agent/browser", otherToken, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("other agent state: %d %#v", response.StatusCode, payload)
	}
	if profiles := payload["browser"].(map[string]any)["profiles"].([]any); len(profiles) != 0 {
		t.Fatalf("cross-task state leaked profiles: %#v", profiles)
	}
	response, payload = f.agentDo(t, http.MethodGet,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs", otherToken, nil)
	if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_grant_forbidden" {
		t.Fatalf("cross-task grant access: %d %#v", response.StatusCode, payload)
	}

	// The Task can release its own lease; afterwards every action fails closed.
	response, payload = f.agentDo(t, http.MethodDelete,
		"/api/v1/agent/browser/grants/"+grantID, token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("agent release: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.agentDo(t, http.MethodGet,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/snapshot", token, nil)
	if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_grant_revoked" {
		t.Fatalf("revoked snapshot: %d %#v", response.StatusCode, payload)
	}
}

func TestAgentBrowserGrantExpiryIsFailClosed(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{
		"profile_id": profileID, "expires_in_seconds": 60,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create grant: %d %#v", response.StatusCode, payload)
	}
	grantID := payload["grant"].(map[string]any)["id"].(string)
	host.SetCurrentPages(profileID, []browser.HostTab{
		{ID: "host-tab-owner", URL: "https://example.com/login", Title: "Login", Active: true},
	})
	token := f.agentToken(t, f.task.ID)
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/current", token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bind: %d %#v", response.StatusCode, payload)
	}
	tabID := payload["tab"].(map[string]any)["id"].(string)

	f.service.SetNowForTest(func() time.Time { return time.Now().UTC().Add(2 * time.Minute) })
	response, payload = f.agentDo(t, http.MethodGet,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/snapshot", token, nil)
	if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_grant_expired" {
		t.Fatalf("expired snapshot: %d %#v", response.StatusCode, payload)
	}
}

func TestProfileExclusiveLeaseConflictReleaseAndTransferOverHTTP(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{"profile_id": profileID})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("Task A lease: %d %#v", response.StatusCode, payload)
	}
	grantA := payload["grant"].(map[string]any)["id"].(string)

	otherTask := createHardwareAPITask(t, f.store)
	response, payload = f.do(t, http.MethodPost, "/api/v1/tasks/"+otherTask.ID+"/browser-grants", map[string]any{"profile_id": profileID})
	if response.StatusCode != http.StatusConflict || payload["error"] != "browser_profile_in_use" {
		t.Fatalf("Task B lease conflict: %d %#v", response.StatusCode, payload)
	}

	// Release by the holder frees the Profile for Task B.
	response, payload = f.do(t, http.MethodDelete, "/api/v1/browser-grants/"+grantA, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("release: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.do(t, http.MethodPost, "/api/v1/tasks/"+otherTask.ID+"/browser-grants", map[string]any{"profile_id": profileID})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("Task B after release: %d %#v", response.StatusCode, payload)
	}
	grantB := payload["grant"].(map[string]any)["id"].(string)

	// Owner transfer moves control back to Task A without Task A asking.
	response, payload = f.do(t, http.MethodPost, "/api/v1/browser/profiles/"+profileID+"/transfer",
		map[string]any{"task_id": f.task.ID})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("transfer: %d %#v", response.StatusCode, payload)
	}
	transferred := payload["grant"].(map[string]any)
	if transferred["task_id"] != f.task.ID || transferred["status"] != "active" {
		t.Fatalf("transferred grant=%#v", transferred)
	}
	response, payload = f.do(t, http.MethodGet, "/api/v1/browser-grants/"+grantB, nil)
	if response.StatusCode != http.StatusOK || payload["grant"].(map[string]any)["status"] != "revoked" {
		t.Fatalf("old grant after transfer: %d %#v", response.StatusCode, payload)
	}
}

func TestReleaseOverHTTPFailsClosedWhenHostUnreachable(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{"profile_id": profileID})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("lease: %d %#v", response.StatusCode, payload)
	}
	grantID := payload["grant"].(map[string]any)["id"].(string)

	host.FailSession = &browser.Error{
		Code: "browser_host_unavailable", Message: "host down", Status: http.StatusServiceUnavailable,
	}
	response, payload = f.do(t, http.MethodDelete, "/api/v1/browser-grants/"+grantID, nil)
	if response.StatusCode != http.StatusServiceUnavailable || payload["error"] != "browser_host_unavailable" {
		t.Fatalf("unreachable release must fail: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.do(t, http.MethodGet, "/api/v1/browser-grants/"+grantID, nil)
	if response.StatusCode != http.StatusOK || payload["grant"].(map[string]any)["status"] != "active" {
		t.Fatalf("lease after failed release: %d %#v", response.StatusCode, payload)
	}

	// An explicit 404 means the host has no execution endpoint, so a DB-only
	// release is allowed; the next acquire can then take the Profile.
	host.FailSession = &browser.Error{
		Code: "browser_session_not_found", Message: "no session", Status: http.StatusNotFound,
	}
	response, payload = f.do(t, http.MethodDelete, "/api/v1/browser-grants/"+grantID, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("missing-session release: %d %#v", response.StatusCode, payload)
	}
	if payload["grant"].(map[string]any)["status"] != "revoked" {
		t.Fatalf("released grant=%#v", payload["grant"])
	}
}

func TestLeaseHoldSQLFailureReturns409BeforeHostCall(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{
		"profile_id": profileID, "expires_in_seconds": 60,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("lease: %d %#v", response.StatusCode, payload)
	}
	grantID := payload["grant"].(map[string]any)["id"].(string)
	f.service.SetHoldForTest(func() error { return errors.New("injected SQL failure") })
	response, payload = f.do(t, http.MethodPost, "/api/v1/browser-grants/"+grantID+"/tabs", map[string]any{"url": "https://example.com/"})
	if response.StatusCode != http.StatusConflict || payload["error"] != "browser_lease_hold_failed" {
		t.Fatalf("hold SQL failure: %d %#v", response.StatusCode, payload)
	}
	if host.OpenTabCalls != 0 {
		t.Fatalf("host was called without a hold: %d", host.OpenTabCalls)
	}
}

func TestExclusiveTaskControlsOwnerOpenedTabsOverHTTP(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{"profile_id": profileID})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("lease: %d %#v", response.StatusCode, payload)
	}
	grantID := payload["grant"].(map[string]any)["id"].(string)
	host.SetCurrentPages(profileID, []browser.HostTab{
		{ID: "host-page-one", URL: "https://owner.example/one", Title: "One", Active: true},
		{ID: "host-page-two", URL: "https://owner.example/two", Title: "Two"},
	})
	response, payload = f.do(t, http.MethodGet, "/api/v1/tasks/"+f.task.ID+"/browser", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("task browser state: %d %#v", response.StatusCode, payload)
	}
	tabs := payload["browser"].(map[string]any)["tabs"].([]any)
	if len(tabs) != 2 {
		t.Fatalf("owner tabs=%#v", tabs)
	}
	byURL := map[string]string{}
	for _, raw := range tabs {
		tab := raw.(map[string]any)
		byURL[tab["url"].(string)] = tab["id"].(string)
	}
	response, payload = f.do(t, http.MethodPost, "/api/v1/browser-grants/"+grantID+"/tabs/"+byURL["https://owner.example/two"]+"/activate", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("activate: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.do(t, http.MethodPost, "/api/v1/browser-grants/"+grantID+"/tabs/"+byURL["https://owner.example/one"]+"/navigate",
		map[string]any{"url": "https://owner.example/next"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("navigate: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.do(t, http.MethodGet, "/api/v1/browser-grants/"+grantID+"/tabs/"+byURL["https://owner.example/two"]+"/snapshot", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("snapshot: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.do(t, http.MethodDelete, "/api/v1/browser-grants/"+grantID+"/tabs/"+byURL["https://owner.example/one"], nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("close: %d %#v", response.StatusCode, payload)
	}
	if host.CloseTabCalls != 1 {
		t.Fatalf("owner tab close calls=%d", host.CloseTabCalls)
	}
}

func TestAgentBookmarksActivateAndOwnerTabCloseOverHTTP(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{"profile_id": profileID})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("lease: %d %#v", response.StatusCode, payload)
	}
	grantID := payload["grant"].(map[string]any)["id"].(string)
	host.SetCurrentPages(profileID, []browser.HostTab{
		{ID: "host-page-owner", URL: "https://owner.example/work", Title: "Work", Active: true},
	})
	host.BookmarkList = []browser.HostBookmark{
		{Title: "Mail", URL: "https://mail.example/", Folder: "Bookmarks bar"},
	}
	token := f.agentToken(t, f.task.ID)

	response, payload = f.agentDo(t, http.MethodGet,
		"/api/v1/agent/browser/grants/"+grantID+"/bookmarks", token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bookmarks: %d %#v", response.StatusCode, payload)
	}
	bookmarks := payload["bookmarks"].([]any)
	if len(bookmarks) != 1 || bookmarks[0].(map[string]any)["url"] != "https://mail.example/" {
		t.Fatalf("bookmarks=%#v", bookmarks)
	}
	encoded, _ := json.Marshal(payload)
	if strings.Contains(strings.ToLower(string(encoded)), "appdata") {
		t.Fatalf("bookmarks leaked a path: %#v", payload)
	}
	response, payload = f.agentDo(t, http.MethodGet,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs", token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("tabs: %d %#v", response.StatusCode, payload)
	}
	tabs := payload["tabs"].([]any)
	ownerTabID := tabs[0].(map[string]any)["id"].(string)
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+ownerTabID+"/activate", token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("agent activate: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs", token, map[string]any{"url": "https://mail.example/"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("open bookmark URL: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.agentDo(t, http.MethodDelete,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+ownerTabID, token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("agent close owner tab: %d %#v", response.StatusCode, payload)
	}
	if host.CloseTabCalls != 1 {
		t.Fatalf("agent close owner tab calls=%d", host.CloseTabCalls)
	}
}

func TestExclusiveLeaseDoesNotCrossProfiles(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "One"})
	profileOne := payload["profile"].(map[string]any)["id"].(string)
	_, payload = f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Two"})
	profileTwo := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{"profile_id": profileOne})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("profile one lease: %d %#v", response.StatusCode, payload)
	}
	grantOne := payload["grant"].(map[string]any)["id"].(string)
	response, payload = f.do(t, http.MethodPost, "/api/v1/browser-grants/"+grantOne+"/tabs", map[string]any{"url": "https://one.example/"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("profile one tab: %d %#v", response.StatusCode, payload)
	}
	tabOne := payload["tab"].(map[string]any)["id"].(string)
	otherTask := createHardwareAPITask(t, f.store)
	response, payload = f.do(t, http.MethodPost, "/api/v1/tasks/"+otherTask.ID+"/browser-grants", map[string]any{"profile_id": profileTwo})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("profile two lease: %d %#v", response.StatusCode, payload)
	}
	grantTwo := payload["grant"].(map[string]any)["id"].(string)
	response, payload = f.do(t, http.MethodGet, "/api/v1/browser-grants/"+grantTwo+"/tabs/"+tabOne+"/snapshot", nil)
	if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_tab_forbidden" {
		t.Fatalf("cross-profile snapshot: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.do(t, http.MethodPost, "/api/v1/browser-grants/"+grantTwo+"/tabs", map[string]any{"url": "https://two.example/"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("profile two open: %d %#v", response.StatusCode, payload)
	}
}
