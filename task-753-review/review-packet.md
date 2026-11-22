# Task 753 授权修复评审包

base: 2c5f9f79d85b10e0f7435b161d213a1b364d6c71
head: 0d70ee4e93b6983772e9c3b5091cea5cc1048ceb
branch: aha/task_f6eaa3a0d0be71281428
worktree: /srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_f6eaa3a0d0be71281428
review level: L2（授权/安全）；独立评审由调度者安排，不同模型，建议时限 ≤30 分钟。

Owner 要求：已授予 Task 的硬件和共享控制授权不因 active 群路由、渠道 batch 或混合 batch 被撤销。只移除硬件、共享桌面和内置浏览器的渠道条件；邮件、知识、工作区、协作策略不变。

实现：mcp_runtime.go 的 browserMCPInjected/hardwareMCPInjected、desktopMCPInjected 及 HTTP/MCP 共用的 AgentDomainCallContext 去掉渠道排除。仍使用实时绑定/grant/session、宠物实时权限、Main/可写 snapshot、资源只读、Task 边界、观察和 revision 检查。没有新增 grant、owner 身份或权限修改入口。

桌面 discovery 继续沿用 Supported 条件；无共享时可见状态工具并不意味着能控制桌面。hardware_mcp_write/browser_mcp_write/desktop_mcp_write 是域及 snapshot 能力，具体资源仍由 handler 校验；兼容字段 hardware=true 不作为授权依据。畸形 Inbox payload 也不撤销独立的 owner 资源授权；其他域的 fail-closed 渠道识别保持原样。

## 提供情况

| 项目 | 状态 | 说明 |
|---|---|---|
| base/head、分支、worktree、完整 diff | provided | 完整源码及测试变更在本包末尾，无省略 |
| 授权函数及调用方 | provided | 下方调用链和完整关键函数；未改函数也标明来源 |
| 隔离动态回归、基线失败、vet | provided | 下方真实命令、退出码及输出 |
| 负向资源/快照/Main/撤销/越 Task | provided | fake transport/host/provider，拒绝时检查执行次数、连接状态或完整绑定/grant 行 |
| 独立 L2 评审、合并 | missing | 调度者后续处理，本 Task 未执行 |
| 生产实机、真实桌面/浏览器、邮件 | not_applicable | 明确禁止，本轮没有执行 |
| 全仓/前端矩阵、发布构建、部署、push/tag | not_applicable | 不在授权范围，没有执行 |
| race | not_applicable | 无新增并发机制；仅运行定向授权测试和 vet |

## 授权调用链与审查入口

- 运行时：mcpRuntimeForTurn → hardwareMCPInjected/browserMCPInjected/desktopMCPInjected；ioWrite 仍由 hardwareMCPSnapshotWritable 决定。硬件和浏览器保持各自独立触发条件，桌面仍只给 Main。
- 域 gate：internal/app/agent_hardware_mcp.go、agent_browser_mcp.go、agent_desktop_mcp.go 的 Agent*Allowed 分别调用对应 *Injected；Agent*WriteAllowed = Allowed && hardwareMCPSnapshotWritable，均实时检查。
- 能力：internal/httpapi/agent_state_handlers.go: agentCapabilityMap → 三域 Allowed/WriteAllowed；tools/list 使用同样的 Allowed/WriteAllowed。hardware=true 固定兼容字段未改。
- HTTP：withAgentCapability → authorizeAgentDomainRoute → AgentDomainCallContext → AgentCallContext；内部 agent_token_route_policy.go 仍按 ServeMux pattern/方法分类，未知入口拒绝。
- MCP：agentHardwareMCP/agentBrowserMCP/agentDesktopMCP → AgentDomainCallContext + 各域 Allowed；tools/call 再校验 WriteAllowed，写工具由 runAgentMCPOperation 调用现有 HTTP handler。读取也受 Task/资源/grant 边界约束。
- 硬件：agentConnectHardware/agentDisconnectHardware/agentSendHardware/agentLoginHardware/ADB handler → bindAgentTask → hardwareTarget。后者用 token TaskID 查 HardwareGroup，并保留 Writable/transport/凭据/连接检查；不会以资源 ID 替代 Task binding。
- 浏览器：agentBrowserGrantTarget → agentBrowserGrantAccessError → browser.Service 对 session/capability/tab 的检查。active grant 不能越 Task；过期/撤销/关闭 session 不能操作。
- 桌面：desktopTarget → claimDesktop/ensureSharedDesktopClaim → desktop.Manager 的 session/目标/claim/观察/revision 检查；agent controller 只能为 agent，不能冒充 owner。没有 owner 共享目标的调用到不了 provider。
- 其他域：agentMCPChannelBound 及 workspaceMCPInjected/knowledgeMCPInjected/collabMCPInjected 未改；渠道拒绝回归保留。邮件 routes、MailTaskResources、agentMailCall 未改；mcpRuntimeForTask 的 mail 注入段无 diff。

## 测试证据

环境：go1.27.4 linux/amd64；全部在此 worktree，TMPDIR=$AHA2_SCRATCH_DIR/tmp，fixture 数据和日志仅写本 Task scratch。每个命令均带 -count=1，不使用缓存测试结果。

### 初始基线（生产源码仍为 base；有授权的四种输入均被误拦）

```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" go test ./internal/app ./internal/httpapi -run 'Test(BrowserHardwareMCPInjectionUsesIndependentTriggers|TaskRouteAddsNarrowChannelMCP|HardwareChannelGrantHTTPAndMCP|BrowserChannelGrantHTTPAndMCP|DesktopMCPUsesExistingShareAndSingleUseObservation)$' -count=1
```

exit=1

结果：app 17.523s、httpapi 176.780s 均 FAIL；hardware/browser/desktop 各 active_route、channel_batch、plain_with_route、mixed_batch 四例在 capabilities 的 *_mcp/ *_mcp_write=false 断言失败。仅旧 hardware=true 字段保持 true。该轮新增 route 注入测试误用了 mcpRuntimeForTask，后续改为保留原渠道断言并在 mcpRuntimeForTurn 验证；该项以最终 overlay 结果为准。

### 最终 app 测试覆盖旧生产源码（overlay 三个实际变更授权文件为 base，测试保持最终版本）

```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" go test -overlay "$AHA2_SCRATCH_DIR/baseline-overlay.json" ./internal/app -run 'Test(BrowserHardwareMCPInjectionUsesIndependentTriggers|TaskRouteAddsNarrowChannelMCP)$' -count=1 -timeout=3m
```

exit=1

```text
--- FAIL: TestBrowserHardwareMCPInjectionUsesIndependentTriggers (10.36s)
    agent_browser_hardware_mcp_test.go:267: conversation_host batch desktop server=false want=true
--- FAIL: TestTaskRouteAddsNarrowChannelMCP (21.29s)
    --- FAIL: TestTaskRouteAddsNarrowChannelMCP/group (10.76s)
        mcp_runtime_test.go:182: active route changed read-only aha2_hardware tools
    --- FAIL: TestTaskRouteAddsNarrowChannelMCP/private (10.53s)
        mcp_runtime_test.go:182: active route changed read-only aha2_hardware tools
FAIL
FAIL	github.com/ChinaKai/AHA2/internal/app	31.652s
FAIL
```

### 最终 app 定向回归

```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" go test ./internal/app -run 'Test(BrowserAndHardwareMCPAreIndependentBuiltins|BrowserHardwareMCPInjectionUsesIndependentTriggers|BrowserHardwareMCPGatesRejectDirectCalls|TaskRouteAddsNarrowChannelMCP|PetChannelBatchKeepsResourceGrantsAfterRealInboxClaim|PetDomainRuntimeUsesCurrentBindingsPermissionsAndMode|WorkspaceMCPRuntimeExcludesChannelTurnContexts|KnowledgeMCPRuntimeExcludesChannelTurnContexts|CollabMCPGateScopesMainAutoAndChannel|CollabMCPRuntimeExcludesChannelTurns|StrictSnapshotWritableFailsClosed|HardwarePolicyReadOnlyTaskMetadataKeepsReadsAndDeniesWrites)$' -count=1 -timeout=5m
```

exit=0

```text
ok  	github.com/ChinaKai/AHA2/internal/app	112.668s
```

### HTTP/MCP 四输入矩阵与宠物（HTTP 包通过；同一次命令 app 有已修正的测试入口错误）

```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" go test ./internal/app ./internal/httpapi -run 'Test(BrowserHardwareMCPInjectionUsesIndependentTriggers|TaskRouteAddsNarrowChannelMCP|PetChannelBatchKeepsResourceGrantsAfterRealInboxClaim|HardwareChannelGrantHTTPAndMCP|BrowserChannelGrantHTTPAndMCP|BrowserChannelGrantStillRequiresWritableMainAndOwnedCapabilities|DesktopMCPUsesExistingShareAndSingleUseObservation|DesktopChannelNonMainCannotControl|PetChannelTurnKeepsResourceGrantsAndUnrelatedDomainRestrictions)$' -count=1 -timeout=4m -v
```

exit=1

```text
--- PASS: TestPetChannelBatchKeepsResourceGrantsAfterRealInboxClaim (7.91s)
--- PASS: TestHardwareChannelGrantHTTPAndMCP (34.74s)
--- PASS: TestBrowserChannelGrantHTTPAndMCP (101.07s)
--- PASS: TestBrowserChannelGrantStillRequiresWritableMainAndOwnedCapabilities (29.68s)
--- PASS: TestDesktopMCPUsesExistingShareAndSingleUseObservation (57.85s)
--- PASS: TestDesktopChannelNonMainCannotControl (6.35s)
--- PASS: TestPetChannelTurnKeepsResourceGrantsAndUnrelatedDomainRestrictions (7.41s)
ok  	github.com/ChinaKai/AHA2/internal/httpapi	237.126s
```

### 最终 HTTP 负向和无关域授权回归（桌面及浏览器加强后的测试）

```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" go test ./internal/httpapi -run 'Test(DesktopMCPUsesExistingShareAndSingleUseObservation|BrowserChannelGrantStillRequiresWritableMainAndOwnedCapabilities|AgentHardwareSharedAuthorizationRejectsWritesBeforeEffects|AgentHardwareMCPRequiresCapabilityAndDomainGate|AgentBrowserMCPRequiresCapabilityAndDomainGate|AgentHardwareMCPChannelInputKeepsBinding|AgentBrowserMCPAuthorizationFailuresAreFinal|AgentKnowledgeMCPRejectsChannelBoundTurns|AgentWorkspaceMCPRejectsChannelBoundTurns|AgentCollabMCPRejectsChannelBoundTurns|EveryAgentTokenRouteHasExplicitPolicy|OpaqueMCPResourceNamesCannotSkipLiveAuthorization|UnclassifiedTokenRoutesFailBeforeEitherAuthWrapperDispatches|DesktopMCPOperationsForPetUseOwnerShareAndLivePermission|BrowserMCPEveryOperationForOrdinaryAndPetTasks)$' -count=1 -timeout=8m
```

exit=0

```text
ok  	github.com/ChinaKai/AHA2/internal/httpapi	408.912s
```

### 最终硬件矩阵及加强的无副作用拒绝（含外 Task binding/resource ID、能力和只读工具列表）

```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" go test ./internal/httpapi -run 'Test(HardwareChannelGrantHTTPAndMCP|AgentHardwareSharedAuthorizationRejectsWritesBeforeEffects)$' -count=1 -timeout=4m
```

exit=0

```text
ok  	github.com/ChinaKai/AHA2/internal/httpapi	37.131s
```

### 静态检查

```sh
TMPDIR="$AHA2_SCRATCH_DIR/tmp" go vet ./internal/app ./internal/httpapi
```

exit=0

```text
(无诊断输出)
```

git diff --cached --check / git diff --check：exit=0。邮件及无关域生产文件无变更，mcp_runtime.go 仅改两个资源注入条件及其注释。源码提交后 HEAD 的差异与本包完整 diff 一致。Knowledge closeout：基线诊断 knowledge_29959260eb6cb76b3a78 与负向行快照经验 knowledge_418c0a64716ffbd7d100 已各提交 helped 反馈。原诊断明确限定 0.5.556，仍保留该版本事实；本分支尚待独立评审，不把候选实现写成已部署指导。

## 关键授权函数节选（head 源码，每个所列函数均为完整函数体）

### internal/app/agent_control.go

```go
func (s *Service) AgentCallContext(ctx context.Context, claims agentapi.Claims, mainOnly bool) (AgentCallContext, error) {
	turn, err := s.store.Turn(ctx, claims.TurnID)
	if err != nil || turn.TaskID != claims.TaskID || turn.AgentID != claims.AgentID {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	if turn.Status != domain.TurnRunning && turn.Status != domain.TurnStarting {
		return AgentCallContext{}, ErrAgentTurnInactive
	}
	if mainOnly && turn.AgentID != "main" {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	task, err := s.store.Task(ctx, turn.TaskID)
	if err != nil {
		return AgentCallContext{}, err
	}
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		return AgentCallContext{}, err
	}
	return AgentCallContext{Task: task, Turn: turn, Project: project}, nil
}

```

### internal/app/agent_domain_authorization.go

```go
func (s *Service) AgentDomainCallContext(ctx context.Context, claims agentapi.Claims, domainName string, write bool) (AgentCallContext, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return AgentCallContext{}, err
	}
	allowed := false
	// Discovery additionally needs a binding/active session/backend. Status
	// reads can safely report an empty or unsupported state; grant-addressed
	// handlers still enforce the actual binding, grant status and capability.
	// Never replace their resource authorization with tools/list visibility.
	// Owner resource grants are independent of channel input and routes.
	// Workspace keeps its separate channel policy in AgentWorkspaceMCPAllowed.
	readAllowed := s.petDomainReadable(ctx, call.Task)
	writeAllowed := false
	if write {
		writeAllowed = readAllowed && s.hardwareMCPSnapshotWritable(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
	}
	switch domainName {
	case "hardware":
		allowed = readAllowed
		if write {
			allowed = s.AgentHardwareMCPWriteAllowed(ctx, call)
		}
	case "browser":
		allowed = readAllowed
		if write {
			allowed = writeAllowed
		}
	case "workspace":
		allowed = s.AgentWorkspaceMCPAllowed(ctx, call)
		if write {
			allowed = s.AgentWorkspaceMCPWriteAllowed(ctx, call)
		}
	case "desktop":
		allowed = readAllowed && call.Turn.AgentID == "main"
		if write {
			allowed = writeAllowed
		}
	}
	if !allowed {
		return AgentCallContext{}, ErrAgentCallForbidden
	}
	return call, nil
}

```

### internal/app/agent_core.go

```go
func (s *Service) agentSnapshotWritable(ctx context.Context, task domain.Task, snapshotID, agentID string) bool {
	if agentID != "main" || task.ReadOnly || task.Status.Terminal() {
		return false
	}
	snapshot, err := s.store.RuntimeSnapshot(ctx, snapshotID)
	if err != nil {
		return false
	}
	return strictSnapshotWritable(snapshot.PermissionsJSON)
}

func strictSnapshotWritable(permissionsJSON string) bool {
	var payload struct {
		Filesystem *string `json:"filesystem"`
		Approval   *string `json:"approval"`
	}
	if err := json.Unmarshal([]byte(permissionsJSON), &payload); err != nil {
		return false
	}
	if payload.Filesystem == nil || payload.Approval == nil {
		return false
	}
	switch *payload.Approval {
	case "never", "auto":
	default:
		return false
	}
	switch *payload.Filesystem {
	case "workspace-write", "danger-full-access":
		return true
	default:
		return false
	}
}

```

### internal/app/agent_pet_domains.go

```go
func (s *Service) petDomainReadable(ctx context.Context, task domain.Task) bool {
	if task.ID != petTaskID {
		return task.ProjectID != petProjectID
	}
	if task.ProjectID != petProjectID {
		return false
	}
	assistant, err := s.petAssistant(ctx)
	return err == nil && assistant.PetAllowViewTasks
}

```

### internal/app/mcp_runtime.go

```go
func (s *Service) browserMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if !s.petDomainReadable(ctx, task) {
		return false
	}
	grants, err := s.store.BrowserTaskGrants(ctx, task.ID)
	if err != nil {
		return false
	}
	now := s.now().UTC()
	for _, grant := range grants {
		if grant.Status(now) != domain.BrowserGrantStatusActive {
			continue
		}
		session, sessionErr := s.store.BrowserSession(ctx, grant.SessionID)
		if sessionErr == nil && session.State != domain.BrowserSessionStateClosed {
			return true
		}
	}
	return false
}

func (s *Service) hardwareMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if !s.petDomainReadable(ctx, task) {
		return false
	}
	groups, err := s.store.HardwareGroups(ctx, task.ID)
	return err == nil && len(groups) > 0
}

```

### internal/app/agent_hardware_mcp.go

```go
func (s *Service) hardwareMCPSnapshotWritable(ctx context.Context, task domain.Task, snapshotID, agentID string) bool {
	if task.Status.Terminal() || !s.agentSnapshotWritable(ctx, task, snapshotID, agentID) {
		return false
	}
	if task.ID == petTaskID {
		assistant, err := s.petAssistant(ctx)
		return err == nil && assistant.PetAllowCreateTasks
	}
	return true
}

```

### internal/app/agent_desktop_mcp.go

```go
func (s *Service) desktopMCPInjected(ctx context.Context, task domain.Task, agentID, batchID string) bool {
	return agentID == "main" && s.petDomainReadable(ctx, task) && s.desktopMCPAvailable != nil && s.desktopMCPAvailable(task.ID)
}

```

### internal/httpapi/agent_domain_authorization.go

```go
func (s *Server) authorizeAgentDomainRoute(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims) bool {
	policy, known := agentTokenRoutePolicies[request.Pattern]
	if !known {
		writeError(writer, http.StatusForbidden, "agent_route_policy_missing")
		return false
	}
	if policy.Mode == agentRouteMCP {
		method, path, valid := strings.Cut(request.Pattern, " ")
		if !valid || method != http.MethodPost || request.Method != http.MethodPost || request.URL.EscapedPath() != path {
			writeError(writer, http.StatusForbidden, "agent_route_policy_mismatch")
			return false
		}
		return true // The exact registered MCP handler checks the named tool.
	}
	if policy.Mode == agentRouteBusiness && policy.Domain != "attachment" {
		return true
	}
	if policy.Mode != agentRouteResource && !(policy.Mode == agentRouteBusiness && policy.Domain == "attachment") {
		writeError(writer, http.StatusForbidden, "agent_route_policy_invalid")
		return false
	}
	if s.app == nil {
		writeError(writer, http.StatusServiceUnavailable, "agent_api_unavailable")
		return false
	}
	var err error
	if policy.Domain == "attachment" {
		// This dual-auth resource keeps ordinary collaborators' own-Task reads,
		// but an expired Turn cannot retain data access through a live token.
		_, err = s.app.AgentCallContext(request.Context(), claims, false)
	} else {
		_, err = s.app.AgentDomainCallContext(request.Context(), claims, policy.Domain, policy.Access == agentRouteWrite)
	}
	if err != nil {
		writeAgentControlError(writer, err)
		return false
	}
	return true
}

```

### internal/httpapi/agent_handlers.go

```go
func (s *Server) bindAgentTask(writer http.ResponseWriter, request *http.Request) bool {
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok || claims.TaskID == "" {
		writeError(writer, http.StatusUnauthorized, "agent_capability_invalid")
		return false
	}
	request.SetPathValue("id", claims.TaskID)
	return true
}

```

### internal/httpapi/hardware_handlers.go

```go
func (s *Server) hardwareTarget(
	writer http.ResponseWriter,
	request *http.Request,
) (domain.Task, domain.HardwareGroup, string, bool, bool) {
	taskID := request.PathValue("id")
	hardwareID := request.PathValue("hardware")
	task, err := s.store.Task(request.Context(), taskID)
	if err != nil {
		if _, mirrorErr := s.store.RemoteTaskMirror(request.Context(), taskID); mirrorErr == nil {
			writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "hardware_read_only", "message": "远端 Task 硬件配置不能连接、登录或发送，请先显式接管"})
			return domain.Task{}, domain.HardwareGroup{}, "", true, false
		}
		writeError(writer, http.StatusNotFound, "task_not_found")
		return domain.Task{}, domain.HardwareGroup{}, "", false, false
	}
	group, err := s.store.HardwareGroup(request.Context(), taskID, hardwareID)
	if err != nil {
		writeError(writer, http.StatusNotFound, "hardware_group_not_found")
		return domain.Task{}, domain.HardwareGroup{}, "", false, false
	}
	transport := strings.TrimSpace(request.URL.Query().Get("transport"))
	if transport == "" {
		if group.Supports(domain.HardwareTransportSerial) {
			transport = domain.HardwareTransportSerial
		} else {
			transport = domain.HardwareTransportNetwork
		}
	}
	if !group.Supports(transport) {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "hardware_transport_invalid", "message": "硬件组未配置该连接方式",
		})
		return domain.Task{}, domain.HardwareGroup{}, "", false, false
	}
	// Owner Web retains its resource/terminal-state policy. Agent-only Task and
	// snapshot restrictions belong to the shared Agent authorization boundary.
	readOnly := task.Status.Terminal() || !group.Writable()
	if claims, agent := agentClaimsFromContext(request.Context()); agent {
		if _, err := s.app.AgentDomainCallContext(request.Context(), claims, "hardware", request.Method != http.MethodGet); err != nil {
			writeAgentControlError(writer, err)
			return domain.Task{}, domain.HardwareGroup{}, "", true, false
		}
		if request.Method != http.MethodGet && !group.Writable() {
			writeError(writer, http.StatusForbidden, "hardware_read_only")
			return domain.Task{}, domain.HardwareGroup{}, "", true, false
		}
	}
	return task, group, transport, readOnly, true
}

```

### internal/httpapi/browser_handlers.go

```go
func agentBrowserGrantAccessError(grant browser.GrantView, taskID string) *browser.Error {
	if grant.TaskID != taskID {
		return &browser.Error{
			Code: "browser_grant_forbidden", Message: "该浏览器授权不属于当前 Task",
			Status: http.StatusForbidden,
		}
	}
	switch grant.Status {
	case domain.BrowserGrantStatusRevoked:
		return &browser.Error{
			Code: "browser_grant_revoked", Message: "浏览器授权已撤销",
			Status: http.StatusForbidden,
		}
	case domain.BrowserGrantStatusExpired:
		return &browser.Error{
			Code: "browser_grant_expired", Message: "浏览器授权已过期",
			Status: http.StatusForbidden,
		}
	}
	return nil
}

```

## 完整 diff（base → head，所有 14 个源码/测试文件）

```diff
diff --git a/internal/app/agent_browser_hardware_mcp_test.go b/internal/app/agent_browser_hardware_mcp_test.go
index c122e37..e964d45 100644
--- a/internal/app/agent_browser_hardware_mcp_test.go
+++ b/internal/app/agent_browser_hardware_mcp_test.go
@@ -151,15 +151,21 @@ func TestBrowserAndHardwareMCPAreIndependentBuiltins(t *testing.T) {
 
 // TestBrowserHardwareMCPInjectionUsesIndependentTriggers proves the two
 // domains are not merged into one condition: a hardware binding alone injects
-// only hardware, a browser lease alone injects only browser, and a channel
-// hosted batch removes both.
+// only hardware, a browser lease alone injects only browser, and channel input
+// does not revoke either binding.
 func TestBrowserHardwareMCPInjectionUsesIndependentTriggers(t *testing.T) {
 	ctx, database, service, task, model := newStreamCompatFixture(t)
 	const apiURL = "http://127.0.0.1:8766"
+	sharedTaskID := task.ID
+	desktopAvailable := false
+	service.SetDesktopMCPAvailability(func(id string) bool { return desktopAvailable && id == task.ID })
 
 	check := func(label string, task domain.Task, batchID string, wantBrowser, wantHardware bool) {
 		t.Helper()
 		servers, allowed := service.mcpRuntimeForTurn(ctx, task, "main", apiURL, batchID)
+		if got, want := hasMCPServer(servers, desktopMCPName), desktopAvailable && task.ID == sharedTaskID; got != want {
+			t.Fatalf("%s desktop server=%v want=%v", label, got, want)
+		}
 		if got := hasMCPServer(servers, browserMCPName); got != wantBrowser {
 			t.Fatalf("%s browser server=%v want=%v servers=%#v", label, got, wantBrowser, servers)
 		}
@@ -188,6 +194,7 @@ func TestBrowserHardwareMCPInjectionUsesIndependentTriggers(t *testing.T) {
 	check("hardware only", task, "", false, true)
 
 	seedBrowserMCPGrant(t, ctx, database, task, "grant-both", "active")
+	desktopAvailable = true
 	check("both triggers", task, "", true, true)
 
 	browserOnly, err := service.CreateTask(ctx, CreateTaskInput{
@@ -212,8 +219,7 @@ func TestBrowserHardwareMCPInjectionUsesIndependentTriggers(t *testing.T) {
 		check(state+" grant", inactive, "", false, false)
 	}
 
-	// A channel-hosted batch removes both domains even though the Task holds
-	// both triggers.
+	// Resource grants remain valid for pure channel and mixed batches.
 	raw, err := sql.Open("sqlite", filepath.Join(database.DataDir(), "aha2.db"))
 	if err != nil {
 		t.Fatal(err)
@@ -257,13 +263,15 @@ func TestBrowserHardwareMCPInjectionUsesIndependentTriggers(t *testing.T) {
 	}
 	for _, mode := range []string{"conversation_host", "task_route"} {
 		batchID := "batch-browser-hardware-" + strings.ReplaceAll(mode, "_", "-")
-		insertInbox(batchID, "plain", "channel:"+mode)
-		check(mode+" batch", task, batchID, false, false)
+		insertInbox(batchID, "channel:"+mode)
+		check(mode+" batch", task, batchID, true, true)
+		insertInbox(batchID+"-mixed", "plain", "channel:"+mode)
+		check(mode+" mixed batch", task, batchID+"-mixed", true, true)
 	}
 	insertInbox("batch-browser-hardware-plain", "plain")
 	check("plain batch", task, "batch-browser-hardware-plain", true, true)
 	insertInbox("batch-browser-hardware-malformed", "raw:not-json")
-	check("malformed batch", task, "batch-browser-hardware-malformed", false, false)
+	check("malformed batch", task, "batch-browser-hardware-malformed", true, true)
 
 	check("pet task", domain.Task{ID: petTaskID}, "", false, false)
 	check("pet project", domain.Task{ID: "task-pet-project", ProjectID: petProjectID}, "", false, false)
diff --git a/internal/app/agent_browser_mcp.go b/internal/app/agent_browser_mcp.go
index a146363..93de4fb 100644
--- a/internal/app/agent_browser_mcp.go
+++ b/internal/app/agent_browser_mcp.go
@@ -6,8 +6,8 @@ import "context"
 // domain MCP. The runtime injects the server only when browserMCPInjected
 // returns true, and every MCP method re-checks this gate, so knowing the URL or
 // reading tools/list is never an authorization shortcut. The domain is absent
-// for a Task without an active browser grant, a channel-hosted Turn, or a pet
-// whose live view permission has been revoked.
+// for a Task without an active browser grant or a pet whose live view
+// permission has been revoked. Channel input does not change a resource grant.
 func (s *Service) AgentBrowserMCPAllowed(ctx context.Context, call AgentCallContext) bool {
 	return s.browserMCPInjected(ctx, call.Task, call.Turn.InboxBatchID)
 }
diff --git a/internal/app/agent_desktop_mcp.go b/internal/app/agent_desktop_mcp.go
index 01d2ab7..d2f8750 100644
--- a/internal/app/agent_desktop_mcp.go
+++ b/internal/app/agent_desktop_mcp.go
@@ -23,8 +23,10 @@ func (s *Service) SetDesktopMCPAvailability(available func(string) bool) {
 	s.desktopMCPAvailable = available
 }
 
+// Channel input does not change the existing owner share. The desktop
+// handlers still require the Task session, target, controller and observation.
 func (s *Service) desktopMCPInjected(ctx context.Context, task domain.Task, agentID, batchID string) bool {
-	return agentID == "main" && s.petDomainReadable(ctx, task) && !s.agentMCPChannelBound(ctx, task.ID, batchID) && s.desktopMCPAvailable != nil && s.desktopMCPAvailable(task.ID)
+	return agentID == "main" && s.petDomainReadable(ctx, task) && s.desktopMCPAvailable != nil && s.desktopMCPAvailable(task.ID)
 }
 
 func (s *Service) AgentDesktopMCPAllowed(ctx context.Context, call AgentCallContext) bool {
diff --git a/internal/app/agent_domain_authorization.go b/internal/app/agent_domain_authorization.go
index 681883d..f3ef91a 100644
--- a/internal/app/agent_domain_authorization.go
+++ b/internal/app/agent_domain_authorization.go
@@ -20,7 +20,9 @@ func (s *Service) AgentDomainCallContext(ctx context.Context, claims agentapi.Cl
 	// reads can safely report an empty or unsupported state; grant-addressed
 	// handlers still enforce the actual binding, grant status and capability.
 	// Never replace their resource authorization with tools/list visibility.
-	readAllowed := s.petDomainReadable(ctx, call.Task) && !s.agentMCPChannelBound(ctx, call.Task.ID, call.Turn.InboxBatchID)
+	// Owner resource grants are independent of channel input and routes.
+	// Workspace keeps its separate channel policy in AgentWorkspaceMCPAllowed.
+	readAllowed := s.petDomainReadable(ctx, call.Task)
 	writeAllowed := false
 	if write {
 		writeAllowed = readAllowed && s.hardwareMCPSnapshotWritable(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
diff --git a/internal/app/agent_hardware_mcp.go b/internal/app/agent_hardware_mcp.go
index 71fe625..6a034db 100644
--- a/internal/app/agent_hardware_mcp.go
+++ b/internal/app/agent_hardware_mcp.go
@@ -8,8 +8,8 @@ import (
 // AgentHardwareMCPAllowed is the single server-side gate for the Task hardware
 // domain MCP. It deliberately does not share the browser trigger: the domain is
 // present exactly when the calling Task has at least one bound hardware group,
-// and never for a channel-hosted Turn. Pet access rechecks live view permission. Every method
-// re-checks it, so tool visibility is never authorization.
+// regardless of channel input or routes. Pet access rechecks live view
+// permission. Every method re-checks it, so tool visibility is never authorization.
 func (s *Service) AgentHardwareMCPAllowed(ctx context.Context, call AgentCallContext) bool {
 	return s.hardwareMCPInjected(ctx, call.Task, call.Turn.InboxBatchID)
 }
diff --git a/internal/app/agent_pet_channel_scope_test.go b/internal/app/agent_pet_channel_scope_test.go
index 95b7695..9c4dfd5 100644
--- a/internal/app/agent_pet_channel_scope_test.go
+++ b/internal/app/agent_pet_channel_scope_test.go
@@ -8,7 +8,7 @@ import (
 	"time"
 )
 
-func TestPetChannelBatchExcludesEverySharedDomainAfterRealInboxClaim(t *testing.T) {
+func TestPetChannelBatchKeepsResourceGrantsAfterRealInboxClaim(t *testing.T) {
 	f := newPetTaskFixture(t)
 	task := f.createPetDraft(t, "channel scope fixture")
 	assistant, err := f.database.EnsureOwnerAssistant(f.ctx, f.createdAt)
@@ -50,11 +50,16 @@ func TestPetChannelBatchExcludesEverySharedDomainAfterRealInboxClaim(t *testing.
 		t.Fatal("Inbox batch identity was lost")
 	}
 	servers, _ := f.service.mcpRuntimeForTurn(f.ctx, task, "main", "http://127.0.0.1:8766", stored.InboxBatchID)
-	for _, name := range []string{knowledgeMCPName, workspaceMCPName, browserMCPName, hardwareMCPName, desktopMCPName, collabMCPName} {
+	for _, name := range []string{knowledgeMCPName, workspaceMCPName, collabMCPName} {
 		if hasMCPServer(servers, name) {
 			t.Fatalf("pet channel Turn received %s", name)
 		}
 	}
+	for _, name := range []string{browserMCPName, hardwareMCPName, desktopMCPName} {
+		if !hasMCPServer(servers, name) {
+			t.Fatalf("pet channel Turn lost %s", name)
+		}
+	}
 	claims := agentapi.Claims{TaskID: task.ID, AgentID: "main", TurnID: turn.ID}
 	call, err := f.service.AgentCallContext(f.ctx, claims, false)
 	if err != nil {
@@ -65,8 +70,9 @@ func TestPetChannelBatchExcludesEverySharedDomainAfterRealInboxClaim(t *testing.
 	}
 	for _, domainName := range []string{"workspace", "browser", "hardware", "desktop"} {
 		for _, write := range []bool{false, true} {
-			if _, err := f.service.AgentDomainCallContext(f.ctx, claims, domainName, write); err == nil {
-				t.Fatalf("pet channel Turn can call %s write=%v", domainName, write)
+			_, err := f.service.AgentDomainCallContext(f.ctx, claims, domainName, write)
+			if (err == nil) != (domainName != "workspace") {
+				t.Fatalf("pet channel %s write=%v err=%v", domainName, write, err)
 			}
 		}
 	}
diff --git a/internal/app/mcp_runtime.go b/internal/app/mcp_runtime.go
index 5ebb1a7..3da5958 100644
--- a/internal/app/mcp_runtime.go
+++ b/internal/app/mcp_runtime.go
@@ -331,16 +331,13 @@ func (s *Service) workspaceMCPInjected(ctx context.Context, task domain.Task, in
 // browserMCPInjected reports whether the shared-browser domain belongs to this
 // Turn. Its trigger is the presence of an active browser grant for the Task:
 // without a lease the tools could only report "no grant", so the domain is not
-// advertised at all. Pet access requires its live view permission; channel-hosted
-// Turns remain excluded. A grant whose session is already closed does not count, which
-// mirrors the Agent-facing HTTP view.
+// advertised at all. Pet access requires its live view permission. Channel
+// input and routes do not revoke owner resource grants. A grant whose session
+// is already closed does not count, mirroring the Agent-facing HTTP view.
 func (s *Service) browserMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
 	if !s.petDomainReadable(ctx, task) {
 		return false
 	}
-	if s.agentMCPChannelBound(ctx, task.ID, inboxBatchID) {
-		return false
-	}
 	grants, err := s.store.BrowserTaskGrants(ctx, task.ID)
 	if err != nil {
 		return false
@@ -361,14 +358,11 @@ func (s *Service) browserMCPInjected(ctx context.Context, task domain.Task, inbo
 // hardwareMCPInjected reports whether the Task hardware domain belongs to this
 // Turn. It is intentionally independent from the browser trigger: a Task with
 // bound hardware groups receives this domain even when it holds no browser
-// lease, and vice versa.
+// lease, and vice versa. Channel input and routes do not revoke bindings.
 func (s *Service) hardwareMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
 	if !s.petDomainReadable(ctx, task) {
 		return false
 	}
-	if s.agentMCPChannelBound(ctx, task.ID, inboxBatchID) {
-		return false
-	}
 	groups, err := s.store.HardwareGroups(ctx, task.ID)
 	return err == nil && len(groups) > 0
 }
diff --git a/internal/app/mcp_runtime_test.go b/internal/app/mcp_runtime_test.go
index 22ec838..d30aa03 100644
--- a/internal/app/mcp_runtime_test.go
+++ b/internal/app/mcp_runtime_test.go
@@ -141,6 +141,11 @@ func TestTaskRouteAddsNarrowChannelMCP(t *testing.T) {
 				t.Fatal(err)
 			}
 
+			// A Web/plain Turn with an active route keeps owner resource grants.
+			seedHardwareMCPGroup(t, ctx, fixture.database, task)
+			seedBrowserMCPGrant(t, ctx, fixture.database, task, "routed-grant", "active")
+			fixture.service.SetDesktopMCPAvailability(func(string) bool { return true })
+
 			servers, allowed := fixture.service.mcpRuntimeForTask(ctx, task, "http://127.0.0.1:8766/")
 			found := backend.MCPServer{}
 			for _, server := range servers {
@@ -172,6 +177,11 @@ func TestTaskRouteAddsNarrowChannelMCP(t *testing.T) {
 			// knowledge domain MCP must not be injected even though the Task's
 			// own switch is on.
 			turnServers, turnAllowed := fixture.service.mcpRuntimeForTurn(ctx, task, "main", "http://127.0.0.1:8766/", "")
+			for name, want := range map[string]int{hardwareMCPName: 2, browserMCPName: 8, desktopMCPName: 2} {
+				if !hasMCPServer(turnServers, name) || len(mcpDomainTools(turnAllowed, name)) != want {
+					t.Fatalf("active route changed read-only %s tools", name)
+				}
+			}
 			for _, server := range turnServers {
 				if server.Name == knowledgeMCPName {
 					t.Fatalf("routed turn gained knowledge MCP: %#v", turnServers)
diff --git a/internal/httpapi/agent_browser_mcp_test.go b/internal/httpapi/agent_browser_mcp_test.go
index ed3f5ea..2d25944 100644
--- a/internal/httpapi/agent_browser_mcp_test.go
+++ b/internal/httpapi/agent_browser_mcp_test.go
@@ -521,14 +521,14 @@ func TestAgentBrowserMCPAuthorizationFailuresAreFinal(t *testing.T) {
 		t.Fatalf("error leaked the owning task: %s", foreign.Text)
 	}
 
-	// A channel-hosted Turn loses the domain even while the lease is active.
+	// A channel-hosted Turn retains its active owner grant.
 	retireCoreTurn(t, f.store, f.turn)
 	channelTurn, channelToken := browserMCPToken(t, f.browserAPIFixture, f.task, "batch-browser-mcp-channel")
 	seedKnowledgeMCPInbox(t, filepath.Join(f.store.DataDir(), "aha2.db"), f.task, channelTurn, "channel:task_route")
 	response := agentRequest(t, browserMCPURL(f.browserAPIFixture), http.MethodPost, channelToken, map[string]any{
 		"jsonrpc": "2.0", "id": 1, "method": "initialize",
 	})
-	if response.StatusCode != http.StatusForbidden {
+	if response.StatusCode != http.StatusOK {
 		t.Fatalf("channel turn status=%d body=%s", response.StatusCode, readBody(t, response))
 	}
 	response.Body.Close()
@@ -550,7 +550,7 @@ func TestAgentBrowserMCPAuthorizationFailuresAreFinal(t *testing.T) {
 	response = agentRequest(t, browserMCPURL(f.browserAPIFixture), http.MethodPost, brokenToken, map[string]any{
 		"jsonrpc": "2.0", "id": 1, "method": "initialize",
 	})
-	if response.StatusCode != http.StatusForbidden {
+	if response.StatusCode != http.StatusOK {
 		t.Fatalf("malformed batch status=%d body=%s", response.StatusCode, readBody(t, response))
 	}
 	response.Body.Close()
diff --git a/internal/httpapi/agent_control_channel_test.go b/internal/httpapi/agent_control_channel_test.go
new file mode 100644
index 0000000..b681e86
--- /dev/null
+++ b/internal/httpapi/agent_control_channel_test.go
@@ -0,0 +1,273 @@
+package httpapi
+
+import (
+	"context"
+	"encoding/json"
+	"net/http"
+	"path/filepath"
+	"reflect"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/store"
+)
+
+var controlChannelScenarios = []string{"active_route", "channel_batch", "plain_with_route", "mixed_batch"}
+
+// These are isolated owner-created routes/bindings. No channel participant is
+// given an owner capability, and no real channel or device is contacted.
+func seedControlChannel(t *testing.T, database *store.Store, task domain.Task, turn domain.Turn, scenario string) {
+	t.Helper()
+	ctx := context.Background()
+	if scenario == "active_route" || scenario == "plain_with_route" {
+		now := time.Now().UTC()
+		id := domain.NewID("control_channel")
+		owner := domain.Owner{ID: id, Username: id, CreatedAt: now}
+		if err := database.CreateOwner(ctx, owner); err != nil {
+			t.Fatal(err)
+		}
+		plugin := domain.ChannelPlugin{ID: id, ProviderKey: id, DisplayName: "Fixture", ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1, InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now}
+		if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
+			t.Fatal(err)
+		}
+		instance := domain.ChannelInstance{ID: id, PluginID: id, OwnerID: owner.ID, RuntimeDeviceID: "fixture", Name: "Fixture", Status: "ready", Revision: 1, HostProjectID: task.ProjectID, HostWorkspaceID: task.WorkspaceID, OwnerBound: true, CreatedAt: now, UpdatedAt: now}
+		endpoint := domain.ChannelEndpoint{ID: id, InstanceID: id, Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, CreatedAt: now, UpdatedAt: now}
+		if err := database.CreateChannelInstance(ctx, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
+			t.Fatal(err)
+		}
+		conversation := domain.ChannelConversation{ID: id, InstanceID: id, EndpointID: id, ScopeKeyVersion: 1, ScopeKey: id, DisplayName: "Fixture", Status: "active", HostTaskID: task.ID, CreatedAt: now, UpdatedAt: now}
+		session := domain.ChannelSession{ID: id, ConversationID: id, Generation: 1, Mode: "task_route", Status: "active", StartedAt: now}
+		if err := database.CreateChannelConversation(ctx, conversation, session); err != nil {
+			t.Fatal(err)
+		}
+		if _, err := database.ActivateOwnerChannelTaskRoute(ctx, id, id, task.ID, now); err != nil {
+			t.Fatal(err)
+		}
+		if active, err := database.HasActiveChannelTaskRoute(ctx, task.ID); err != nil || !active {
+			t.Fatalf("route not active: %v", err)
+		}
+	}
+	path := filepath.Join(database.DataDir(), "aha2.db")
+	switch scenario {
+	case "plain_with_route":
+		seedKnowledgeMCPInbox(t, path, task, turn, "plain")
+	case "channel_batch":
+		seedKnowledgeMCPInbox(t, path, task, turn, "channel:task_route")
+	case "mixed_batch":
+		seedKnowledgeMCPInbox(t, path, task, turn, "plain", "channel:conversation_host")
+	}
+}
+
+func assertControlHTTP(t *testing.T, base, token, method, path string, body any, want int) {
+	t.Helper()
+	response := agentRequest(t, base+"/api/v1/agent"+path, method, token, body)
+	defer response.Body.Close()
+	if response.StatusCode != want {
+		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, want, readBody(t, response))
+	}
+}
+
+func assertControlCapabilities(t *testing.T, base, token, name string, read, write bool) {
+	t.Helper()
+	response := agentRequest(t, base+"/api/v1/agent/capabilities", http.MethodGet, token, nil)
+	var payload struct {
+		Capabilities map[string]bool `json:"capabilities"`
+	}
+	decodeResponse(t, response, &payload)
+	if response.StatusCode != 200 || payload.Capabilities[name+"_mcp"] != read || payload.Capabilities[name+"_mcp_write"] != write {
+		t.Fatalf("%s capabilities=%v status=%d", name, payload.Capabilities, response.StatusCode)
+	}
+}
+
+func TestHardwareChannelGrantHTTPAndMCP(t *testing.T) {
+	for _, scenario := range controlChannelScenarios {
+		t.Run(scenario, func(t *testing.T) {
+			f := newHardwareAuthorizationFixture(t, false)
+			seedControlChannel(t, f.h.database, f.task, f.turn, scenario)
+			assertControlCapabilities(t, f.server.URL, f.token, "hardware", true, true)
+			status, names, _ := coreMCPToolsList(t, f.server.URL+"/api/v1/agent/hardware/mcp", f.token)
+			if status != 200 || !coreMCPNameIncluded(names, "hardware_connect") {
+				t.Fatalf("discovery=%d %v", status, names)
+			}
+			if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"}); result.Status != 200 || result.IsError {
+				t.Fatalf("connect=%#v", result)
+			}
+			_ = f.connection(t)
+			assertControlHTTP(t, f.server.URL, f.token, http.MethodGet, "/hardware", nil, 200)
+			assertControlHTTP(t, f.server.URL, f.token, http.MethodGet, "/hardware/board/terminal", nil, 200)
+			if result := f.mcp(t, "hardware_terminal_read", map[string]any{"hardware_id": "board"}); result.Status != 200 || result.IsError {
+				t.Fatalf("read=%#v", result)
+			}
+			assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/hardware/device/adb/exec", map[string]any{"args": []string{"shell", "fixture"}}, 200)
+			if f.executions.Load() != 1 {
+				t.Fatal("HTTP write did not reach fake ADB exactly once")
+			}
+			assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/hardware/board/disconnect", map[string]any{}, 200)
+			if f.manager.Status(f.task.ID, "board", "network", false).Connected {
+				t.Fatal("HTTP disconnect left fixture open")
+			}
+
+			// The same token loses its binding immediately. A bare resource ID from
+			// another Task is not a binding, and a failed call must preserve that Task.
+			other := createCoreTaskInProject(t, f.h, f.task, "workspace-write")
+			binding := []domain.TaskHardwareBinding{{TaskID: other.ID, ID: "foreign", ResourceID: f.board.ID}}
+			if err := f.h.database.ReplaceTaskHardwareBindings(f.h.ctx, other.ID, binding); err != nil {
+				t.Fatal(err)
+			}
+			before, err := f.h.database.TaskHardwareBindings(f.h.ctx, other.ID)
+			if err != nil {
+				t.Fatal(err)
+			}
+			// Even while this Task's own domain is available, a foreign binding
+			// ID or the resource ID cannot be used in place of a Task binding.
+			beforeAccepts := f.accepts.Load()
+			for _, id := range []string{"foreign", f.board.ID} {
+				assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/hardware/"+id+"/connect", map[string]any{}, 404)
+				if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": id}); !refused(result) {
+					t.Fatalf("foreign hardware passed: %#v", result)
+				}
+			}
+			if err := f.h.database.ReplaceTaskHardwareBindings(f.h.ctx, f.task.ID, nil); err != nil {
+				t.Fatal(err)
+			}
+			for _, id := range []string{"board", "foreign", f.board.ID} {
+				assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/hardware/"+id+"/connect", map[string]any{}, 403)
+				if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": id}); result.Status != 403 {
+					t.Fatalf("revoked/foreign MCP=%#v", result)
+				}
+			}
+			after, err := f.h.database.TaskHardwareBindings(f.h.ctx, other.ID)
+			if err != nil || !reflect.DeepEqual(before, after) || f.accepts.Load() != beforeAccepts {
+				t.Fatal("denied calls changed foreign bindings or opened transport")
+			}
+			assertControlCapabilities(t, f.server.URL, f.token, "hardware", false, false)
+		})
+	}
+}
+
+func TestBrowserChannelGrantHTTPAndMCP(t *testing.T) {
+	for _, scenario := range controlChannelScenarios {
+		t.Run(scenario, func(t *testing.T) {
+			f := newBrowserMCPFixture(t, "https://example.test/fixture", "Fixture")
+			seedControlChannel(t, f.store, f.task, f.turn, scenario)
+			assertControlCapabilities(t, f.server.URL, f.token, "browser", true, true)
+			status, names, _ := coreMCPToolsList(t, browserMCPURL(f.browserAPIFixture), f.token)
+			if status != 200 || !coreMCPNameIncluded(names, "browser_tab_open") {
+				t.Fatalf("discovery=%d %v", status, names)
+			}
+			opened := f.call(t, "browser_tab_open", map[string]any{"grant_id": f.grantID, "url": "https://example.test/open"})
+			if opened.Status != 200 || opened.IsError || f.host.OpenTabCalls != 1 {
+				t.Fatalf("open=%#v calls=%d", opened, f.host.OpenTabCalls)
+			}
+			var payload struct {
+				Tab struct {
+					ID string `json:"id"`
+				} `json:"tab"`
+			}
+			if err := json.Unmarshal([]byte(opened.Text), &payload); err != nil || payload.Tab.ID == "" {
+				t.Fatal("no tab returned")
+			}
+			base := "/browser/grants/" + f.grantID
+			assertControlHTTP(t, f.server.URL, f.token, http.MethodGet, base+"/tabs", nil, 200)
+			if result := f.call(t, "browser_tabs_list", map[string]any{"grant_id": f.grantID}); result.Status != 200 || result.IsError {
+				t.Fatalf("tabs=%#v", result)
+			}
+			assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, base+"/tabs/"+payload.Tab.ID+"/navigate", map[string]any{"url": "https://example.test/next"}, 200)
+			// Owner revocation must invalidate this already-issued Turn token.
+			if response, _ := f.do(t, http.MethodDelete, "/api/v1/browser-grants/"+f.grantID, nil); response.StatusCode != 200 {
+				t.Fatal("owner revoke failed")
+			}
+			before, err := f.store.BrowserTaskGrants(context.Background(), f.task.ID)
+			if err != nil {
+				t.Fatal(err)
+			}
+			calls := f.host.OpenTabCalls
+			assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, base+"/tabs", map[string]any{"url": "https://example.test/denied"}, 403)
+			if result := f.call(t, "browser_tab_open", map[string]any{"grant_id": f.grantID, "url": "https://example.test/denied"}); result.Status != 403 {
+				t.Fatalf("revoked MCP=%#v", result)
+			}
+			after, err := f.store.BrowserTaskGrants(context.Background(), f.task.ID)
+			if err != nil || !reflect.DeepEqual(before, after) || f.host.OpenTabCalls != calls {
+				t.Fatal("denied call changed grants or reached host")
+			}
+			assertControlCapabilities(t, f.server.URL, f.token, "browser", false, false)
+		})
+	}
+}
+
+func TestBrowserChannelGrantStillRequiresWritableMainAndOwnedCapabilities(t *testing.T) {
+	f := newBrowserMCPFixture(t, "https://example.test/fixture", "Fixture")
+	seedControlChannel(t, f.store, f.task, f.turn, "mixed_batch")
+	other := createHardwareAPITask(t, f.store)
+	foreign := seededBrowserGrant(t, f.store, other, "foreign-channel", "active")
+	for _, grant := range []string{foreign, seededBrowserGrant(t, f.store, f.task, "expired-channel", "expired"), seededBrowserGrant(t, f.store, f.task, "revoked-channel", "revoked")} {
+		before, err := f.store.BrowserTaskGrants(context.Background(), other.ID)
+		if err != nil {
+			t.Fatal(err)
+		}
+		calls := f.host.OpenTabCalls
+		assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/browser/grants/"+grant+"/tabs", map[string]any{"url": "https://example.test/denied"}, 403)
+		result := f.call(t, "browser_tab_open", map[string]any{"grant_id": grant, "url": "https://example.test/denied"})
+		if !result.IsError {
+			t.Fatalf("foreign/inactive grant passed: %#v", result)
+		}
+		after, err := f.store.BrowserTaskGrants(context.Background(), other.ID)
+		if err != nil || !reflect.DeepEqual(before, after) || f.host.OpenTabCalls != calls {
+			t.Fatal("denied grant changed rows or reached host")
+		}
+	}
+	// Default owner grant lacks interact: channel input cannot add it.
+	var opened struct {
+		Tab struct {
+			ID string `json:"id"`
+		} `json:"tab"`
+	}
+	decodeMCPText(t, f.call(t, "browser_tab_open", map[string]any{"grant_id": f.grantID, "url": "https://example.test/owned"}), &opened)
+	if opened.Tab.ID == "" {
+		t.Fatal("missing owned tab")
+	}
+	result := f.call(t, "browser_action", map[string]any{"grant_id": f.grantID, "tab_id": opened.Tab.ID, "kind": "click", "x": 1, "y": 1})
+	if !result.IsError || len(f.host.ActionCalls) != 0 {
+		t.Fatal("channel input expanded capabilities")
+	}
+	assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/browser/grants/"+f.grantID+"/tabs/"+opened.Tab.ID+"/actions", map[string]any{"kind": "click", "x": 1, "y": 1}, 403)
+	if len(f.host.ActionCalls) != 0 {
+		t.Fatal("HTTP expanded grant capabilities")
+	}
+	snapshot, err := f.store.RuntimeSnapshot(context.Background(), f.task.RuntimeConfigSnapshotID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	snapshot.ID = domain.NewID("runtime")
+	snapshot.PermissionsJSON = `{"filesystem":"read-only","approval":"never"}`
+	if err := f.store.CreateRuntimeSnapshot(context.Background(), snapshot); err != nil {
+		t.Fatal(err)
+	}
+	for _, scenario := range []string{"read_only_snapshot", "non_main"} {
+		t.Run(scenario, func(t *testing.T) {
+			retireCoreTurn(t, f.store, f.turn)
+			task, agent := f.task, "sub"
+			if scenario == "read_only_snapshot" {
+				task.RuntimeConfigSnapshotID, agent = snapshot.ID, "main"
+			}
+			f.turn = createCoreTurn(t, f.store, task, agent, domain.NewID("batch"))
+			seedControlChannel(t, f.store, f.task, f.turn, "channel_batch")
+			f.token, err = f.agents.Issue(f.task.ID, agent, f.turn.ID, time.Hour)
+			if err != nil {
+				t.Fatal(err)
+			}
+			assertControlCapabilities(t, f.server.URL, f.token, "browser", true, false)
+			status, names, _ := coreMCPToolsList(t, browserMCPURL(f.browserAPIFixture), f.token)
+			if status != 200 || coreMCPNameIncluded(names, "browser_tab_open") {
+				t.Fatalf("read-only discovery=%d %v", status, names)
+			}
+			before := f.host.OpenTabCalls
+			assertControlHTTP(t, f.server.URL, f.token, http.MethodPost, "/browser/grants/"+f.grantID+"/tabs", map[string]any{"url": "https://example.test/denied"}, 403)
+			result := f.call(t, "browser_tab_open", map[string]any{"grant_id": f.grantID, "url": "https://example.test/denied"})
+			if result.Status != 403 || f.host.OpenTabCalls != before {
+				t.Fatalf("write reached host: %#v", result)
+			}
+		})
+	}
+}
diff --git a/internal/httpapi/agent_desktop_mcp_test.go b/internal/httpapi/agent_desktop_mcp_test.go
index d7ee6fe..11d70d0 100644
--- a/internal/httpapi/agent_desktop_mcp_test.go
+++ b/internal/httpapi/agent_desktop_mcp_test.go
@@ -10,6 +10,12 @@ import (
 )
 
 func TestDesktopMCPUsesExistingShareAndSingleUseObservation(t *testing.T) {
+	for _, scenario := range controlChannelScenarios {
+		t.Run(scenario, func(t *testing.T) { testDesktopChannelShare(t, scenario) })
+	}
+}
+
+func testDesktopChannelShare(t *testing.T, scenario string) {
 	h := newCoreMCPHarness(t)
 	task := createHardwareAPITask(t, h.database)
 	provider := &desktopFixture{}
@@ -17,8 +23,10 @@ func TestDesktopMCPUsesExistingShareAndSingleUseObservation(t *testing.T) {
 	t.Cleanup(func() { _ = manager.Close() })
 	server := httptest.NewServer(New(Config{Store: h.database, App: h.service, AgentCapabilities: h.caps, Desktop: manager}).Handler())
 	t.Cleanup(server.Close)
-	turn := createCoreTurn(t, h.database, task, "main", "")
+	turn := createCoreTurn(t, h.database, task, "main", "batch-desktop-control")
+	seedControlChannel(t, h.database, task, turn, scenario)
 	token := h.issue(task.ID, "main", turn.ID)
+	assertControlCapabilities(t, server.URL, token, "desktop", true, true)
 	url := server.URL + "/api/v1/agent/desktop/mcp"
 	status, names, _ := coreMCPToolsList(t, url, token)
 	if status != http.StatusOK || len(names) != 5 || coreMCPNameIncluded(names, "desktop_open") {
@@ -78,23 +86,80 @@ func TestDesktopMCPUsesExistingShareAndSingleUseObservation(t *testing.T) {
 	if !denied.IsError || manager.Status(task.ID).Session == nil {
 		t.Fatal("other Task stopped current share")
 	}
+	assertControlHTTP(t, server.URL, token, http.MethodGet, "/desktop", nil, 200)
+	assertControlHTTP(t, server.URL, token, http.MethodPost, "/desktop/control", map[string]any{"session_id": session.ID, "revision": manager.Status(task.ID).Session.Revision, "controller": "agent"}, 200)
+	response := agentRequest(t, server.URL+"/api/v1/agent/desktop/observation?session_id="+session.ID, http.MethodGet, token, nil)
+	var httpObservation struct {
+		Observation desktop.Observation `json:"observation"`
+	}
+	decodeResponse(t, response, &httpObservation)
+	if response.StatusCode != 200 || httpObservation.Observation.ID == "" {
+		t.Fatal("HTTP observation failed")
+	}
+	httpArgs := map[string]any{"session_id": session.ID, "revision": httpObservation.Observation.Revision, "observation_id": httpObservation.Observation.ID, "kind": "click", "element_id": "$surface", "x": 10, "y": 10, "button": "left"}
+	assertControlHTTP(t, server.URL, token, http.MethodPost, "/desktop/actions", httpArgs, 200)
+	if provider.calls.Load() != 2 {
+		t.Fatal("HTTP action did not reach fixture exactly once")
+	}
 	stopped := coreMCPCallTool(t, url, token, "desktop_stop", map[string]any{"session_id": session.ID})
 	if stopped.IsError || manager.Status(task.ID).Session != nil {
 		t.Fatalf("stop=%#v", stopped)
 	}
 	denied = coreMCPCallTool(t, url, token, "desktop_action", args)
-	if !denied.IsError || provider.calls.Load() != 1 {
+	if !denied.IsError || provider.calls.Load() != 2 {
 		t.Fatal("revoked share reached provider")
 	}
+	assertControlHTTP(t, server.URL, token, http.MethodPost, "/desktop/actions", httpArgs, http.StatusConflict)
+	if provider.calls.Load() != 2 {
+		t.Fatal("revoked HTTP share reached provider")
+	}
+	// Owner stopping a newly shared session also invalidates the old token.
+	ownerShare, err := manager.OpenSharedTarget(h.ctx, task.ID, desktop.TargetSelection{Kind: "window", WindowID: "fixture"}, "foreground", true)
+	if err != nil {
+		t.Fatal(err)
+	}
+	manager.Revoke(task.ID)
+	denied = coreMCPCallTool(t, url, token, "desktop_control", map[string]any{"session_id": ownerShare.Session.ID, "revision": ownerShare.Session.Revision, "controller": "agent"})
+	if !denied.IsError || manager.Status(task.ID).Session != nil || provider.calls.Load() != 2 {
+		t.Fatal("owner-revoked share was restored")
+	}
+	assertControlHTTP(t, server.URL, token, http.MethodPost, "/desktop/control", map[string]any{"session_id": ownerShare.Session.ID, "revision": ownerShare.Session.Revision, "controller": "agent"}, http.StatusConflict)
 	readOnly := createCoreTaskInProject(t, h, task, "read-only")
 	readTurn := createCoreTurn(t, h.database, readOnly, "main", "")
+	seedControlChannel(t, h.database, readOnly, readTurn, "mixed_batch")
 	readToken := h.issue(readOnly.ID, "main", readTurn.ID)
 	_, names, _ = coreMCPToolsList(t, url, readToken)
 	if len(names) != 2 {
 		t.Fatalf("read-only tools=%v", names)
 	}
+	assertControlCapabilities(t, server.URL, readToken, "desktop", true, false)
+	assertControlHTTP(t, server.URL, readToken, http.MethodPost, "/desktop/control", map[string]any{"session_id": session.ID, "revision": 1, "controller": "agent"}, 403)
 	denied = coreMCPCallTool(t, url, readToken, "desktop_control", map[string]any{"session_id": session.ID, "revision": 1, "controller": "agent"})
 	if denied.Status != http.StatusForbidden {
 		t.Fatal("read-only Task gained control")
 	}
 }
+
+func TestDesktopChannelNonMainCannotControl(t *testing.T) {
+	h := newCoreMCPHarness(t)
+	task := createHardwareAPITask(t, h.database)
+	provider := &desktopFixture{}
+	manager := desktop.New(provider)
+	t.Cleanup(func() { _ = manager.Close() })
+	server := httptest.NewServer(New(Config{Store: h.database, App: h.service, AgentCapabilities: h.caps, Desktop: manager}).Handler())
+	t.Cleanup(server.Close)
+	shared, err := manager.OpenSharedTarget(h.ctx, task.ID, desktop.TargetSelection{Kind: "window", WindowID: "fixture"}, "foreground", true)
+	if err != nil {
+		t.Fatal(err)
+	}
+	turn := createCoreTurn(t, h.database, task, "sub", "batch-desktop-sub")
+	seedControlChannel(t, h.database, task, turn, "mixed_batch")
+	token := h.issue(task.ID, "sub", turn.ID)
+	args := map[string]any{"session_id": shared.Session.ID, "revision": shared.Session.Revision, "controller": "agent"}
+	before := manager.Status(task.ID).Session.Revision
+	assertControlHTTP(t, server.URL, token, http.MethodPost, "/desktop/control", args, 403)
+	result := coreMCPCallTool(t, server.URL+"/api/v1/agent/desktop/mcp", token, "desktop_control", args)
+	if result.Status != 403 || provider.calls.Load() != 0 || manager.Status(task.ID).Session.Revision != before {
+		t.Fatal("non-Main control had effects")
+	}
+}
diff --git a/internal/httpapi/agent_hardware_gate_test.go b/internal/httpapi/agent_hardware_gate_test.go
index 2867618..b3e8524 100644
--- a/internal/httpapi/agent_hardware_gate_test.go
+++ b/internal/httpapi/agent_hardware_gate_test.go
@@ -57,6 +57,10 @@ func TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects(t *testing.T
 						t.Fatal(err)
 					}
 				}
+				if err := f.h.database.ReplaceTaskHardwareBindings(f.h.ctx, f.task.ID, []domain.TaskHardwareBinding{{TaskID: f.task.ID, ID: "board", ResourceID: f.board.ID}, {TaskID: f.task.ID, ID: "device", ResourceID: f.adb.ID}}); err != nil {
+					t.Fatal(err)
+				}
+				seedKnowledgeMCPInbox(t, f.h.dbPath, f.task, f.turn, "channel:task_route")
 				f.token = f.h.issue(f.task.ID, "main", f.turn.ID)
 				if pet {
 					assistant, _ := f.h.database.EnsureOwnerAssistant(f.h.ctx, time.Now())
@@ -65,7 +69,7 @@ func TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects(t *testing.T
 					}
 				}
 			}
-			for _, scenario := range []string{"non_main", "read_only_snapshot", "read_only_resource", "channel_turn", "terminal_task", "revoked_pet"} {
+			for _, scenario := range []string{"non_main", "read_only_snapshot", "read_only_resource", "revoked_binding", "terminal_task", "revoked_pet"} {
 				if scenario == "revoked_pet" && !pet {
 					continue
 				}
@@ -93,13 +97,19 @@ func TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects(t *testing.T
 						}
 						assistant, _ := f.h.database.EnsureOwnerAssistant(f.h.ctx, time.Now())
 						err = f.h.database.UpdateOwnerPetPermissions(f.h.ctx, assistant.ID, false, false, false, false, false, false, false, false, time.Now())
-					case "channel_turn":
-						payload := `{"channel_context":{"schema":"aha2.channel.v1","instance_id":"fixture","endpoint":"group_digital_human","conversation_id":"fixture","route":{"mode":"task_route"}}}`
-						_, err = raw.Exec(`INSERT INTO agent_inbox(id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,content,payload_json,status,batch_id,created_at,claimed_at,processed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, domain.NewID("inbox"), f.task.ID, f.turn.RoundID, "main", "channel", "channel_message", "", "", "fixture", payload, "claimed", f.turn.InboxBatchID, time.Now().Format(time.RFC3339Nano), "", "")
+					case "revoked_binding":
+						err = f.h.database.ReplaceTaskHardwareBindings(f.h.ctx, f.task.ID, nil)
 					}
 					if err != nil {
 						t.Fatal(err)
 					}
+					if scenario == "read_only_snapshot" || scenario == "non_main" {
+						assertControlCapabilities(t, f.server.URL, f.token, "hardware", true, false)
+						status, names, _ := coreMCPToolsList(t, f.server.URL+"/api/v1/agent/hardware/mcp", f.token)
+						if status != 200 || len(names) != 2 {
+							t.Fatalf("read-only tools=%d %v", status, names)
+						}
+					}
 					beforeAccepts, beforeExec := f.accepts.Load(), f.executions.Load()
 					for _, op := range []struct {
 						name, path string
@@ -129,7 +139,7 @@ func TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects(t *testing.T
 					if f.accepts.Load() != beforeAccepts || f.executions.Load() != beforeExec || !f.manager.Status(f.task.ID, "board", "network", false).Connected {
 						t.Fatal("denied operation changed transport state")
 					}
-					if scenario == "channel_turn" || scenario == "revoked_pet" {
+					if scenario == "revoked_pet" {
 						if status := f.raw(t, http.MethodGet, "", nil); status != 403 {
 							t.Fatalf("HTTP read after revocation status=%d", status)
 						}
diff --git a/internal/httpapi/agent_hardware_mcp_test.go b/internal/httpapi/agent_hardware_mcp_test.go
index cd713b2..fc88274 100644
--- a/internal/httpapi/agent_hardware_mcp_test.go
+++ b/internal/httpapi/agent_hardware_mcp_test.go
@@ -439,7 +439,7 @@ func TestAgentHardwareMCPTerminalReadEnforcesResponseBudget(t *testing.T) {
 	}
 }
 
-func TestAgentHardwareMCPRejectsChannelBoundTurns(t *testing.T) {
+func TestAgentHardwareMCPChannelInputKeepsBinding(t *testing.T) {
 	f := newHardwareMCPFixture(t)
 	url := hardwareMCPURL(f.h)
 	retireCoreTurn(t, f.h.database, f.turn)
@@ -466,9 +466,9 @@ func TestAgentHardwareMCPRejectsChannelBoundTurns(t *testing.T) {
 		response.Body.Close()
 		retireCoreTurn(t, f.h.database, turn)
 	}
-	assertStatus("channel batch", "batch-hardware-mcp-channel", http.StatusForbidden)
+	assertStatus("channel batch", "batch-hardware-mcp-channel", http.StatusOK)
 	assertStatus("plain batch", "batch-hardware-mcp-plain", http.StatusOK)
-	assertStatus("malformed batch", "batch-hardware-mcp-broken", http.StatusForbidden)
+	assertStatus("malformed batch", "batch-hardware-mcp-broken", http.StatusOK)
 }
 
 type hardwareMCPTerminalPayload struct {
diff --git a/internal/httpapi/agent_pet_channel_authorization_test.go b/internal/httpapi/agent_pet_channel_authorization_test.go
index 45ae7c6..e2fea4e 100644
--- a/internal/httpapi/agent_pet_channel_authorization_test.go
+++ b/internal/httpapi/agent_pet_channel_authorization_test.go
@@ -13,7 +13,7 @@ import (
 	"github.com/ChinaKai/AHA2/internal/domain"
 )
 
-func TestPetChannelTurnRejectsDomainDiscoveryAndDirectHTTPCalls(t *testing.T) {
+func TestPetChannelTurnKeepsResourceGrantsAndUnrelatedDomainRestrictions(t *testing.T) {
 	f := newHardwareAuthorizationFixture(t, true)
 	seededBrowserGrant(t, f.h.database, f.task, "pet-channel-http", "active")
 	manager := desktop.New(&desktopFixture{})
@@ -39,11 +39,15 @@ func TestPetChannelTurnRejectsDomainDiscoveryAndDirectHTTPCalls(t *testing.T) {
 	for name, tool := range tools {
 		url := server.URL + "/api/v1/agent/" + name + "/mcp"
 		status, _, _ := coreMCPToolsList(t, url, f.token)
-		if status != 403 {
+		want := http.StatusOK
+		if name == "knowledge" || name == "workspace" {
+			want = http.StatusForbidden
+		}
+		if status != want {
 			t.Fatalf("channel pet discovers %s: %d", name, status)
 		}
 		result := coreMCPCallTool(t, url, f.token, tool, map[string]any{})
-		if result.Status != 403 {
+		if result.Status != want {
 			t.Fatalf("channel pet calls %s: %d", name, result.Status)
 		}
 	}
@@ -51,7 +55,11 @@ func TestPetChannelTurnRejectsDomainDiscoveryAndDirectHTTPCalls(t *testing.T) {
 		response := agentRequest(t, server.URL+"/api/v1/agent"+path, http.MethodGet, f.token, nil)
 		status := response.StatusCode
 		response.Body.Close()
-		if status != 403 {
+		want := http.StatusOK
+		if path == "/processes" {
+			want = http.StatusForbidden
+		}
+		if status != want {
 			t.Fatalf("channel pet direct %s status=%d", path, status)
 		}
 	}
```
