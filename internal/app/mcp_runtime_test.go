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
