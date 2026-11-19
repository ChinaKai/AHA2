package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/browser/browsertest"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func browserMCPURL(f *browserAPIFixture) string {
	return f.server.URL + "/api/v1/agent/browser/mcp"
}

// browserMCPToken issues a real capability for a real running Turn. The MCP
// facade revalidates AgentCallContext on every method, so a synthetic turn ID
// would not exercise the production path.
func browserMCPToken(t *testing.T, f *browserAPIFixture, task domain.Task, batchID string) (domain.Turn, string) {
	t.Helper()
	turn := createCoreTurn(t, f.store, task, "main", batchID)
	token, err := f.agents.Issue(task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return turn, token
}

// retireCoreTurn closes a running Turn and its Round so the same Task can start
// the next one. Only one Turn and one Round may be active per Task at a time.
func retireCoreTurn(t *testing.T, database *store.Store, turn domain.Turn) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	turn.Status = domain.TurnSucceeded
	turn.FinishedAt = now
	if err := database.UpdateTurn(ctx, turn, domain.TurnRunning); err != nil {
		t.Fatal(err)
	}
	round, err := database.Round(ctx, turn.RoundID)
	if err != nil {
		t.Fatal(err)
	}
	round.Status = domain.RoundCompleted
	round.FinishedAt = now
	if err := database.UpdateRound(ctx, round, domain.RoundRunning); err != nil {
		t.Fatal(err)
	}
}

type browserMCPFixture struct {
	*browserAPIFixture
	host      *browsertest.FakeHost
	profileID string
	grantID   string
	token     string
	turn      domain.Turn
}

// newBrowserMCPFixture builds one live lease through the Owner HTTP API, binds
// a page with credentials in its URL, then issues an Agent capability for the
// same Task.
func newBrowserMCPFixture(t *testing.T, pageURL, title string) *browserMCPFixture {
	t.Helper()
	host := browsertest.New()
	f := newBrowserAPIFixture(t, host)
	response, payload := f.do(t, http.MethodPost, "/api/v1/browser/profiles", map[string]any{"name": "MCP Work"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create profile: %d %#v", response.StatusCode, payload)
	}
	profileID := payload["profile"].(map[string]any)["id"].(string)
	response, payload = f.do(t, http.MethodPost, "/api/v1/tasks/"+f.task.ID+"/browser-grants",
		map[string]any{"profile_id": profileID})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create grant: %d %#v", response.StatusCode, payload)
	}
	grantID := payload["grant"].(map[string]any)["id"].(string)
	host.SetCurrentPages(profileID, []browser.HostTab{
		{ID: "host-tab-mcp", URL: pageURL, Title: title, Active: true},
	})
	turn, token := browserMCPToken(t, f, f.task, "batch-browser-mcp")
	return &browserMCPFixture{
		browserAPIFixture: f, host: host, profileID: profileID, grantID: grantID,
		token: token, turn: turn,
	}
}

func (f *browserMCPFixture) call(t *testing.T, name string, arguments any) coreMCPToolCall {
	t.Helper()
	return coreMCPCallTool(t, browserMCPURL(f.browserAPIFixture), f.token, name, arguments)
}

// seededBrowserGrant writes a lease row straight into the store so the test can
// hold a second, non-active grant while the HTTP-created lease keeps the domain
// injected. The MCP tool must classify the seeded lease before any host call.
func seededBrowserGrant(t *testing.T, database *store.Store, task domain.Task, id, state string) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	profileID := "profile-" + id
	sessionID := "session-" + id
	if err := database.UpsertBrowserProfile(ctx, domain.BrowserProfile{
		ID: profileID, Name: profileID, BrowserKind: "edge", HostID: "local", State: "ready",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateBrowserSession(ctx, domain.BrowserSession{
		ID: sessionID, ProfileID: profileID, HostID: "local", HostSessionID: "host-" + sessionID,
		State: domain.BrowserSessionStateReady, StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(time.Hour)
	revokedAt := time.Time{}
	switch state {
	case "revoked":
		revokedAt = now
	case "expired":
		expiresAt = now.Add(-time.Minute)
	}
	if err := database.CreateBrowserTaskGrant(ctx, domain.BrowserTaskGrant{
		ID: id, TaskID: task.ID, ProfileID: profileID, SessionID: sessionID,
		Capabilities: []string{"open_tab", "navigate", "snapshot"},
		ExpiresAt:    expiresAt, RevokedAt: revokedAt, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func waitForBrowserMCPAudit(t *testing.T, path, tool string) string {
	t.Helper()
	return waitForBrowserMCPAuditResult(t, path, tool, "ok")
}

// waitForBrowserMCPAuditResult waits for one audit row produced by the given
// tool and outcome. Audit rows are appended in the handler defer, so a client
// that already has the HTTP response may still need to wait for the row.
func waitForBrowserMCPAuditResult(t *testing.T, path, tool, result string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var data string
		err := db.QueryRowContext(context.Background(),
			`SELECT data_json FROM audit_events WHERE action='agent.browser_mcp.call' AND data_json LIKE ? AND data_json LIKE ? ORDER BY sequence DESC LIMIT 1`,
			`%"tool":"`+tool+`"%`, `%"result":"`+result+`"%`,
		).Scan(&data)
		if err == nil && data != "" {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("no browser MCP audit row: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertAuditActionLacks(t *testing.T, path, action, needle string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM audit_events WHERE action=? AND data_json LIKE ?`,
		action, "%"+needle+"%",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%q leaked into %d %s audit rows", needle, count, action)
	}
}

func TestAgentBrowserMCPRequiresCapabilityAndDomainGate(t *testing.T) {
	f := newBrowserMCPFixture(t, "https://example.com/login", "Login")
	url := browserMCPURL(f.browserAPIFixture)

	response := agentRequest(t, url, http.MethodPost, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = agentRequest(t, url, http.MethodPost, "not-a-real-token", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid capability status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	// A valid capability for a Task without an active lease is rejected by the
	// server-side domain gate: tool visibility is never authorization.
	otherTask := createHardwareAPITask(t, f.store)
	_, otherToken := browserMCPToken(t, f.browserAPIFixture, otherTask, "batch-browser-mcp-other")
	response = agentRequest(t, url, http.MethodPost, otherToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("un-injected initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = agentRequest(t, url, http.MethodPost, f.token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	var initialized struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	decodeResponse(t, response, &initialized)
	if initialized.Result.ServerInfo.Name != "aha2-browser" {
		t.Fatalf("serverInfo=%#v", initialized.Result.ServerInfo)
	}

	forwarded, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatal(err)
	}
	forwarded.Header.Set("Authorization", "Bearer "+f.token)
	forwarded.Header.Set("X-Forwarded-For", "10.0.0.1")
	forwardedResponse, err := http.DefaultClient.Do(forwarded)
	if err != nil {
		t.Fatal(err)
	}
	if forwardedResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("forwarded status=%d body=%s", forwardedResponse.StatusCode, readBody(t, forwardedResponse))
	}
	forwardedResponse.Body.Close()

	oversized := agentRequestRaw(t, url, http.MethodPost, f.token,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","padding":"`+strings.Repeat("a", 1<<20)+`"}`))
	if oversized.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d body=%s", oversized.StatusCode, readBody(t, oversized))
	}
	oversized.Body.Close()

	nullBody := agentRequestRaw(t, url, http.MethodPost, f.token, []byte(`null`))
	if nullBody.StatusCode != http.StatusBadRequest {
		t.Fatalf("null body status=%d body=%s", nullBody.StatusCode, readBody(t, nullBody))
	}
	nullBody.Body.Close()

	unknown := agentRequest(t, url, http.MethodPost, f.token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "resources/list",
	})
	var unknownPayload struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	decodeResponse(t, unknown, &unknownPayload)
	if unknownPayload.Error.Code != -32601 {
		t.Fatalf("unknown method=%#v", unknownPayload)
	}
}

func TestAgentBrowserMCPToolsListSchemas(t *testing.T) {
	f := newBrowserMCPFixture(t, "https://example.com/login", "Login")
	status, names, schemas := coreMCPToolsList(t, browserMCPURL(f.browserAPIFixture), f.token)
	if status != http.StatusOK {
		t.Fatalf("tools/list status=%d", status)
	}
	want := []string{"browser_snapshot", "browser_status", "browser_tabs_list"}
	sort.Strings(names)
	if len(names) != len(want) {
		t.Fatalf("tools=%v", names)
	}
	for index, name := range want {
		if names[index] != name {
			t.Fatalf("tools=%v", names)
		}
	}
	for _, name := range want {
		schema := schemas[name]
		if schema["additionalProperties"] != false {
			t.Fatalf("%s schema=%#v", name, schema)
		}
	}
	for _, forbidden := range []string{"browser_click", "browser_type", "browser_navigate", "browser_open_tab", "browser_cookie", "browser_grant_release"} {
		if _, ok := schemas[forbidden]; ok {
			t.Fatalf("%s must not be exposed", forbidden)
		}
	}
}

func TestAgentBrowserMCPSanitizesTabsAndBoundsSnapshots(t *testing.T) {
	const pageURL = "https://alice:s3cret@example.com:8443/console/path?access_token=topsecret#frag"
	const title = "Secret Console - Example"
	f := newBrowserMCPFixture(t, pageURL, title)

	tabs := f.call(t, "browser_tabs_list", map[string]any{"grant_id": f.grantID})
	if tabs.Status != http.StatusOK || tabs.IsError {
		t.Fatalf("tabs_list=%#v", tabs)
	}
	if strings.Contains(tabs.Text, "s3cret") || strings.Contains(tabs.Text, "topsecret") ||
		strings.Contains(tabs.Text, "frag") || strings.Contains(tabs.Text, "alice") {
		t.Fatalf("credentials leaked from tab URL: %s", tabs.Text)
	}
	var tabPayload struct {
		Tabs []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Site  string `json:"site"`
			URL   string `json:"url"`
		} `json:"tabs"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(tabs.Text), &tabPayload); err != nil {
		t.Fatal(err)
	}
	if tabPayload.Total != 1 || len(tabPayload.Tabs) != 1 {
		t.Fatalf("tabs=%s", tabs.Text)
	}
	tab := tabPayload.Tabs[0]
	if tab.URL != "https://example.com:8443/console/path" {
		t.Fatalf("sanitized url=%q", tab.URL)
	}
	if tab.Site != "https://example.com:8443" || tab.Title != title {
		t.Fatalf("tab=%#v", tab)
	}

	status := f.call(t, "browser_status", map[string]any{})
	if status.Status != http.StatusOK || status.IsError {
		t.Fatalf("status=%#v", status)
	}
	for _, forbidden := range []string{"profile_id", "session_id", "credential", "cookie", "cdp", "host_session"} {
		if strings.Contains(status.Text, forbidden) {
			t.Fatalf("browser_status leaked %q: %s", forbidden, status.Text)
		}
	}
	var statusPayload struct {
		Leases []struct {
			GrantID  string `json:"grant_id"`
			Status   string `json:"status"`
			TabCount int    `json:"tab_count"`
		} `json:"leases"`
	}
	if err := json.Unmarshal([]byte(status.Text), &statusPayload); err != nil {
		t.Fatal(err)
	}
	if len(statusPayload.Leases) != 1 || statusPayload.Leases[0].GrantID != f.grantID ||
		statusPayload.Leases[0].TabCount != 1 || statusPayload.Leases[0].Status != "active" {
		t.Fatalf("leases=%s", status.Text)
	}

	// One snapshot with a text body larger than the cap and more elements than
	// the cap. Both truncation flags must be explicit.
	const textMarker = "MARKER-PAGE-TEXT-7f3a"
	const elementMarker = "BUTTON-MARKER-9c2"
	f.host.SnapshotText = textMarker + strings.Repeat("页", 17000)
	elements := make([]browser.HostElement, 250)
	for index := range elements {
		elements[index] = browser.HostElement{
			Ref:  "e" + strings.Repeat("x", index%5),
			Role: "button", Name: elementMarker + "-name",
			Bounds:  browser.HostBounds{X: index, Y: index, Width: 10, Height: 10},
			Actions: []string{"click", "type"},
		}
	}
	f.host.SnapshotElements = elements

	snapshot := f.call(t, "browser_snapshot", map[string]any{"grant_id": f.grantID, "tab_id": tab.ID})
	if snapshot.Status != http.StatusOK || snapshot.IsError {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if strings.Contains(snapshot.Text, `"actions"`) || strings.Contains(snapshot.Text, "click") {
		t.Fatalf("snapshot exposed host actions: %s", snapshot.Text)
	}
	var snapshotPayload struct {
		Text          string `json:"text"`
		TextTruncated bool   `json:"text_truncated"`
		Elements      []struct {
			Ref    string         `json:"ref"`
			Role   string         `json:"role"`
			Name   string         `json:"name"`
			Bounds map[string]int `json:"bounds"`
		} `json:"elements"`
		ElementsTruncated bool `json:"elements_truncated"`
	}
	if err := json.Unmarshal([]byte(snapshot.Text), &snapshotPayload); err != nil {
		t.Fatal(err)
	}
	if !snapshotPayload.TextTruncated || utf8.RuneCountInString(snapshotPayload.Text) != agentBrowserMCPMaxTextRunes {
		t.Fatalf("text_truncated=%v runes=%d", snapshotPayload.TextTruncated,
			utf8.RuneCountInString(snapshotPayload.Text))
	}
	if !strings.HasPrefix(snapshotPayload.Text, textMarker) {
		t.Fatalf("snapshot text lost its head: %q", snapshotPayload.Text[:32])
	}
	if !snapshotPayload.ElementsTruncated || len(snapshotPayload.Elements) != agentBrowserMCPMaxElements {
		t.Fatalf("elements_truncated=%v len=%d", snapshotPayload.ElementsTruncated, len(snapshotPayload.Elements))
	}
	if snapshotPayload.Elements[0].Name != elementMarker+"-name" || snapshotPayload.Elements[0].Bounds["x"] != 0 {
		t.Fatalf("element=%#v", snapshotPayload.Elements[0])
	}

	// A second read inside the caps reports clean, untruncated data.
	f.host.SnapshotText = "short page"
	f.host.SnapshotElements = []browser.HostElement{{Ref: "e1", Role: "link", Name: "Docs"}}
	small := f.call(t, "browser_snapshot", map[string]any{"grant_id": f.grantID, "tab_id": tab.ID})
	if small.IsError {
		t.Fatalf("small snapshot=%#v", small)
	}
	var smallPayload struct {
		Text              string `json:"text"`
		TextTruncated     bool   `json:"text_truncated"`
		ElementsTruncated bool   `json:"elements_truncated"`
	}
	if err := json.Unmarshal([]byte(small.Text), &smallPayload); err != nil {
		t.Fatal(err)
	}
	if smallPayload.Text != "short page" || smallPayload.TextTruncated || smallPayload.ElementsTruncated {
		t.Fatalf("small snapshot=%#v", smallPayload)
	}

	// The audit row carries identifiers and argument key names only. Page
	// text, element names, titles and URLs must never reach it.
	dbPath := filepath.Join(f.store.DataDir(), "aha2.db")
	audit := waitForBrowserMCPAudit(t, dbPath, "browser_snapshot")
	for _, want := range []string{`"grant_id"`, `"tab_id"`, `"argument_keys":["grant_id","tab_id"]`} {
		if !strings.Contains(audit, want) {
			t.Fatalf("audit row missing %s: %s", want, audit)
		}
	}
	for _, needle := range []string{textMarker, elementMarker, title, "example.com", "topsecret"} {
		assertAuditActionLacks(t, dbPath, "agent.browser_mcp.call", needle)
	}
}

func TestAgentBrowserMCPRejectsUnknownToolsAndArguments(t *testing.T) {
	f := newBrowserMCPFixture(t, "https://example.com/login", "Login")

	unknown := f.call(t, "browser_click", map[string]any{"grant_id": f.grantID})
	if unknown.Status != http.StatusOK || unknown.Code != -32602 {
		t.Fatalf("unknown tool=%#v", unknown)
	}
	for label, call := range map[string]coreMCPToolCall{
		"status with profile": f.call(t, "browser_status", map[string]any{"profile": "/tmp/secret"}),
		"status with cookie":  f.call(t, "browser_status", map[string]any{"cookie": "session=1"}),
		"tabs missing grant":  f.call(t, "browser_tabs_list", map[string]any{}),
		"snapshot command": f.call(t, "browser_snapshot", map[string]any{
			"grant_id": f.grantID, "tab_id": "tab_1", "command": "click",
		}),
		"snapshot missing tab": f.call(t, "browser_snapshot", map[string]any{"grant_id": f.grantID}),
	} {
		if !call.IsError || !strings.HasPrefix(call.Text, "invalid_arguments:") {
			t.Fatalf("%s=%#v", label, call)
		}
	}

	missingTab := f.call(t, "browser_snapshot", map[string]any{"grant_id": f.grantID, "tab_id": "tab_missing"})
	if !missingTab.IsError || !strings.HasPrefix(missingTab.Text, "browser_tab_not_found:") {
		t.Fatalf("missing tab=%#v", missingTab)
	}
	if strings.Contains(missingTab.Text, "tab_missing:") {
		t.Fatalf("missing tab error echoed its identifier: %s", missingTab.Text)
	}
}

func TestAgentBrowserMCPAuthorizationFailuresAreFinal(t *testing.T) {
	f := newBrowserMCPFixture(t, "https://example.com/login", "Login")

	otherTask := createHardwareAPITask(t, f.store)
	foreignGrant := seededBrowserGrant(t, f.store, otherTask, "grant-foreign", "active")
	revokedGrant := seededBrowserGrant(t, f.store, f.task, "grant-revoked", "revoked")
	expiredGrant := seededBrowserGrant(t, f.store, f.task, "grant-expired", "expired")

	for label, item := range map[string]struct {
		grant string
		code  string
	}{
		"cross task": {foreignGrant, "browser_grant_forbidden"},
		"revoked":    {revokedGrant, "browser_grant_revoked"},
		"expired":    {expiredGrant, "browser_grant_expired"},
	} {
		call := f.call(t, "browser_tabs_list", map[string]any{"grant_id": item.grant})
		if !call.IsError || !strings.HasPrefix(call.Text, item.code+":") {
			t.Fatalf("%s tabs=%#v", label, call)
		}
		snapshot := f.call(t, "browser_snapshot", map[string]any{"grant_id": item.grant, "tab_id": "tab_1"})
		if !snapshot.IsError || !strings.HasPrefix(snapshot.Text, item.code+":") {
			t.Fatalf("%s snapshot=%#v", label, snapshot)
		}
	}

	// A client cannot promote a seeded lease into a lease it does not own by
	// guessing the owning Task: cross-Task access is rejected before any host
	// call and the error never names the real task.
	foreign := f.call(t, "browser_tabs_list", map[string]any{"grant_id": foreignGrant})
	if strings.Contains(foreign.Text, otherTask.ID) {
		t.Fatalf("error leaked the owning task: %s", foreign.Text)
	}

	// A channel-hosted Turn loses the domain even while the lease is active.
	retireCoreTurn(t, f.store, f.turn)
	channelTurn, channelToken := browserMCPToken(t, f.browserAPIFixture, f.task, "batch-browser-mcp-channel")
	seedKnowledgeMCPInbox(t, filepath.Join(f.store.DataDir(), "aha2.db"), f.task, channelTurn, "channel:task_route")
	response := agentRequest(t, browserMCPURL(f.browserAPIFixture), http.MethodPost, channelToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("channel turn status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	retireCoreTurn(t, f.store, channelTurn)
	plainTurn, plainToken := browserMCPToken(t, f.browserAPIFixture, f.task, "batch-browser-mcp-plain")
	seedKnowledgeMCPInbox(t, filepath.Join(f.store.DataDir(), "aha2.db"), f.task, plainTurn, "plain")
	response = agentRequest(t, browserMCPURL(f.browserAPIFixture), http.MethodPost, plainToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("plain batch status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	retireCoreTurn(t, f.store, plainTurn)
	brokenTurn, brokenToken := browserMCPToken(t, f.browserAPIFixture, f.task, "batch-browser-mcp-broken")
	seedKnowledgeMCPInbox(t, filepath.Join(f.store.DataDir(), "aha2.db"), f.task, brokenTurn, "raw:not-json")
	response = agentRequest(t, browserMCPURL(f.browserAPIFixture), http.MethodPost, brokenToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("malformed batch status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
}

func TestSanitizeBrowserMCPURLRejectsNonPageSchemes(t *testing.T) {
	for _, raw := range []string{"about:blank", "devtools://devtools/bundled/x", "file:///etc/passwd", "", "not a url"} {
		if sanitized, site := sanitizeBrowserMCPURL(raw); sanitized != "" || site != "" {
			t.Fatalf("%q => %q %q", raw, sanitized, site)
		}
	}
	sanitized, site := sanitizeBrowserMCPURL("http://127.0.0.1:8080/a/b?x=1#y")
	if sanitized != "http://127.0.0.1:8080/a/b" || site != "http://127.0.0.1:8080" {
		t.Fatalf("sanitized=%q site=%q", sanitized, site)
	}
}

// TestAgentBrowserMCPUnverifiedIdentifiersStayOutOfAudit reproduces the review
// finding: a page canary supplied as an invalid grant or tab ID must not be
// persisted by the MCP audit row. Only canonical IDs from a successful,
// verified call may appear.
func TestAgentBrowserMCPUnverifiedIdentifiersStayOutOfAudit(t *testing.T) {
	f := newBrowserMCPFixture(t, "https://example.com/login", "Login")
	tabs := f.call(t, "browser_tabs_list", map[string]any{"grant_id": f.grantID})
	if tabs.IsError {
		t.Fatalf("tabs=%#v", tabs)
	}
	var tabPayload struct {
		Tabs []struct {
			ID string `json:"id"`
		} `json:"tabs"`
	}
	if err := json.Unmarshal([]byte(tabs.Text), &tabPayload); err != nil {
		t.Fatal(err)
	}
	if len(tabPayload.Tabs) != 1 {
		t.Fatalf("tabs=%s", tabs.Text)
	}

	const grantCanary = "CANARY-PAGE-TEXT-grant-0e4"
	const tabCanary = "CANARY-PAGE-TEXT-tab-0e4"
	for label, call := range map[string]coreMCPToolCall{
		"invalid grant": f.call(t, "browser_tabs_list", map[string]any{"grant_id": grantCanary}),
		"invalid tab": f.call(t, "browser_snapshot", map[string]any{
			"grant_id": f.grantID, "tab_id": tabCanary,
		}),
		"invalid grant snapshot": f.call(t, "browser_snapshot", map[string]any{
			"grant_id": grantCanary, "tab_id": tabPayload.Tabs[0].ID,
		}),
	} {
		if !call.IsError {
			t.Fatalf("%s=%#v", label, call)
		}
		if strings.Contains(call.Text, grantCanary) || strings.Contains(call.Text, tabCanary) {
			t.Fatalf("%s echoed the canary: %s", label, call.Text)
		}
	}
	dbPath := filepath.Join(f.store.DataDir(), "aha2.db")
	// The audit row is appended in the handler defer, after the response is
	// written; wait for the failure rows before proving they carry no canary.
	tabsFailure := waitForBrowserMCPAuditResult(t, dbPath, "browser_tabs_list", "error")
	snapshotFailure := waitForBrowserMCPAuditResult(t, dbPath, "browser_snapshot", "error")
	for label, row := range map[string]string{"tabs": tabsFailure, "snapshot": snapshotFailure} {
		if !strings.Contains(row, `"result":"error"`) || !strings.Contains(row, `"argument_keys"`) {
			t.Fatalf("%s failure audit missing classification or keys: %s", label, row)
		}
		if strings.Contains(row, `"grant_id":"`) || strings.Contains(row, `"tab_id":"`) {
			t.Fatalf("%s failure audit recorded an unverified ID: %s", label, row)
		}
	}
	assertAuditActionLacks(t, dbPath, "agent.browser_mcp.call", grantCanary)
	assertAuditActionLacks(t, dbPath, "agent.browser_mcp.call", tabCanary)

	// The declared 128-character schema bound is enforced before any lookup.
	tooLong := f.call(t, "browser_tabs_list", map[string]any{"grant_id": strings.Repeat("g", 129)})
	if !tooLong.IsError || !strings.HasPrefix(tooLong.Text, "invalid_arguments:") {
		t.Fatalf("overlong grant=%#v", tooLong)
	}
	tooLongTab := f.call(t, "browser_snapshot", map[string]any{
		"grant_id": f.grantID, "tab_id": strings.Repeat("t", 129),
	})
	if !tooLongTab.IsError || !strings.HasPrefix(tooLongTab.Text, "invalid_arguments:") {
		t.Fatalf("overlong tab=%#v", tooLongTab)
	}
	waitForBrowserMCPAuditResult(t, dbPath, "browser_tabs_list", "invalid")
	waitForBrowserMCPAuditResult(t, dbPath, "browser_snapshot", "invalid")
	assertAuditActionLacks(t, dbPath, "agent.browser_mcp.call", strings.Repeat("g", 129))
	assertAuditActionLacks(t, dbPath, "agent.browser_mcp.call", strings.Repeat("t", 129))

	// The successful snapshot keeps the canonical grant ID and verified tab ID.
	snapshot := f.call(t, "browser_snapshot", map[string]any{
		"grant_id": f.grantID, "tab_id": tabPayload.Tabs[0].ID,
	})
	if snapshot.IsError {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	audit := waitForBrowserMCPAudit(t, dbPath, "browser_snapshot")
	if !strings.Contains(audit, `"grant_id":"`+f.grantID+`"`) ||
		!strings.Contains(audit, `"tab_id":"`+tabPayload.Tabs[0].ID+`"`) {
		t.Fatalf("audit lost the verified IDs: %s", audit)
	}
}

// TestAgentBrowserMCPErrorLogsCarryNoPageData reproduces the review finding: an
// unknown host failure can embed page content in the raw error. The model and
// the log must both see only the fixed classification.
func TestAgentBrowserMCPErrorLogsCarryNoPageData(t *testing.T) {
	f := newBrowserMCPFixture(t, "https://example.com/login", "Login")
	tabs := f.call(t, "browser_tabs_list", map[string]any{"grant_id": f.grantID})
	if tabs.IsError {
		t.Fatalf("tabs=%#v", tabs)
	}
	var tabPayload struct {
		Tabs []struct {
			ID string `json:"id"`
		} `json:"tabs"`
	}
	if err := json.Unmarshal([]byte(tabs.Text), &tabPayload); err != nil {
		t.Fatal(err)
	}
	if len(tabPayload.Tabs) != 1 {
		t.Fatalf("tabs=%s", tabs.Text)
	}

	const canary = "CANARY-PAGE-TEXT-host-7d2"
	f.host.FailSnapshot = errors.New("host exploded while reading page: " + canary)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	snapshot := f.call(t, "browser_snapshot", map[string]any{
		"grant_id": f.grantID, "tab_id": tabPayload.Tabs[0].ID,
	})

	if !snapshot.IsError {
		t.Fatalf("snapshot should fail: %#v", snapshot)
	}
	if strings.Contains(snapshot.Text, canary) {
		t.Fatalf("tool text leaked the canary: %s", snapshot.Text)
	}
	if strings.Contains(logs.String(), canary) {
		t.Fatalf("server log leaked the canary: %s", logs.String())
	}
}

// A lease with no expiry must not render the Go zero time as
// 0001-01-01T00:00:00Z. A real expiry keeps its JSON shape.
func TestAgentBrowserMCPLeaseOmitsZeroExpiry(t *testing.T) {
	zero, err := json.Marshal(agentBrowserMCPLease{GrantID: "grant-zero", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(zero), "expires_at") {
		t.Fatalf("zero lease rendered expires_at: %s", zero)
	}
	expires := time.Date(2026, 11, 20, 8, 30, 0, 0, time.UTC)
	withExpiry, err := json.Marshal(agentBrowserMCPLease{GrantID: "grant-real", ExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withExpiry), `"expires_at":"2026-11-20T08:30:00Z"`) {
		t.Fatalf("real expiry missing: %s", withExpiry)
	}
}
