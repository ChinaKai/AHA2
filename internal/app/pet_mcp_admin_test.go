package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var petMCPAdminToolNames = []string{
	"aha_list_mcp_servers", "aha_test_mcp_server",
	"aha_list_task_mcp_servers", "aha_set_task_mcp_servers",
	"aha_request_mcp_server_create", "aha_request_mcp_server_update",
	"aha_request_mcp_server_delete",
}

func petMCPAdminToolCall(t *testing.T, fixture *petTaskFixture, name, args string) string {
	t.Helper()
	raw, err := fixture.service.PetMCPTool(fixture.ctx, name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s(%s): %v", name, args, err)
	}
	return raw
}

func petMCPAdminToolError(t *testing.T, fixture *petTaskFixture, name, args string) error {
	t.Helper()
	_, err := fixture.service.PetMCPTool(fixture.ctx, name, json.RawMessage(args))
	if err == nil {
		t.Fatalf("%s(%s) unexpectedly succeeded", name, args)
	}
	return err
}

func decodePetMCPApproval(t *testing.T, raw string) domain.PetApproval {
	t.Helper()
	var approval domain.PetApproval
	if err := json.Unmarshal([]byte(raw), &approval); err != nil {
		t.Fatalf("decode approval %q: %v", raw, err)
	}
	return approval
}

func decodePetMCPTaskView(t *testing.T, raw string) PetTaskMCPServersView {
	t.Helper()
	var view PetTaskMCPServersView
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatalf("decode task mcp view %q: %v", raw, err)
	}
	return view
}

func insertPetMCPTestServer(t *testing.T, fixture *petTaskFixture, item domain.MCPServer) {
	t.Helper()
	item.Transport = firstNonEmptyTrimmed(item.Transport, "http")
	item.Name = firstNonEmptyTrimmed(item.Name, item.ID)
	item.Health = firstNonEmptyTrimmed(item.Health, "unknown")
	item.CreatedAt = fixture.createdAt
	item.UpdatedAt = fixture.createdAt
	if err := fixture.database.UpsertMCPServer(fixture.ctx, item); err != nil {
		t.Fatal(err)
	}
}

func createPetMCPAdminTask(t *testing.T, fixture *petTaskFixture, name string) domain.Task {
	t.Helper()
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: name + " Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: name + " Task",
		Request: "等待 MCP 绑定", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestPetMCPAdminToolRegistrationAndSchema(t *testing.T) {
	registered := map[string]gatewayToolShape{}
	for _, tool := range ahaPetTools() {
		registered[tool.Name] = gatewayToolShape{Description: tool.Description, Parameters: tool.Parameters}
	}
	allowedTools := map[string]bool{}
	for _, name := range petMCPAllowedTools(domain.Task{ID: petTaskID}) {
		allowedTools[name] = true
	}
	allowed := domain.OwnerAssistant{PetAllowManageSystem: true}
	denied := domain.OwnerAssistant{
		PetAllowViewTasks: true, PetAllowCreateTasks: true,
		PetAllowCreateWorkspaces: true, PetAllowMessageTasks: true,
	}
	for _, name := range petMCPAdminToolNames {
		tool, ok := registered[name]
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if !isAhaPetTool(name) {
			t.Fatalf("%s is not recognized as an AHA pet tool", name)
		}
		if !petToolAllowed(allowed, name) {
			t.Fatalf("%s is not allowed with manage_system", name)
		}
		if petToolAllowed(denied, name) {
			t.Fatalf("%s is allowed without manage_system", name)
		}
		if !allowedTools["mcp__aha2-pet__"+name] {
			t.Fatalf("%s is not exposed through the pet MCP allowlist", name)
		}
		if !strings.Contains(tool.Description, "只") && !strings.Contains(tool.Description, "审批") &&
			!strings.Contains(tool.Description, "设置") && !strings.Contains(tool.Description, "探测") {
			t.Fatalf("%s has an uninformative description: %q", name, tool.Description)
		}
	}

	create := registered["aha_request_mcp_server_create"]
	properties, _ := create.Parameters["properties"].(map[string]any)
	if _, ok := properties["env"]; ok {
		t.Fatal("create schema must not accept env values")
	}
	if _, ok := properties["headers"]; ok {
		t.Fatal("create schema must not accept header values")
	}
	for _, key := range []string{"env_keys", "header_keys"} {
		if _, ok := properties[key]; !ok {
			t.Fatalf("create schema missing %s: %#v", key, properties)
		}
	}
	if !strings.Contains(create.Description, "不接收 secret") || !strings.Contains(create.Description, "shell") {
		t.Fatalf("create description missing security boundary: %q", create.Description)
	}
	testTool := registered["aha_test_mcp_server"]
	if !strings.Contains(testTool.Description, "脱敏") || !strings.Contains(testTool.Description, "不修改配置") {
		t.Fatalf("test description missing boundary: %q", testTool.Description)
	}
}

type gatewayToolShape struct {
	Description string
	Parameters  map[string]any
}

func TestPetMCPAdminRequiresManageSystem(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, false)
	for _, name := range petMCPAdminToolNames {
		_, err := fixture.service.PetMCPTool(fixture.ctx, name, json.RawMessage(`{}`))
		if err == nil || !strings.Contains(err.Error(), "主人未开启") {
			t.Fatalf("%s without manage_system err=%v", name, err)
		}
	}
}

func TestPetMCPAdminListRedactsSecrets(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_secret_test", Name: "Secret Desk", Transport: "http",
		URL: "https://example.test/mcp", Enabled: true,
		Env:     map[string]string{"API_TOKEN": "env-secret-value", "PLAIN": "plain-value"},
		Headers: map[string]string{"Authorization": "Bearer header-secret-value"},
		Tools:   []domain.MCPTool{{Name: "desk_status"}},
	})

	raw := petMCPAdminToolCall(t, fixture, "aha_list_mcp_servers", `{}`)
	for _, secret := range []string{"env-secret-value", "header-secret-value", "Bearer"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("list leaked %q: %s", secret, raw)
		}
	}
	for _, want := range []string{"mcp_secret_test", "API_TOKEN", "PLAIN", "Authorization"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("list missing %q: %s", want, raw)
		}
	}
	if strings.Contains(raw, "https://example.test") {
		t.Fatalf("list should not echo the endpoint URL: %s", raw)
	}
	if !strings.Contains(raw, "mcp_pet") {
		t.Fatalf("list should include the builtin pet MCP: %s", raw)
	}
	var view PetMCPServerListView
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	var listed *PetMCPServerView
	for index := range view.Servers {
		if view.Servers[index].ID == "mcp_secret_test" {
			listed = &view.Servers[index]
		}
	}
	if listed == nil || listed.ToolCount != 1 || listed.Health != "unknown" ||
		len(listed.EnvKeys) != 2 || listed.EnvKeys[0] != "API_TOKEN" || listed.EnvKeys[1] != "PLAIN" ||
		len(listed.HeaderKeys) != 1 || listed.HeaderKeys[0] != "Authorization" {
		t.Fatalf("listed server=%#v", listed)
	}
}

func TestPetMCPAdminTestProbeSuccessAndFailure(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	success := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"desk_status"},{"name":"desk_move"}]}}`))
	}))
	defer success.Close()
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_ok", Name: "OK Desk", Transport: "http", URL: success.URL, Enabled: true,
	})

	raw := petMCPAdminToolCall(t, fixture, "aha_test_mcp_server", `{"server_id":"mcp_ok"}`)
	var result PetMCPServerTestResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Health != "ok" || result.ToolCount != 2 || result.Probe != "http" ||
		len(result.Tools) != 2 || result.Tools[0] != "desk_status" {
		t.Fatalf("success probe=%#v", result)
	}

	failure := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "boom", http.StatusInternalServerError)
	}))
	defer failure.Close()
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_bad", Name: "Bad Desk", Transport: "http", URL: failure.URL, Enabled: true,
		Env: map[string]string{"API_TOKEN": "probe-secret-value"},
	})
	raw = petMCPAdminToolCall(t, fixture, "aha_test_mcp_server", `{"server_id":"mcp_bad"}`)
	result = PetMCPServerTestResult{}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Health != "error" || result.Error == "" {
		t.Fatalf("failure probe=%#v", result)
	}
	if strings.Contains(raw, "probe-secret-value") {
		t.Fatalf("probe leaked env value: %s", raw)
	}

	builtin := petMCPAdminToolCall(t, fixture, "aha_test_mcp_server", `{"server_id":"mcp_pet"}`)
	if !strings.Contains(builtin, `"health":"ok"`) || !strings.Contains(builtin, `"probe":"builtin"`) {
		t.Fatalf("builtin probe=%s", builtin)
	}

	// stdio probes start the configured process and read its tools/list reply.
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_stdio", Name: "Stdio Desk", Transport: "stdio",
		Command: os.Args[0], Args: []string{"-test.run=TestStdioMCPServerHelper"},
		Env: map[string]string{"GO_WANT_MCP_STDIO_HELPER": "1"}, Enabled: true,
	})
	stdioRaw := petMCPAdminToolCall(t, fixture, "aha_test_mcp_server", `{"server_id":"mcp_stdio"}`)
	var stdioResult PetMCPServerTestResult
	if err := json.Unmarshal([]byte(stdioRaw), &stdioResult); err != nil {
		t.Fatal(err)
	}
	if stdioResult.Health != "ok" || stdioResult.Probe != "stdio" ||
		len(stdioResult.Tools) != 1 || stdioResult.Tools[0] != "stdio_echo" {
		t.Fatalf("stdio probe=%#v", stdioResult)
	}

	// A hostile server can name its own tools after the secret it was given;
	// the probe result must still be redacted.
	echo := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"tool-probe-secret-value"}]}}`))
	}))
	defer echo.Close()
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_echo", Name: "Echo Desk", Transport: "http", URL: echo.URL, Enabled: true,
		Env: map[string]string{"API_TOKEN": "probe-secret-value"},
	})
	echoRaw := petMCPAdminToolCall(t, fixture, "aha_test_mcp_server", `{"server_id":"mcp_echo"}`)
	if strings.Contains(echoRaw, "probe-secret-value") {
		t.Fatalf("tool names leaked env value: %s", echoRaw)
	}
	if err := petMCPAdminToolError(t, fixture, "aha_test_mcp_server", `{"server_id":"missing"}`); !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("missing probe err=%v", err)
	}
}

func TestPetMCPTestPersistsProbedToolsForLaterTurns(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	probedToolNames := []string{
		"board_status", "display_text", "display_clear", "display_screenshot",
		"camera_snapshot", "audio_beep", "audio_play",
	}
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[` +
			`{"name":"board_status"},{"name":"display_text"},{"name":"display_clear"},` +
			`{"name":"display_screenshot"},{"name":"camera_snapshot"},` +
			`{"name":"audio_beep"},{"name":"audio_play"}]}}`))
	}))
	defer endpoint.Close()
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_persist", Name: "Persist Desk", Transport: "http", URL: endpoint.URL, Enabled: true,
		Tools: []domain.MCPTool{
			{Name: "board_status"}, {Name: "display_text"}, {Name: "display_clear"},
		}, Health: "ok",
	})

	result, err := fixture.service.PetTestMCPServer(fixture.ctx, "mcp_persist")
	if err != nil {
		t.Fatal(err)
	}
	if result.ToolCount != len(probedToolNames) {
		t.Fatalf("probe result=%#v", result)
	}
	stored, err := fixture.service.MCPServerByID(fixture.ctx, "mcp_persist")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Tools) != len(probedToolNames) || stored.Health != "ok" || stored.LastCheckedAt.IsZero() {
		t.Fatalf("probe was not cached: %#v", stored)
	}
	list, err := fixture.service.PetListMCPServers(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, server := range list.Servers {
		if server.ID == "mcp_persist" {
			found = true
			if server.ToolCount != len(probedToolNames) {
				t.Fatalf("listed tool_count=%d, want %d", server.ToolCount, len(probedToolNames))
			}
		}
	}
	if !found {
		t.Fatal("mcp_persist missing from list")
	}
	// The next Turn builds --allowedTools from the freshly probed cache.
	_, allowed := fixture.service.mcpRuntimeForTask(fixture.ctx, domain.Task{
		ID: "task_after_mcp_probe", MCPServerIDs: []string{"mcp_persist"},
	}, "")
	want := make(map[string]bool, len(probedToolNames))
	for _, tool := range probedToolNames {
		want["mcp__mcp_persist__"+tool] = true
	}
	if len(allowed) != len(want) {
		t.Fatalf("allowed=%#v", allowed)
	}
	for _, name := range allowed {
		if !want[name] {
			t.Fatalf("allowed=%#v contains unexpected or duplicate %q", allowed, name)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("allowed=%#v is missing %v", allowed, want)
	}

	failure := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "boom", http.StatusInternalServerError)
	}))
	defer failure.Close()
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_keep_cache", Name: "Keep Cache", Transport: "http", URL: failure.URL, Enabled: true,
		Tools: []domain.MCPTool{{Name: "board_status"}}, Health: "ok",
	})
	if _, err := fixture.service.PetTestMCPServer(fixture.ctx, "mcp_keep_cache"); err != nil {
		t.Fatal(err)
	}
	kept, err := fixture.service.MCPServerByID(fixture.ctx, "mcp_keep_cache")
	if err != nil {
		t.Fatal(err)
	}
	if len(kept.Tools) != 1 || kept.Tools[0].Name != "board_status" || kept.Health != "error" {
		t.Fatalf("failed probe dropped cached tools: %#v", kept)
	}
}

func TestPetMCPAdminCreateApprovalLifecycle(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	if err := fixture.service.secrets.PutMany(map[string]string{
		"mcp/mcp_luckfox_desk/env/API_TOKEN": "secret-from-store",
	}); err != nil {
		t.Fatal(err)
	}

	raw := petMCPAdminToolCall(t, fixture, "aha_request_mcp_server_create",
		`{"name":"Luckfox Desk","server_id":"mcp_luckfox_desk","description":"桌面串口","transport":"stdio","command":"node","args":["server.js"],"env_keys":["API_TOKEN"],"reason":"需要桌宠串口工具"}`)
	approval := decodePetMCPApproval(t, raw)
	if approval.Kind != petMCPServerCreateKind || approval.Status != petApprovalPending || approval.Risk != "high" {
		t.Fatalf("approval=%#v", approval)
	}
	for _, forbidden := range []string{"secret-from-store", `"env":`, `"headers":`} {
		if strings.Contains(approval.Payload, forbidden) {
			t.Fatalf("approval payload leaked %q: %s", forbidden, approval.Payload)
		}
	}
	if !strings.Contains(approval.Payload, "env_keys") || !strings.Contains(approval.Summary, "Secret Store") {
		t.Fatalf("approval payload/summary missing key metadata: %#v", approval)
	}
	if !strings.Contains(approval.Summary, "参数：server.js") {
		t.Fatalf("create summary missing stdio args: %s", approval.Summary)
	}
	if strings.Contains(approval.Summary, "secret-from-store") {
		t.Fatalf("create summary leaked the stored secret: %s", approval.Summary)
	}
	if _, err := fixture.service.MCPServerByID(fixture.ctx, "mcp_luckfox_desk"); err == nil {
		t.Fatal("MCP was saved before approval")
	}

	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	saved, err := fixture.service.MCPServerByID(fixture.ctx, "mcp_luckfox_desk")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Transport != "stdio" || saved.Command != "node" || !saved.Enabled ||
		saved.Env["API_TOKEN"] != "secret-from-store" {
		t.Fatalf("saved MCP=%#v", saved)
	}
	// Replaying the approved action is a no-op, not a duplicate write.
	if err := fixture.service.executePetApproval(fixture.ctx, approval); err != nil {
		t.Fatalf("idempotent create replay failed: %v", err)
	}

	rejectRaw := petMCPAdminToolCall(t, fixture, "aha_request_mcp_server_create",
		`{"name":"Rejected Desk","server_id":"mcp_rejected_desk","transport":"stdio","command":"node","reason":"验证拒绝路径不写库"}`)
	rejected := decodePetMCPApproval(t, rejectRaw)
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, rejected.ID, "reject", "不需要这个 MCP"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.MCPServerByID(fixture.ctx, "mcp_rejected_desk"); err == nil {
		t.Fatal("rejected MCP was written")
	}
}

func TestPetMCPAdminUpdateAndDeleteApproval(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_desk", Name: "Desk", Transport: "http",
		URL: "https://example.test/mcp", Enabled: true,
		Env: map[string]string{"API_TOKEN": "existing-secret"},
	})

	raw := petMCPAdminToolCall(t, fixture, "aha_request_mcp_server_update",
		`{"server_id":"mcp_desk","name":"Desk Renamed","reason":"统一桌面工具命名"}`)
	approval := decodePetMCPApproval(t, raw)
	if approval.Kind != petMCPServerUpdateKind || strings.Contains(approval.Payload, "existing-secret") {
		t.Fatalf("update approval=%#v", approval)
	}
	before, _ := fixture.service.MCPServerByID(fixture.ctx, "mcp_desk")
	if before.Name != "Desk" {
		t.Fatalf("update applied before approval: %#v", before)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	updated, _ := fixture.service.MCPServerByID(fixture.ctx, "mcp_desk")
	if updated.Name != "Desk Renamed" || updated.Env["API_TOKEN"] != "existing-secret" {
		t.Fatalf("updated MCP=%#v", updated)
	}
	if err := fixture.service.executePetApproval(fixture.ctx, approval); err != nil {
		t.Fatalf("idempotent update replay failed: %v", err)
	}

	deleteRaw := petMCPAdminToolCall(t, fixture, "aha_request_mcp_server_delete",
		`{"server_id":"mcp_desk","reason":"不再使用桌面 MCP"}`)
	deleted := decodePetMCPApproval(t, deleteRaw)
	if deleted.Kind != petMCPServerDeleteKind {
		t.Fatalf("delete approval=%#v", deleted)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, deleted.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.MCPServerByID(fixture.ctx, "mcp_desk"); err == nil {
		t.Fatal("MCP was not deleted")
	}
	if err := fixture.service.executePetApproval(fixture.ctx, deleted); err != nil {
		t.Fatalf("idempotent delete replay failed: %v", err)
	}
}

func TestPetMCPAdminRejectsUnsafeConfigAndBuiltins(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	before, _ := fixture.service.PetApprovals(fixture.ctx, 100)

	cases := []struct {
		args string
		want string
	}{
		{`{"name":"x","transport":"sse","url":"https://example.test/mcp","reason":"非法 transport 测试"}`, "transport"},
		{`{"name":"x","transport":"stdio","command":"cmd.exe","reason":"shell 包装测试"}`, "shell"},
		{`{"name":"x","transport":"stdio","command":"sh","reason":"shell 包装测试"}`, "shell"},
		{`{"name":"x","transport":"stdio","command":"node.cmd","reason":"bat 包装测试"}`, "shell"},
		{`{"name":"x","transport":"stdio","command":"node","args":["--token=leak"],"reason":"secret 参数测试"}`, "secret"},
		{`{"name":"x","transport":"http","url":"https://example.test/mcp?token=leak","reason":"拒绝 URL 查询参数"}`, "查询参数"},
		{`{"name":"x","transport":"http","url":"https://user:pass@example.test/mcp","reason":"URL 凭据测试"}`, "凭据"},
		{`{"name":"x","transport":"http","url":"https://example.test/mcp","reason":"secret 夹带测试","env":{"TOKEN":"leak"}}`, "unknown field"},
		{`{"name":"x","transport":"http","url":"https://example.test/mcp","reason":"header 夹带测试","headers":{"Authorization":"leak"}}`, "unknown field"},
		{`{"name":"x","transport":"http","url":"https://example.test/mcp","server_id":"mcp_pet","reason":"内置 ID 测试"}`, "内置"},
	}
	for _, testCase := range cases {
		err := petMCPAdminToolError(t, fixture, "aha_request_mcp_server_create", testCase.args)
		if !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("args=%s err=%v want=%q", testCase.args, err, testCase.want)
		}
		if strings.Contains(err.Error(), "leak") {
			t.Fatalf("error echoed secret: %v", err)
		}
	}
	after, _ := fixture.service.PetApprovals(fixture.ctx, 100)
	if len(after) != len(before) {
		t.Fatalf("rejected configs created approvals: before=%d after=%d", len(before), len(after))
	}

	if err := petMCPAdminToolError(t, fixture, "aha_request_mcp_server_update",
		`{"server_id":"mcp_pet","name":"改名","reason":"内置 MCP 修改测试"}`); !strings.Contains(err.Error(), "内置") {
		t.Fatalf("builtin update err=%v", err)
	}
	if err := petMCPAdminToolError(t, fixture, "aha_request_mcp_server_delete",
		`{"server_id":"mcp_pet","reason":"内置 MCP 删除测试"}`); !strings.Contains(err.Error(), "内置") {
		t.Fatalf("builtin delete err=%v", err)
	}
}

func TestPetMCPAdminTaskBinding(t *testing.T) {
	fixture := newPetTaskFixture(t)
	petTask := fixture.createPetDraft(t, "管理自己的 MCP")
	enablePetHardwareTestSettings(t, fixture, true)
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_luckfox_desk", Name: "Luckfox Desk", Transport: "http",
		URL: "https://example.test/mcp", Enabled: true,
	})
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_disabled", Name: "Disabled", Transport: "http",
		URL: "https://example.test/disabled", Enabled: false,
	})

	raw := petMCPAdminToolCall(t, fixture, "aha_set_task_mcp_servers",
		`{"task_id":"`+petTask.ID+`","server_ids":["mcp_luckfox_desk"]}`)
	view := decodePetMCPTaskView(t, raw)
	if view.Status != "applied" {
		t.Fatalf("pet binding status=%q", view.Status)
	}
	bound := map[string]bool{}
	for _, id := range view.MCPServerIDs {
		bound[id] = true
	}
	if !bound["mcp_luckfox_desk"] || !bound["mcp_pet"] {
		t.Fatalf("pet binding lost mcp_luckfox_desk or mcp_pet: %v", view.MCPServerIDs)
	}
	stored, _ := fixture.database.Task(fixture.ctx, petTask.ID)
	for _, want := range []string{"mcp_luckfox_desk", "mcp_pet"} {
		found := false
		for _, id := range stored.MCPServerIDs {
			if id == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("stored task MCP ids=%v missing %s", stored.MCPServerIDs, want)
		}
	}
	listRaw := petMCPAdminToolCall(t, fixture, "aha_list_task_mcp_servers", `{"task_id":"`+petTask.ID+`"}`)
	listView := decodePetMCPTaskView(t, listRaw)
	if len(listView.Servers) != 2 || listView.Servers[0].EnvKeys != nil {
		t.Fatalf("task list view=%#v", listView)
	}

	if err := petMCPAdminToolError(t, fixture, "aha_set_task_mcp_servers",
		`{"task_id":"`+petTask.ID+`","server_ids":["mcp_disabled"]}`); !strings.Contains(err.Error(), "未启用") {
		t.Fatalf("disabled binding err=%v", err)
	}
	if err := petMCPAdminToolError(t, fixture, "aha_set_task_mcp_servers",
		`{"task_id":"`+petTask.ID+`","server_ids":["mcp_missing"]}`); !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("missing binding err=%v", err)
	}
	if err := petMCPAdminToolError(t, fixture, "aha_set_task_mcp_servers",
		`{"task_id":"`+petTask.ID+`","server_ids":["mcp_channel"]}`); !strings.Contains(err.Error(), "自动注入") {
		t.Fatalf("auto inject binding err=%v", err)
	}

	other := createPetMCPAdminTask(t, fixture, "普通任务")
	if err := petMCPAdminToolError(t, fixture, "aha_set_task_mcp_servers",
		`{"task_id":"`+other.ID+`","server_ids":["mcp_luckfox_desk"]}`); !strings.Contains(err.Error(), "审批") {
		t.Fatalf("other task without reason err=%v", err)
	}
	if err := petMCPAdminToolError(t, fixture, "aha_set_task_mcp_servers",
		`{"task_id":"`+other.ID+`","server_ids":["mcp_pet"],"reason":"尝试绑定宠物 MCP 到普通任务"}`); !strings.Contains(err.Error(), "宠物 Task") {
		t.Fatalf("mcp_pet on normal task err=%v", err)
	}
	raw = petMCPAdminToolCall(t, fixture, "aha_set_task_mcp_servers",
		`{"task_id":"`+other.ID+`","server_ids":["mcp_luckfox_desk"],"reason":"这个任务也需要桌宠工具"}`)
	pending := decodePetMCPTaskView(t, raw)
	if pending.Status != petApprovalPending || pending.Approval == nil || pending.Approval.Kind != petSetTaskMCPKind {
		t.Fatalf("pending task view=%#v", pending)
	}
	unchanged, _ := fixture.database.Task(fixture.ctx, other.ID)
	if len(unchanged.MCPServerIDs) != 0 {
		t.Fatalf("other task changed before approval: %v", unchanged.MCPServerIDs)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, pending.Approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	changed, _ := fixture.database.Task(fixture.ctx, other.ID)
	if len(changed.MCPServerIDs) != 1 || changed.MCPServerIDs[0] != "mcp_luckfox_desk" {
		t.Fatalf("approved task MCP ids=%v", changed.MCPServerIDs)
	}
}
