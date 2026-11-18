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

// TestWorkspaceMCPGateRejectsUnboundAndPet proves the server-side gate, not
// tool visibility, is what refuses a direct call: a Task without a Workspace
// and the pet Task are both denied even when a valid call context is supplied.
func TestWorkspaceMCPGateRejectsUnboundAndPet(t *testing.T) {
	ctx, _, service, task, _ := newStreamCompatFixture(t)
	project := domain.Project{ID: task.ProjectID}
	if service.AgentWorkspaceMCPAllowed(ctx, AgentCallContext{
		Task: domain.Task{ID: "task-unbound", ProjectID: task.ProjectID}, Project: project,
	}) {
		t.Fatal("unbound workspace passed the gate")
	}
	if service.AgentWorkspaceMCPAllowed(ctx, AgentCallContext{
		Task:    domain.Task{ID: petTaskID, ProjectID: task.ProjectID, WorkspaceID: task.WorkspaceID},
		Project: project,
	}) {
		t.Fatal("pet task passed the gate")
	}
}

func TestWorkspaceMCPIsListedAsAutoInjectedBuiltin(t *testing.T) {
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
	var workspace domain.MCPServer
	found := false
	for _, item := range servers {
		if item.ID == workspaceMCPName {
			workspace = item
			found = true
		}
	}
	if !found {
		t.Fatal("workspace MCP is missing from the catalog")
	}
	if !workspace.Builtin || !workspace.AutoInject || !workspace.Enabled || workspace.Health != "ok" {
		t.Fatalf("unexpected workspace MCP: %#v", workspace)
	}
	if len(workspace.Tools) != len(WorkspaceMCPToolNames()) {
		t.Fatalf("workspace MCP tools=%#v", workspace.Tools)
	}

	detail, err := service.MCPServerDetail(ctx, workspaceMCPName)
	if err != nil || len(detail.Tools) != len(WorkspaceMCPToolNames()) {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	if _, err := service.SaveMCPServer(ctx, domain.MCPServer{
		ID: workspaceMCPName, Name: "x", Transport: "http", URL: "https://example.test",
	}); err == nil {
		t.Fatal("reserved workspace MCP save was allowed")
	}
	if err := service.DeleteMCPServer(ctx, workspaceMCPName); err == nil {
		t.Fatal("workspace MCP delete was allowed")
	}
	normalized, err := service.normalizeTaskMCPServers(ctx, domain.Project{ID: "project-normal"}, []string{workspaceMCPName})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 0 {
		t.Fatalf("workspace MCP was assignable to a task: %#v", normalized)
	}
}

// TestWorkspaceMCPRuntimeInjectionScopesTools proves the runtime injects the
// workspace domain only for an ordinary Task with a Workspace bound to its own
// Project, and that the first slice is read-only for every Agent.
func TestWorkspaceMCPRuntimeInjectionScopesTools(t *testing.T) {
	ctx, database, service, task, model := newStreamCompatFixture(t)
	readOnly, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: task.ProjectID, WorkspaceID: task.WorkspaceID, Title: "workspace review", Request: "review",
		ModelID: model.ID, Filesystem: "read-only", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	otherProject := domain.Project{ID: domain.NewID("project"), Name: "Other", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, otherProject); err != nil {
		t.Fatal(err)
	}
	otherWorkspace := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: otherProject.ID, Name: "Foreign",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, otherWorkspace); err != nil {
		t.Fatal(err)
	}
	unbound := task
	unbound.WorkspaceID = ""
	crossProject := task
	crossProject.WorkspaceID = otherWorkspace.ID

	scenarios := []struct {
		name    string
		task    domain.Task
		agentID string
		apiURL  string
		wantMCP bool
	}{
		{name: "writable main", task: task, agentID: "main", apiURL: "http://127.0.0.1:8766/", wantMCP: true},
		{name: "non-main", task: task, agentID: "sub", apiURL: "http://127.0.0.1:8766/", wantMCP: true},
		{name: "read-only main", task: readOnly, agentID: "main", apiURL: "http://127.0.0.1:8766/", wantMCP: true},
		{name: "unbound workspace", task: unbound, agentID: "main", apiURL: "http://127.0.0.1:8766/"},
		{name: "cross-project workspace", task: crossProject, agentID: "main", apiURL: "http://127.0.0.1:8766/"},
		{name: "pet task", task: domain.Task{ID: petTaskID}, agentID: "main", apiURL: "http://127.0.0.1:8766/"},
		{name: "pet project", task: domain.Task{ID: "task-pet-project", ProjectID: petProjectID}, agentID: "main", apiURL: "http://127.0.0.1:8766/"},
		{name: "no agent API", task: task, agentID: "main", apiURL: ""},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			servers, allowed := service.mcpRuntimeForTurn(ctx, scenario.task, scenario.agentID, scenario.apiURL, "")
			var workspaceServer backend.MCPServer
			for _, server := range servers {
				if server.Name == workspaceMCPName {
					workspaceServer = server
				}
			}
			if !scenario.wantMCP {
				if workspaceServer.Name != "" {
					t.Fatalf("unexpected workspace server: %#v", workspaceServer)
				}
				for _, name := range allowed {
					if strings.HasPrefix(name, "mcp__"+workspaceMCPName+"__") {
						t.Fatalf("unexpected workspace tool: %q", name)
					}
				}
				return
			}
			if workspaceServer.URL != "http://127.0.0.1:8766/api/v1/agent/workspace/mcp" ||
				workspaceServer.URLFromEnv != "AHA2_AGENT_API_URL" ||
				workspaceServer.URLPath != "/api/v1/agent/workspace/mcp" ||
				workspaceServer.BearerTokenEnv != "AHA2_AGENT_API_TOKEN" {
				t.Fatalf("workspace server=%#v", workspaceServer)
			}
			want := make([]string, 0, len(WorkspaceMCPToolNames()))
			for _, name := range WorkspaceMCPToolNames() {
				want = append(want, "mcp__"+workspaceMCPName+"__"+name)
			}
			got := make([]string, 0, len(want))
			for _, name := range allowed {
				if strings.HasPrefix(name, "mcp__"+workspaceMCPName+"__") {
					got = append(got, name)
				}
			}
			if len(got) != len(want) {
				t.Fatalf("allowed workspace tools=%#v want=%#v", got, want)
			}
			for index := range want {
				if got[index] != want[index] {
					t.Fatalf("allowed workspace tools=%#v want=%#v", got, want)
				}
			}
		})
	}
}

// TestWorkspaceMCPRuntimeExcludesChannelTurnContexts proves the runtime uses
// the Turn's actual inbox batch, not only the Task route. A channel_host or
// task_route batch is denied in either mixed order, a malformed payload fails
// closed, and an active task route is denied even with a plain batch.
func TestWorkspaceMCPRuntimeExcludesChannelTurnContexts(t *testing.T) {
	ctx, database, service, task, _ := newStreamCompatFixture(t)
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
			switch {
			case strings.HasPrefix(kind, "channel:"):
				mode := strings.TrimPrefix(kind, "channel:")
				encoded, err := json.Marshal(map[string]any{
					"channel_context": map[string]any{
						"schema":          "aha2.channel.v1",
						"instance_id":     "instance-workspace-runtime",
						"provider":        "test",
						"endpoint":        domain.ChannelEndpointGroupDigitalHuman,
						"conversation_id": "conversation-workspace-runtime",
						"route":           map[string]any{"mode": mode},
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				payload = string(encoded)
				sourceAgent, sourceKind = "channel", "channel_message"
			case strings.HasPrefix(kind, "raw:"):
				payload = strings.TrimPrefix(kind, "raw:")
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			if _, err := raw.ExecContext(ctx, `
				INSERT INTO agent_inbox(
					id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
					content,payload_json,status,batch_id,created_at,claimed_at,processed_at
				) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				domain.NewID("inbox"), task.ID, "round-workspace-mcp", "main", sourceAgent, sourceKind,
				"", "", "hello", payload, "claimed", batchID, now, now, "",
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	workspaceToolCount := func(allowed []string) int {
		count := 0
		for _, name := range allowed {
			if strings.HasPrefix(name, "mcp__"+workspaceMCPName+"__") {
				count++
			}
		}
		return count
	}
	hasWorkspace := func(servers []backend.MCPServer, allowed []string) bool {
		for _, server := range servers {
			if server.Name == workspaceMCPName {
				return true
			}
		}
		return workspaceToolCount(allowed) > 0
	}

	servers, allowed := service.mcpRuntimeForTurn(ctx, task, "main", "http://127.0.0.1:8766", "")
	if !hasWorkspace(servers, allowed) {
		t.Fatalf("plain Turn lost workspace MCP: %#v %#v", servers, allowed)
	}
	insertInbox("batch-workspace-runtime-plain", "plain")
	servers, allowed = service.mcpRuntimeForTurn(ctx, task, "main", "http://127.0.0.1:8766", "batch-workspace-runtime-plain")
	if !hasWorkspace(servers, allowed) {
		t.Fatalf("non-channel batch lost workspace MCP: %#v %#v", servers, allowed)
	}

	orders := []struct {
		name  string
		kinds []string
	}{
		{name: "plain-first", kinds: []string{"plain", "channel"}},
		{name: "channel-first", kinds: []string{"channel", "plain"}},
	}
	for _, mode := range []string{"conversation_host", "task_route"} {
		for _, order := range orders {
			batchID := "batch-workspace-runtime-" + strings.ReplaceAll(mode, "_", "-") + "-" + order.name
			kinds := make([]string, 0, len(order.kinds))
			for _, kind := range order.kinds {
				if kind == "channel" {
					kinds = append(kinds, "channel:"+mode)
					continue
				}
				kinds = append(kinds, kind)
			}
			insertInbox(batchID, kinds...)
			servers, allowed = service.mcpRuntimeForTurn(ctx, task, "main", "http://127.0.0.1:8766", batchID)
			if hasWorkspace(servers, allowed) {
				t.Fatalf("%s mixed %s batch received workspace MCP: %#v %#v", mode, order.name, servers, allowed)
			}
			if count := workspaceToolCount(allowed); count != 0 {
				t.Fatalf("%s mixed %s batch received %d workspace tools: %#v", mode, order.name, count, allowed)
			}
		}
	}

	insertInbox("batch-workspace-runtime-malformed", "raw:not-json")
	servers, allowed = service.mcpRuntimeForTurn(ctx, task, "main", "http://127.0.0.1:8766", "batch-workspace-runtime-malformed")
	if hasWorkspace(servers, allowed) {
		t.Fatalf("malformed batch received workspace MCP: %#v %#v", servers, allowed)
	}

	// An active task route denies the domain even when the Turn's batch is
	// plain. The route tables need only the three rows hasActiveTaskRoute joins;
	// foreign keys are disabled on this inspection connection so no channel
	// runtime fixture is required.
	if _, err := raw.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO channel_endpoints(id,instance_id,kind,enabled,config_json,created_at,updated_at) VALUES('workspace-route-endpoint','workspace-route-instance','group_digital_human',1,'{}','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO channel_conversations(id,instance_id,endpoint_id,scope_key_version,scope_key,host_task_id,status,created_at,updated_at) VALUES('workspace-route-conversation','workspace-route-instance','workspace-route-endpoint',1,'workspace-route-scope','` + task.ID + `','active','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO channel_task_routes(id,instance_id,conversation_id,target_task_id,state,revision,activated_at,exited_at,exit_reason) VALUES('workspace-route','workspace-route-instance','workspace-route-conversation','` + task.ID + `','active',1,'2026-01-01T00:00:00Z','','')`,
	} {
		if _, err := raw.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	insertInbox("batch-workspace-runtime-active-route", "plain")
	servers, allowed = service.mcpRuntimeForTurn(ctx, task, "main", "http://127.0.0.1:8766", "batch-workspace-runtime-active-route")
	if hasWorkspace(servers, allowed) {
		t.Fatalf("active-route batch received workspace MCP: %#v %#v", servers, allowed)
	}
}
