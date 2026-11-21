package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/browser/browsertest"
	"github.com/ChinaKai/AHA2/internal/desktop"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// createPetTaskInStore is createPetDomainHTTPTask for fixtures that own their
// store: it creates the real task_pet in project_pet and grants the pet live
// view and Task-management permission, the two switches these domains read.
func createPetTaskInStore(t *testing.T, database *store.Store, base domain.Task) domain.Task {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := database.CreateProject(ctx, domain.Project{ID: "project_pet", Name: "Pet", ProjectType: "system", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, domain.Workspace{ID: "workspace_pet", ProjectID: "project_pet", Name: "Pet", Transport: "native", Locality: "local", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.RuntimeSnapshot(ctx, base.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := app.NewService(database, nil, app.StubExecutor{}).CreateTask(ctx, app.CreateTaskInput{ID: "task_pet", ProjectID: "project_pet", WorkspaceID: "workspace_pet", Title: "Pet", Request: "fixture only", ModelID: snapshot.ModelID, Filesystem: "danger-full-access", Approval: "never", CollaborationMode: "single", MaxAgents: 1, AllowSystemProject: true, StartMode: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	setPetSharedControlPermissions(t, database, true, true)
	return task
}

// setPetSharedControlPermissions sets the live pet switches: view gates every
// read of these domains, Task management additionally gates writes.
func setPetSharedControlPermissions(t *testing.T, database *store.Store, view, manageTasks bool) {
	t.Helper()
	now := time.Now().UTC()
	assistant, err := database.EnsureOwnerAssistant(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateOwnerPetPermissions(context.Background(), assistant.ID, view, manageTasks, false, false, false, true, false, false, now); err != nil {
		t.Fatal(err)
	}
}

// refused reports a call stopped by the server, either at the HTTP boundary
// (403 before dispatch) or as a tool error result.
func refused(call coreMCPToolCall) bool { return call.Status == http.StatusForbidden || call.IsError }

func decodeMCPText(t *testing.T, call coreMCPToolCall, target any) {
	t.Helper()
	if call.IsError || json.Unmarshal([]byte(call.Text), target) != nil {
		t.Fatalf("tool result: status=%d isError=%v text=%s", call.Status, call.IsError, call.Text)
	}
}

// Every shared-browser operation tool, called by an ordinary Task and by the
// pet through the same aha2_browser server, reaches the real browser service
// and host under the owner's existing grant. The pet additionally loses the
// domain the moment its live permission is withdrawn, without a host call.
func TestBrowserMCPEveryOperationForOrdinaryAndPetTasks(t *testing.T) {
	for _, pet := range []bool{false, true} {
		name := "ordinary"
		if pet {
			name = "pet"
		}
		t.Run(name, func(t *testing.T) {
			host := browsertest.New()
			f := newBrowserAPIFixture(t, host)
			if pet {
				f.task = createPetTaskInStore(t, f.store, f.task)
			}
			response, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "Shared"})
			if response.StatusCode != http.StatusCreated {
				t.Fatalf("profile: %d %#v", response.StatusCode, payload)
			}
			profileID := payload["profile"].(map[string]any)["id"].(string)
			response, payload = f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants", map[string]any{"profile_id": profileID, "capabilities": domain.BrowserCapabilities})
			if response.StatusCode != http.StatusCreated {
				t.Fatalf("owner grant: %d %#v", response.StatusCode, payload)
			}
			grant := payload["grant"].(map[string]any)["id"].(string)
			host.SetCurrentPages(profileID, []browser.HostTab{{ID: "host-current", URL: "https://example.test/current", Title: "Current", Active: true}})
			host.BookmarkList = []browser.HostBookmark{{Title: "Docs", URL: "https://example.test/docs"}}
			download := "download-0123456789abcdef01234567"
			host.DownloadsList = []browser.HostDownload{{ID: download, Filename: "report.csv", Size: 8}}
			host.DownloadFiles = map[string]browser.HostDownloadFile{download: {Download: browser.HostDownload{ID: download, Filename: "report.csv", Size: 8}, Body: io.NopCloser(bytes.NewReader([]byte("a,b\n1,2\n")))}}
			_, token := browserMCPToken(t, f, f.task, "batch-shared-"+name)
			call := func(tool string, args map[string]any) coreMCPToolCall {
				t.Helper()
				return coreMCPCallTool(t, browserMCPURL(f), token, tool, args)
			}
			status, names, _ := coreMCPToolsList(t, browserMCPURL(f), token)
			if status != http.StatusOK || len(names) != len((&Server{}).browserMCPToolsFor(true)) {
				t.Fatalf("tools/list status=%d names=%v", status, names)
			}
			var bound struct {
				Tab struct{ ID string } `json:"tab"`
			}
			decodeMCPText(t, call("browser_tab_bind_current", map[string]any{"grant_id": grant}), &bound)
			var opened struct {
				Tab struct{ ID string } `json:"tab"`
			}
			decodeMCPText(t, call("browser_tab_open", map[string]any{"grant_id": grant, "url": "https://example.test/work"}), &opened)
			if bound.Tab.ID == "" || opened.Tab.ID == "" || host.OpenTabCalls != 1 {
				t.Fatalf("bind=%q open=%q host opens=%d", bound.Tab.ID, opened.Tab.ID, host.OpenTabCalls)
			}
			tab := opened.Tab.ID
			if result := call("browser_status", map[string]any{}); result.IsError || !strings.Contains(result.Text, grant) {
				t.Fatalf("status=%#v", result)
			}
			if result := call("browser_tabs_list", map[string]any{"grant_id": grant}); result.IsError || !strings.Contains(result.Text, tab) {
				t.Fatalf("tabs=%#v", result)
			}
			if result := call("browser_snapshot", map[string]any{"grant_id": grant, "tab_id": tab}); result.IsError {
				t.Fatalf("snapshot=%#v", result)
			}
			host.CookieList = []browser.HostCookie{{Name: "session", Value: "SHARED-COOKIE-VALUE", Domain: "example.test", Path: "/", Secure: true}}
			if result := call("browser_cookie_metadata", map[string]any{"grant_id": grant, "domain": "example.test"}); result.IsError || strings.Contains(result.Text, "SHARED-COOKIE-VALUE") || !strings.Contains(result.Text, `"name":"session"`) {
				t.Fatalf("cookies=%#v", result)
			}
			if result := call("browser_tab_activate", map[string]any{"grant_id": grant, "tab_id": bound.Tab.ID}); result.IsError || host.ActivateCalls != 1 {
				t.Fatalf("activate=%#v calls=%d", result, host.ActivateCalls)
			}
			if result := call("browser_navigate", map[string]any{"grant_id": grant, "tab_id": tab, "url": "https://example.test/next"}); result.IsError {
				t.Fatalf("navigate=%#v", result)
			}
			if result := call("browser_action", map[string]any{"grant_id": grant, "tab_id": tab, "kind": "click", "x": 3, "y": 4}); result.IsError || len(host.ActionCalls) != 1 {
				t.Fatalf("action=%#v", result)
			}
			if result := call("browser_upload", map[string]any{"grant_id": grant, "tab_id": tab, "ref": "e1", "files": []map[string]any{{"name": "a.txt", "content_base64": "YQ=="}}}); result.IsError || len(host.UploadCalls) != 1 {
				t.Fatalf("upload=%#v", result)
			}
			if result := call("browser_frame", map[string]any{"grant_id": grant, "tab_id": tab}); result.IsError || host.FrameCalls != 1 {
				t.Fatalf("frame=%#v", result)
			}
			var bookmarks struct {
				Bookmarks []map[string]any `json:"bookmarks"`
			}
			decodeMCPText(t, call("browser_bookmarks_list", map[string]any{"grant_id": grant}), &bookmarks)
			if len(bookmarks.Bookmarks) != 1 || host.BookmarkCalls != 1 {
				t.Fatalf("bookmarks=%#v calls=%d", bookmarks, host.BookmarkCalls)
			}
			var downloads struct {
				Downloads []map[string]any `json:"downloads"`
			}
			decodeMCPText(t, call("browser_downloads_list", map[string]any{"grant_id": grant}), &downloads)
			if len(downloads.Downloads) != 1 || downloads.Downloads[0]["id"] != download {
				t.Fatalf("downloads=%#v", downloads)
			}
			fetched := call("browser_download_get", map[string]any{"grant_id": grant, "download_id": download})
			if fetched.IsError || !strings.Contains(fetched.Text, "YSxiCjEsMgo=") {
				t.Fatalf("download=%#v", fetched)
			}
			if result := call("browser_tab_close", map[string]any{"grant_id": grant, "tab_id": tab}); result.IsError || host.CloseTabCalls != 1 {
				t.Fatalf("close=%#v", result)
			}
			if pet {
				// Withdrawing the live view permission removes the domain at once:
				// no tools are listed and calls fail before the browser host.
				before := host.OpenTabCalls
				setPetSharedControlPermissions(t, f.store, false, true)
				status, names, _ := coreMCPToolsList(t, browserMCPURL(f), token)
				denied := call("browser_tab_open", map[string]any{"grant_id": grant, "url": "https://example.test/after-revoke"})
				if (status == http.StatusOK && len(names) != 0) || !refused(denied) || host.OpenTabCalls != before {
					t.Fatalf("revoked pet kept browser access: status=%d names=%v denied=%#v", status, names, denied)
				}
				// Without Task management the pet keeps reads but loses writes.
				setPetSharedControlPermissions(t, f.store, true, false)
				if reads := call("browser_bookmarks_list", map[string]any{"grant_id": grant}); reads.IsError {
					t.Fatalf("view-only pet lost reads: %#v", reads)
				}
				if write := call("browser_tab_open", map[string]any{"grant_id": grant, "url": "https://example.test/view-only"}); !refused(write) || host.OpenTabCalls != before {
					t.Fatalf("view-only pet could write: %#v", write)
				}
				setPetSharedControlPermissions(t, f.store, true, true)
			}
			if result := call("browser_release", map[string]any{"grant_id": grant}); result.IsError {
				t.Fatalf("release=%#v", result)
			}
			if result := call("browser_tab_open", map[string]any{"grant_id": grant, "url": "https://example.test/released"}); !refused(result) {
				t.Fatalf("released grant still usable: %#v", result)
			}
		})
	}
}

// The pet drives the shared desktop through aha2_desktop exactly as an
// ordinary Task: only the share the owner opened for task_pet, a single-use
// observation per action, and nothing after live permission is withdrawn.
func TestDesktopMCPOperationsForPetUseOwnerShareAndLivePermission(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	pet := createPetDomainHTTPTask(t, h, base)
	provider := &desktopFixture{}
	manager := desktop.New(provider)
	t.Cleanup(func() { _ = manager.Close() })
	server := httptest.NewServer(New(Config{Store: h.database, App: h.service, AgentCapabilities: h.caps, Desktop: manager}).Handler())
	t.Cleanup(server.Close)
	turn := createCoreTurn(t, h.database, pet, "main", "")
	token := h.issue(pet.ID, "main", turn.ID)
	url := server.URL + "/api/v1/agent/desktop/mcp"
	status, names, _ := coreMCPToolsList(t, url, token)
	if status != http.StatusOK || len(names) != 5 {
		t.Fatalf("pet desktop discovery=%v status=%d", names, status)
	}
	shared, err := manager.OpenSharedTarget(h.ctx, pet.ID, desktop.TargetSelection{Kind: "window", WindowID: "fixture"}, "foreground", true)
	if err != nil {
		t.Fatal(err)
	}
	session := shared.Session
	if result := coreMCPCallTool(t, url, token, "desktop_status", map[string]any{}); result.IsError || !strings.Contains(result.Text, session.ID) {
		t.Fatalf("status=%#v", result)
	}
	if result := coreMCPCallTool(t, url, token, "desktop_control", map[string]any{"session_id": session.ID, "revision": session.Revision, "controller": "agent"}); result.IsError {
		t.Fatalf("control=%#v", result)
	}
	var observed struct {
		Observation desktop.Observation `json:"observation"`
	}
	decodeMCPText(t, coreMCPCallTool(t, url, token, "desktop_observe", map[string]any{"session_id": session.ID}), &observed)
	args := map[string]any{"session_id": session.ID, "revision": observed.Observation.Revision, "observation_id": observed.Observation.ID, "kind": "click", "element_id": "$surface", "x": 5, "y": 5, "button": "left"}
	if result := coreMCPCallTool(t, url, token, "desktop_action", args); result.IsError || provider.calls.Load() != 1 {
		t.Fatalf("action=%#v calls=%d", result, provider.calls.Load())
	}
	if replay := coreMCPCallTool(t, url, token, "desktop_action", args); !refused(replay) || provider.calls.Load() != 1 {
		t.Fatal("pet replayed a single-use observation")
	}
	setPetSharedControlPermissions(t, h.database, false, true)
	if denied := coreMCPCallTool(t, url, token, "desktop_status", map[string]any{}); !refused(denied) {
		t.Fatalf("revoked pet still reads desktop: %#v", denied)
	}
	setPetSharedControlPermissions(t, h.database, true, false)
	if denied := coreMCPCallTool(t, url, token, "desktop_stop", map[string]any{"session_id": session.ID}); !refused(denied) || manager.Status(pet.ID).Session == nil {
		t.Fatal("view-only pet stopped the share")
	}
	setPetSharedControlPermissions(t, h.database, true, true)
	if result := coreMCPCallTool(t, url, token, "desktop_stop", map[string]any{"session_id": session.ID}); result.IsError || manager.Status(pet.ID).Session != nil {
		t.Fatalf("stop=%#v", result)
	}
}

// The pet manages processes of its own workspace through aha2_workspace with
// the same managed-process service an ordinary Task uses, and only its own.
func TestWorkspaceProcessMCPForPetIsScopedAndPermissionGated(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	pet := createPetDomainHTTPTask(t, h, base)
	setPetSharedControlPermissions(t, h.database, true, true)
	turn := createCoreTurn(t, h.database, pet, "main", "")
	token := h.issue(pet.ID, "main", turn.ID)
	url := workspaceMCPURL(h)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := map[string]any{"name": "pet-fixture", "executable": executable, "args": []string{"-test.run=^TestWorkspaceMCPManagedProcessFixture$"}, "env": map[string]string{"AHA_MCP_PROCESS_FIXTURE": "1"}}
	if result := coreMCPCallTool(t, url, token, "workspace_process_start", start); result.IsError {
		t.Fatalf("pet start=%#v", result)
	}
	t.Cleanup(func() { _, _ = h.processes.Stop(pet.ID, "pet-fixture") })
	if status, err := h.processes.Status(pet.ID, "pet-fixture"); err != nil || status.State != "running" {
		t.Fatalf("process=%#v err=%v", status, err)
	}
	for _, tool := range []string{"workspace_list", "workspace_runtime_list", "workspace_process_list"} {
		if result := coreMCPCallTool(t, url, token, tool, map[string]any{}); result.IsError {
			t.Fatalf("%s=%#v", tool, result)
		}
	}
	if result := coreMCPCallTool(t, url, token, "workspace_process_get", map[string]any{"name": "pet-fixture"}); result.IsError || !strings.Contains(result.Text, "running") {
		t.Fatalf("get=%#v", result)
	}
	// An ordinary Task cannot see or stop the pet's process.
	otherTurn := createCoreTurn(t, h.database, base, "main", "")
	otherToken := h.issue(base.ID, "main", otherTurn.ID)
	if result := coreMCPCallTool(t, url, otherToken, "workspace_process_stop", map[string]any{"name": "pet-fixture"}); !refused(result) {
		t.Fatal("ordinary Task stopped the pet process")
	}
	setPetSharedControlPermissions(t, h.database, true, false)
	if result := coreMCPCallTool(t, url, token, "workspace_process_stop", map[string]any{"name": "pet-fixture"}); !refused(result) {
		t.Fatal("view-only pet stopped a process")
	}
	if status, _ := h.processes.Status(pet.ID, "pet-fixture"); status.State != "running" {
		t.Fatal("refused stop still stopped the process")
	}
	setPetSharedControlPermissions(t, h.database, false, true)
	if result := coreMCPCallTool(t, url, token, "workspace_process_list", map[string]any{}); !refused(result) {
		t.Fatal("revoked pet still lists processes")
	}
	setPetSharedControlPermissions(t, h.database, true, true)
	if result := coreMCPCallTool(t, url, token, "workspace_process_stop", map[string]any{"name": "pet-fixture"}); result.IsError {
		t.Fatalf("pet stop=%#v", result)
	}
	if status, _ := h.processes.Status(pet.ID, "pet-fixture"); status.State == "running" {
		t.Fatal("pet process still running")
	}
}

// The pet delivers files the way an ordinary Task does: bytes through the
// Agent upload transport of its own Turn, then aha2_core.attachment_bind
// attaches them to one progress message. A second bind is refused.
func TestPetUploadsAndBindsOwnTurnAttachment(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	pet := createPetDomainHTTPTask(t, h, base)
	turn := createCoreTurn(t, h.database, pet, "main", "batch-pet-attach")
	token := h.issue(pet.ID, "main", turn.ID)
	response := agentMultipartRequest(t, h.server.URL+"/api/v1/agent/turn/attachments", token, "pet-note.txt", []byte("pet attachment body"))
	var uploaded struct {
		Attachment struct {
			ID string `json:"id"`
		} `json:"attachment"`
	}
	decodeResponse(t, response, &uploaded)
	if response.StatusCode != http.StatusCreated || uploaded.Attachment.ID == "" {
		t.Fatalf("pet upload status=%d", response.StatusCode)
	}
	url := h.server.URL + "/api/v1/agent/core/mcp"
	bound := coreMCPCallTool(t, url, token, "attachment_bind", map[string]any{"attachment_ids": []string{uploaded.Attachment.ID}, "message": "pet report"})
	if bound.Status != http.StatusOK || bound.IsError || !strings.Contains(bound.Text, uploaded.Attachment.ID) {
		t.Fatalf("pet bind=%#v", bound)
	}
	item, err := h.database.Attachment(h.ctx, pet.ID, uploaded.Attachment.ID)
	if err != nil || item.MessageID == "" {
		t.Fatalf("pet attachment not bound: %#v err=%v", item, err)
	}
	if again := coreMCPCallTool(t, url, token, "attachment_bind", map[string]any{"attachment_ids": []string{uploaded.Attachment.ID}}); !refused(again) {
		t.Fatal("pet bound the same attachment twice")
	}
}

// With an owner-configured mail binding on task_pet, the pet gets the same
// aha2_mail tools as an ordinary Task: account and template reads never carry
// the stored authorization code, and every send waits for owner approval.
func TestPetMailMCPUsesOwnerBindingAndSendStaysPendingApproval(t *testing.T) {
	caps := agentapi.NewCapabilities()
	f := newMailAPIFixtureWith(t, func(config *Config) { config.AgentCapabilities = caps })
	pet := createPetTaskInStore(t, f.database, f.task)
	turn := createCoreTurn(t, f.database, pet, "main", "batch-pet-mail")
	token, err := caps.Issue(pet.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	url := f.server.URL + "/api/v1/agent/mail/mcp"
	account := f.createAccount("PET-MAIL-SECRET-CODE")
	// The account exists but is not bound to task_pet yet: nothing is usable.
	if unbound := coreMCPCallTool(t, url, token, "mail_list_accounts", map[string]any{}); strings.Contains(unbound.Text, account["id"].(string)) {
		t.Fatalf("unbound account visible to the pet: %#v", unbound)
	}
	template := f.createTemplate(map[string]any{"scope": "global", "name": "Pet report", "subject_template": "[AHA] {{test.scope}}", "body_text_template": "范围：{{test.scope}}", "required_variables": []string{"test.scope"}})
	binding := f.request(http.MethodPost, "/api/v1/mail-bindings", map[string]any{"scope_type": "task", "scope_id": pet.ID, "account_id": account["id"], "default_template_id": template["id"], "approval_mode": "always", "enabled": true}, f.csrf)
	if binding["_status"].(float64) != http.StatusCreated {
		t.Fatalf("owner binding for task_pet = %#v", binding)
	}
	status, names, _ := coreMCPToolsList(t, url, token)
	if status != http.StatusOK || len(names) != 6 {
		t.Fatalf("pet mail tools status=%d names=%v", status, names)
	}
	call := func(name string, args map[string]any) coreMCPToolCall {
		t.Helper()
		result := coreMCPCallTool(t, url, token, name, args)
		if result.IsError || result.Status != http.StatusOK || strings.Contains(result.Text, "PET-MAIL-SECRET-CODE") {
			t.Fatalf("%s=%#v", name, result)
		}
		return result
	}
	if !strings.Contains(call("mail_list_accounts", map[string]any{}).Text, account["id"].(string)) {
		t.Fatal("account not listed")
	}
	if !strings.Contains(call("mail_list_templates", map[string]any{}).Text, template["id"].(string)) {
		t.Fatal("template not listed")
	}
	call("mail_template_detail", map[string]any{"template_id": template["id"]})
	call("mail_preview", map[string]any{"account_id": account["id"], "template_id": template["id"], "to": []string{"qa@example.com"}, "variables": map[string]string{"test.scope": "pet"}})
	sent := call("mail_send", map[string]any{"account_id": account["id"], "template_id": template["id"], "to": []string{"qa@example.com"}, "variables": map[string]string{"test.scope": "pet"}, "idempotency_key": "pet-mail-1"})
	if !strings.Contains(sent.Text, `"status":"pending_approval"`) {
		t.Fatalf("pet send was not held for approval: %s", sent.Text)
	}
	var message struct {
		ID               string `json:"message_id"`
		ApprovalRequired bool   `json:"approval_required"`
	}
	if json.Unmarshal([]byte(sent.Text), &message) != nil || message.ID == "" || !message.ApprovalRequired {
		t.Fatalf("send result: %s", sent.Text)
	}
	if !strings.Contains(call("mail_get_status", map[string]any{"message_id": message.ID}).Text, "pending_approval") {
		t.Fatal("status lost the pending approval")
	}
}
