package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/browser/browsertest"
)

func agentGrantFixture(t *testing.T, f *browserAPIFixture, capabilities []string) (string, string) {
	t.Helper()
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Work"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{
		"profile_id": profileID, "capabilities": capabilities,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create grant: %d %#v", response.StatusCode, payload)
	}
	grantID := payload["grant"].(map[string]any)["id"].(string)
	f.host.SetCurrentPages(profileID, []browser.HostTab{
		{ID: "host-tab-agent", URL: "https://example.com/login", Title: "Login", Active: true},
	})
	token := f.agentToken(t, f.task.ID)
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/current", token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bind current: %d %#v", response.StatusCode, payload)
	}
	return grantID, payload["tab"].(map[string]any)["id"].(string)
}

func TestAgentBrowserPageActionsOverHTTP(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	grantID, tabID := agentGrantFixture(t, f, []string{
		"open_tab", "navigate", "snapshot", "interact", "files", "cookies",
	})
	token := f.agentToken(t, f.task.ID)

	response, payload := f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/actions", token,
		map[string]any{"kind": "type", "ref": "e1-2", "text": "2fa-code"})
	if response.StatusCode != http.StatusOK || payload["action"].(map[string]any)["kind"] != "type" {
		t.Fatalf("agent action: %d %#v", response.StatusCode, payload)
	}
	if len(host.ActionCalls) != 1 || host.ActionCalls[0].Text != "2fa-code" {
		t.Fatalf("host action calls=%#v", host.ActionCalls)
	}
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/actions", token,
		map[string]any{"kind": "click"})
	if response.StatusCode != http.StatusBadRequest || payload["error"] != "browser_action_invalid" {
		t.Fatalf("invalid action: %d %#v", response.StatusCode, payload)
	}

	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/files", token,
		map[string]any{"ref": "e1-3", "files": []map[string]any{
			{"name": "note.txt", "content_base64": "aGVsbG8="},
		}})
	if response.StatusCode != http.StatusOK || payload["upload"].(map[string]any)["file_count"].(float64) != 1 {
		t.Fatalf("agent upload: %d %#v", response.StatusCode, payload)
	}
	// The default JSON decoder limit is 1 MiB; an upload up to the declared
	// per-file limit must still decode.
	large := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("a"), 3<<20))
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/files", token,
		map[string]any{"ref": "e1-3", "files": []map[string]any{
			{"name": "large.bin", "content_base64": large},
		}})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("large upload: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/files", token,
		map[string]any{"ref": "e1-3", "files": []map[string]any{
			{"name": "../escape.txt", "content_base64": "aGVsbG8="},
		}})
	if response.StatusCode != http.StatusBadRequest || payload["error"] != "browser_upload_invalid" {
		t.Fatalf("path filename upload: %d %#v", response.StatusCode, payload)
	}

	host.DownloadsList = []browser.HostDownload{
		{ID: "download-0123456789abcdef01234567", Filename: "report.csv", Size: 8},
	}
	response, payload = f.agentDo(t, http.MethodGet,
		"/api/v1/agent/browser/grants/"+grantID+"/downloads", token, nil)
	if response.StatusCode != http.StatusOK || len(payload["downloads"].([]any)) != 1 {
		t.Fatalf("agent downloads: %d %#v", response.StatusCode, payload)
	}
	host.DownloadFiles = map[string]browser.HostDownloadFile{
		"download-0123456789abcdef01234567": {
			Download: browser.HostDownload{Filename: "report.csv", Size: int64(len("a,b\n1,2\n"))},
			Body:     io.NopCloser(bytes.NewReader([]byte("a,b\n1,2\n"))),
		},
	}
	downloadRequest, err := http.NewRequest(http.MethodGet,
		f.server.URL+"/api/v1/agent/browser/grants/"+grantID+"/downloads/download-0123456789abcdef01234567", nil)
	if err != nil {
		t.Fatal(err)
	}
	downloadRequest.Header.Set("Authorization", "Bearer "+token)
	downloadResponse, err := http.DefaultClient.Do(downloadRequest)
	if err != nil {
		t.Fatal(err)
	}
	downloadBody, _ := io.ReadAll(downloadResponse.Body)
	downloadResponse.Body.Close()
	if downloadResponse.StatusCode != http.StatusOK || string(downloadBody) != "a,b\n1,2\n" ||
		downloadResponse.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(downloadResponse.Header.Get("Content-Disposition"), "report.csv") {
		t.Fatalf("agent download: %d %q %q", downloadResponse.StatusCode, downloadBody, downloadResponse.Header.Get("Content-Disposition"))
	}
	response, payload = f.agentDo(t, http.MethodGet,
		"/api/v1/agent/browser/grants/"+grantID+"/downloads/..%2F..%2Fetc%2Fpasswd", token, nil)
	if response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusNotFound {
		t.Fatalf("path traversal download id: %d %#v", response.StatusCode, payload)
	}

	host.CookieList = []browser.HostCookie{{
		Name: "session", Value: "cookie-secret", Domain: "example.com", Path: "/",
		Expires: -1, HTTPOnly: true, Secure: true, SameSite: "Lax",
	}}
	response, payload = f.agentDo(t, http.MethodGet,
		"/api/v1/agent/browser/grants/"+grantID+"/cookies?url=https%3A%2F%2Fexample.com%2F", token, nil)
	if response.StatusCode != http.StatusOK || payload["count"].(float64) != 1 {
		t.Fatalf("agent cookies: %d %#v", response.StatusCode, payload)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cookie export must be no-store: %q", response.Header.Get("Cache-Control"))
	}
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/cookies", token, map[string]any{
			"cookies": []map[string]any{{
				"name": "imported", "value": "import-secret", "domain": "example.com",
				"path": "/", "expires": -1, "secure": true, "same_site": "None",
			}},
		})
	if response.StatusCode != http.StatusOK || payload["imported"].(float64) != 1 {
		t.Fatalf("agent cookie import: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/cookies", token, map[string]any{
			"cookies": []map[string]any{{
				"name": "bad", "value": "x", "domain": "example.com", "path": "/",
				"expires": -1, "same_site": "None",
			}},
		})
	if response.StatusCode != http.StatusBadRequest || payload["error"] != "browser_cookie_invalid" {
		t.Fatalf("cookie import without secure: %d %#v", response.StatusCode, payload)
	}
}

func TestAgentBrowserOldGrantCannotUseNewEndpointsOverHTTP(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	grantID, tabID := agentGrantFixture(t, f, []string{"open_tab", "navigate", "snapshot"})
	token := f.agentToken(t, f.task.ID)

	for name, call := range map[string]func() (*http.Response, map[string]any){
		"action": func() (*http.Response, map[string]any) {
			return f.agentDo(t, http.MethodPost,
				"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/actions", token,
				map[string]any{"kind": "click", "ref": "e1-1"})
		},
		"upload": func() (*http.Response, map[string]any) {
			return f.agentDo(t, http.MethodPost,
				"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/files", token,
				map[string]any{"ref": "e1-1", "files": []map[string]any{{"name": "a.txt", "content_base64": "YQ=="}}})
		},
		"downloads": func() (*http.Response, map[string]any) {
			return f.agentDo(t, http.MethodGet,
				"/api/v1/agent/browser/grants/"+grantID+"/downloads", token, nil)
		},
		"cookies": func() (*http.Response, map[string]any) {
			return f.agentDo(t, http.MethodGet,
				"/api/v1/agent/browser/grants/"+grantID+"/cookies?domain=example.com", token, nil)
		},
	} {
		response, payload := call()
		if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_capability_denied" {
			t.Fatalf("%s with old grant: %d %#v", name, response.StatusCode, payload)
		}
	}

	otherTask := createHardwareAPITask(t, f.store)
	otherToken := f.agentToken(t, otherTask.ID)
	response, payload := f.agentDo(t, http.MethodGet,
		"/api/v1/agent/browser/grants/"+grantID+"/cookies?domain=example.com", otherToken, nil)
	if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_grant_forbidden" {
		t.Fatalf("cross-task cookies: %d %#v", response.StatusCode, payload)
	}
}

func TestAgentBrowserActionFailsClosedAfterRelease(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	grantID, tabID := agentGrantFixture(t, f, []string{"snapshot", "interact"})
	token := f.agentToken(t, f.task.ID)
	response, payload := f.agentDo(t, http.MethodDelete,
		"/api/v1/agent/browser/grants/"+grantID, token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("release: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/actions", token,
		map[string]any{"kind": "click", "ref": "e1-1"})
	if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_grant_revoked" {
		t.Fatalf("action after release: %d %#v", response.StatusCode, payload)
	}
	if len(host.ActionCalls) != 0 {
		t.Fatalf("released action reached the host: %#v", host.ActionCalls)
	}
}

// ownerGrantFixture creates the session-authenticated grant the Web panel uses.
// The owner route shares the service layer with the Agent route; only the actor,
// source and accepted action kinds differ.
func ownerGrantFixture(t *testing.T, f *browserAPIFixture, capabilities []string) (grantID, tabID string) {
	t.Helper()
	_, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Owner Web"})
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{
		"profile_id": profileID, "capabilities": capabilities,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create owner grant: %d %#v", response.StatusCode, payload)
	}
	grantID = payload["grant"].(map[string]any)["id"].(string)
	f.host.SetCurrentPages(profileID, []browser.HostTab{
		{ID: "host-tab-owner", URL: "https://example.com/login", Title: "Login", Active: true},
	})
	response, payload = f.do(t, http.MethodPost, "/api/v1/browser-grants/"+grantID+"/tabs/current", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bind owner tab: %d %#v", response.StatusCode, payload)
	}
	return grantID, payload["tab"].(map[string]any)["id"].(string)
}

func TestOwnerBrowserPageActionsOverHTTP(t *testing.T) {
	ctx := context.Background()
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	grantID, tabID := ownerGrantFixture(t, f, []string{"open_tab", "navigate", "snapshot", "interact"})
	path := "/api/v1/browser-grants/" + grantID + "/tabs/" + tabID + "/actions"

	response, payload := f.do(t, http.MethodPost, path, map[string]any{"kind": "click", "x": 100, "y": 60})
	if response.StatusCode != http.StatusOK || payload["action"].(map[string]any)["kind"] != "click" {
		t.Fatalf("owner click: %d %#v", response.StatusCode, payload)
	}
	if len(host.ActionCalls) != 1 || host.ActionCalls[0].X == nil || *host.ActionCalls[0].X != 100 {
		t.Fatalf("owner click host calls=%#v", host.ActionCalls)
	}

	for name, body := range map[string]map[string]any{
		"text":          {"kind": "text", "text": "owner-typed-2FA-987654"},
		"context click": {"kind": "context_click", "x": 10, "y": 20},
		"drag":          {"kind": "drag", "x": 1, "y": 2, "to_x": 30, "to_y": 40},
	} {
		response, payload = f.do(t, http.MethodPost, path, body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("owner %s: %d %#v", name, response.StatusCode, payload)
		}
	}

	// Agent capability cannot use the Owner-only kinds; the boundary lives in
	// the service, not in the Web client.
	token := f.agentToken(t, f.task.ID)
	response, payload = f.agentDo(t, http.MethodPost,
		"/api/v1/agent/browser/grants/"+grantID+"/tabs/"+tabID+"/actions", token,
		map[string]any{"kind": "text", "text": "agent-should-not-type"})
	if response.StatusCode != http.StatusBadRequest || payload["error"] != "browser_action_invalid" {
		t.Fatalf("agent owner-only kind: %d %#v", response.StatusCode, payload)
	}

	// The audit trail names the owner and the grant/tab, never the typed text.
	events, err := f.store.BrowserAuditEvents(ctx, "browser_tab.action", 10)
	if err != nil || len(events) != 4 {
		t.Fatalf("owner action audit=%#v err=%v", events, err)
	}
	for _, event := range events {
		encoded, _ := json.Marshal(event.Data)
		text := string(encoded)
		// A Web session actor is the owner account id (owner_...), never an
		// agent:<agent>@<turn> identity.
		if !strings.HasPrefix(event.OwnerID, "owner") {
			t.Fatalf("owner action actor=%q", event.OwnerID)
		}
		if !strings.Contains(text, `"source":"owner"`) || !strings.Contains(text, `"grant_id":"`+grantID+`"`) {
			t.Fatalf("owner action audit lost provenance: %s", text)
		}
		if strings.Contains(text, "owner-typed-2FA-987654") {
			t.Fatalf("owner action audit leaked typed text: %s", text)
		}
	}

	// The same route fails closed once the grant is released.
	if response, payload := f.do(t, http.MethodDelete, "/api/v1/browser-grants/"+grantID, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("release owner grant: %d %#v", response.StatusCode, payload)
	}
	response, payload = f.do(t, http.MethodPost, path, map[string]any{"kind": "click", "x": 1, "y": 1})
	if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_grant_revoked" {
		t.Fatalf("owner action after release: %d %#v", response.StatusCode, payload)
	}
	if len(host.ActionCalls) != 4 {
		t.Fatalf("released owner action reached the host: %#v", host.ActionCalls)
	}
}

func TestOwnerBrowserActionsRequireInteractOverHTTP(t *testing.T) {
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	grantID, tabID := ownerGrantFixture(t, f, []string{"open_tab", "navigate", "snapshot"})
	response, payload := f.do(t, http.MethodPost,
		"/api/v1/browser-grants/"+grantID+"/tabs/"+tabID+"/actions",
		map[string]any{"kind": "click", "x": 1, "y": 1})
	if response.StatusCode != http.StatusForbidden || payload["error"] != "browser_capability_denied" {
		t.Fatalf("owner action without interact: %d %#v", response.StatusCode, payload)
	}
	if len(host.ActionCalls) != 0 {
		t.Fatalf("capability-denied action reached the host: %#v", host.ActionCalls)
	}
}
