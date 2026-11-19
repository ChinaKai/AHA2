package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// seedBrowserMCPGrant inserts a profile, session and grant straight into the
// store. The injection gate only reads these rows, so no browser host is
// needed. state selects an active, revoked, expired or closed-session lease.
func seedBrowserMCPGrant(t *testing.T, ctx context.Context, database *store.Store, task domain.Task, id, state string) {
	t.Helper()
	now := time.Now().UTC()
	profileID := "profile-" + id
	sessionID := "session-" + id
	if err := database.UpsertBrowserProfile(ctx, domain.BrowserProfile{
		ID: profileID, Name: profileID, BrowserKind: "edge", HostID: "local", State: "ready",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	sessionState := domain.BrowserSessionStateReady
	if state == "closed" {
		sessionState = domain.BrowserSessionStateClosed
	}
	if err := database.CreateBrowserSession(ctx, domain.BrowserSession{
		ID: sessionID, ProfileID: profileID, HostID: "local", HostSessionID: "host-" + sessionID,
		State: sessionState, StartedAt: now, CreatedAt: now, UpdatedAt: now,
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
}

func seedHardwareMCPGroup(t *testing.T, ctx context.Context, database *store.Store, task domain.Task) {
	t.Helper()
	now := time.Now().UTC()
	if err := database.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{{
		TaskID: task.ID, ID: "board", Position: 0, Description: "Main board",
		Mode:   domain.HardwareModeSerial,
		Serial: domain.HardwareSerialConfig{Device: "COM3", Baudrate: 115200},
		Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
}

func mcpDomainTools(allowed []string, name string) []string {
	result := make([]string, 0)
	for _, item := range allowed {
		if strings.HasPrefix(item, "mcp__"+name+"__") {
			result = append(result, item)
		}
	}
	return result
}

func hasMCPServer(servers []backend.MCPServer, name string) bool {
	for _, server := range servers {
		if server.Name == name {
			return true
		}
	}
	return false
}

func TestBrowserAndHardwareMCPAreIndependentBuiltins(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewService(database, nil, nil)

	servers, err := service.MCPServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]domain.MCPServer{}
	for _, item := range servers {
		byID[item.ID] = item
	}
	for id, wantTools := range map[string]int{
		browserMCPName:  len(BrowserMCPToolNames()),
		hardwareMCPName: len(HardwareMCPToolNames()),
	} {
		item, ok := byID[id]
		if !ok {
			t.Fatalf("%s is missing from the catalog", id)
		}
		if !item.Builtin || !item.AutoInject || !item.Enabled || item.Health != "ok" {
			t.Fatalf("unexpected %s catalog entry: %#v", id, item)
		}
		if len(item.Tools) != wantTools {
			t.Fatalf("%s tools=%#v", id, item.Tools)
		}
		detail, err := service.MCPServerByID(ctx, id)
		if err != nil || len(detail.Tools) != wantTools {
			t.Fatalf("%s detail=%#v err=%v", id, detail, err)
		}
		if _, err := service.SaveMCPServer(ctx, domain.MCPServer{
			ID: id, Name: "x", Transport: "http", URL: "https://example.test",
		}); err == nil {
			t.Fatalf("reserved %s save was allowed", id)
		}
		if err := service.DeleteMCPServer(ctx, id); err == nil {
			t.Fatalf("%s delete was allowed", id)
		}
		normalized, err := service.normalizeTaskMCPServers(ctx, domain.Project{ID: "project-normal"}, []string{id})
		if err != nil || len(normalized) != 0 {
			t.Fatalf("%s was assignable: %#v err=%v", id, normalized, err)
		}
	}
	if len(BrowserMCPToolNames()) != 3 || len(HardwareMCPToolNames()) != 2 {
		t.Fatalf("browser=%v hardware=%v", BrowserMCPToolNames(), HardwareMCPToolNames())
	}
}

// TestBrowserHardwareMCPInjectionUsesIndependentTriggers proves the two
// domains are not merged into one condition: a hardware binding alone injects
// only hardware, a browser lease alone injects only browser, and a channel
// hosted batch removes both.
func TestBrowserHardwareMCPInjectionUsesIndependentTriggers(t *testing.T) {
	ctx, database, service, task, model := newStreamCompatFixture(t)
	const apiURL = "http://127.0.0.1:8766"

	check := func(label string, task domain.Task, batchID string, wantBrowser, wantHardware bool) {
		t.Helper()
		servers, allowed := service.mcpRuntimeForTurn(ctx, task, "main", apiURL, batchID)
		if got := hasMCPServer(servers, browserMCPName); got != wantBrowser {
			t.Fatalf("%s browser server=%v want=%v servers=%#v", label, got, wantBrowser, servers)
		}
		browserTools := mcpDomainTools(allowed, browserMCPName)
		if len(browserTools) != len(BrowserMCPToolNames()) && wantBrowser {
			t.Fatalf("%s browser tools=%#v", label, browserTools)
		}
		if !wantBrowser && len(browserTools) != 0 {
			t.Fatalf("%s unexpected browser tools=%#v", label, browserTools)
		}
		if got := hasMCPServer(servers, hardwareMCPName); got != wantHardware {
			t.Fatalf("%s hardware server=%v want=%v servers=%#v", label, got, wantHardware, servers)
		}
		hardwareTools := mcpDomainTools(allowed, hardwareMCPName)
		if len(hardwareTools) != len(HardwareMCPToolNames()) && wantHardware {
			t.Fatalf("%s hardware tools=%#v", label, hardwareTools)
		}
		if !wantHardware && len(hardwareTools) != 0 {
			t.Fatalf("%s unexpected hardware tools=%#v", label, hardwareTools)
		}
	}

	check("no trigger", task, "", false, false)

	seedHardwareMCPGroup(t, ctx, database, task)
	check("hardware only", task, "", false, true)

	seedBrowserMCPGrant(t, ctx, database, task, "grant-both", "active")
	check("both triggers", task, "", true, true)

	browserOnly, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: task.ProjectID, WorkspaceID: task.WorkspaceID, Title: "browser only",
		Request: "browser", ModelID: model.ID, CollaborationMode: "single", MaxAgents: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	seedBrowserMCPGrant(t, ctx, database, browserOnly, "grant-browser-only", "active")
	check("browser only", browserOnly, "", true, false)

	for _, state := range []string{"revoked", "expired", "closed"} {
		inactive, err := service.CreateTask(ctx, CreateTaskInput{
			ProjectID: task.ProjectID, WorkspaceID: task.WorkspaceID, Title: "inactive " + state,
			Request: "browser", ModelID: model.ID, CollaborationMode: "single", MaxAgents: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		seedBrowserMCPGrant(t, ctx, database, inactive, "grant-"+state, state)
		check(state+" grant", inactive, "", false, false)
	}

	// A channel-hosted batch removes both domains even though the Task holds
	// both triggers.
	raw, err := sql.Open("sqlite", filepath.Join(database.DataDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	insertInbox := func(batchID string, kinds ...string) {
		t.Helper()
		for _, kind := range kinds {
			payload := `{}`
			sourceAgent, sourceKind := "owner", "owner_message"
			if kind != "plain" {
				mode := strings.TrimPrefix(kind, "channel:")
				encoded, err := json.Marshal(map[string]any{
					"channel_context": map[string]any{
						"schema":          "aha2.channel.v1",
						"instance_id":     "instance-browser-hardware",
						"provider":        "test",
						"endpoint":        domain.ChannelEndpointGroupDigitalHuman,
						"conversation_id": "conversation-browser-hardware",
						"route":           map[string]any{"mode": mode},
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				payload = string(encoded)
				sourceAgent, sourceKind = "channel", "channel_message"
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			if _, err := raw.ExecContext(ctx, `
				INSERT INTO agent_inbox(
					id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
					content,payload_json,status,batch_id,created_at,claimed_at,processed_at
				) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				domain.NewID("inbox"), task.ID, "round-browser-hardware", "main", sourceAgent, sourceKind,
				"", "", "hello", payload, "claimed", batchID, now, now, "",
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, mode := range []string{"conversation_host", "task_route"} {
		batchID := "batch-browser-hardware-" + strings.ReplaceAll(mode, "_", "-")
		insertInbox(batchID, "plain", "channel:"+mode)
		check(mode+" batch", task, batchID, false, false)
	}
	insertInbox("batch-browser-hardware-plain", "plain")
	check("plain batch", task, "batch-browser-hardware-plain", true, true)
	insertInbox("batch-browser-hardware-malformed", "raw:not-json")
	check("malformed batch", task, "batch-browser-hardware-malformed", false, false)

	check("pet task", domain.Task{ID: petTaskID}, "", false, false)
	check("pet project", domain.Task{ID: "task-pet-project", ProjectID: petProjectID}, "", false, false)
}

// TestBrowserHardwareMCPGatesRejectDirectCalls proves tool visibility is not
// authorization: the server-side gate refuses a Task whose triggers are absent
// even when a caller supplies a fully valid AgentCallContext.
func TestBrowserHardwareMCPGatesRejectDirectCalls(t *testing.T) {
	ctx, database, service, task, _ := newStreamCompatFixture(t)
	call := AgentCallContext{Task: task, Project: domain.Project{ID: task.ProjectID}}
	if service.AgentBrowserMCPAllowed(ctx, call) {
		t.Fatal("browser gate passed without an active grant")
	}
	if service.AgentHardwareMCPAllowed(ctx, call) {
		t.Fatal("hardware gate passed without a bound group")
	}
	seedHardwareMCPGroup(t, ctx, database, task)
	seedBrowserMCPGrant(t, ctx, database, task, "grant-direct", "active")
	if !service.AgentBrowserMCPAllowed(ctx, call) || !service.AgentHardwareMCPAllowed(ctx, call) {
		t.Fatal("gates rejected a Task holding both triggers")
	}
	other := domain.Task{ID: "task-without-triggers", ProjectID: task.ProjectID}
	otherCall := AgentCallContext{Task: other, Project: call.Project}
	if service.AgentBrowserMCPAllowed(ctx, otherCall) || service.AgentHardwareMCPAllowed(ctx, otherCall) {
		t.Fatal("gate borrowed another Task's triggers")
	}
}
