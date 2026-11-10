package app

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetMCPAdminUpdateDiffDoesNotLeakExistingConfig(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_web_stdio", Name: "Web Stdio", Transport: "stdio",
		Command: "node", Args: []string{"server.js", "positional-secret-value"},
		Enabled: true, Env: map[string]string{"API_TOKEN": "existing-env-secret"},
	})
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_web_http", Name: "Web HTTP", Transport: "http",
		URL:     "https://old.example.test/path-secret-token/mcp",
		Enabled: true,
		Headers: map[string]string{"Authorization": "Bearer existing-header-secret"},
	})

	forbidden := []string{
		"server.js", "positional-secret-value", "existing-env-secret",
		"path-secret-token", "old.example.test", "existing-header-secret",
	}
	for _, serverID := range []string{"mcp_web_stdio", "mcp_web_http"} {
		raw := petMCPAdminToolCall(t, fixture, "aha_request_mcp_server_update",
			`{"server_id":"`+serverID+`","name":"Renamed `+serverID+`","reason":"统一桌面工具命名"}`)
		approval := decodePetMCPApproval(t, raw)
		for _, value := range forbidden {
			if strings.Contains(raw, value) {
				t.Fatalf("%s update result leaked %q: %s", serverID, value, raw)
			}
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(approval.Payload), &payload); err != nil {
			t.Fatalf("%s payload %q: %v", serverID, approval.Payload, err)
		}
		for _, key := range []string{"command", "args", "url", "env_keys", "header_keys", "enabled"} {
			if _, ok := payload[key]; ok {
				t.Fatalf("%s payload carried unchanged field %q: %s", serverID, key, approval.Payload)
			}
		}
		if _, ok := payload["name"]; !ok {
			t.Fatalf("%s payload missing changed name: %s", serverID, approval.Payload)
		}
		if strings.Contains(approval.Summary, "stdio args") || strings.Contains(approval.Summary, "HTTP 地址") {
			t.Fatalf("%s summary described unchanged endpoint: %s", serverID, approval.Summary)
		}
	}

	listRaw := petMCPAdminToolCall(t, fixture, "pet_list_approvals", `{}`)
	for _, value := range forbidden {
		if strings.Contains(listRaw, value) {
			t.Fatalf("pet_list_approvals leaked %q: %s", value, listRaw)
		}
	}
}

func TestPetMCPAdminUpdateEndpointChangeDropsSecretsAndWarns(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	var mu sync.Mutex
	authHeaders := []string{}
	newEndpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		authHeaders = append(authHeaders, request.Header.Get("Authorization"))
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"desk_status"}]}}`))
	}))
	defer newEndpoint.Close()

	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_move", Name: "Move Desk", Transport: "http",
		URL:     "https://old.example.test/mcp",
		Enabled: true,
		Env:     map[string]string{"API_TOKEN": "old-env-secret"},
		Headers: map[string]string{"Authorization": "Bearer hdr-secret-1"},
	})

	raw := petMCPAdminToolCall(t, fixture, "aha_request_mcp_server_update",
		`{"server_id":"mcp_move","url":"`+newEndpoint.URL+`","reason":"迁移到新的桌面端点"}`)
	approval := decodePetMCPApproval(t, raw)
	for _, forbidden := range []string{"old-env-secret", "hdr-secret-1", "old.example.test"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("update approval leaked %q: %s", forbidden, raw)
		}
	}
	for _, want := range []string{newEndpoint.URL, "不会自动沿用", "2 个"} {
		if !strings.Contains(approval.Summary, want) {
			t.Fatalf("summary missing %q: %s", want, approval.Summary)
		}
	}
	if !strings.Contains(approval.Payload, `"url"`) || !strings.Contains(approval.Payload, newEndpoint.URL) {
		t.Fatalf("payload did not record the changed url: %s", approval.Payload)
	}

	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	updated, err := fixture.service.MCPServerByID(fixture.ctx, "mcp_move")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Env) != 0 || len(updated.Headers) != 0 {
		t.Fatalf("endpoint change carried old secrets: %#v", updated)
	}

	probeRaw := petMCPAdminToolCall(t, fixture, "aha_test_mcp_server", `{"server_id":"mcp_move"}`)
	if !strings.Contains(probeRaw, `"health":"ok"`) {
		t.Fatalf("probe did not reach the new endpoint: %s", probeRaw)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(authHeaders) == 0 {
		t.Fatal("probe did not hit the new endpoint")
	}
	for _, header := range authHeaders {
		if header != "" {
			t.Fatalf("probe sent the old Authorization header: %q", header)
		}
	}
}

func TestPetMCPAdminUpdateSummaryListsChangedCommandAndArgs(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_stdio_move", Name: "Stdio Move", Transport: "stdio",
		Command: "node", Args: []string{"old.js"}, Enabled: true,
	})

	raw := petMCPAdminToolCall(t, fixture, "aha_request_mcp_server_update",
		`{"server_id":"mcp_stdio_move","command":"node","args":["new.js","--port","8080"],"reason":"更换桌面服务入口"}`)
	approval := decodePetMCPApproval(t, raw)
	for _, want := range []string{"stdio args 改为 new.js --port 8080", "端点已变更"} {
		if !strings.Contains(approval.Summary, want) {
			t.Fatalf("summary missing %q: %s", want, approval.Summary)
		}
	}
	if strings.Contains(approval.Summary, "old.js") {
		t.Fatalf("summary echoed the previous args: %s", approval.Summary)
	}
	if !strings.Contains(approval.Payload, `"args":["new.js","--port","8080"]`) {
		t.Fatalf("payload missing new args: %s", approval.Payload)
	}
}

func TestPetMCPAdminUpdateRejectsUnknownAndNoop(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_edit", Name: "Edit Desk", Transport: "http",
		URL: "https://example.test/mcp", Enabled: true,
	})

	if err := petMCPAdminToolError(t, fixture, "aha_request_mcp_server_update",
		`{"server_id":"mcp_edit","reason":"尝试夹带 secret","env":{"TOKEN":"leak"}}`); !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field err=%v", err)
	}
	if err := petMCPAdminToolError(t, fixture, "aha_request_mcp_server_update",
		`{"server_id":"mcp_edit","reason":"没有实际需要修改的字段"}`); !strings.Contains(err.Error(), "没有检测到") {
		t.Fatalf("no-op update err=%v", err)
	}
}

func TestPetMCPAdminGenericApprovalRejectsReservedKinds(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, false)
	before, _ := fixture.service.PetApprovals(fixture.ctx, 100)

	kinds := []string{
		petMCPServerCreateKind, petMCPServerUpdateKind, petMCPServerDeleteKind, petSetTaskMCPKind,
		"MCP_SERVER_CREATE", " mcp_server_delete ",
	}
	for _, kind := range kinds {
		_, err := fixture.service.PetMCPTool(fixture.ctx, "pet_request_approval", json.RawMessage(
			`{"kind":"`+kind+`","title":"伪造 MCP 审批","summary":"试图绕过专用工具","risk":"high","reason":"这是一个伪造审批测试","payload":{"server_id":"mcp_pet"}}`))
		if err == nil || !strings.Contains(err.Error(), "专用工具") {
			t.Fatalf("kind %q err=%v", kind, err)
		}
	}
	after, _ := fixture.service.PetApprovals(fixture.ctx, 100)
	if len(after) != len(before) {
		t.Fatalf("reserved kinds created approvals: before=%d after=%d", len(before), len(after))
	}
}

func TestPetMCPAdminExecutionRechecksManageSystem(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	raw := petMCPAdminToolCall(t, fixture, "aha_request_mcp_server_create",
		`{"name":"Revoked Desk","server_id":"mcp_revoked","transport":"stdio","command":"node","args":["server.js"],"reason":"验证执行时复查权限"}`)
	approval := decodePetMCPApproval(t, raw)

	enablePetHardwareTestSettings(t, fixture, false)
	_, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", "")
	if err == nil || !strings.Contains(err.Error(), "主人未开启") {
		t.Fatalf("approval executed without manage_system: %v", err)
	}
	if _, err := fixture.service.MCPServerByID(fixture.ctx, "mcp_revoked"); err == nil {
		t.Fatal("server was written after permission was revoked")
	}
}

func TestPetMCPAdminOwnerDecisionPayloadTypeRegression(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	raw := petMCPAdminToolCall(t, fixture, "pet_request_approval",
		`{"kind":"owner_decision","title":"确认自由 payload","summary":"这些键在 main 上会被忽略","risk":"low","payload":{"name":42,"description":[],"url":{},"command":[],"args":"not-a-list","transport":{},"server_id":true,"server_ids":"nope","env_keys":"x","header_keys":1}}`)
	approval := decodePetMCPApproval(t, raw)
	if approval.Kind != "owner_decision" {
		t.Fatalf("approval=%#v", approval)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatalf("owner_decision with conflicting payload types failed: %v", err)
	}
	stored, err := fixture.service.store.PetApproval(fixture.ctx, approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != petApprovalApproved {
		t.Fatalf("owner_decision status=%q", stored.Status)
	}
}

func TestPetMCPAdminTestRejectsDisabledServer(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	var calls atomic.Int32
	handler := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"should_not_run"}]}}`))
	}))
	defer handler.Close()
	insertPetMCPTestServer(t, fixture, domain.MCPServer{
		ID: "mcp_disabled_probe", Name: "Disabled Desk", Transport: "http",
		URL: handler.URL, Enabled: false,
	})

	raw := petMCPAdminToolCall(t, fixture, "aha_test_mcp_server", `{"server_id":"mcp_disabled_probe"}`)
	var result PetMCPServerTestResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Health != "disabled" || result.Probe != "" || len(result.Tools) != 0 || result.Error == "" {
		t.Fatalf("disabled probe=%#v", result)
	}
	if calls.Load() != 0 {
		t.Fatalf("disabled server was probed %d times", calls.Load())
	}
}

func TestPetMCPAdminRejectsExeShellHosts(t *testing.T) {
	for _, command := range []string{
		"bash.exe", "BASH.EXE", "sh.exe", "dash.exe", "zsh.exe",
		"cscript.exe", "wscript.exe", "powershell.exe", "pwsh.exe",
		"cmd.exe", filepath.Join("System32", "cmd.exe"),
	} {
		if err := validatePetMCPStdioCommand(command, nil); err == nil {
			t.Fatalf("%q was accepted as a stdio command", command)
		}
	}
	for _, command := range []string{"node", "node.exe", "python3", "python.exe"} {
		if err := validatePetMCPStdioCommand(command, nil); err != nil {
			t.Fatalf("legitimate command %q rejected: %v", command, err)
		}
	}
}

func TestPetMCPAdminCreateReplayWithoutStoredSecret(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	if err := fixture.service.secrets.PutMany(map[string]string{
		"mcp/mcp_half/env/PRESENT": "stored-secret-value",
	}); err != nil {
		t.Fatal(err)
	}
	raw := petMCPAdminToolCall(t, fixture, "aha_request_mcp_server_create",
		`{"name":"Half Desk","server_id":"mcp_half","transport":"stdio","command":"node","env_keys":["MISSING","PRESENT"],"reason":"验证缺 secret 时的幂等重放"}`)
	approval := decodePetMCPApproval(t, raw)
	if strings.Contains(approval.Payload, "stored-secret-value") {
		t.Fatalf("create payload leaked a stored secret: %s", approval.Payload)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	saved, err := fixture.service.MCPServerByID(fixture.ctx, "mcp_half")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Env["PRESENT"] != "stored-secret-value" {
		t.Fatalf("saved env=%#v", saved.Env)
	}
	if _, ok := saved.Env["MISSING"]; ok {
		t.Fatalf("missing secret was invented: %#v", saved.Env)
	}
	if err := fixture.service.executePetApproval(fixture.ctx, approval); err != nil {
		t.Fatalf("replaying a successful create failed: %v", err)
	}
}

func TestPetMCPAdminAuditUsesOwnerAndApprovalID(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	if err := fixture.service.secrets.PutMany(map[string]string{
		"mcp/mcp_audit/env/API_TOKEN": "audit-secret-value",
	}); err != nil {
		t.Fatal(err)
	}
	raw := petMCPAdminToolCall(t, fixture, "aha_request_mcp_server_create",
		`{"name":"Audit Desk","server_id":"mcp_audit","transport":"stdio","command":"node","env_keys":["API_TOKEN"],"reason":"验证审计归属和审批编号"}`)
	approval := decodePetMCPApproval(t, raw)
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ownerID, data string
	if err := db.QueryRow(`SELECT owner_id, data_json FROM audit_events WHERE action='pet.mcp_server.create'`).Scan(&ownerID, &data); err != nil {
		t.Fatal(err)
	}
	if ownerID == "pet" {
		t.Fatalf("audit owner_id is still the pet pseudo-actor: %q", ownerID)
	}
	if !strings.Contains(data, approval.ID) || !strings.Contains(data, `"requested_by":"pet"`) {
		t.Fatalf("audit missing approval provenance: %s", data)
	}
	if strings.Contains(data, "audit-secret-value") {
		t.Fatalf("audit leaked the secret value: %s", data)
	}
}

func TestPetMCPAdminListSanitizesStoredUpdatePayload(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	// Simulate an approval stored before update payloads were reduced to a diff.
	if _, err := fixture.service.CreatePetApproval(fixture.ctx, PetApprovalInput{
		Kind: petMCPServerUpdateKind, Title: "旧版更新审批", Summary: "旧版审批只显示固定文案",
		Reason: "验证旧审批列表不泄漏配置", Risk: "high",
		Payload: `{"request_id":"legacy","server_id":"mcp_legacy","command":"node","args":["secret-positional"],"url":"https://old.example.test/path-secret-token/mcp"}`,
	}); err != nil {
		t.Fatal(err)
	}
	listRaw := petMCPAdminToolCall(t, fixture, "pet_list_approvals", `{}`)
	for _, forbidden := range []string{"secret-positional", "path-secret-token", "old.example.test"} {
		if strings.Contains(listRaw, forbidden) {
			t.Fatalf("pet_list_approvals leaked legacy update config %q: %s", forbidden, listRaw)
		}
	}
}
