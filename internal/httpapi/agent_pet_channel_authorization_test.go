package httpapi

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/browser"
	"github.com/ChinaKai/AHA2/internal/browser/browsertest"
	"github.com/ChinaKai/AHA2/internal/desktop"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetChannelTurnKeepsResourceGrantsAndUnrelatedDomainRestrictions(t *testing.T) {
	f := newHardwareAuthorizationFixture(t, true)
	seededBrowserGrant(t, f.h.database, f.task, "pet-channel-http", "active")
	manager := desktop.New(&desktopFixture{})
	t.Cleanup(func() { _ = manager.Close() })
	server := httptest.NewServer(New(Config{Store: f.h.database, App: f.h.service, AgentCapabilities: f.h.caps, Hardware: f.manager, Browser: browser.NewService(f.h.database, browsertest.New(), "local"), Desktop: manager, ManagedProcesses: f.h.processes}).Handler())
	t.Cleanup(server.Close)
	for _, name := range []string{"knowledge", "workspace", "browser", "hardware", "desktop"} {
		status, _, _ := coreMCPToolsList(t, server.URL+"/api/v1/agent/"+name+"/mcp", f.token)
		if status != http.StatusOK {
			t.Fatalf("ordinary pet %s discovery status=%d", name, status)
		}
	}
	raw, err := sql.Open("sqlite", f.h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	payload := `{"channel_context":{"schema":"aha2.channel.v1","instance_id":"fixture","endpoint":"group_digital_human","conversation_id":"fixture","route":{"mode":"task_route"}}}`
	if _, err := raw.Exec(`INSERT INTO agent_inbox(id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,content,payload_json,status,batch_id,created_at,claimed_at,processed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, domain.NewID("inbox"), f.task.ID, f.turn.RoundID, "main", "channel", "channel_message", "", "", "fixture", payload, "claimed", f.turn.InboxBatchID, time.Now().Format(time.RFC3339Nano), "", ""); err != nil {
		t.Fatal(err)
	}
	tools := map[string]string{"knowledge": "knowledge_guide", "workspace": "workspace_list", "browser": "browser_status", "hardware": "hardware_list", "desktop": "desktop_status"}
	for name, tool := range tools {
		url := server.URL + "/api/v1/agent/" + name + "/mcp"
		status, _, _ := coreMCPToolsList(t, url, f.token)
		want := http.StatusOK
		if name == "knowledge" || name == "workspace" {
			want = http.StatusForbidden
		}
		if status != want {
			t.Fatalf("channel pet discovers %s: %d", name, status)
		}
		result := coreMCPCallTool(t, url, f.token, tool, map[string]any{})
		if result.Status != want {
			t.Fatalf("channel pet calls %s: %d", name, result.Status)
		}
	}
	for _, path := range []string{"/hardware", "/browser", "/processes", "/desktop"} {
		response := agentRequest(t, server.URL+"/api/v1/agent"+path, http.MethodGet, f.token, nil)
		status := response.StatusCode
		response.Body.Close()
		want := http.StatusOK
		if path == "/processes" {
			want = http.StatusForbidden
		}
		if status != want {
			t.Fatalf("channel pet direct %s status=%d", path, status)
		}
	}
	status, names, _ := coreMCPToolsList(t, server.URL+"/api/v1/agent/core/mcp", f.token)
	if status != 200 || !coreMCPNameIncluded(names, "inbox_wait") {
		t.Fatal("channel gate removed essential own-Turn Inbox access")
	}
}
