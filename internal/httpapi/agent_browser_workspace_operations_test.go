package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestBrowserMCPOperationsUseExistingGrantAndKeepSecretValuesOut(t *testing.T) {
	f := newBrowserMCPFixture(t, "https://example.test/start", "Fixture")
	opened := f.call(t, "browser_tab_open", map[string]any{"grant_id": f.grantID, "url": "https://example.test/work?token=URL-SECRET-MARKER"})
	if opened.Status != http.StatusOK || opened.IsError || strings.Contains(opened.Text, "URL-SECRET-MARKER") || strings.Contains(opened.Text, "session_id") {
		t.Fatalf("open=%#v", opened)
	}
	var payload struct {
		Tab struct {
			ID string `json:"id"`
		} `json:"tab"`
	}
	if json.Unmarshal([]byte(opened.Text), &payload) != nil || payload.Tab.ID == "" {
		t.Fatal("open did not return tab identifier")
	}
	tab := payload.Tab.ID
	navigated := f.call(t, "browser_navigate", map[string]any{"grant_id": f.grantID, "tab_id": tab, "url": "https://example.test/next?auth=URL-SECRET-MARKER"})
	if navigated.IsError || strings.Contains(navigated.Text, "URL-SECRET-MARKER") {
		t.Fatalf("navigate=%#v", navigated)
	}

	blocked := f.call(t, "browser_action", map[string]any{"grant_id": f.grantID, "tab_id": tab, "kind": "click", "x": 10, "y": 20})
	if !blocked.IsError || len(f.host.ActionCalls) != 0 {
		t.Fatal("missing interact capability reached host")
	}
	response, grantPayload := f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{"profile_id": f.profileID, "capabilities": domain.BrowserCapabilities})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("owner fixture grant update status=%d", response.StatusCode)
	}
	f.grantID = grantPayload["grant"].(map[string]any)["id"].(string)
	action := f.call(t, "browser_action", map[string]any{"grant_id": f.grantID, "tab_id": tab, "kind": "click", "x": 10, "y": 20})
	if action.IsError || len(f.host.ActionCalls) != 1 {
		t.Fatalf("action=%#v calls=%d", action, len(f.host.ActionCalls))
	}
	upload := f.call(t, "browser_upload", map[string]any{"grant_id": f.grantID, "tab_id": tab, "ref": "e1", "files": []map[string]any{{"name": "fixture.txt", "content_base64": "Zml4dHVyZQ=="}}})
	if upload.IsError || len(f.host.UploadCalls) != 1 {
		t.Fatalf("upload=%#v", upload)
	}
	frame := f.call(t, "browser_frame", map[string]any{"grant_id": f.grantID, "tab_id": tab})
	if frame.IsError || !strings.Contains(frame.Text, "image/jpeg") || f.host.FrameCalls != 1 {
		t.Fatalf("frame=%#v", frame)
	}
	f.host.CookieList = []browser.HostCookie{{Name: "session", Value: "COOKIE-SECRET-MARKER", Domain: "example.test", Path: "/", Secure: true, HTTPOnly: true}}
	cookies := f.call(t, "browser_cookie_metadata", map[string]any{"grant_id": f.grantID, "domain": "example.test"})
	if cookies.IsError || strings.Contains(cookies.Text, "COOKIE-SECRET-MARKER") || strings.Contains(cookies.Text, `"value"`) || !strings.Contains(cookies.Text, `"secure":true`) {
		t.Fatalf("cookie metadata=%#v", cookies)
	}
	closed := f.call(t, "browser_tab_close", map[string]any{"grant_id": f.grantID, "tab_id": tab})
	if closed.IsError || f.host.CloseTabCalls != 1 {
		t.Fatalf("close=%#v", closed)
	}
	released := f.call(t, "browser_release", map[string]any{"grant_id": f.grantID})
	if released.IsError {
		t.Fatalf("release=%#v", released)
	}
	denied := f.call(t, "browser_tab_open", map[string]any{"grant_id": f.grantID, "url": "https://example.test/forbidden"})
	if denied.Status != http.StatusForbidden {
		t.Fatalf("revoked grant still usable: %#v", denied)
	}
}

func TestWorkspaceMCPManagedProcessFixture(t *testing.T) {
	if os.Getenv("AHA_MCP_PROCESS_FIXTURE") != "1" {
		return
	}
	<-time.After(time.Minute)
}

func TestWorkspaceMCPStartsAndStopsOnlyCurrentTaskProcess(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "")
	token := h.issue(task.ID, "main", turn.ID)
	url := workspaceMCPURL(h)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	started := coreMCPCallTool(t, url, token, "workspace_process_start", map[string]any{"name": "fixture", "executable": executable, "args": []string{"-test.run=^TestWorkspaceMCPManagedProcessFixture$"}, "env": map[string]string{"AHA_MCP_PROCESS_FIXTURE": "1"}})
	if started.Status != http.StatusOK || started.IsError {
		t.Fatalf("start=%#v", started)
	}
	status, err := h.processes.Status(task.ID, "fixture")
	if err != nil || status.State != "running" {
		t.Fatalf("process=%#v err=%v", status, err)
	}
	other := createCoreTaskInProject(t, h, task, "workspace-write")
	otherTurn := createCoreTurn(t, h.database, other, "main", "")
	otherToken := h.issue(other.ID, "main", otherTurn.ID)
	denied := coreMCPCallTool(t, url, otherToken, "workspace_process_stop", map[string]any{"name": "fixture"})
	if !denied.IsError {
		t.Fatalf("cross Task stop=%#v", denied)
	}
	status, _ = h.processes.Status(task.ID, "fixture")
	if status.State != "running" {
		t.Fatal("other Task stopped process")
	}
	readOnly := createCoreTaskInProject(t, h, task, "read-only")
	readOnlyTurn := createCoreTurn(t, h.database, readOnly, "main", "")
	readOnlyToken := h.issue(readOnly.ID, "main", readOnlyTurn.ID)
	_, names, _ := coreMCPToolsList(t, url, readOnlyToken)
	if coreMCPNameIncluded(names, "workspace_process_start") {
		t.Fatal("read-only Task sees start tool")
	}
	denied = coreMCPCallTool(t, url, readOnlyToken, "workspace_process_start", map[string]any{"name": "blocked", "executable": executable})
	if denied.Status != http.StatusForbidden {
		t.Fatalf("read-only start=%#v", denied)
	}
	stopped := coreMCPCallTool(t, url, token, "workspace_process_stop", map[string]any{"name": "fixture"})
	if stopped.IsError {
		t.Fatalf("stop=%#v", stopped)
	}
	status, _ = h.processes.Status(task.ID, "fixture")
	if status.State == "running" {
		t.Fatal("process still running")
	}
}

func TestAgentOperationCancellationAndHeadersCannotImpersonateOwner(t *testing.T) {
	called := 0
	op := agentMCPOperation{Tool: mcpTool{InputSchema: mcpObject(map[string]any{})}, Method: http.MethodPost, Handler: func(w http.ResponseWriter, r *http.Request) {
		called++
		for _, key := range []string{"Cookie", "Authorization", "X-CSRF-Token"} {
			if r.Header.Get(key) != "" {
				t.Errorf("forwarded %s", key)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}}
	request := httptest.NewRequest(http.MethodPost, "/fixed", nil)
	request.Header.Set("Cookie", "owner=DO-NOT-FORWARD")
	request.Header.Set("Authorization", "Bearer DO-NOT-FORWARD")
	request.Header.Set("X-CSRF-Token", "DO-NOT-FORWARD")
	if _, err := runAgentMCPOperation(request, op, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	if _, err := runAgentMCPOperation(request.WithContext(ctx), op, json.RawMessage(`{}`)); err == nil || called != 1 {
		t.Fatal("cancelled operation reached handler")
	}
}
