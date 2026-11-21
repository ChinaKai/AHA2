# P1c v2：覆盖门禁改为 Go 语法树与可核对证据（固定 6d785b3）

源码 `6d785b35e5455d06ad2e12ac6fd5d2827a9f724a`，tree `8b6209025d6dd874a931facd69fe18d8f9c23c7f`；增量 `322c154..6d785b3` 共 12 个文件（`322c154` 是 M5 材料提交，本包不改 M2-a/M5 及已投审包）。diff 与函数全文都从固定提交取得，diff 保留 3 行上下文。本轮按明确边界分 5 个包：A 扫描器与单元测试，B 账本规则/门禁/文档，L1、L2 新账本全文（按行号切两段），L3 逐行变更对照。合计覆盖全部增量，不截断。

旧扫描器的三类问题（宠物转述的 723 对 P1c 的 REQUEST_CHANGES 三类结论；报告原文不在本轮上下文中，若原文还有其他条目需另行补）：只用正则看 `internal/httpapi` 里的 `mux.Handle` 行，别处的注册会漏；`--complete` 只看状态字段，"existing HTTP server tests"、通配路径、不存在的测试名、任意 N/A 都能关闭一行；`--update` 只按路由键继承旧结论，handler 改了也不作废。

固定副本（git archive，blob 逐个核对一致）验证：

- `go test -json ./internal/petcoverage -count=1` → 退出 0，0.1s，具名用例 7 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestPetCoverageLedgersMatchRegistrationsAndEvidence|TestMCPToolCatalogCoversEveryBuiltInServer|TestHealthProbeCarriesNoBusinessData)$' -count=1` → 退出 0，6.8s，具名用例 3 个全部 run/pass
- `go test -json ./internal/centersync -run '^(TestDeviceRenameKeepsCenterGeneratedID|TestHealthAndAuthentication|TestOneTimeDeviceRegistrationStoresOnlyHashes|TestPushIsIdempotentAndPullIsIncremental)$' -count=1 -timeout=30m` → 退出 0，1.9s，具名用例 4 个全部 run/pass
- `go test -json ./internal/httpapi -run '^(TestAgentBrowserMCPRequiresCapabilityAndDomainGate|TestAgentChannelMCPRequiresCapabilityAndExposesOnlyTwoTools|TestAgentCollabMCPRequiresCapabilityAndDomainGate|TestAgentCoreMCPRequiresCapabilityAndRunningTurn|TestAgentHardwareMCPRequiresCapabilityAndDomainGate|TestAgentKnowledgeMCPRequiresCapabilityAndDomainGate|TestAgentMailMCPTools|TestAgentWorkspaceMCPRequiresCapabilityAndDomainGate|TestDesktopMCPUsesExistingShareAndSingleUseObservation|TestHealthProbeCarriesNoBusinessData|TestPetMCPNotificationsAreAcceptedWithoutJSON|TestPetMCPPingReturnsResult|TestRemovedSkillWriteRoutesReturnMethodNotAllowed|TestServeWebServesBrowserNewTabAsHTTPPage|TestServeWebUnknownRouteStillFallsBackToSPA)$' -count=1 -timeout=30m` → 退出 0，70.4s，具名用例 15 个全部 run/pass
- `go vet ./internal/petcoverage ./cmd/aha-pet-coverage ./internal/httpapi` → 退出 0，2.2s；输出：
- `go run ./cmd/aha-pet-coverage` → 退出 0，0.8s；输出：HTTP operations: 433 (closed 21, open 412); Web literal call sites: 284; ordinary-Task MCP tools: 61 (closed 0)
- `go run ./cmd/aha-pet-coverage -complete` → 退出 1（预期 1：门禁必须失败），0.3s；输出：coverage is incomplete: 412 open operations, 61 open tool rows / exit status 1
- `GOOS=windows go vet ./internal/petcoverage ./cmd/aha-pet-coverage` → 退出 0，5.2s；输出：

已关闭行引用的每个测试都在固定副本里实际跑过（上表中按包列出），不只检查存在。

## B. 账本合并、证据规则与门禁

- `MergeLedger`：元数据列每次从扫描重建，人工列（mcp、permission、protected_flow、tests、status）保留。已审行（状态不是 pending/stale）只要监听、源文件、handler、边界或指纹任一变化就退回 `stale`；v1 迁移时没有指纹的已审行全部按此退回。新行 pending，消失的行报告 removed。
- `ValidateRow`：开放行可以留 pending，但写了的 tests/mcp 也要真实；关闭行四列都必须有内容，tests 只接受 `路径_test.go:TestName` 且测试已声明，mcp 点名的工具必须在 `MCPToolCatalog` 里。transport_na 只给 MCP 协议端点（`POST /api/v1/agent/*/mcp`、`/api/v1/pet/mcp` 且 handler 以 MCP 结尾）、无认证传输、设备令牌、渠道运行时；web_prohibited_na 只给 rejectOnly handler；两者的 mcp 都必须是 none。未知状态（例如 owner_only）直接拒绝。
- `Check` 生成三份账本并与仓库文件逐字节比较，`-update` 才写回；`-complete` 额外要求所有操作行和工具行关闭。`TestPetCoverageLedgersMatchRegistrationsAndEvidence` 在 `go test ./internal/httpapi` 里执行同样的检查。
- `MCPToolCatalog` 列出各内置 server 能广播的全部工具（不受单个 Turn 门禁影响），下方附 tools/list 实际使用的同一批函数全文，说明目录与 tools/list 同源。`task-mcp-tools.tsv` 是新增的普通 Task 工具账本，61 行目前全部 pending。
- 旧 Python 扫描器与测试删除，README 改写为新的命令与规则。

完整 diff 文件边界：

- `internal/petcoverage/ledger.go`（新文件）
- `internal/httpapi/mcp_tool_catalog.go`（新文件）
- `internal/httpapi/pet_coverage_ledger_test.go`（新文件）
- `docs/pet-mcp/README.md`
- `docs/pet-mcp/task-mcp-tools.tsv`（新文件）
- `scripts/pet-mcp-coverage.py`（删除）
- `scripts/test_pet_mcp_coverage.py`（删除）

## 规则对应的单元测试与目录来源（固定提交全文）

### `internal/petcoverage/petcoverage_test.go` · `TestMergeMarksChangedReviewedRowsStaleAndKeepsPending`（全文，原文件完整 diff 在包 A）

```go
func TestMergeMarksChangedReviewedRowsStaleAndKeepsPending(t *testing.T) {
	routes := []Registration{
		{Operation: "GET /a", Server: "httpapi", Source: "internal/api/a.go", Handler: "a", Boundary: "owner_session", Fingerprint: "new"},
		{Operation: "GET /b", Server: "httpapi", Source: "internal/api/a.go", Handler: "b", Boundary: "owner_session", Fingerprint: "same"},
		{Operation: "GET /c", Server: "httpapi", Source: "internal/api/a.go", Handler: "c", Boundary: "owner_session", Fingerprint: "changed"},
		{Operation: "GET /new", Server: "httpapi", Source: "internal/api/a.go", Handler: "n", Boundary: "owner_session", Fingerprint: "n"},
	}
	previous := []Row{
		{"operation": "GET /a", "server": "httpapi", "source": "internal/api/a.go", "handler": "a", "boundary": "owner_session", "fingerprint": "old", "mcp": "aha2_pet.aha_overview", "permission": "p", "protected_flow": "none", "tests": "x_test.go:TestX", "status": "direct"},
		{"operation": "GET /b", "server": "httpapi", "source": "internal/api/a.go", "handler": "b", "boundary": "owner_session", "fingerprint": "same", "mcp": "m", "permission": "p", "protected_flow": "f", "tests": "t", "status": "direct"},
		{"operation": "GET /c", "server": "httpapi", "source": "internal/api/a.go", "handler": "c", "boundary": "owner_session", "fingerprint": "old", "mcp": "pending", "permission": "pending", "protected_flow": "pending", "tests": "pending", "status": "pending"},
		{"operation": "GET /gone", "server": "httpapi", "status": "direct"},
	}
	rows, stale, removed := MergeLedger(previous, routes)
	status := map[string]string{}
	for _, row := range rows {
		status[row["operation"]] = row["status"]
	}
	if status["GET /a"] != "stale" || rows[0]["mcp"] != "aha2_pet.aha_overview" || rows[0]["fingerprint"] != "new" {
		t.Fatal("a changed handler kept its old verdict or lost the reviewed text")
	}
	if status["GET /b"] != "direct" || status["GET /c"] != "pending" || status["GET /new"] != "pending" {
		t.Fatalf("unexpected merge statuses %v", status)
	}
	if len(stale) != 1 || len(removed) != 1 || removed[0] != "httpapi GET /gone" {
		t.Fatalf("stale=%v removed=%v", stale, removed)
	}
}
```

### `internal/petcoverage/petcoverage_test.go` · `TestEvidenceRulesRejectVagueOrMisappliedClosure`（全文，原文件完整 diff 在包 A）

```go
func TestEvidenceRulesRejectVagueOrMisappliedClosure(t *testing.T) {
	root := writeTree(t, map[string]string{"internal/api/a_test.go": "package api\n\nimport \"testing\"\n\nfunc TestReal(t *testing.T) {}\n"})
	catalog := NewCatalog(map[string][]string{"aha2_pet": {"aha_overview"}})
	owner := Registration{Operation: "GET /api/v1/system", Handler: "system", Boundary: "owner_session"}
	protocol := Registration{Operation: "POST /api/v1/agent/core/mcp", Handler: "agentCoreMCP", Boundary: "agent_turn"}
	reject := Registration{Operation: "ANY /api/v1/prompts", Handler: "reject", Boundary: "owner_session", RejectOnly: true}
	good := Row{"mcp": "aha2_pet.aha_overview", "permission": "view tasks", "protected_flow": "none", "tests": "internal/api/a_test.go:TestReal", "status": "direct"}
	if err := ValidateRow(root, catalog, owner, good); err != nil {
		t.Fatalf("valid direct row rejected: %v", err)
	}
	with := func(changes Row) Row {
		row := Row{}
		for key, value := range good {
			row[key] = value
		}
		for key, value := range changes {
			row[key] = value
		}
		return row
	}
	bad := map[string]struct {
		route Registration
		row   Row
	}{
		"prose tests":             {owner, with(Row{"tests": "existing HTTP server tests"})},
		"glob tests":              {owner, with(Row{"tests": "internal/api/*_test.go"})},
		"missing test":            {owner, with(Row{"tests": "internal/api/a_test.go:TestMissing"})},
		"missing tool":            {owner, with(Row{"mcp": "aha2_pet.aha_missing"})},
		"no tool named":           {owner, with(Row{"mcp": "reuses Web flow"})},
		"pending field":           {owner, with(Row{"permission": "pending"})},
		"empty field":             {owner, with(Row{"protected_flow": ""})},
		"transport on owner op":   {owner, with(Row{"mcp": "none", "status": "transport_na"})},
		"prohibited not rejected": {owner, with(Row{"mcp": "none", "status": "web_prohibited_na"})},
		"na with tool":            {protocol, with(Row{"status": "transport_na"})},
		"unknown status":          {owner, with(Row{"status": "owner_only"})},
		"open row bad reference":  {owner, with(Row{"status": "pending", "tests": "see suite"})},
	}
	for name, item := range bad {
		if err := ValidateRow(root, catalog, item.route, item.row); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	for name, item := range map[string]struct {
		route Registration
		row   Row
	}{
		"protocol transport": {protocol, with(Row{"mcp": "none", "status": "transport_na"})},
		"web rejects":        {reject, with(Row{"mcp": "none", "status": "web_prohibited_na"})},
		"open with pending":  {owner, Row{"mcp": "pending", "permission": "pending", "protected_flow": "pending", "tests": "pending", "status": "stale"}},
	} {
		if err := ValidateRow(root, catalog, item.route, item.row); err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
	}
}
```

### `internal/petcoverage/petcoverage_test.go` · `TestCheckDetectsDriftAndCompleteRequiresClosure`（全文，原文件完整 diff 在包 A）

```go
func TestCheckDetectsDriftAndCompleteRequiresClosure(t *testing.T) {
	classify(t, map[string]string{"internal/api (*Server).Routes": "httpapi"})
	root := writeTree(t, map[string]string{
		"internal/api/server.go": serverPreamble + "func (s *Server) Routes() { mux := http.NewServeMux(); mux.Handle(\"GET /api/v1/system\", s.withAuth(http.HandlerFunc(s.list))) }\n",
		"web/src/api.ts":         "fetch(\"/api/v1/system\");\n",
		"docs/pet-mcp/.keep":     "",
	})
	catalog := NewCatalog(map[string][]string{"aha2_pet": {"aha_overview"}, "aha2_core": {"progress"}})
	if _, err := Check(root, catalog, false, false); err == nil || !strings.Contains(err.Error(), "differs from the scan") {
		t.Fatalf("missing ledger was not reported as drift: %v", err)
	}
	report, err := Check(root, catalog, true, false)
	if err != nil || report.Operations != 1 || report.Open != 1 || report.Tools != 1 {
		t.Fatalf("update failed: %+v %v", report, err)
	}
	if _, err := Check(root, catalog, false, false); err != nil {
		t.Fatalf("freshly updated ledger did not pass the drift check: %v", err)
	}
	if _, err := Check(root, catalog, false, true); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("open rows passed the complete gate: %v", err)
	}
	// A handler edit invalidates the stored fingerprint.
	path := filepath.Join(root, "internal/api/server.go")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "s.withAuth(nil) }", "s.withAuth(nil); _ = r }", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(root, catalog, false, false); err == nil {
		t.Fatal("handler change did not invalidate the ledger")
	}
}
```

### `internal/httpapi/agent_hardware_mcp_operations.go` · `hardwareMCPToolsFor`

```go
func (s *Server) hardwareMCPToolsFor(write bool) []mcpTool {
	tools := agentHardwareMCPTools()
	if write {
		for _, op := range s.hardwareMCPOperations() {
			tools = append(tools, op.Tool)
		}
	}
	return tools
}
```

### `internal/httpapi/agent_browser_mcp_operations.go` · `browserMCPToolsFor`

```go
func (s *Server) browserMCPToolsFor(write bool) []mcpTool {
	tools := agentBrowserMCPTools()
	for _, operation := range s.browserMCPOperations() {
		if !operation.Write || write {
			tools = append(tools, operation.Tool)
		}
	}
	return tools
}
```

### `internal/httpapi/agent_workspace_mcp_operations.go` · `workspaceMCPToolsFor`

```go
func (s *Server) workspaceMCPToolsFor(write bool) []mcpTool {
	tools := agentWorkspaceMCPTools()
	if write {
		for _, op := range s.workspaceMCPOperations() {
			tools = append(tools, op.Tool)
		}
	}
	return tools
}
```

### `internal/app/pet.go` · `PetMCPTools`

```go
func (s *Service) PetMCPTools(ctx context.Context) []gateway.ChatTool {
	// Tools are always advertised so a permission change takes effect without a
	// backend-session reset; PetMCPTool enforces the switch on every call.
	return allPetMCPTools()
}
```

### `internal/app/pet.go` · `allPetMCPTools`

```go
func allPetMCPTools() []gateway.ChatTool {
	tools := append([]gateway.ChatTool(nil), assistantPetTools()...)
	return append(tools, ahaPetTools()...)
}
```

## 该边界完整 diff

```diff
diff --git a/docs/pet-mcp/README.md b/docs/pet-mcp/README.md
index 25c95ee..476e4aa 100644
--- a/docs/pet-mcp/README.md
+++ b/docs/pet-mcp/README.md
@@ -4,16 +4,31 @@
 
 基线 `a3ed6cb566680ff079d660685126a2b77a76bad8`，分支 `aha/task_e3d0848747ddc2018605`，工作目录为本 Task 独立 worktree。所有提交保留在此分支，不合并 main、不推送、不部署、不操作生产资源。
 
-`coverage.tsv` 逐行记录实际注册的 HTTP 操作、源文件、handler、现有或新增 MCP、权限、安全交接与测试。source 列相对 `internal/httpapi/`。它目前是路由级账本，后续还需展开同一入口的业务子操作，并完成全部映射与效果验证。`web-call-sites.tsv` 保存前端字面 API 调用点与方法提示；方法提示不能代替完整 TypeScript 调用链审查。
+## 账本与门禁
+
+三份账本都由 `internal/petcoverage` 生成和校验，它用 Go 自己的语法解析器读源码，不靠文本正则：
+
+- `coverage.tsv`：`internal/` 与 `cmd/` 下所有 `ServeMux` 注册（任意变量名、常量、拼接、局部前缀、字面量路由表都会展开），按监听分 `httpapi`、`mail-download`、`centersync`。每行带源文件、handler、认证边界（由外层包装推出）和 handler 指纹（包装链加 handler 源码的哈希）。注册在未登记的函数里、注册到 DefaultServeMux 或无法识别的接收者上、路由串无法求值、handler 找不到唯一声明，扫描都会直接失败。
+- `web-call-sites.tsv`：`web/src` 里所有以 `/api/` 开头的字符串/模板字面量。每个调用点必须能对上一条已注册的 httpapi 路由（方法明确时方法也要一致），对不上就说明有注册逃过了扫描，校验失败。
+- `task-mcp-tools.tsv`：普通 Task 可用的全部内置 MCP 工具（`httpapi.MCPToolCatalog()`，宠物自己的 server 除外），逐个记录宠物 Turn 如何拿到同样能力。
 
 ```sh
-python3 scripts/pet-mcp-coverage.py            # 检查注册与账本一致
-python3 scripts/pet-mcp-coverage.py --update   # 更新后逐项审查差异
-python3 scripts/pet-mcp-coverage.py --complete # 最终覆盖门禁，当前必须失败
-python3 scripts/test_pet_mcp_coverage.py
+go run ./cmd/aha-pet-coverage            # 账本与注册/调用点/工具目录一致，且每行证据真实
+go run ./cmd/aha-pet-coverage -update    # 重写账本；人工列保留，handler 变了的已审行退回 stale
+go run ./cmd/aha-pet-coverage -complete  # 最终门禁：所有行都关闭且证据有效，当前必须失败
+go test ./internal/petcoverage ./internal/httpapi -run 'PetCoverage|MCPToolCatalog|HealthProbe'
 ```
 
-未完成项使用 pending 或 implemented/implemented_unverified。后两个状态只说明已有代码，仍不能通过最终门禁。transport_na 只用于协议、静态资源等传输入口；web_prohibited_na 必须能证明 Web 自身也禁止该操作。受保护功能需要真实申请、状态与 owner 安全输入/授权交接，不能标成 owner_only 后略过。
+`go test ./internal/httpapi` 里的 `TestPetCoverageLedgersMatchRegistrationsAndEvidence` 做的就是第一条命令：新增、删除或改动路由而不更新账本，测试失败。
+
+状态分两类。开放：`pending`、`implemented`、`implemented_unverified`、`stale`。`stale` 表示该行之前审过，但 handler、源文件、边界或指纹已经变了，旧结论不再成立，必须重新核对后才能改回关闭状态；从 v1 账本迁移时，所有没有指纹的已审行都按这个规则退回了 stale。关闭：`direct`、`equivalent`、`protected_handoff`、`transport_na`、`web_prohibited_na`。关闭行的 mcp、permission、protected_flow、tests 四列都不能为空或 pending，并且：
+
+- tests 只接受 `路径_test.go:TestName`（多个用 `; ` 分隔），文件必须存在且声明了该顶层测试。"existing ... tests"、通配路径这类说法会被拒绝；开放行里写了非 pending 的 tests 也同样核对。
+- direct/equivalent/protected_handoff 的 mcp 至少点名一个 `server.tool`，且每个都在内置工具目录里。
+- transport_na 的 mcp 必须是 none，且只用于 MCP 协议端点、无认证传输、设备令牌或渠道运行时路由；owner 会话下的业务操作不能标 N/A。
+- web_prohibited_na 的 mcp 必须是 none，且 handler 必须是"只回写客户端错误"的拒绝函数（扫描器逐个调用核对）。
+
+受保护功能需要真实申请、状态与 owner 安全输入/授权交接，不能标成 owner_only 后略过；账本也不接受 owner_only 这个状态。
 
 首批实现中，宠物 core 不再提前退出注入；知识只补指南和反馈，其他知识与 Skill 操作复用宠物已有入口；Workspace、浏览器与硬件按当前绑定、授权和实时查看权限注入。硬件写入另外要求 Main、可写 snapshot、可写资源以及宠物实时 Task 管理权限。普通 Task、只读 Task 和渠道 Turn 的原作用域继续生效。`capabilities` 新增各 MCP 域和硬件写入的实际门禁标志，原 HTTP 能力标志仍表示该 HTTP 面是否存在。
 
diff --git a/docs/pet-mcp/task-mcp-tools.tsv b/docs/pet-mcp/task-mcp-tools.tsv
new file mode 100644
index 0000000..13db6bc
--- /dev/null
+++ b/docs/pet-mcp/task-mcp-tools.tsv
@@ -0,0 +1,62 @@
+tool	pet_access	permission	tests	status
+aha2_browser.browser_action	pending	pending	pending	pending
+aha2_browser.browser_bookmarks_list	pending	pending	pending	pending
+aha2_browser.browser_cookie_metadata	pending	pending	pending	pending
+aha2_browser.browser_download_get	pending	pending	pending	pending
+aha2_browser.browser_downloads_list	pending	pending	pending	pending
+aha2_browser.browser_frame	pending	pending	pending	pending
+aha2_browser.browser_navigate	pending	pending	pending	pending
+aha2_browser.browser_release	pending	pending	pending	pending
+aha2_browser.browser_snapshot	pending	pending	pending	pending
+aha2_browser.browser_status	pending	pending	pending	pending
+aha2_browser.browser_tab_activate	pending	pending	pending	pending
+aha2_browser.browser_tab_bind_current	pending	pending	pending	pending
+aha2_browser.browser_tab_close	pending	pending	pending	pending
+aha2_browser.browser_tab_open	pending	pending	pending	pending
+aha2_browser.browser_tabs_list	pending	pending	pending	pending
+aha2_browser.browser_upload	pending	pending	pending	pending
+aha2_channel.channel_contacts	pending	pending	pending	pending
+aha2_channel.channel_reply	pending	pending	pending	pending
+aha2_channel.channel_reply_decision	pending	pending	pending	pending
+aha2_channel.channel_request_permission	pending	pending	pending	pending
+aha2_collab.collab_batch_submit	pending	pending	pending	pending
+aha2_collab.collab_batch_wait	pending	pending	pending	pending
+aha2_core.attachment_bind	pending	pending	pending	pending
+aha2_core.capabilities	pending	pending	pending	pending
+aha2_core.inbox_wait	pending	pending	pending	pending
+aha2_core.memory_update	pending	pending	pending	pending
+aha2_core.progress	pending	pending	pending	pending
+aha2_core.task_get	pending	pending	pending	pending
+aha2_core.task_list	pending	pending	pending	pending
+aha2_core.task_message	pending	pending	pending	pending
+aha2_desktop.desktop_action	pending	pending	pending	pending
+aha2_desktop.desktop_control	pending	pending	pending	pending
+aha2_desktop.desktop_observe	pending	pending	pending	pending
+aha2_desktop.desktop_status	pending	pending	pending	pending
+aha2_desktop.desktop_stop	pending	pending	pending	pending
+aha2_hardware.hardware_adb_exec	pending	pending	pending	pending
+aha2_hardware.hardware_connect	pending	pending	pending	pending
+aha2_hardware.hardware_disconnect	pending	pending	pending	pending
+aha2_hardware.hardware_list	pending	pending	pending	pending
+aha2_hardware.hardware_login	pending	pending	pending	pending
+aha2_hardware.hardware_send	pending	pending	pending	pending
+aha2_hardware.hardware_terminal_read	pending	pending	pending	pending
+aha2_knowledge.knowledge_feedback	pending	pending	pending	pending
+aha2_knowledge.knowledge_get	pending	pending	pending	pending
+aha2_knowledge.knowledge_guide	pending	pending	pending	pending
+aha2_knowledge.knowledge_list	pending	pending	pending	pending
+aha2_knowledge.knowledge_submit	pending	pending	pending	pending
+aha2_knowledge.skill_get	pending	pending	pending	pending
+aha2_knowledge.skill_list	pending	pending	pending	pending
+aha2_mail.mail_get_status	pending	pending	pending	pending
+aha2_mail.mail_list_accounts	pending	pending	pending	pending
+aha2_mail.mail_list_templates	pending	pending	pending	pending
+aha2_mail.mail_preview	pending	pending	pending	pending
+aha2_mail.mail_send	pending	pending	pending	pending
+aha2_mail.mail_template_detail	pending	pending	pending	pending
+aha2_workspace.workspace_list	pending	pending	pending	pending
+aha2_workspace.workspace_process_get	pending	pending	pending	pending
+aha2_workspace.workspace_process_list	pending	pending	pending	pending
+aha2_workspace.workspace_process_start	pending	pending	pending	pending
+aha2_workspace.workspace_process_stop	pending	pending	pending	pending
+aha2_workspace.workspace_runtime_list	pending	pending	pending	pending
diff --git a/internal/httpapi/mcp_tool_catalog.go b/internal/httpapi/mcp_tool_catalog.go
new file mode 100644
index 0000000..0daf89a
--- /dev/null
+++ b/internal/httpapi/mcp_tool_catalog.go
@@ -0,0 +1,44 @@
+package httpapi
+
+import (
+	"context"
+
+	"github.com/ChinaKai/AHA2/internal/app"
+)
+
+// MCPToolCatalog lists every tool each built-in MCP server can advertise,
+// before any per-Turn gate. Coverage tooling checks ledger evidence against
+// it; it carries names only, which tools/list already publishes.
+func MCPToolCatalog() map[string][]string {
+	s := &Server{}
+	names := func(tools []mcpTool) []string {
+		result := make([]string, 0, len(tools))
+		for _, tool := range tools {
+			result = append(result, tool.Name)
+		}
+		return result
+	}
+	operations := func(items []agentMCPOperation) []string {
+		result := make([]string, 0, len(items))
+		for _, item := range items {
+			result = append(result, item.Tool.Name)
+		}
+		return result
+	}
+	pet := []string{}
+	for _, tool := range (&app.Service{}).PetMCPTools(context.Background()) {
+		pet = append(pet, tool.Name)
+	}
+	return map[string][]string{
+		"aha2_core":      names(coreMCPTools()),
+		"aha2_knowledge": names(knowledgeMCPTools()),
+		"aha2_workspace": names(s.workspaceMCPToolsFor(true)),
+		"aha2_browser":   names(s.browserMCPToolsFor(true)),
+		"aha2_desktop":   operations(s.desktopMCPOperations()),
+		"aha2_hardware":  names(s.hardwareMCPToolsFor(true)),
+		"aha2_mail":      names(mailMCPTools()),
+		"aha2_channel":   names(channelMCPTools()),
+		"aha2_collab":    names(agentCollabMCPTools()),
+		"aha2_pet":       pet,
+	}
+}
diff --git a/internal/httpapi/pet_coverage_ledger_test.go b/internal/httpapi/pet_coverage_ledger_test.go
new file mode 100644
index 0000000..21ffc3e
--- /dev/null
+++ b/internal/httpapi/pet_coverage_ledger_test.go
@@ -0,0 +1,54 @@
+package httpapi
+
+import (
+	"encoding/json"
+	"net/http"
+	"net/http/httptest"
+	"path/filepath"
+	"testing"
+
+	"github.com/ChinaKai/AHA2/internal/petcoverage"
+)
+
+// The coverage ledgers must describe the code as it is: every registered
+// operation and built-in MCP tool is listed, every Web call reaches a
+// registered route, changed handlers are not left with an old verdict, and
+// every claim a row makes names real tools and real tests. Completeness is
+// a separate release gate (go run ./cmd/aha-pet-coverage -complete).
+func TestPetCoverageLedgersMatchRegistrationsAndEvidence(t *testing.T) {
+	report, err := petcoverage.Check(filepath.Join("..", ".."), petcoverage.NewCatalog(MCPToolCatalog()), false, false)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if report.Operations == 0 || report.WebSites == 0 || report.Tools == 0 {
+		t.Fatalf("empty inventory: %+v", report)
+	}
+}
+
+func TestMCPToolCatalogCoversEveryBuiltInServer(t *testing.T) {
+	catalog := MCPToolCatalog()
+	for server, tool := range map[string]string{
+		"aha2_core": "inbox_wait", "aha2_knowledge": "knowledge_guide", "aha2_workspace": "workspace_process_list",
+		"aha2_browser": "browser_status", "aha2_desktop": "desktop_status", "aha2_hardware": "hardware_adb_exec",
+		"aha2_mail": "mail_send", "aha2_channel": "channel_reply", "aha2_collab": "collab_batch_submit", "aha2_pet": "aha_overview",
+	} {
+		found := false
+		for _, name := range catalog[server] {
+			found = found || name == tool
+		}
+		if !found {
+			t.Fatalf("%s catalog lacks %s: %v", server, tool, catalog[server])
+		}
+	}
+}
+
+// /healthz is a transport probe: unauthenticated, and it returns nothing
+// beyond liveness, so it has no pet MCP counterpart.
+func TestHealthProbeCarriesNoBusinessData(t *testing.T) {
+	recorder := httptest.NewRecorder()
+	New(Config{}).Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
+	var body map[string]any
+	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &body) != nil || len(body) != 2 || body["ok"] != true || body["service"] != "aha2" {
+		t.Fatalf("health probe changed: %d %s", recorder.Code, recorder.Body.String())
+	}
+}
diff --git a/internal/petcoverage/ledger.go b/internal/petcoverage/ledger.go
new file mode 100644
index 0000000..0a99b66
--- /dev/null
+++ b/internal/petcoverage/ledger.go
@@ -0,0 +1,396 @@
+package petcoverage
+
+import (
+	"bytes"
+	"encoding/csv"
+	"errors"
+	"fmt"
+	"os"
+	"path/filepath"
+	"regexp"
+	"sort"
+	"strings"
+)
+
+const (
+	LedgerPath   = "docs/pet-mcp/coverage.tsv"
+	WebPath      = "docs/pet-mcp/web-call-sites.tsv"
+	ToolsPath    = "docs/pet-mcp/task-mcp-tools.tsv"
+	pendingValue = "pending"
+)
+
+var ledgerFields = []string{"operation", "server", "source", "handler", "boundary", "fingerprint", "mcp", "permission", "protected_flow", "tests", "status"}
+var webFields = []string{"source", "line", "method_hint", "expression"}
+var toolFields = []string{"tool", "pet_access", "permission", "tests", "status"}
+
+// Final statuses close a row. Everything else keeps the gate open.
+var finalStatus = map[string]bool{"direct": true, "equivalent": true, "protected_handoff": true, "transport_na": true, "web_prohibited_na": true}
+var openStatus = map[string]bool{pendingValue: true, "implemented": true, "implemented_unverified": true, "stale": true}
+
+// Row is one ledger line. Metadata columns are regenerated from the scan;
+// the reviewed columns are only ever written by a person.
+type Row map[string]string
+
+func (row Row) key() string { return row["server"] + " " + row["operation"] }
+
+// Catalog maps an MCP server (aha2_core, aha2_pet, ...) to its tool names.
+type Catalog map[string]map[string]bool
+
+// NewCatalog builds a Catalog from server -> names.
+func NewCatalog(input map[string][]string) Catalog {
+	catalog := Catalog{}
+	for server, names := range input {
+		catalog[server] = map[string]bool{}
+		for _, name := range names {
+			catalog[server][name] = true
+		}
+	}
+	return catalog
+}
+
+func readTSV(path string) ([]Row, []string, error) {
+	data, err := os.ReadFile(path)
+	if errors.Is(err, os.ErrNotExist) {
+		return nil, nil, nil
+	}
+	if err != nil {
+		return nil, nil, err
+	}
+	reader := csv.NewReader(bytes.NewReader(data))
+	reader.Comma = '\t'
+	reader.LazyQuotes = true
+	records, err := reader.ReadAll()
+	if err != nil {
+		return nil, nil, fmt.Errorf("%s: %w", path, err)
+	}
+	if len(records) == 0 {
+		return nil, nil, nil
+	}
+	header := records[0]
+	rows := make([]Row, 0, len(records)-1)
+	for _, record := range records[1:] {
+		row := Row{}
+		for index, field := range header {
+			if index < len(record) {
+				row[field] = record[index]
+			}
+		}
+		rows = append(rows, row)
+	}
+	return rows, header, nil
+}
+
+func encodeTSV(fields []string, rows []Row) []byte {
+	var output bytes.Buffer
+	writer := csv.NewWriter(&output)
+	writer.Comma = '\t'
+	_ = writer.Write(fields)
+	for _, row := range rows {
+		record := make([]string, len(fields))
+		for index, field := range fields {
+			record[index] = row[field]
+		}
+		_ = writer.Write(record)
+	}
+	writer.Flush()
+	return output.Bytes()
+}
+
+// MergeLedger regenerates metadata from the scan and keeps reviewed columns.
+// A previously reviewed row whose server, source, handler, boundary or handler
+// fingerprint changed is reset to "stale": its old verdict no longer describes
+// the code and must be re-verified. New operations start pending; operations
+// no longer registered are dropped and reported.
+func MergeLedger(previous []Row, routes []Registration) (rows []Row, stale, removed []string) {
+	byKey := map[string]Row{}
+	for _, row := range previous {
+		server := row["server"]
+		if server == "" {
+			server = "httpapi" // ledger v1 had a single listener
+		}
+		byKey[server+" "+row["operation"]] = row
+	}
+	seen := map[string]bool{}
+	for _, route := range routes {
+		row := Row{"operation": route.Operation, "server": route.Server, "source": route.Source, "handler": route.Handler, "boundary": route.Boundary, "fingerprint": route.Fingerprint}
+		key := row.key()
+		seen[key] = true
+		old, existed := byKey[key]
+		for _, field := range ledgerFields[6:] {
+			row[field] = pendingValue
+			if existed && old[field] != "" {
+				row[field] = old[field]
+			}
+		}
+		if existed && row["status"] != pendingValue && row["status"] != "stale" {
+			oldSource := old["source"]
+			if !strings.Contains(oldSource, "/") && oldSource != "" {
+				oldSource = "internal/httpapi/" + oldSource // v1 paths were package-relative
+			}
+			if oldSource != route.Source || old["handler"] != route.Handler || old["boundary"] != route.Boundary || old["fingerprint"] != route.Fingerprint {
+				row["status"] = "stale"
+				stale = append(stale, key)
+			}
+		}
+		rows = append(rows, row)
+	}
+	for key := range byKey {
+		if !seen[key] {
+			removed = append(removed, key)
+		}
+	}
+	sort.Strings(removed)
+	return rows, stale, removed
+}
+
+var testReference = regexp.MustCompile(`^([A-Za-z0-9_./-]+_test\.go):(Test[A-Za-z0-9_]+)$`)
+var toolReference = regexp.MustCompile(`\b(aha2_[a-z]+)\.([a-z][a-z0-9_]*)\b`)
+var mcpProtocolRoute = regexp.MustCompile(`^POST /api/v1/(agent/[a-z]+|pet)/mcp$`)
+
+// checkTests requires every reference to name an existing top-level Go test.
+func checkTests(root, value string) error {
+	if strings.TrimSpace(value) == "" {
+		return errors.New("tests is empty")
+	}
+	for _, token := range strings.Split(value, ";") {
+		token = strings.TrimSpace(token)
+		match := testReference.FindStringSubmatch(token)
+		if match == nil {
+			return fmt.Errorf("test reference %q is not path_test.go:TestName", token)
+		}
+		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(match[1])))
+		if err != nil {
+			return fmt.Errorf("test file %s does not exist", match[1])
+		}
+		if !regexp.MustCompile(`(?m)^func ` + match[2] + `\(t \*testing\.T\)`).Match(data) {
+			return fmt.Errorf("test %s is not declared in %s", match[2], match[1])
+		}
+	}
+	return nil
+}
+
+func checkTools(catalog Catalog, value string, required bool) error {
+	matches := toolReference.FindAllStringSubmatch(value, -1)
+	if required && len(matches) == 0 {
+		return fmt.Errorf("mcp %q names no server.tool", value)
+	}
+	for _, match := range matches {
+		if !catalog[match[1]][match[2]] {
+			return fmt.Errorf("mcp tool %s.%s does not exist in the built-in catalog", match[1], match[2])
+		}
+	}
+	return nil
+}
+
+func meaningful(value string) bool {
+	value = strings.TrimSpace(value)
+	return value != "" && value != pendingValue
+}
+
+// ValidateRow applies the evidence rules. A row may stay open, but whatever
+// it claims must be true; a closing status needs complete, checkable proof.
+func ValidateRow(root string, catalog Catalog, route Registration, row Row) error {
+	status := row["status"]
+	if !finalStatus[status] && !openStatus[status] {
+		return fmt.Errorf("unknown status %q", status)
+	}
+	if meaningful(row["tests"]) {
+		if err := checkTests(root, row["tests"]); err != nil {
+			return err
+		}
+	}
+	if meaningful(row["mcp"]) && row["mcp"] != "none" {
+		if err := checkTools(catalog, row["mcp"], false); err != nil {
+			return err
+		}
+	}
+	if !finalStatus[status] {
+		return nil
+	}
+	for _, field := range []string{"mcp", "permission", "protected_flow", "tests"} {
+		if !meaningful(row[field]) {
+			return fmt.Errorf("%s status needs %s evidence", status, field)
+		}
+	}
+	switch status {
+	case "direct", "equivalent", "protected_handoff":
+		if err := checkTools(catalog, row["mcp"], true); err != nil {
+			return err
+		}
+	case "transport_na":
+		protocol := mcpProtocolRoute.MatchString(route.Operation) && strings.HasSuffix(route.Handler, "MCP")
+		if row["mcp"] != "none" {
+			return errors.New("transport_na rows must set mcp to none")
+		}
+		if !protocol && route.Boundary != "transport_auth" && route.Boundary != "device_token" && route.Boundary != "channel_runtime" {
+			return fmt.Errorf("transport_na is only for MCP protocol, transport, device or channel-runtime routes, not %s", route.Boundary)
+		}
+	case "web_prohibited_na":
+		if row["mcp"] != "none" {
+			return errors.New("web_prohibited_na rows must set mcp to none")
+		}
+		if !route.RejectOnly {
+			return errors.New("web_prohibited_na needs a handler that only rejects the request")
+		}
+	}
+	return nil
+}
+
+// Report summarizes one check.
+type Report struct {
+	Operations, Final, Open, WebSites, Tools, ToolsFinal int
+	Stale, Removed                                       []string
+}
+
+// Check regenerates every inventory, compares it with the checked-in files
+// and validates evidence. With update it rewrites the files first. With
+// complete it also requires every operation and tool row to be closed.
+func Check(root string, catalog Catalog, update, complete bool) (Report, error) {
+	var report Report
+	routes, err := ScanRoutes(root)
+	if err != nil {
+		return report, err
+	}
+	previous, _, err := readTSV(filepath.Join(root, LedgerPath))
+	if err != nil {
+		return report, err
+	}
+	rows, stale, removed := MergeLedger(previous, routes)
+	report.Stale, report.Removed = stale, removed
+	sites, err := ScanWeb(root)
+	if err != nil {
+		return report, err
+	}
+	unmatched, err := UnmatchedWebSites(sites, routes)
+	if err != nil {
+		return report, err
+	}
+	if len(unmatched) > 0 {
+		var lines []string
+		for _, site := range unmatched {
+			lines = append(lines, fmt.Sprintf("%s:%d %s %s", site.Source, site.Line, site.MethodHint, site.Expression))
+		}
+		return report, fmt.Errorf("Web calls with no registered route (a registration escaped the scan):\n%s", strings.Join(lines, "\n"))
+	}
+	webRows := make([]Row, 0, len(sites))
+	for _, site := range sites {
+		webRows = append(webRows, Row{"source": site.Source, "line": fmt.Sprint(site.Line), "method_hint": site.MethodHint, "expression": site.Expression})
+	}
+	toolsPrevious, _, err := readTSV(filepath.Join(root, ToolsPath))
+	if err != nil {
+		return report, err
+	}
+	toolRows := mergeTools(toolsPrevious, catalog)
+	files := map[string][]byte{
+		LedgerPath: encodeTSV(ledgerFields, rows),
+		WebPath:    encodeTSV(webFields, webRows),
+		ToolsPath:  encodeTSV(toolFields, toolRows),
+	}
+	for _, path := range []string{LedgerPath, WebPath, ToolsPath} {
+		target := filepath.Join(root, filepath.FromSlash(path))
+		if update {
+			if err := os.WriteFile(target, files[path], 0o644); err != nil {
+				return report, err
+			}
+			continue
+		}
+		current, _ := os.ReadFile(target)
+		if !bytes.Equal(current, files[path]) {
+			return report, fmt.Errorf("%s differs from the scan; run the coverage tool with -update and review every new, stale or removed row", path)
+		}
+	}
+	byKey := map[string]Registration{}
+	for _, route := range routes {
+		byKey[route.Server+" "+route.Operation] = route
+	}
+	var problems []string
+	for _, row := range rows {
+		if err := ValidateRow(root, catalog, byKey[row.key()], row); err != nil {
+			problems = append(problems, row.key()+": "+err.Error())
+		}
+		if finalStatus[row["status"]] {
+			report.Final++
+		} else {
+			report.Open++
+		}
+	}
+	for _, row := range toolRows {
+		if err := validateTool(root, catalog, row); err != nil {
+			problems = append(problems, "tool "+row["tool"]+": "+err.Error())
+		}
+		if toolFinal[row["status"]] {
+			report.ToolsFinal++
+		}
+	}
+	report.Operations, report.WebSites, report.Tools = len(rows), len(sites), len(toolRows)
+	if len(problems) > 0 {
+		return report, fmt.Errorf("ledger evidence is invalid:\n%s", strings.Join(problems, "\n"))
+	}
+	if complete && (report.Open > 0 || report.ToolsFinal < report.Tools) {
+		return report, fmt.Errorf("coverage is incomplete: %d open operations, %d open tool rows", report.Open, report.Tools-report.ToolsFinal)
+	}
+	return report, nil
+}
+
+// The ordinary-Task tool ledger lists every built-in MCP tool except the
+// pet's own server, and records how a pet Turn reaches the same capability.
+var toolFinal = map[string]bool{"direct": true, "equivalent": true, "mode_gated": true}
+var toolOpen = map[string]bool{pendingValue: true, "implemented": true, "stale": true}
+
+func mergeTools(previous []Row, catalog Catalog) []Row {
+	old := map[string]Row{}
+	for _, row := range previous {
+		old[row["tool"]] = row
+	}
+	var names []string
+	for server, tools := range catalog {
+		if server == "aha2_pet" {
+			continue
+		}
+		for tool := range tools {
+			names = append(names, server+"."+tool)
+		}
+	}
+	sort.Strings(names)
+	rows := make([]Row, 0, len(names))
+	for _, name := range names {
+		row := Row{"tool": name}
+		for _, field := range toolFields[1:] {
+			row[field] = pendingValue
+			if value := old[name][field]; value != "" {
+				row[field] = value
+			}
+		}
+		rows = append(rows, row)
+	}
+	return rows
+}
+
+func validateTool(root string, catalog Catalog, row Row) error {
+	status := row["status"]
+	if !toolFinal[status] && !toolOpen[status] {
+		return fmt.Errorf("unknown status %q", status)
+	}
+	if meaningful(row["tests"]) {
+		if err := checkTests(root, row["tests"]); err != nil {
+			return err
+		}
+	}
+	if !toolFinal[status] {
+		return nil
+	}
+	for _, field := range []string{"pet_access", "permission", "tests"} {
+		if !meaningful(row[field]) {
+			return fmt.Errorf("%s status needs %s evidence", status, field)
+		}
+	}
+	if status == "equivalent" {
+		// The equivalent must be a real tool other than the row itself.
+		matches := toolReference.FindAllStringSubmatch(row["pet_access"], -1)
+		if len(matches) == 0 {
+			return errors.New("equivalent needs pet_access to name the pet-reachable server.tool")
+		}
+		return checkTools(catalog, row["pet_access"], true)
+	}
+	return nil
+}
diff --git a/scripts/pet-mcp-coverage.py b/scripts/pet-mcp-coverage.py
deleted file mode 100644
index b790e65..0000000
--- a/scripts/pet-mcp-coverage.py
+++ /dev/null
@@ -1,158 +0,0 @@
-#!/usr/bin/env python3
-"""Inventory registered HTTP operations and literal Web call sites.
-
-No runtime database, credentials or network are used. The checked-in ledger is
-deliberately explicit about unfinished operations; --complete is the release
-gate and rejects every pending row. Registration syntax outside the supported
-forms fails closed rather than silently disappearing from the inventory.
-"""
-import argparse
-import csv
-import io
-import re
-from pathlib import Path
-
-ROOT = Path(__file__).resolve().parents[1]
-LEDGER = ROOT / "docs/pet-mcp/coverage.tsv"
-WEB = ROOT / "docs/pet-mcp/web-call-sites.tsv"
-
-
-def routes():
-    rows = {}
-    constants = {}
-    for source_path in (ROOT / "internal/httpapi").glob("*.go"):
-        if not source_path.name.endswith("_test.go"):
-            constants.update(re.findall(r'\b(\w+)\s*=\s*"([^"\n]+)"', source_path.read_text()))
-    for path in sorted((ROOT / "internal/httpapi").glob("*.go")):
-        if path.name.endswith("_test.go"):
-            continue
-        source = path.read_text()
-        for match in re.finditer(r'mux\.Handle(?:Func)?\(([^\n]+)', source):
-            line = match.group(1)
-            symbol = re.match(r'(\w+)\s*,',line)
-            if symbol and symbol.group(1) in constants:
-                line = '"' + constants[symbol.group(1)] + '",' + line[symbol.end():]
-            literal = re.match(r'"([^"\n]+)"\s*,', line)
-            if literal:
-                pattern = literal.group(1)
-                names = re.findall(r'\bs\.([A-Za-z0-9_]+)', line)
-                handler = names[-1] if names else "inline"
-                boundary = "agent_turn" if "withAgentCapability" in line else "owner_session" if "withAuth" in line else "transport_auth"
-                add_route(rows, pattern, path, handler, boundary)
-            elif path.name == "desktop_handlers.go" and line.startswith("route.pattern,"):
-                # Both finite desktop tables are expanded below. Any third
-                # dynamic registration must be taught to this scanner.
-                continue
-            else:
-                raise ValueError(f"unsupported registration: {path.name}: {line}")
-        if path.name == "desktop_handlers.go":
-            if len(re.findall(r'mux\.Handle\(route.pattern,', source)) != 2:
-                raise ValueError("desktop route tables changed; review scanner")
-            for expression, handler in re.findall(r'\{([^\n]+?),\s*s\.(\w+)\},', source):
-                if not re.match(r'"(?:GET|POST|DELETE|PUT|PATCH) ', expression):
-                    continue
-                expression = expression.replace('prefix', '"/api/v1/tasks/{id}/desktop"')
-                parts = re.findall(r'"([^"\n]*)"', expression)
-                remainder = re.sub(r'"[^"\n]*"|[+\s]', '', expression)
-                if remainder:
-                    raise ValueError(f"unsupported desktop pattern: {expression}")
-                pattern = ''.join(parts)
-                add_route(rows, pattern, path, handler, "agent_turn" if "/agent/" in pattern else "owner_session")
-    return rows
-
-
-def add_route(rows, pattern, path, handler, boundary):
-    # Static resources and methodless fallback are still accounted for.
-    method, sep, route = pattern.partition(' ')
-    if not sep:
-        method, route = "ANY", pattern
-    key = f"{method} {route}"
-    if key in rows:
-        raise ValueError(f"duplicate route: {key}")
-    rows[key] = {"operation": key, "source": path.name, "handler": handler, "boundary": boundary}
-
-
-def js_literal_end(source, start):
-    quote = source[start]
-    index = start + 1
-    while index < len(source):
-        char = source[index]
-        if char == '\\':
-            index += 2
-            continue
-        if char == quote:
-            return index + 1
-        if quote == '`' and source.startswith('${', index):
-            index += 2
-            depth = 1
-            while index < len(source) and depth:
-                char = source[index]
-                if char in "\"'`":
-                    index = js_literal_end(source, index)
-                    continue
-                depth += (char == '{') - (char == '}')
-                index += 1
-            if depth:
-                raise ValueError('unterminated Web template expression')
-            continue
-        index += 1
-    raise ValueError('unterminated Web literal')
-
-
-def web_sites():
-    result = []
-    for path in sorted((ROOT / "web/src").rglob("*.ts")):
-        source = path.read_text()
-        # Includes API.request/cache wrappers, raw fetch, downloads and streams.
-        for match in re.finditer(r'["\x27`]\/api\/', source):
-            end = js_literal_end(source, match.start())
-            value = source[match.start() + 1:end - 1]
-            tail = source[end:source.find(';', end) + 1]
-            method = re.search(r'method:\s*["\x27](GET|POST|PUT|PATCH|DELETE)["\x27]', tail)
-            result.append({"source": str(path.relative_to(ROOT)), "line": source.count('\n', 0, match.start()) + 1,
-                           "method_hint": method.group(1) if method else "GET/see-source", "expression": value})
-    return result
-
-
-def encode(fields, rows):
-    output = io.StringIO()
-    writer = csv.DictWriter(output, fields, delimiter='\t', lineterminator='\n')
-    writer.writeheader()
-    writer.writerows(rows)
-    return output.getvalue()
-
-
-def main():
-    parser = argparse.ArgumentParser(description=__doc__)
-    parser.add_argument('--update', action='store_true')
-    parser.add_argument('--complete', action='store_true')
-    args = parser.parse_args()
-    actual = routes()
-    fields = ["operation", "source", "handler", "boundary", "mcp", "permission", "protected_flow", "tests", "status"]
-    previous = {}
-    if LEDGER.exists():
-        previous = {row['operation']: row for row in csv.DictReader(LEDGER.open(), delimiter='\t')}
-    rows = []
-    for key, row in sorted(actual.items()):
-        entry = {**row, **{field: previous.get(key, {}).get(field, "pending") for field in fields[4:]}}
-        rows.append(entry)
-    ledger = encode(fields, rows)
-    sites = web_sites()
-    web = encode(["source", "line", "method_hint", "expression"], sites)
-    if args.update:
-        LEDGER.parent.mkdir(parents=True, exist_ok=True)
-        LEDGER.write_text(ledger)
-        WEB.write_text(web)
-    else:
-        if not LEDGER.exists() or LEDGER.read_text() != ledger:
-            raise SystemExit("HTTP coverage ledger differs from registration; run --update and review every new/changed row")
-        if not WEB.exists() or WEB.read_text() != web:
-            raise SystemExit("Web call-site inventory differs; run --update and review")
-    pending = [row for row in rows if row['status'] not in {'direct', 'equivalent', 'protected_handoff', 'transport_na', 'web_prohibited_na'} or any(row[key] == 'pending' for key in fields[4:])]
-    print(f"HTTP operations: {len(rows)}; Web literal call sites: {len(sites)}; pending operations: {len(pending)}")
-    if args.complete and pending:
-        raise SystemExit("coverage is incomplete")
-
-
-if __name__ == '__main__':
-    main()
diff --git a/scripts/test_pet_mcp_coverage.py b/scripts/test_pet_mcp_coverage.py
deleted file mode 100644
index 008a35e..0000000
--- a/scripts/test_pet_mcp_coverage.py
+++ /dev/null
@@ -1,66 +0,0 @@
-#!/usr/bin/env python3
-import importlib.util
-import tempfile
-import unittest
-from pathlib import Path
-
-spec = importlib.util.spec_from_file_location("pet_mcp_coverage", Path(__file__).with_name("pet-mcp-coverage.py"))
-coverage = importlib.util.module_from_spec(spec)
-spec.loader.exec_module(coverage)
-
-
-class CoverageInventoryTest(unittest.TestCase):
-    def setUp(self):
-        self.temp = tempfile.TemporaryDirectory()
-        self.addCleanup(self.temp.cleanup)
-        coverage.ROOT = Path(self.temp.name)
-        self.http = coverage.ROOT / "internal/httpapi"
-        self.http.mkdir(parents=True)
-        self.web = coverage.ROOT / "web/src"
-        self.web.mkdir(parents=True)
-
-    def test_literal_constant_and_desktop_tables_are_traced(self):
-        (self.http / "server.go").write_text('''
-const waitRoute = "POST /api/v1/agent/turn/inbox/wait"
-mux.Handle("GET /api/v1/system", s.withAuth(http.HandlerFunc(s.systemInfo)))
-mux.Handle(waitRoute, s.withAgentCapability(http.HandlerFunc(s.waitAgentInbox)))
-mux.HandleFunc("/", s.serveWeb)
-''')
-        (self.http / "desktop_handlers.go").write_text('''
-{"GET " + prefix + "/observation", s.desktopObserve},
-mux.Handle(route.pattern, s.withAuth(route.handler))
-{"GET /api/v1/agent/desktop", s.desktopStatus},
-mux.Handle(route.pattern, s.withAgentCapability(route.handler))
-''')
-        rows = coverage.routes()
-        self.assertEqual(len(rows), 5)
-        self.assertEqual(rows["GET /api/v1/system"]["handler"], "systemInfo")
-        self.assertEqual(rows["POST /api/v1/agent/turn/inbox/wait"]["boundary"], "agent_turn")
-        self.assertIn("GET /api/v1/tasks/{id}/desktop/observation", rows)
-        self.assertIn("ANY /", rows)
-
-    def test_unknown_registration_and_duplicate_fail_closed(self):
-        path = self.http / "server.go"
-        path.write_text('mux.Handle(dynamicRoute, s.handler)')
-        with self.assertRaisesRegex(ValueError, "unsupported registration"):
-            coverage.routes()
-        path.write_text('mux.Handle("GET /same", s.a)\nmux.Handle("GET /same", s.b)')
-        with self.assertRaisesRegex(ValueError, "duplicate route"):
-            coverage.routes()
-
-    def test_web_templates_keep_source_and_method_hint(self):
-        (self.web / "api.ts").write_text('''
-return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {method: "PATCH", body: "{}"});
-fetch("/api/v1/system");
-return this.request(`/api/v1/projects${query.size ? `?${query}` : ""}`);
-''')
-        sites = coverage.web_sites()
-        self.assertEqual(len(sites), 3)
-        self.assertEqual(sites[0]["method_hint"], "PATCH")
-        self.assertEqual(sites[0]["line"], 2)
-        self.assertIn("${encodeURIComponent(id)}", sites[0]["expression"])
-        self.assertEqual(sites[2]["expression"], '/api/v1/projects${query.size ? `?${query}` : ""}')
-
-
-if __name__ == '__main__':
-    unittest.main()
```
