package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

var controlChannelScenarios = []string{"active_route", "channel_batch", "plain_with_route", "mixed_batch"}

// These are isolated owner-created routes/bindings. No channel participant is
// given an owner capability, and no real channel or device is contacted.
func seedControlChannel(t *testing.T, database *store.Store, task domain.Task, turn domain.Turn, scenario string) {
	t.Helper()
	ctx := context.Background()
	if scenario == "active_route" || scenario == "plain_with_route" {
		now := time.Now().UTC()
		id := domain.NewID("control_channel")
		owner := domain.Owner{ID: id, Username: id, CreatedAt: now}
		if err := database.CreateOwner(ctx, owner); err != nil {
			t.Fatal(err)
		}
		plugin := domain.ChannelPlugin{ID: id, ProviderKey: id, DisplayName: "Fixture", ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1, InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now}
		if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
			t.Fatal(err)
		}
		instance := domain.ChannelInstance{ID: id, PluginID: id, OwnerID: owner.ID, RuntimeDeviceID: "fixture", Name: "Fixture", Status: "ready", Revision: 1, HostProjectID: task.ProjectID, HostWorkspaceID: task.WorkspaceID, OwnerBound: true, CreatedAt: now, UpdatedAt: now}
		endpoint := domain.ChannelEndpoint{ID: id, InstanceID: id, Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, CreatedAt: now, UpdatedAt: now}
		if err := database.CreateChannelInstance(ctx, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
			t.Fatal(err)
		}
		conversation := domain.ChannelConversation{ID: id, InstanceID: id, EndpointID: id, ScopeKeyVersion: 1, ScopeKey: id, DisplayName: "Fixture", Status: "active", HostTaskID: task.ID, CreatedAt: now, UpdatedAt: now}
		session := domain.ChannelSession{ID: id, ConversationID: id, Generation: 1, Mode: "task_route", Status: "active", StartedAt: now}
		if err := database.CreateChannelConversation(ctx, conversation, session); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ActivateOwnerChannelTaskRoute(ctx, id, id, task.ID, now); err != nil {
			t.Fatal(err)
		}
		if active, err := database.HasActiveChannelTaskRoute(ctx, task.ID); err != nil || !active {
			t.Fatalf("route not active: %v", err)
		}
	}
	path := filepath.Join(database.DataDir(), "aha2.db")
	switch scenario {
	case "plain_with_route":
		seedKnowledgeMCPInbox(t, path, task, turn, "plain")
	case "channel_batch":
		seedKnowledgeMCPInbox(t, path, task, turn, "channel:task_route")
	case "mixed_batch":
		seedKnowledgeMCPInbox(t, path, task, turn, "plain", "channel:conversation_host")
	}
}

func assertControlHTTP(t *testing.T, base, token, method, path string, body any, want int) {
	t.Helper()
	response := agentRequest(t, base+"/api/v1/agent"+path, method, token, body)
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, want, readBody(t, response))
	}
}

func assertControlCapabilities(t *testing.T, base, token, name string, read, write bool) {
	t.Helper()
	response := agentRequest(t, base+"/api/v1/agent/capabilities", http.MethodGet, token, nil)
	var payload struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != 200 || payload.Capabilities[name+"_mcp"] != read || payload.Capabilities[name+"_mcp_write"] != write {
		t.Fatalf("%s capabilities=%v status=%d", name, payload.Capabilities, response.StatusCode)
	}
}

func TestHardwareChannelGrantHTTPAndMCP(t *testing.T) {
	for _, scenario := range controlChannelScenarios {
		t.Run(scenario, func(t *testing.T) {
			f := newHardwareAuthorizationFixture(t, false)
			seedControlChannel(t, f.h.database, f.task, f.turn, scenario)
			assertControlCapabilities(t, f.server.URL, f.token, "hardware", true, true)
			status, names, _ := coreMCPToolsList(t, f.server.URL+"/api/v1/agent/hardware/mcp", f.token)
			if status != 200 || !coreMCPNameIncluded(names, "hardware_connect") {
				t.Fatalf("discovery=%d %v", status, names)
			}
			if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"}); result.Status != 200 || result.IsError {
				t.Fatalf("connect=%#v", result)
			}
			_ = f.connection(t)
			assertControlHTTP(t, f.server.URL, f.token, http.MethodGet, "/hardware", nil, 200)
			assertControlHTTP(t, f.server.URL, f.token, http.MethodGet, "/hardware/board/terminal", nil, 200)
			if result := f.mcp(t, "hardware_terminal_read", map[string]any{"hardware_id": "board"}); result.Status != 200 || result.IsError {
				t.Fatalf("read=%#v", result)
			}
			assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/hardware/device/adb/exec", map[string]any{"args": []string{"shell", "fixture"}}, 200)
			if f.executions.Load() != 1 {
				t.Fatal("HTTP write did not reach fake ADB exactly once")
			}
			assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/hardware/board/disconnect", map[string]any{}, 200)
			if f.manager.Status(f.task.ID, "board", "network", false).Connected {
				t.Fatal("HTTP disconnect left fixture open")
			}

			// The same token loses its binding immediately. A bare resource ID from
			// another Task is not a binding, and a failed call must preserve that Task.
			other := createCoreTaskInProject(t, f.h, f.task, "workspace-write")
			binding := []domain.TaskHardwareBinding{{TaskID: other.ID, ID: "foreign", ResourceID: f.board.ID}}
			if err := f.h.database.ReplaceTaskHardwareBindings(f.h.ctx, other.ID, binding); err != nil {
				t.Fatal(err)
			}
			before, err := f.h.database.TaskHardwareBindings(f.h.ctx, other.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Even while this Task's own domain is available, a foreign binding
			// ID or the resource ID cannot be used in place of a Task binding.
			beforeAccepts := f.accepts.Load()
			for _, id := range []string{"foreign", f.board.ID} {
				assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/hardware/"+id+"/connect", map[string]any{}, 404)
				if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": id}); !refused(result) {
					t.Fatalf("foreign hardware passed: %#v", result)
				}
			}
			if err := f.h.database.ReplaceTaskHardwareBindings(f.h.ctx, f.task.ID, nil); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"board", "foreign", f.board.ID} {
				assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/hardware/"+id+"/connect", map[string]any{}, 403)
				if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": id}); result.Status != 403 {
					t.Fatalf("revoked/foreign MCP=%#v", result)
				}
			}
			after, err := f.h.database.TaskHardwareBindings(f.h.ctx, other.ID)
			if err != nil || !reflect.DeepEqual(before, after) || f.accepts.Load() != beforeAccepts {
				t.Fatal("denied calls changed foreign bindings or opened transport")
			}
			assertControlCapabilities(t, f.server.URL, f.token, "hardware", false, false)
		})
	}
}

func TestBrowserChannelGrantHTTPAndMCP(t *testing.T) {
	for _, scenario := range controlChannelScenarios {
		t.Run(scenario, func(t *testing.T) {
			f := newBrowserMCPFixture(t, "https://example.test/fixture", "Fixture")
			seedControlChannel(t, f.store, f.task, f.turn, scenario)
			assertControlCapabilities(t, f.server.URL, f.token, "browser", true, true)
			status, names, _ := coreMCPToolsList(t, browserMCPURL(f.browserAPIFixture), f.token)
			if status != 200 || !coreMCPNameIncluded(names, "browser_tab_open") {
				t.Fatalf("discovery=%d %v", status, names)
			}
			opened := f.call(t, "browser_tab_open", map[string]any{"grant_id": f.grantID, "url": "https://example.test/open"})
			if opened.Status != 200 || opened.IsError || f.host.OpenTabCalls != 1 {
				t.Fatalf("open=%#v calls=%d", opened, f.host.OpenTabCalls)
			}
			var payload struct {
				Tab struct {
					ID string `json:"id"`
				} `json:"tab"`
			}
			if err := json.Unmarshal([]byte(opened.Text), &payload); err != nil || payload.Tab.ID == "" {
				t.Fatal("no tab returned")
			}
			base := "/browser/grants/" + f.grantID
			assertControlHTTP(t, f.server.URL, f.token, http.MethodGet, base+"/tabs", nil, 200)
			if result := f.call(t, "browser_tabs_list", map[string]any{"grant_id": f.grantID}); result.Status != 200 || result.IsError {
				t.Fatalf("tabs=%#v", result)
			}
			assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, base+"/tabs/"+payload.Tab.ID+"/navigate", map[string]any{"url": "https://example.test/next"}, 200)
			// Owner revocation must invalidate this already-issued Turn token.
			if response, _ := f.do(t, http.MethodDelete, "/api/v1/browser-grants/"+f.grantID, nil); response.StatusCode != 200 {
				t.Fatal("owner revoke failed")
			}
			before, err := f.store.BrowserTaskGrants(context.Background(), f.task.ID)
			if err != nil {
				t.Fatal(err)
			}
			calls := f.host.OpenTabCalls
			assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, base+"/tabs", map[string]any{"url": "https://example.test/denied"}, 403)
			if result := f.call(t, "browser_tab_open", map[string]any{"grant_id": f.grantID, "url": "https://example.test/denied"}); result.Status != 403 {
				t.Fatalf("revoked MCP=%#v", result)
			}
			after, err := f.store.BrowserTaskGrants(context.Background(), f.task.ID)
			if err != nil || !reflect.DeepEqual(before, after) || f.host.OpenTabCalls != calls {
				t.Fatal("denied call changed grants or reached host")
			}
			assertControlCapabilities(t, f.server.URL, f.token, "browser", false, false)
		})
	}
}

func TestBrowserChannelGrantStillRequiresWritableMainAndOwnedCapabilities(t *testing.T) {
	f := newBrowserMCPFixture(t, "https://example.test/fixture", "Fixture")
	seedControlChannel(t, f.store, f.task, f.turn, "mixed_batch")
	other := createHardwareAPITask(t, f.store)
	foreign := seededBrowserGrant(t, f.store, other, "foreign-channel", "active")
	for _, grant := range []string{foreign, seededBrowserGrant(t, f.store, f.task, "expired-channel", "expired"), seededBrowserGrant(t, f.store, f.task, "revoked-channel", "revoked")} {
		before, err := f.store.BrowserTaskGrants(context.Background(), other.ID)
		if err != nil {
			t.Fatal(err)
		}
		calls := f.host.OpenTabCalls
		assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/browser/grants/"+grant+"/tabs", map[string]any{"url": "https://example.test/denied"}, 403)
		result := f.call(t, "browser_tab_open", map[string]any{"grant_id": grant, "url": "https://example.test/denied"})
		if !result.IsError {
			t.Fatalf("foreign/inactive grant passed: %#v", result)
		}
		after, err := f.store.BrowserTaskGrants(context.Background(), other.ID)
		if err != nil || !reflect.DeepEqual(before, after) || f.host.OpenTabCalls != calls {
			t.Fatal("denied grant changed rows or reached host")
		}
	}
	// Default owner grant lacks interact: channel input cannot add it.
	var opened struct {
		Tab struct {
			ID string `json:"id"`
		} `json:"tab"`
	}
	decodeMCPText(t, f.call(t, "browser_tab_open", map[string]any{"grant_id": f.grantID, "url": "https://example.test/owned"}), &opened)
	if opened.Tab.ID == "" {
		t.Fatal("missing owned tab")
	}
	result := f.call(t, "browser_action", map[string]any{"grant_id": f.grantID, "tab_id": opened.Tab.ID, "kind": "click", "x": 1, "y": 1})
	if !result.IsError || len(f.host.ActionCalls) != 0 {
		t.Fatal("channel input expanded capabilities")
	}
	assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/browser/grants/"+f.grantID+"/tabs/"+opened.Tab.ID+"/actions", map[string]any{"kind": "click", "x": 1, "y": 1}, 403)
	if len(f.host.ActionCalls) != 0 {
		t.Fatal("HTTP expanded grant capabilities")
	}
	snapshot, err := f.store.RuntimeSnapshot(context.Background(), f.task.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ID = domain.NewID("runtime")
	snapshot.PermissionsJSON = `{"filesystem":"read-only","approval":"never"}`
	if err := f.store.CreateRuntimeSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"read_only_snapshot", "non_main"} {
		t.Run(scenario, func(t *testing.T) {
			retireCoreTurn(t, f.store, f.turn)
			task, agent := f.task, "sub"
			if scenario == "read_only_snapshot" {
				task.RuntimeConfigSnapshotID, agent = snapshot.ID, "main"
			}
			f.turn = createCoreTurn(t, f.store, task, agent, domain.NewID("batch"))
			seedControlChannel(t, f.store, f.task, f.turn, "channel_batch")
			f.token, err = f.agents.Issue(f.task.ID, agent, f.turn.ID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			assertControlCapabilities(t, f.server.URL, f.token, "browser", true, false)
			status, names, _ := coreMCPToolsList(t, browserMCPURL(f.browserAPIFixture), f.token)
			if status != 200 || coreMCPNameIncluded(names, "browser_tab_open") {
				t.Fatalf("read-only discovery=%d %v", status, names)
			}
			before := f.host.OpenTabCalls
			assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/browser/grants/"+f.grantID+"/tabs", map[string]any{"url": "https://example.test/denied"}, 403)
			result := f.call(t, "browser_tab_open", map[string]any{"grant_id": f.grantID, "url": "https://example.test/denied"})
			if result.Status != 403 || f.host.OpenTabCalls != before {
				t.Fatalf("write reached host: %#v", result)
			}
		})
	}
}
