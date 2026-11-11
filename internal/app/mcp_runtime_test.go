package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestFetchMCPToolsAndExactAllowlist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		writer.Header().Set("Content-Type", "application/json")
		if payload.Method == "tools/list" {
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"echo","description":"echo back","inputSchema":{"type":"object"}}]}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer server.Close()

	tools, err := fetchMCPTools(context.Background(), domain.MCPServer{URL: server.URL, Transport: "http"})
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools=%#v err=%v", tools, err)
	}

	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewService(database, nil, nil)
	item := domain.MCPServer{ID: "mcp_test", Name: "test", Transport: "http", URL: server.URL, Enabled: true, Tools: tools}
	if err := database.UpsertMCPServer(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	servers, allowed := service.mcpRuntimeForTask(context.Background(), domain.Task{ID: "task-test", MCPServerIDs: []string{"mcp_test"}}, "")
	if len(servers) != 1 || servers[0].Name != "mcp_test" || servers[0].URL != server.URL || servers[0].Transport != "http" {
		t.Fatalf("servers=%#v", servers)
	}
	if len(allowed) != 1 || allowed[0] != "mcp__mcp_test__echo" {
		t.Fatalf("allowed=%#v", allowed)
	}
	if _, extra := service.mcpRuntimeForTask(context.Background(), domain.Task{ID: "task-test"}, "http://127.0.0.1:8766"); len(extra) != 0 {
		t.Fatalf("unrouted task gained MCP tools: %#v", extra)
	}
}

func TestTaskRouteAddsNarrowChannelMCP(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		endpoint string
	}{
		{name: "group", endpoint: domain.ChannelEndpointGroupDigitalHuman},
		{name: "private", endpoint: domain.ChannelEndpointAssistantDM},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newPetTaskFixture(t)
			ctx := fixture.ctx
			now := time.Now().UTC()
			owner := domain.Owner{ID: "owner-mcp", Username: "owner-mcp", CreatedAt: now}
			if err := fixture.database.CreateOwner(ctx, owner); err != nil {
				t.Fatal(err)
			}
			project := domain.Project{
				ID: "mcp-project", Name: "MCP", DefaultWorkspaceID: "mcp-workspace",
				KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
			}
			workspace := domain.Workspace{
				ID: project.DefaultWorkspaceID, ProjectID: project.ID, Name: "MCP",
				Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
				AgentAPIMode: "global", AgentAPIStatus: "unknown", CreatedAt: now, UpdatedAt: now,
			}
			if err := fixture.database.CreateProject(ctx, project); err != nil {
				t.Fatal(err)
			}
			if err := fixture.database.CreateWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			task, err := fixture.service.CreateTask(ctx, CreateTaskInput{
				ProjectID: project.ID, WorkspaceID: workspace.ID, Title: scenario.name, Request: scenario.name,
				Isolation: "inplace", Backend: fixture.model.Backend, ModelSource: fixture.model.Source,
				ModelID: fixture.model.ID, WireModel: fixture.model.WireModel,
				Filesystem: "read-only", Approval: "never", CollaborationMode: "single", MaxAgents: 1,
				KnowledgePolicy: "inherit", StartMode: "manual",
			})
			if err != nil {
				t.Fatal(err)
			}
			plugin := domain.ChannelPlugin{
				ID: "mcp-plugin", ProviderKey: "feishu", DisplayName: "Feishu", ManifestVersion: 1,
				PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1, InstallState: "installed",
				Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
			}
			if err := fixture.database.UpsertChannelPlugin(ctx, plugin); err != nil {
				t.Fatal(err)
			}
			instance := domain.ChannelInstance{
				ID: "mcp-channel", PluginID: plugin.ID, OwnerID: owner.ID, RuntimeDeviceID: "device",
				Name: scenario.name, Status: "ready", Revision: 1, HostProjectID: project.ID,
				HostWorkspaceID: workspace.ID, OwnerBound: true, CredentialConfigured: true,
				CreatedAt: now, UpdatedAt: now,
			}
			endpoint := domain.ChannelEndpoint{
				ID: "mcp-endpoint", InstanceID: instance.ID, Kind: scenario.endpoint,
				Enabled: true, CreatedAt: now, UpdatedAt: now,
			}
			if err := fixture.database.CreateChannelInstance(ctx, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
				t.Fatal(err)
			}
			conversation := domain.ChannelConversation{
				ID: "mcp-conversation", InstanceID: instance.ID, EndpointID: endpoint.ID,
				ScopeKeyVersion: 1, ScopeKey: "scope", DisplayName: scenario.name, Status: "active",
				HostTaskID: task.ID, CreatedAt: now, UpdatedAt: now,
			}
			session := domain.ChannelSession{
				ID: "mcp-session", ConversationID: conversation.ID, Generation: 1,
				Mode: "task_route", Status: "active", StartedAt: now,
			}
			if err := fixture.database.CreateChannelConversation(ctx, conversation, session); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.database.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, task.ID, now); err != nil {
				t.Fatal(err)
			}

			servers, allowed := fixture.service.mcpRuntimeForTask(ctx, task, "http://127.0.0.1:8766/")
			found := backend.MCPServer{}
			for _, server := range servers {
				if server.Name == channelMCPName {
					found = server
				}
			}
			if found.Name == "" {
				t.Fatalf("%s route gained no channel MCP: %#v", scenario.name, servers)
			}
			if found.URL != "http://127.0.0.1:8766/api/v1/agent/channel/mcp" ||
				found.URLFromEnv != "AHA2_AGENT_API_URL" ||
				found.URLPath != "/api/v1/agent/channel/mcp" ||
				found.BearerTokenEnv != "AHA2_AGENT_API_TOKEN" {
				t.Fatalf("channel MCP=%#v", found)
			}
			wantAllowed := []string{
				"mcp__aha2_channel__channel_reply",
				"mcp__aha2_channel__channel_reply_decision",
				"mcp__aha2_channel__channel_request_permission",
			}
			if len(allowed) != len(wantAllowed) || allowed[0] != wantAllowed[0] ||
				allowed[1] != wantAllowed[1] || allowed[2] != wantAllowed[2] {
				t.Fatalf("allowed=%#v", allowed)
			}
		})
	}
}

func TestStdioMCPServerHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_STDIO_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(scanner.Bytes(), &request)
		switch request.Method {
		case "initialize":
			fmt.Println(`{"jsonrpc":"2.0","id":1,"result":{}}`)
		case "tools/list":
			fmt.Println(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"stdio_echo","description":"stdio"}]}}`)
		}
	}
}

func TestFetchStdioMCPTools(t *testing.T) {
	command := os.Args[0]
	item := domain.MCPServer{Command: command, Args: []string{"-test.run=TestStdioMCPServerHelper"}, Transport: "stdio", Env: map[string]string{"GO_WANT_MCP_STDIO_HELPER": "1"}}
	tools, err := fetchStdioTools(context.Background(), item)
	if err != nil || len(tools) != 1 || tools[0].Name != "stdio_echo" {
		t.Fatalf("tools=%#v err=%v", tools, err)
	}
}

func TestMailBindingAddsNarrowMailMCP(t *testing.T) {
	fixture := newPetTaskFixture(t)
	ctx := fixture.ctx
	now := time.Now().UTC()
	account, err := fixture.database.CreateMailAccount(ctx, domain.MailAccount{
		ID: "mail_account_mcp", Name: "MCP", Address: "noreply@example.com",
		DisplayName: "AHA", CredentialRef: "configured", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "task-mail-mcp", WorkspaceID: "workspace-mail-mcp", ProjectID: "project-mail-mcp"}
	if _, err := fixture.database.CreateMailBinding(ctx, domain.MailBinding{
		ID: "mail_binding_mcp", ScopeType: domain.MailBindingScopeTask, ScopeID: task.ID,
		AccountID: account.ID, ApprovalMode: domain.MailApprovalAlways, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	servers, allowed := fixture.service.mcpRuntimeForTask(ctx, task, "http://127.0.0.1:8766/")
	found := backend.MCPServer{}
	for _, server := range servers {
		if server.Name == mailMCPName {
			found = server
		}
	}
	if found.Name == "" {
		t.Fatalf("mail bound task gained no mail MCP: %#v", servers)
	}
	if found.URL != "http://127.0.0.1:8766/api/v1/agent/mail/mcp" ||
		found.URLFromEnv != "AHA2_AGENT_API_URL" ||
		found.URLPath != "/api/v1/agent/mail/mcp" ||
		found.BearerTokenEnv != "AHA2_AGENT_API_TOKEN" {
		t.Fatalf("mail MCP=%#v", found)
	}
	wantAllowed := []string{
		"mcp__aha2_mail__mail_list_accounts",
		"mcp__aha2_mail__mail_list_templates",
		"mcp__aha2_mail__mail_template_detail",
		"mcp__aha2_mail__mail_preview",
		"mcp__aha2_mail__mail_send",
		"mcp__aha2_mail__mail_get_status",
	}
	if len(allowed) != len(wantAllowed) {
		t.Fatalf("allowed=%#v", allowed)
	}
	for index, want := range wantAllowed {
		if allowed[index] != want {
			t.Fatalf("allowed[%d]=%q want %q", index, allowed[index], want)
		}
	}

	accounts, _, _ := fixture.service.MailTaskResources(ctx, task)
	if len(accounts) != 1 || accounts[0].CredentialRef != "" {
		t.Fatalf("task mail resources=%#v", accounts)
	}

	// No binding means no account, so the mail MCP must not be injected.
	if _, extra := fixture.service.mcpRuntimeForTask(ctx, domain.Task{ID: "task-no-mail"}, "http://127.0.0.1:8766/"); len(extra) != 0 {
		t.Fatalf("unbound task gained mail tools: %#v", extra)
	}
}

func TestMCPRuntimeRefreshesStaleToolCache(t *testing.T) {
	ctx := context.Background()
	var listed atomic.Int32
	tools := `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"board_status"},{"name":"display_text"},{"name":"display_clear"},{"name":"camera_snapshot"}]}}`
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		writer.Header().Set("Content-Type", "application/json")
		if payload.Method == "tools/list" {
			listed.Add(1)
			_, _ = writer.Write([]byte(tools))
			return
		}
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer endpoint.Close()

	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewService(database, nil, nil)
	now := time.Now().UTC()
	item := domain.MCPServer{
		ID: "mcp_luckfox_desk", Name: "Luckfox Desk", Transport: "http", URL: endpoint.URL,
		Enabled: true, Health: "ok",
		Tools: []domain.MCPTool{
			{Name: "board_status"}, {Name: "display_text"}, {Name: "display_clear"},
		},
		LastCheckedAt: now.Add(-2 * mcpToolCacheTTL),
		CreatedAt:     now, UpdatedAt: now,
	}
	if err := database.UpsertMCPServer(ctx, item); err != nil {
		t.Fatal(err)
	}

	_, allowed := service.mcpRuntimeForTask(ctx, domain.Task{ID: "task-refresh", MCPServerIDs: []string{item.ID}}, "")
	want := map[string]bool{
		"mcp__mcp_luckfox_desk__board_status":    true,
		"mcp__mcp_luckfox_desk__display_text":    true,
		"mcp__mcp_luckfox_desk__display_clear":   true,
		"mcp__mcp_luckfox_desk__camera_snapshot": true,
	}
	if len(allowed) != len(want) {
		t.Fatalf("allowed=%#v", allowed)
	}
	for _, name := range allowed {
		if !want[name] {
			t.Fatalf("allowed=%#v, unexpected %q", allowed, name)
		}
	}
	if listed.Load() != 1 {
		t.Fatalf("tools/list calls=%d, want 1", listed.Load())
	}
	stored, err := service.MCPServerByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Tools) != 4 || stored.Health != "ok" || stored.LastCheckedAt.Before(now) {
		t.Fatalf("stored=%#v", stored)
	}
}

func TestMCPRuntimeKeepsCachedToolsWhenRefreshFails(t *testing.T) {
	ctx := context.Background()
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "boom", http.StatusInternalServerError)
	}))
	defer endpoint.Close()

	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewService(database, nil, nil)
	now := time.Now().UTC()
	item := domain.MCPServer{
		ID: "mcp_unreachable", Name: "Unreachable", Transport: "http", URL: endpoint.URL,
		Enabled: true, Health: "ok",
		Tools:         []domain.MCPTool{{Name: "board_status"}},
		LastCheckedAt: now.Add(-2 * mcpToolCacheTTL),
		CreatedAt:     now, UpdatedAt: now,
	}
	if err := database.UpsertMCPServer(ctx, item); err != nil {
		t.Fatal(err)
	}

	_, allowed := service.mcpRuntimeForTask(ctx, domain.Task{ID: "task-fallback", MCPServerIDs: []string{item.ID}}, "")
	if len(allowed) != 1 || allowed[0] != "mcp__mcp_unreachable__board_status" {
		t.Fatalf("allowed=%#v", allowed)
	}
	stored, err := service.MCPServerByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Tools) != 1 || stored.Tools[0].Name != "board_status" || stored.Health != "error" || stored.LastCheckedAt.IsZero() {
		t.Fatalf("stored=%#v", stored)
	}
}

func TestMCPRuntimeUsesFreshCacheWithoutProbing(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"new_tool"}]}}`))
	}))
	defer endpoint.Close()

	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewService(database, nil, nil)
	now := time.Now().UTC()
	item := domain.MCPServer{
		ID: "mcp_fresh", Name: "Fresh", Transport: "http", URL: endpoint.URL,
		Enabled: true, Health: "ok",
		Tools:         []domain.MCPTool{{Name: "cached_tool"}},
		LastCheckedAt: now,
		CreatedAt:     now, UpdatedAt: now,
	}
	if err := database.UpsertMCPServer(ctx, item); err != nil {
		t.Fatal(err)
	}

	_, allowed := service.mcpRuntimeForTask(ctx, domain.Task{ID: "task-fresh", MCPServerIDs: []string{item.ID}}, "")
	if len(allowed) != 1 || allowed[0] != "mcp__mcp_fresh__cached_tool" {
		t.Fatalf("allowed=%#v", allowed)
	}
	if requests.Load() != 0 {
		t.Fatalf("probed a fresh cache: requests=%d", requests.Load())
	}
}

func TestMCPProbePersistKeepsConcurrentConfigEdit(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewService(database, nil, nil)
	now := time.Now().UTC()
	item := domain.MCPServer{
		ID: "mcp_race", Name: "Race", Transport: "http", URL: "http://old.test/mcp",
		Enabled: true, Health: "ok", Env: map[string]string{"API_TOKEN": "old-secret"},
		Tools: []domain.MCPTool{{Name: "old_tool"}}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertMCPServer(ctx, item); err != nil {
		t.Fatal(err)
	}
	// The Turn loaded item before an Owner edit landed; the probe must only
	// touch probe columns, not write the stale config back.
	edited := item
	edited.URL = "http://new.test/mcp"
	edited.Env = map[string]string{"API_TOKEN": "new-secret"}
	edited.UpdatedAt = now.Add(time.Second)
	if err := database.UpsertMCPServer(ctx, edited); err != nil {
		t.Fatal(err)
	}

	service.persistMCPProbe(ctx, item, []domain.MCPTool{{Name: "new_tool"}, {Name: "second_tool"}}, nil)
	stored, err := service.MCPServerByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.URL != edited.URL || stored.Env["API_TOKEN"] != "new-secret" {
		t.Fatalf("probe clobbered config: %#v", stored)
	}
	if len(stored.Tools) != 2 || stored.Health != "ok" || stored.LastCheckedAt.IsZero() {
		t.Fatalf("probe result not stored: %#v", stored)
	}
}
