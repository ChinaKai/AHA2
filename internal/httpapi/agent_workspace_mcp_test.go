package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/managedprocess"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

func workspaceMCPURL(h *coreMCPHarness) string {
	return h.server.URL + "/api/v1/agent/workspace/mcp"
}

// createWorkspaceMCPTask builds a Workspace whose stored row carries every
// field the domain must not expose: root path, ssh identity, credential ref,
// capability secrets and internal Agent API addresses.
func createWorkspaceMCPTask(t *testing.T, h *coreMCPHarness) domain.Task {
	t.Helper()
	ctx := h.ctx
	now := time.Now().UTC()
	project := domain.Project{ID: domain.NewID("project"), Name: "Workspace domain", CreatedAt: now, UpdatedAt: now}
	item := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: project.ID, Name: "Bound workspace",
		Locality: "local", Transport: "native", RootPath: "/srv/secret/root",
		SSHHost: "10.9.8.7", SSHUser: "deploy", SSHPort: 2222, SSHAuth: "password",
		SSHCredentialRef: "vault:ssh-secret", SSHPasswordConfigured: true,
		Distro: "ubuntu", Platform: "linux", Health: "ready",
		Capabilities: map[string]any{
			"claude": map[string]any{
				"status": "ready", "auth_status": "logged_in",
				"auth_token": "sk-live-secret", "base_url": "http://10.9.8.7:8080",
				"models": []any{map[string]any{
					"wire_model": "claude-opus-4-6", "display_name": "Claude Opus",
				}},
			},
		},
		Repository:          map[string]any{"remote": "git@10.9.8.7:secret/repo.git"},
		AgentAPIMode:        "manual",
		AgentAPIURL:         "http://10.9.8.7:8766",
		AgentAPIResolvedURL: "http://10.9.8.7:8766",
		AgentAPIStatus:      "ready",
		AgentAPIError:       "connect http://10.9.8.7:8766 refused",
		LastDetectedAt:      now,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	env := domain.EnvGroup{
		ID: domain.NewID("env"), Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: domain.NewID("model"), DisplayName: "Stub", ProviderID: "stub", Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return h.database.CreateProject(ctx, project) },
		func() error { return h.database.CreateWorkspace(ctx, item) },
		func() error { return h.database.UpsertEnvGroup(ctx, env) },
		func() error { return h.database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	task, err := h.service.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: item.ID, Title: "Workspace domain",
		Request: "inspect workspace", ModelID: model.ID, CollaborationMode: "single", MaxAgents: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

type workspaceMCPBlockingRunner struct {
	started chan struct{}
	once    sync.Once
}

func (r *workspaceMCPBlockingRunner) Run(ctx context.Context, _ workspace.Command, onLine workspace.LineHandler) (workspace.Result, error) {
	r.once.Do(func() {
		onLine("API_KEY=hunter2")
		close(r.started)
	})
	<-ctx.Done()
	return workspace.Result{ExitCode: -1}, ctx.Err()
}

func startWorkspaceMCPProcess(t *testing.T, h *coreMCPHarness, task domain.Task, name string) {
	t.Helper()
	runner := &workspaceMCPBlockingRunner{started: make(chan struct{})}
	if _, err := h.processes.Start(task.ID, "main", runner, managedprocess.StartRequest{
		Name: name, Executable: "/srv/secret/root/bin/agent",
		Args: []string{"--token", "hunter2"}, Dir: "/srv/secret/root",
		Env: map[string]string{"API_KEY": "hunter2"},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatalf("managed process %s did not start", name)
	}
}

func workspaceMCPText(t *testing.T, call coreMCPToolCall) string {
	t.Helper()
	if call.Status != http.StatusOK || call.IsError {
		t.Fatalf("workspace tool payload=%#v", call)
	}
	return call.Text
}

func TestAgentWorkspaceMCPRequiresCapabilityAndDomainGate(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createWorkspaceMCPTask(t, h)
	turn := createCoreTurn(t, h.database, task, "main", "batch-workspace-auth")
	token := h.issue(task.ID, "main", turn.ID)
	url := workspaceMCPURL(h)

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

	missingTurnToken := h.issue(task.ID, "main", "turn-does-not-exist")
	response = agentRequest(t, url, http.MethodPost, missingTurnToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing turn status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	// A capability naming one Task cannot borrow another Task's Turn.
	otherTask := createCoreTaskInProject(t, h, task, "")
	otherTurn := createCoreTurn(t, h.database, otherTask, "main", "batch-workspace-other-task")
	mismatchedToken := h.issue(task.ID, "main", otherTurn.ID)
	response = agentRequest(t, url, http.MethodPost, mismatchedToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("other-task turn status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = agentRequest(t, url, http.MethodPost, token, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "initialize",
	})
	var initialized struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	decodeResponse(t, response, &initialized)
	if response.StatusCode != http.StatusOK || initialized.Result.ServerInfo.Name != "aha2-workspace" {
		t.Fatalf("initialize status=%d payload=%#v", response.StatusCode, initialized)
	}

	status, names, schemas := coreMCPToolsList(t, url, token)
	want := app.WorkspaceMCPToolNames()
	if status != http.StatusOK || len(names) != len(want) {
		t.Fatalf("tools/list status=%d names=%#v want=%#v", status, names, want)
	}
	for index, name := range want {
		if names[index] != name {
			t.Fatalf("tools/list names=%#v want=%#v", names, want)
		}
		schema, ok := schemas[name]
		if !ok || schema["additionalProperties"] != false {
			t.Fatalf("tool %s schema=%#v", name, schema)
		}
	}

	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Real-IP", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		request, err := http.NewRequest(http.MethodPost, url,
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(header, "203.0.113.9")
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s status=%d body=%s", header, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}

	oversized := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":"` +
		strings.Repeat("x", int(agentWorkspaceMCPMaxRequestBytes)+1024) + `"}`)
	response = agentRequestRaw(t, url, http.MethodPost, token, oversized)
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = agentRequestRaw(t, url, http.MethodPost, token, []byte(`null`))
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("null body status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	// A Task in one Project whose stored Workspace belongs to another Project
	// must not receive the domain even though both rows exist.
	now := time.Now().UTC()
	otherProject := domain.Project{ID: domain.NewID("project"), Name: "Other", CreatedAt: now, UpdatedAt: now}
	if err := h.database.CreateProject(h.ctx, otherProject); err != nil {
		t.Fatal(err)
	}
	foreignWorkspace := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: otherProject.ID, Name: "Foreign",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := h.database.CreateWorkspace(h.ctx, foreignWorkspace); err != nil {
		t.Fatal(err)
	}
	crossProjectTask := domain.Task{
		ID: domain.NewID("task"), ProjectID: task.ProjectID, WorkspaceID: foreignWorkspace.ID,
		Title: "Cross project", OriginalRequest: "cross", Status: domain.TaskActive,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, CollaborationMode: "single",
		MaxAgents: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := h.database.CreateTask(h.ctx, crossProjectTask); err != nil {
		t.Fatal(err)
	}
	crossTurn := createCoreTurn(t, h.database, crossProjectTask, "main", "batch-workspace-cross-project")
	crossToken := h.issue(crossProjectTask.ID, "main", crossTurn.ID)
	response = agentRequest(t, url, http.MethodPost, crossToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-project initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	if call := coreMCPCallTool(t, url, crossToken, "workspace_list", map[string]any{}); call.Status != http.StatusForbidden {
		t.Fatalf("cross-project workspace_list payload=%#v", call)
	}

	// A read-only Agent still receives every read tool; the domain has no
	// write surface to hide.
	readOnly, err := h.service.CreateTask(h.ctx, app.CreateTaskInput{
		ProjectID: task.ProjectID, WorkspaceID: task.WorkspaceID, Title: "Read only", Request: "read",
		ModelID: taskModelIDForWorkspaceMCP(t, h, task), Filesystem: "read-only", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	readOnlyTurn := createCoreTurn(t, h.database, readOnly, "sub", "batch-workspace-read-only")
	readOnlyToken := h.issue(readOnly.ID, "sub", readOnlyTurn.ID)
	if status, names, _ := coreMCPToolsList(t, url, readOnlyToken); status != http.StatusOK || len(names) != len(want) {
		t.Fatalf("read-only tools/list status=%d names=%#v", status, names)
	}
	if call := coreMCPCallTool(t, url, readOnlyToken, "workspace_list", map[string]any{}); call.Status != http.StatusOK || call.IsError {
		t.Fatalf("read-only workspace_list payload=%#v", call)
	}
}

func taskModelIDForWorkspaceMCP(t *testing.T, h *coreMCPHarness, task domain.Task) string {
	t.Helper()
	snapshot, err := h.database.RuntimeSnapshot(h.ctx, task.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.ModelID
}

func TestAgentWorkspaceMCPReadsBoundWorkspaceOnly(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createWorkspaceMCPTask(t, h)
	turn := createCoreTurn(t, h.database, task, "main", "batch-workspace-read")
	token := h.issue(task.ID, "main", turn.ID)
	url := workspaceMCPURL(h)

	startWorkspaceMCPProcess(t, h, task, "builder")
	otherTask := createCoreTaskInProject(t, h, task, "")
	startWorkspaceMCPProcess(t, h, otherTask, "worker")

	sensitive := []string{
		"/srv/secret/root", "10.9.8.7", "deploy", "vault:ssh-secret", "sk-live-secret",
		"git@10.9.8.7", `"root_path"`, `"ssh_host"`, `"ssh_user"`, `"ssh_auth"`,
		`"capabilities"`, `"repository"`, `"agent_api_url"`, `"agent_api_resolved_url"`,
	}
	assertClean := func(label, text string) {
		t.Helper()
		for _, leak := range sensitive {
			if strings.Contains(text, leak) {
				t.Fatalf("%s leaked %q: %s", label, leak, text)
			}
		}
	}

	workspaceText := workspaceMCPText(t, coreMCPCallTool(t, url, token, "workspace_list", map[string]any{}))
	assertClean("workspace_list", workspaceText)
	var listed struct {
		Workspaces []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Transport string `json:"transport"`
			Health    string `json:"health"`
		} `json:"workspaces"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(workspaceText), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Total != 1 || len(listed.Workspaces) != 1 ||
		listed.Workspaces[0].ID != task.WorkspaceID || listed.Workspaces[0].Name != "Bound workspace" {
		t.Fatalf("workspace_list payload=%s", workspaceText)
	}

	runtimeText := workspaceMCPText(t, coreMCPCallTool(t, url, token, "workspace_runtime_list", map[string]any{}))
	assertClean("workspace_runtime_list", runtimeText)
	for _, leak := range []string{"sk-live-secret", "codex_account_id", `"environment"`, "auth_token", "base_url"} {
		if strings.Contains(runtimeText, leak) {
			t.Fatalf("workspace_runtime_list leaked %q: %s", leak, runtimeText)
		}
	}
	if !strings.Contains(runtimeText, "stub") || !strings.Contains(runtimeText, "claude-opus-4-6") {
		t.Fatalf("workspace_runtime_list is missing expected non-secret options: %s", runtimeText)
	}

	processText := workspaceMCPText(t, coreMCPCallTool(t, url, token, "workspace_process_list", map[string]any{}))
	for _, leak := range []string{
		"hunter2", "/srv/secret/root", `"executable"`, `"args"`, `"cwd"`, `"output_tail"`, `"error"`,
	} {
		if strings.Contains(processText, leak) {
			t.Fatalf("workspace_process_list leaked %q: %s", leak, processText)
		}
	}
	if !strings.Contains(processText, `"name":"builder"`) || strings.Contains(processText, `"name":"worker"`) {
		t.Fatalf("workspace_process_list scope=%s", processText)
	}

	getText := workspaceMCPText(t, coreMCPCallTool(t, url, token, "workspace_process_get", map[string]any{"name": "builder"}))
	for _, leak := range []string{
		"hunter2", "/srv/secret/root", `"executable"`, `"args"`, `"cwd"`, `"output_tail"`, `"error"`,
	} {
		if strings.Contains(getText, leak) {
			t.Fatalf("workspace_process_get leaked %q: %s", leak, getText)
		}
	}
	if !strings.Contains(getText, `"name":"builder"`) || !strings.Contains(getText, `"state":"running"`) {
		t.Fatalf("workspace_process_get payload=%s", getText)
	}

	// Same-Turn HTTP/MCP equivalence: both surfaces report the same process
	// state, while only the legacy HTTP endpoint still carries the command.
	response := agentRequest(t, h.server.URL+"/api/v1/agent/processes/builder", http.MethodGet, token, nil)
	var httpStatus struct {
		Process struct {
			Name       string `json:"name"`
			State      string `json:"state"`
			Executable string `json:"executable"`
		} `json:"process"`
	}
	decodeResponse(t, response, &httpStatus)
	if response.StatusCode != http.StatusOK || httpStatus.Process.Name != "builder" ||
		httpStatus.Process.State != "running" || !strings.Contains(httpStatus.Process.Executable, "/srv/secret/root") {
		t.Fatalf("HTTP process status=%d payload=%#v", response.StatusCode, httpStatus)
	}

	response = agentRequest(t, h.server.URL+"/api/v1/agent/processes/ghost", http.MethodGet, token, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("HTTP unknown process status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	missing := coreMCPCallTool(t, url, token, "workspace_process_get", map[string]any{"name": "ghost"})
	if missing.Status != http.StatusOK || !missing.IsError || !strings.Contains(missing.Text, "workspace_process_not_found") {
		t.Fatalf("MCP unknown process payload=%#v", missing)
	}
	// A process owned by another Task is reported exactly like an unknown name.
	foreign := coreMCPCallTool(t, url, token, "workspace_process_get", map[string]any{"name": "worker"})
	if foreign.Status != http.StatusOK || !foreign.IsError || !strings.Contains(foreign.Text, "workspace_process_not_found") {
		t.Fatalf("MCP cross-task process payload=%#v", foreign)
	}

	for _, name := range []string{
		"../../etc/passwd", "a/b", `a\b`, ".hidden", "-flag", "name with space", "",
		strings.Repeat("a", 65),
	} {
		call := coreMCPCallTool(t, url, token, "workspace_process_get", map[string]any{"name": name})
		if call.Status != http.StatusOK || !call.IsError ||
			!strings.Contains(call.Text, "workspace_process_name_invalid") {
			t.Fatalf("name %q payload=%#v", name, call)
		}
	}

	unknown := coreMCPCallTool(t, url, token, "workspace_delete", map[string]any{})
	if unknown.Status != http.StatusOK || unknown.IsError || unknown.Code != -32602 {
		t.Fatalf("unknown tool payload=%#v", unknown)
	}
	unknownArgument := coreMCPCallTool(t, url, token, "workspace_list", map[string]any{"workspace_id": task.WorkspaceID})
	if unknownArgument.Status != http.StatusOK || !unknownArgument.IsError ||
		!strings.Contains(unknownArgument.Text, "invalid_arguments") {
		t.Fatalf("unknown argument payload=%#v", unknownArgument)
	}

	data := waitForWorkspaceMCPAudit(t, h.dbPath, "workspace_process_get")
	for _, leak := range []string{"hunter2", "/srv/secret/root", "10.9.8.7"} {
		if strings.Contains(data, leak) {
			t.Fatalf("audit leaked %q: %s", leak, data)
		}
	}
	if !strings.Contains(data, `"workspace_id":"`+task.WorkspaceID+`"`) ||
		!strings.Contains(data, `"argument_keys":["name"]`) {
		t.Fatalf("audit shape=%s", data)
	}
}

func waitForWorkspaceMCPAudit(t *testing.T, path, tool string) string {
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
			`SELECT data_json FROM audit_events WHERE action='agent.workspace_mcp.call' AND data_json LIKE ? ORDER BY sequence DESC LIMIT 1`,
			`%"tool":"`+tool+`"%`,
		).Scan(&data)
		if err == nil && data != "" {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("no workspace MCP audit row: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAgentWorkspaceMCPRejectsChannelBoundTurns covers the shared three-state
// channel determination: a mixed plain+channel batch is denied in either
// insertion order and for both channel modes, an unclassifiable batch fails
// closed, and a plain batch stays allowed.
func TestAgentWorkspaceMCPRejectsChannelBoundTurns(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createWorkspaceMCPTask(t, h)
	url := workspaceMCPURL(h)

	assertDenied := func(label, token string) {
		t.Helper()
		response := agentRequest(t, url, http.MethodPost, token, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
		})
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s initialize status=%d body=%s", label, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
		if status, names, _ := coreMCPToolsList(t, url, token); status != http.StatusForbidden {
			t.Fatalf("%s tools/list status=%d names=%#v", label, status, names)
		}
		if call := coreMCPCallTool(t, url, token, "workspace_list", map[string]any{}); call.Status != http.StatusForbidden {
			t.Fatalf("%s workspace_list payload=%#v", label, call)
		}
	}

	control := createCoreTurn(t, h.database, base, "main", "batch-workspace-plain")
	seedKnowledgeMCPInbox(t, h.dbPath, base, control, "plain")
	controlToken := h.issue(base.ID, "main", control.ID)
	response := agentRequest(t, url, http.MethodPost, controlToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("plain Turn initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	if call := coreMCPCallTool(t, url, controlToken, "workspace_list", map[string]any{}); call.Status != http.StatusOK || call.IsError {
		t.Fatalf("plain Turn workspace_list payload=%#v", call)
	}

	orders := []struct {
		label string
		kinds []string
	}{
		{label: "plain then channel", kinds: []string{"plain", "channel"}},
		{label: "channel then plain", kinds: []string{"channel", "plain"}},
	}
	for name, mode := range map[string]string{
		"conversation host": "conversation_host",
		"task route":        "task_route",
	} {
		for _, order := range orders {
			task := createCoreTaskInProject(t, h, base, "")
			batchID := "batch-workspace-" + strings.ReplaceAll(mode, "_", "-") + "-" +
				strings.ReplaceAll(order.label, " ", "-")
			turn := createCoreTurn(t, h.database, task, "main", batchID)
			kinds := make([]string, 0, len(order.kinds))
			for _, kind := range order.kinds {
				if kind == "channel" {
					kinds = append(kinds, "channel:"+mode)
					continue
				}
				kinds = append(kinds, kind)
			}
			seedKnowledgeMCPInbox(t, h.dbPath, task, turn, kinds...)
			assertDenied(name+" "+order.label, h.issue(task.ID, "main", turn.ID))
		}
	}

	broken := createCoreTaskInProject(t, h, base, "")
	brokenTurn := createCoreTurn(t, h.database, broken, "main", "batch-workspace-malformed")
	seedKnowledgeMCPInbox(t, h.dbPath, broken, brokenTurn, "raw:not-json")
	assertDenied("unclassifiable batch", h.issue(broken.ID, "main", brokenTurn.ID))
}
