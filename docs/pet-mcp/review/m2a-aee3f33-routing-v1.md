# M2-a：固定 aee3f33 增量复审

源码 `aee3f33e926e1f6345810a5de0f8d1dda77e3b51`，tree `c54bd022f66e9c5900a046d4397284a306e778bf`。本轮完整增量 `7eab7ce74630e742d41e066af25a5e92e2234d6e..aee3f33e926e1f6345810a5de0f8d1dda77e3b51` 共7文件；7eab7ce仅是9bca378后的旧材料存档，不把P1b/P1c或未审P2吞成已通过。本轮不改已获有限APPROVE的r1/r3a/r4/r5/r6源码；M5与P1c仍阻塞，完整Task未完成。所有摘录与diff都从固定提交取得，门禁diff保留3行上下文。新材料先在scratch完整生成与校验，再原子写入版本路径、归档后回报ready；此前所有冻结包不改写。

固定副本6个具名顶层测试逐个核对run/pass，遗漏0，退出0（67.042s）；`go vet ./internal/httpapi`退出0（1.129s）。摘要及完整命令为同版本 `m2a-aee3f33-validation-v1.md`。N3仍未完成：固定9bca378的app全包40m累计超时退出1（359个测试pass事件、0个单用例fail事件，不能当通过）；其httpapi仍在运行。新aee3f33全包尚无退出码。

## 精确注册策略与真实HTTP回归

完整diff文件边界：

- `internal/httpapi/agent_domain_authorization.go`
- `internal/httpapi/helpers.go`
- `internal/httpapi/agent_token_route_policy.go`
- `internal/httpapi/agent_token_route_policy_test.go`
- `internal/httpapi/agent_route_opaque_resource_test.go`
- `internal/httpapi/attachment_handlers_test.go`

不再按客户端URL前缀/后缀决定豁免。策略键来自ServeMux选中的 method/template，82个接受Agent token的入口逐个声明域、访问类别和权限执行方。只有表中精确POST MCP入口且实际escaped path一致，才交给真实MCP handler做工具授权。普通`{name}`/`{grant}`即使叫mcp或包含编码斜线，也按普通资源策略检查；未登记的新token路由默认403，两个认证包装器都执行该检查。普通业务API保留各自已有授权，不因这轮修复重构知识/邮件/渠道权限；唯一双鉴权资源是Task附件，保留owner cookie入口和当前Task协作者读取，同时核对真实活动Turn与handler的Task归属。

回归通过真正的Server.Handler与实际受保护handler执行：同token撤权后GET processes/mcp、processes/a%2Fmcp及DELETE browser/grants/mcp全部403。托管进程mcp真实存在；授权浏览器grant通过真实owner cookie/CSRF创建，只在隔离DB把不透明ID改为mcp。额外真实HTTP mux仅装计数装饰器并委托原完整handler，验证拒绝在dispatch前发生；它不替代主Server.Handler回归。断言进程完整状态、grant行、host fence、下载清理计数和handler计数均不变；恢复授权后进程可读且grant释放产生真实业务效果。精确MCP入口和双鉴权附件原合法范围也回归。
## 必要调用链源码

### `internal/app/agent_domain_authorization.go` · `AgentDomainCallContext`

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
	readAllowed := s.petDomainReadable(ctx, call.Task) && !s.agentMCPChannelBound(ctx, call.Task.ID, call.Turn.InboxBatchID)
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

### `internal/app/agent_control.go` · `AgentCallContext`

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

### `internal/app/agent_core.go` · `agentSnapshotWritable`

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
```

### `internal/app/agent_core.go` · `strictSnapshotWritable`

```go
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

### `internal/app/agent_hardware_mcp.go` · `AgentHardwareMCPAllowed`

```go
func (s *Service) AgentHardwareMCPAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.hardwareMCPInjected(ctx, call.Task, call.Turn.InboxBatchID)
}
```

### `internal/app/agent_hardware_mcp.go` · `AgentHardwareMCPWriteAllowed`

```go
func (s *Service) AgentHardwareMCPWriteAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.AgentHardwareMCPAllowed(ctx, call) && s.hardwareMCPSnapshotWritable(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
}
```

### `internal/app/agent_hardware_mcp.go` · `hardwareMCPSnapshotWritable`

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

### `internal/app/agent_pet_domains.go` · `petDomainReadable`

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

### `internal/app/agent_knowledge_mcp.go` · `agentMCPChannelBound`

```go
func (s *Service) agentMCPChannelBound(ctx context.Context, taskID, inboxBatchID string) bool {
	if batchID := strings.TrimSpace(inboxBatchID); batchID != "" {
		hasChannel, err := s.store.InboxBatchHasChannelContext(ctx, batchID)
		if err != nil {
			// The batch cannot be classified (store failure or a malformed
			// payload); treat it as channel-bound rather than as plain.
			return true
		}
		if hasChannel {
			return true
		}
	}
	routed, err := s.store.HasActiveChannelTaskRoute(ctx, taskID)
	if err != nil {
		return true
	}
	return routed
}
```

### `internal/app/mcp_runtime.go` · `hardwareMCPInjected`

```go
func (s *Service) hardwareMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if !s.petDomainReadable(ctx, task) {
		return false
	}
	if s.agentMCPChannelBound(ctx, task.ID, inboxBatchID) {
		return false
	}
	groups, err := s.store.HardwareGroups(ctx, task.ID)
	return err == nil && len(groups) > 0
}
```

### `internal/httpapi/helpers.go` · `withAgentCapability`

```go
func (s *Server) withAgentCapability(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if s.agentCapabilities == nil {
			writeError(writer, http.StatusServiceUnavailable, "agent_api_unavailable")
			return
		}
		scheme, token, ok := strings.Cut(strings.TrimSpace(request.Header.Get("Authorization")), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			writeError(writer, http.StatusUnauthorized, "agent_capability_required")
			return
		}
		claims, err := s.agentCapabilities.Authenticate(strings.TrimSpace(token))
		if err != nil {
			writeError(writer, http.StatusUnauthorized, "agent_capability_invalid")
			return
		}
		ctx := context.WithValue(request.Context(), agentClaimsContextKey{}, claims)
		request = request.WithContext(ctx)
		if !s.authorizeAgentDomainRoute(writer, request, claims) {
			return
		}
		next.ServeHTTP(writer, request)
	})
}
```

### `internal/httpapi/helpers.go` · `withOwnerOrAgentAuth`

```go
func (s *Server) withOwnerOrAgentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		scheme, token, ok := strings.Cut(strings.TrimSpace(request.Header.Get("Authorization")), " ")
		if ok && strings.EqualFold(scheme, "Bearer") && s.agentCapabilities != nil {
			if claims, err := s.agentCapabilities.Authenticate(strings.TrimSpace(token)); err == nil {
				ctx := context.WithValue(request.Context(), agentClaimsContextKey{}, claims)
				request = request.WithContext(ctx)
				if !s.authorizeAgentDomainRoute(writer, request, claims) {
					return
				}
				next.ServeHTTP(writer, request)
				return
			}
		}
		// No usable capability: fall back to the Owner session. A request with
		// neither credential, or with only an invalid one, gets 401 from withAuth.
		s.withAuth(next).ServeHTTP(writer, request)
	})
}
```

### `internal/httpapi/agent_state_handlers.go` · `writeAgentControlError`

```go
func writeAgentControlError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrAgentCallForbidden):
		writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
	case errors.Is(err, app.ErrGlobalSkillForbidden):
		writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "global_skill_forbidden", "message": "全局 Skill 会影响本机所有 Project，只能在 Main 且非受限群聊路由的 Turn 中创建；请改用 scope=\"project\""})
	case errors.Is(err, app.ErrKnowledgeReadOnly):
		writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "knowledge_entry_read_only", "message": "该知识来自外部引用型绑定；请切换为项目协作型，或在来源 Project 中提交修订"})
	case errors.Is(err, app.ErrKnowledgeReadSourceMismatch):
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "knowledge_read_source_mismatch", "message": err.Error()})
	case errors.Is(err, app.ErrAgentTurnInactive):
		writeError(writer, http.StatusConflict, "agent_turn_inactive")
	case errors.Is(err, app.ErrAgentCollaborationRequiresAuto):
		writeJSON(writer, http.StatusForbidden, map[string]any{
			"ok": false, "error": "collaboration_requires_auto_mode",
			"message": "单 Agent 模式没有可派发的子 Agent；请由 Owner 切换到自动协作模式",
		})
	case errors.Is(err, app.ErrAgentLimitReached):
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "agent_limit_reached",
			"message": "已达到该 Task 的 Agent 数量上限；请调整 Max Agents 或复用已有 Agent",
		})
	case errors.Is(err, app.ErrRevisionConflict), errors.Is(err, store.ErrKnowledgeProposalRevision), errors.Is(err, store.ErrKnowledgeProposalPending):
		writeError(writer, http.StatusConflict, "knowledge_revision_conflict")
	case errors.Is(err, store.ErrKnowledgeFeedbackState):
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "knowledge_feedback_conflict", "message": "该 revision 已标记为过时或错误，请提交修订候选，或由 Owner 确认当前内容仍然有效"})
	default:
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "agent_operation_failed", "message": err.Error()})
	}
}
```

### `internal/httpapi/agent_domain_authorization.go` · `authorizeAgentDomainRoute`

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

### `internal/httpapi/agent_handlers.go` · `agentProcessStatus`

```go
func (s *Server) agentProcessStatus(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	if s.rejectChannelManagedProcess(writer, request, claims.TurnID) {
		return
	}
	if s.managedProcesses == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_processes_unavailable")
		return
	}
	status, err := s.managedProcesses.Status(claims.TaskID, request.PathValue("name"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "managed_process_not_found")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "process": status})
}
```

### `internal/httpapi/browser_handlers.go` · `agentBrowserRelease`

```go
func (s *Server) agentBrowserRelease(writer http.ResponseWriter, request *http.Request) {
	service, grant, ok := s.agentBrowserGrantTarget(writer, request)
	if !ok {
		return
	}
	released, err := service.RevokeGrant(request.Context(), browserActor(request), grant.ID)
	if err != nil {
		writeBrowserFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "grant": released})
}
```

### `internal/httpapi/agent_core_mcp.go` · `agentCoreMCP`

```go
func (s *Server) agentCoreMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_core_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	// Read the whole request before dispatch. Decoding straight from the body
	// stops after the first JSON value, which would leave trailing whitespace
	// or a second value beyond the 1 MiB budget unexamined.
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, agentCoreMCPMaxRequestBytes))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	// A JSON-RPC request is one object. Reject an empty body, null, arrays and
	// primitives before decoding; json.Unmarshal accepts exactly one value plus
	// surrounding whitespace, so a second value or trailing garbage also fails.
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	var payload mcpRequest
	if json.Unmarshal(body, &payload) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	call, err := s.app.AgentCallContext(request.Context(), claims, false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	respond := func(result any) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID, "result": result,
		})
	}
	respondError := func(code int, message string) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": code, "message": message},
		})
	}
	switch payload.Method {
	case "initialize":
		respond(map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "aha2-core", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		// JSON-RPC notifications have no response body. The capability and Turn
		// were already validated above.
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		respond(map[string]any{"tools": agentCoreMCPToolsNamed(s.app.AgentCoreMCPToolNames(request.Context(), call))})
	case "tools/call":
		s.callAgentCoreMCPTool(writer, request, claims, call, payload)
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		respondError(-32601, "method not found")
	}
}
```

### `internal/httpapi/attachment_handlers.go` · `taskAttachmentContent`

```go
func (s *Server) taskAttachmentContent(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	attachmentID := request.PathValue("attachment")
	claims, agentRequest := agentClaimsFromContext(request.Context())
	// A Task capability may only read its own Task's attachments. Returning
	// 404 for a foreign Task keeps the endpoint from confirming that another
	// Task's attachment exists.
	if agentRequest && claims.TaskID != taskID {
		s.audit(request, "agent.attachment.download", "attachment", attachmentID, map[string]any{
			"task_id": taskID, "capability_task_id": claims.TaskID, "result": "denied_cross_task",
		})
		writeError(writer, http.StatusNotFound, "attachment_not_found")
		return
	}
	// Attachment lookup is scoped to taskID, so a missing attachment and an
	// attachment owned by another Task are both a 404.
	item, err := s.store.Attachment(request.Context(), taskID, attachmentID)
	if err != nil {
		if agentRequest {
			s.audit(request, "agent.attachment.download", "attachment", attachmentID, map[string]any{
				"task_id": taskID, "result": "not_found",
			})
		}
		writeError(writer, http.StatusNotFound, "attachment_not_found")
		return
	}
	// Stream from the blob instead of loading it into memory; the content is
	// not subject to the 4MiB inline limit.
	file, err := s.store.OpenAttachmentContent(item)
	if err != nil {
		if agentRequest {
			s.audit(request, "agent.attachment.download", "attachment", attachmentID, map[string]any{
				"task_id": taskID, "result": "content_not_found",
			})
		}
		writeError(writer, http.StatusNotFound, "attachment_content_not_found")
		return
	}
	defer file.Close()
	if agentRequest {
		s.audit(request, "agent.attachment.download", "attachment", item.ID, map[string]any{
			"task_id": item.TaskID, "result": "ok",
		})
	}
	disposition := "attachment"
	switch item.MediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		disposition = "inline"
	}
	writer.Header().Set("Content-Type", item.MediaType)
	writer.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": item.Name}))
	writer.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(writer, request, item.Name, item.CreatedAt, file)
}
```

## 该边界完整diff

```diff
diff --git a/internal/httpapi/agent_domain_authorization.go b/internal/httpapi/agent_domain_authorization.go
index b1893a2..d682a34 100644
--- a/internal/httpapi/agent_domain_authorization.go
+++ b/internal/httpapi/agent_domain_authorization.go
@@ -7,29 +7,66 @@ import (
 	"github.com/ChinaKai/AHA2/internal/agentapi"
 )
 
-// authorizeAgentDomainRoute runs after token authentication. Its finite path
-// families are the existing Agent APIs, never owner Web routes. MCP dispatch
-// determines read/write from the named tool and uses the same app boundary.
+type agentRouteMode uint8
+
+const (
+	agentRouteBusiness agentRouteMode = iota + 1
+	agentRouteResource
+	agentRouteMCP
+)
+
+type agentRouteAccess uint8
+
+const (
+	agentRouteRead agentRouteAccess = iota + 1
+	agentRouteWrite
+	agentRouteBookkeeping
+	agentRouteTools
+)
+
+type agentTokenRoutePolicy struct {
+	Domain string
+	Access agentRouteAccess
+	Mode   agentRouteMode
+}
+
+// authorizeAgentDomainRoute uses the pattern chosen by the server's ServeMux.
+// An opaque resource name (including an encoded slash) cannot select a policy.
+// Business-scoped endpoints retain their existing per-operation authorization;
+// shared resources always pass the common domain gate. Unknown routes fail shut.
 func (s *Server) authorizeAgentDomainRoute(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims) bool {
-	path := request.URL.Path
-	if strings.HasSuffix(path, "/mcp") {
-		return true
+	policy, known := agentTokenRoutePolicies[request.Pattern]
+	if !known {
+		writeError(writer, http.StatusForbidden, "agent_route_policy_missing")
+		return false
 	}
-	domainName := ""
-	for prefix, name := range map[string]string{"/api/v1/agent/hardware": "hardware", "/api/v1/agent/browser": "browser", "/api/v1/agent/processes": "workspace", "/api/v1/agent/project/workspaces": "workspace", "/api/v1/agent/project/runtimes": "workspace", "/api/v1/agent/desktop": "desktop"} {
-		if path == prefix || strings.HasPrefix(path, prefix+"/") {
-			domainName = name
-			break
+	if policy.Mode == agentRouteMCP {
+		method, path, valid := strings.Cut(request.Pattern, " ")
+		if !valid || method != http.MethodPost || request.Method != http.MethodPost || request.URL.EscapedPath() != path {
+			writeError(writer, http.StatusForbidden, "agent_route_policy_mismatch")
+			return false
 		}
+		return true // The exact registered MCP handler checks the named tool.
 	}
-	if domainName == "" {
+	if policy.Mode == agentRouteBusiness && policy.Domain != "attachment" {
 		return true
 	}
+	if policy.Mode != agentRouteResource && !(policy.Mode == agentRouteBusiness && policy.Domain == "attachment") {
+		writeError(writer, http.StatusForbidden, "agent_route_policy_invalid")
+		return false
+	}
 	if s.app == nil {
 		writeError(writer, http.StatusServiceUnavailable, "agent_api_unavailable")
 		return false
 	}
-	_, err := s.app.AgentDomainCallContext(request.Context(), claims, domainName, request.Method != http.MethodGet)
+	var err error
+	if policy.Domain == "attachment" {
+		// This dual-auth resource keeps ordinary collaborators' own-Task reads,
+		// but an expired Turn cannot retain data access through a live token.
+		_, err = s.app.AgentCallContext(request.Context(), claims, false)
+	} else {
+		_, err = s.app.AgentDomainCallContext(request.Context(), claims, policy.Domain, policy.Access == agentRouteWrite)
+	}
 	if err != nil {
 		writeAgentControlError(writer, err)
 		return false
diff --git a/internal/httpapi/agent_route_opaque_resource_test.go b/internal/httpapi/agent_route_opaque_resource_test.go
new file mode 100644
index 0000000..30f8803
--- /dev/null
+++ b/internal/httpapi/agent_route_opaque_resource_test.go
@@ -0,0 +1,186 @@
+package httpapi
+
+import (
+	"database/sql"
+	"net/http"
+	"net/http/cookiejar"
+	"net/http/httptest"
+	"os"
+	"reflect"
+	"strings"
+	"sync/atomic"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/auth"
+	"github.com/ChinaKai/AHA2/internal/browser"
+	"github.com/ChinaKai/AHA2/internal/browser/browsertest"
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+func TestAgentRouteLongProcessFixture(t *testing.T) {
+	if os.Getenv("AHA_MCP_ROUTE_PROCESS_FIXTURE") == "1" {
+		<-time.After(5 * time.Minute)
+	}
+}
+
+func TestOpaqueMCPResourceNamesCannotSkipLiveAuthorization(t *testing.T) {
+	h := newCoreMCPHarness(t)
+	base := createHardwareAPITask(t, h.database)
+	task := createPetDomainHTTPTask(t, h, base)
+	now := time.Now().UTC().Format(time.RFC3339Nano)
+	if task.Status == domain.TaskDraft {
+		if err := h.database.UpdateTaskStatus(h.ctx, task.ID, domain.TaskDraft, domain.TaskPreparing, now, ""); err != nil {
+			t.Fatal(err)
+		}
+		if err := h.database.UpdateTaskStatus(h.ctx, task.ID, domain.TaskPreparing, domain.TaskActive, now, ""); err != nil {
+			t.Fatal(err)
+		}
+	}
+	turn := createCoreTurn(t, h.database, task, "main", domain.NewID("inbox_batch"))
+	token := h.issue(task.ID, "main", turn.ID)
+	host := browsertest.New()
+	service := browser.NewService(h.database, host, "local")
+	server := New(Config{Store: h.database, App: h.service, Auth: auth.NewService(h.database, "setup-test", time.Hour), AgentCapabilities: h.caps, ManagedProcesses: h.processes, Browser: service})
+	actual := httptest.NewServer(server.Handler())
+	t.Cleanup(actual.Close)
+	jar, _ := cookiejar.New(nil)
+	owner := &http.Client{Jar: jar}
+	csrf := registerOwner(t, owner, actual.URL)
+	response := requestJSON(t, owner, http.MethodPost, actual.URL+"/api/v1/browser/profiles", map[string]any{"name": "Opaque route fixture"}, csrf)
+	var profile struct {
+		Profile struct {
+			ID string `json:"id"`
+		} `json:"profile"`
+	}
+	decodeResponse(t, response, &profile)
+	if response.StatusCode != http.StatusCreated {
+		t.Fatal("owner fixture profile failed")
+	}
+	response = requestJSON(t, owner, http.MethodPost, actual.URL+"/api/v1/tasks/"+task.ID+"/browser-grants", map[string]any{"profile_id": profile.Profile.ID}, csrf)
+	var granted struct {
+		Grant struct {
+			ID string `json:"id"`
+		} `json:"grant"`
+	}
+	decodeResponse(t, response, &granted)
+	if response.StatusCode != http.StatusCreated {
+		t.Fatal("owner fixture grant failed")
+	}
+	// Use a real service-created session and grant. Only its opaque identifier
+	// is changed in the isolated fixture to exercise the colliding route leaf.
+	raw, err := sql.Open("sqlite", h.dbPath)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer raw.Close()
+	if _, err := raw.Exec(`UPDATE browser_task_grants SET id='mcp' WHERE id=?`, granted.Grant.ID); err != nil {
+		t.Fatal("opaque grant identifier fixture failed")
+	}
+	executable, err := os.Executable()
+	if err != nil {
+		t.Fatal(err)
+	}
+	response = agentRequest(t, actual.URL+"/api/v1/agent/processes", http.MethodPost, token, map[string]any{"name": "mcp", "executable": executable, "args": []string{"-test.run=^TestAgentRouteLongProcessFixture$"}, "env": map[string]string{"AHA_MCP_ROUTE_PROCESS_FIXTURE": "1"}})
+	if response.StatusCode != http.StatusAccepted {
+		response.Body.Close()
+		t.Fatal("authorized process fixture failed")
+	}
+	response.Body.Close()
+	beforeProcess, err := h.processes.Status(task.ID, "mcp")
+	if err != nil || beforeProcess.State != "running" {
+		t.Fatal("real managed process is not running")
+	}
+	var handlerCalls atomic.Int32
+	observe := func(next http.HandlerFunc) http.HandlerFunc {
+		return func(w http.ResponseWriter, r *http.Request) { handlerCalls.Add(1); next(w, r) }
+	}
+	// This second real HTTP mux instruments dispatch after the same production
+	// authenticator, and delegates to the complete production handlers. The
+	// actual Server.Handler is exercised for every attack path as well.
+	mux := http.NewServeMux()
+	mux.Handle("GET /api/v1/agent/processes/{name}", server.withAgentCapability(observe(server.agentProcessStatus)))
+	mux.Handle("DELETE /api/v1/agent/browser/grants/{grant}", server.withAgentCapability(observe(server.agentBrowserRelease)))
+	observed := httptest.NewServer(mux)
+	t.Cleanup(observed.Close)
+	status := func(url, method string) int {
+		t.Helper()
+		response := agentRequest(t, url, method, token, nil)
+		defer response.Body.Close()
+		return response.StatusCode
+	}
+	if status(actual.URL+"/api/v1/agent/processes/mcp", http.MethodGet) != 200 || status(observed.URL+"/api/v1/agent/processes/mcp", http.MethodGet) != 200 || handlerCalls.Load() != 1 {
+		t.Fatal("authorized opaque process route regressed")
+	}
+	if s, _, _ := coreMCPToolsList(t, actual.URL+"/api/v1/agent/core/mcp", token); s != 200 {
+		t.Fatal("real MCP endpoint regressed before revocation")
+	}
+	beforeGrant, err := h.database.BrowserTaskGrant(h.ctx, "mcp")
+	if err != nil || !beforeGrant.RevokedAt.IsZero() {
+		t.Fatal("real grant fixture is not active")
+	}
+	beforeFence, beforeClear := host.Fences[profile.Profile.ID], host.ClearDownload
+	assistant, _ := h.database.EnsureOwnerAssistant(h.ctx, time.Now())
+	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, false, false, false, false, false, false, false, false, time.Now()); err != nil {
+		t.Fatal(err)
+	}
+	beforeCalls := handlerCalls.Load()
+	for _, entry := range []struct{ method, path string }{{http.MethodGet, "/api/v1/agent/processes/mcp"}, {http.MethodGet, "/api/v1/agent/processes/a%2Fmcp"}, {http.MethodDelete, "/api/v1/agent/browser/grants/mcp"}} {
+		for _, root := range []string{actual.URL, observed.URL} {
+			if got := status(root+entry.path, entry.method); got != http.StatusForbidden {
+				t.Fatalf("revoked opaque route %s %s returned %d", entry.method, entry.path, got)
+			}
+		}
+	}
+	afterProcess, err := h.processes.Status(task.ID, "mcp")
+	if err != nil || !reflect.DeepEqual(beforeProcess, afterProcess) {
+		t.Fatal("denied requests changed the managed process")
+	}
+	afterGrant, err := h.database.BrowserTaskGrant(h.ctx, "mcp")
+	if err != nil || !reflect.DeepEqual(beforeGrant, afterGrant) || host.Fences[profile.Profile.ID] != beforeFence || host.ClearDownload != beforeClear || handlerCalls.Load() != beforeCalls {
+		t.Fatal("denied requests reached a handler or changed browser authorization")
+	}
+	if s, _, _ := coreMCPToolsList(t, actual.URL+"/api/v1/agent/core/mcp", token); s != 200 {
+		t.Fatal("exact MCP entry was confused with a resource suffix")
+	}
+	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, true, true, false, false, false, false, false, false, time.Now()); err != nil {
+		t.Fatal(err)
+	}
+	if status(actual.URL+"/api/v1/agent/processes/mcp", http.MethodGet) != 200 {
+		t.Fatal("authorization restoration did not restore legal process access")
+	}
+	if status(actual.URL+"/api/v1/agent/browser/grants/mcp", http.MethodDelete) != 200 {
+		t.Fatal("legal opaque grant release regressed")
+	}
+	released, err := h.database.BrowserTaskGrant(h.ctx, "mcp")
+	if err != nil || released.RevokedAt.IsZero() || host.Fences[profile.Profile.ID] != "released:mcp" || host.ClearDownload <= beforeClear {
+		t.Fatal("authorized release did not produce its real business effects")
+	}
+}
+
+func TestUnclassifiedTokenRoutesFailBeforeEitherAuthWrapperDispatches(t *testing.T) {
+	h := newCoreMCPHarness(t)
+	task := createHardwareAPITask(t, h.database)
+	turn := createCoreTurn(t, h.database, task, "main", domain.NewID("inbox_batch"))
+	token := h.issue(task.ID, "main", turn.ID)
+	server := New(Config{Store: h.database, App: h.service, AgentCapabilities: h.caps})
+	var calls atomic.Int32
+	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusNoContent) })
+	mux := http.NewServeMux()
+	mux.Handle("GET /api/v1/agent/unclassified/{id}", server.withAgentCapability(next))
+	mux.Handle("GET /api/v1/unclassified-dual/{id}", server.withOwnerOrAgentAuth(next))
+	actual := httptest.NewServer(mux)
+	defer actual.Close()
+	for _, path := range []string{"/api/v1/agent/unclassified/mcp", "/api/v1/agent/unclassified/a%2Fmcp", "/api/v1/unclassified-dual/mcp"} {
+		response := agentRequest(t, actual.URL+path, http.MethodGet, token, nil)
+		code := response.StatusCode
+		body := readBody(t, response)
+		response.Body.Close()
+		if code != 403 || !strings.Contains(body, "agent_route_policy_missing") {
+			t.Fatal("unclassified token route was not closed")
+		}
+	}
+	if calls.Load() != 0 {
+		t.Fatal("unknown policy reached the handler")
+	}
+}
diff --git a/internal/httpapi/agent_token_route_policy.go b/internal/httpapi/agent_token_route_policy.go
new file mode 100644
index 0000000..070d242
--- /dev/null
+++ b/internal/httpapi/agent_token_route_policy.go
@@ -0,0 +1,90 @@
+package httpapi
+
+// agentTokenRoutePolicies is a closed registry of authenticated method/template
+// pairs. Keys are ServeMux-selected patterns, never client path suffixes. New
+// token routes must be classified explicitly; the registration test checks this
+// table against both authentication wrappers and the desktop route tables.
+var agentTokenRoutePolicies = map[string]agentTokenRoutePolicy{
+	"DELETE /api/v1/agent/browser/grants/{grant}":                   {Domain: "browser", Access: agentRouteWrite, Mode: agentRouteResource},
+	"DELETE /api/v1/agent/browser/grants/{grant}/tabs/{tab}":        {Domain: "browser", Access: agentRouteWrite, Mode: agentRouteResource},
+	"GET /api/v1/agent/browser":                                     {Domain: "browser", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/browser/grants/{grant}/bookmarks":            {Domain: "browser", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/browser/grants/{grant}/cookies":              {Domain: "browser", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/browser/grants/{grant}/downloads":            {Domain: "browser", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/browser/grants/{grant}/downloads/{download}": {Domain: "browser", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/browser/grants/{grant}/tabs":                 {Domain: "browser", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/frame":     {Domain: "browser", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/snapshot":  {Domain: "browser", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/capabilities":                                {Domain: "turn", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/channel/catalog":                             {Domain: "channel", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/channel/contacts":                            {Domain: "channel", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/channel/context":                             {Domain: "channel", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/desktop":                                     {Domain: "desktop", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/desktop/observation":                         {Domain: "desktop", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/hardware":                                    {Domain: "hardware", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/hardware/{hardware}/terminal":                {Domain: "hardware", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/knowledge":                                   {Domain: "knowledge", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/knowledge/guide":                             {Domain: "knowledge", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/knowledge/{knowledge}":                       {Domain: "knowledge", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/mail/accounts":                               {Domain: "mail", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/mail/messages/{id}":                          {Domain: "mail", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/mail/templates":                              {Domain: "mail", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/mail/templates/{id}":                         {Domain: "mail", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/processes":                                   {Domain: "workspace", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/processes/{name}":                            {Domain: "workspace", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/project/runtimes":                            {Domain: "workspace", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/project/workspaces":                          {Domain: "workspace", Access: agentRouteRead, Mode: agentRouteResource},
+	"GET /api/v1/agent/skills":                                      {Domain: "skill", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/skills/{skill}":                              {Domain: "skill", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/agent/tasks/{task}":                                {Domain: "task", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"GET /api/v1/tasks/{id}/attachments/{attachment}":               {Domain: "attachment", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"PATCH /api/v1/agent/turn/memory":                               {Domain: "turn", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/browser/grants/{grant}/cookies":             {Domain: "browser", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/browser/grants/{grant}/tabs":                {Domain: "browser", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/browser/grants/{grant}/tabs/current":        {Domain: "browser", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/actions":  {Domain: "browser", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/activate": {Domain: "browser", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/files":    {Domain: "browser", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/navigate": {Domain: "browser", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/browser/mcp":                                {Domain: "browser", Access: agentRouteTools, Mode: agentRouteMCP},
+	"POST /api/v1/agent/channel/actions/preview":                    {Domain: "channel", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/channel/handoffs":                           {Domain: "channel", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/channel/mcp":                                {Domain: "channel", Access: agentRouteTools, Mode: agentRouteMCP},
+	"POST /api/v1/agent/channel/messages":                           {Domain: "channel", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/channel/replies":                            {Domain: "channel", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/channel/reply-decision":                     {Domain: "channel", Access: agentRouteBookkeeping, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/collab/mcp":                                 {Domain: "collaboration", Access: agentRouteTools, Mode: agentRouteMCP},
+	"POST /api/v1/agent/collaboration/batches":                      {Domain: "collaboration", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/collaboration/batches/{batch}/wait":         {Domain: "collaboration", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/core/mcp":                                   {Domain: "turn", Access: agentRouteTools, Mode: agentRouteMCP},
+	"POST /api/v1/agent/desktop/actions":                            {Domain: "desktop", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/desktop/control":                            {Domain: "desktop", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/desktop/mcp":                                {Domain: "desktop", Access: agentRouteTools, Mode: agentRouteMCP},
+	"POST /api/v1/agent/desktop/stop":                               {Domain: "desktop", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/hardware/mcp":                               {Domain: "hardware", Access: agentRouteTools, Mode: agentRouteMCP},
+	"POST /api/v1/agent/hardware/{hardware}/adb/exec":               {Domain: "hardware", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/hardware/{hardware}/connect":                {Domain: "hardware", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/hardware/{hardware}/disconnect":             {Domain: "hardware", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/hardware/{hardware}/login":                  {Domain: "hardware", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/hardware/{hardware}/send":                   {Domain: "hardware", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/knowledge/candidates":                       {Domain: "knowledge", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/knowledge/entries":                          {Domain: "knowledge", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/knowledge/feedback":                         {Domain: "knowledge", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/knowledge/mcp":                              {Domain: "knowledge", Access: agentRouteTools, Mode: agentRouteMCP},
+	"POST /api/v1/agent/knowledge/{knowledge}/feedback":             {Domain: "knowledge", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/mail/mcp":                                   {Domain: "mail", Access: agentRouteTools, Mode: agentRouteMCP},
+	"POST /api/v1/agent/mail/messages":                              {Domain: "mail", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/mail/preview":                               {Domain: "mail", Access: agentRouteRead, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/processes":                                  {Domain: "workspace", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/processes/{name}/stop":                      {Domain: "workspace", Access: agentRouteWrite, Mode: agentRouteResource},
+	"POST /api/v1/agent/skills":                                     {Domain: "skill", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/tasks":                                      {Domain: "task", Access: agentRouteWrite, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/turn/attachments":                           {Domain: "turn", Access: agentRouteBookkeeping, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/turn/inbox/ack":                             {Domain: "turn", Access: agentRouteBookkeeping, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/turn/inbox/claim":                           {Domain: "turn", Access: agentRouteBookkeeping, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/turn/inbox/wait":                            {Domain: "turn", Access: agentRouteBookkeeping, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/turn/messages":                              {Domain: "turn", Access: agentRouteBookkeeping, Mode: agentRouteBusiness},
+	"POST /api/v1/agent/workspace/mcp":                              {Domain: "workspace", Access: agentRouteTools, Mode: agentRouteMCP},
+	"POST /api/v1/pet/mcp":                                          {Domain: "pet", Access: agentRouteTools, Mode: agentRouteMCP},
+	"PUT /api/v1/agent/skills/{skill}":                              {Domain: "skill", Access: agentRouteWrite, Mode: agentRouteBusiness},
+}
diff --git a/internal/httpapi/agent_token_route_policy_test.go b/internal/httpapi/agent_token_route_policy_test.go
new file mode 100644
index 0000000..a1262dc
--- /dev/null
+++ b/internal/httpapi/agent_token_route_policy_test.go
@@ -0,0 +1,242 @@
+package httpapi
+
+import (
+	"go/ast"
+	"go/parser"
+	"go/token"
+	"os"
+	"strconv"
+	"strings"
+	"testing"
+)
+
+type tokenRouteRegistration struct{ Pattern, Handler, Wrapper, Source string }
+
+// Read the real registrations, including the finite desktop tables. Unsupported
+// new registration syntax fails the test rather than silently losing a route.
+func registeredAgentTokenRoutes(t *testing.T) map[string]tokenRouteRegistration {
+	t.Helper()
+	files := map[string]*ast.File{}
+	constants := map[string]string{}
+	entries, err := os.ReadDir(".")
+	if err != nil {
+		t.Fatal(err)
+	}
+	for _, entry := range entries {
+		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
+			continue
+		}
+		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.SkipObjectResolution)
+		if err != nil {
+			t.Fatal(err)
+		}
+		files[entry.Name()] = file
+		for _, decl := range file.Decls {
+			group, ok := decl.(*ast.GenDecl)
+			if !ok || group.Tok != token.CONST {
+				continue
+			}
+			for _, spec := range group.Specs {
+				value, ok := spec.(*ast.ValueSpec)
+				if !ok {
+					continue
+				}
+				for index, name := range value.Names {
+					if index < len(value.Values) {
+						if literal, ok := value.Values[index].(*ast.BasicLit); ok && literal.Kind == token.STRING {
+							if decoded, err := strconv.Unquote(literal.Value); err == nil {
+								constants[name.Name] = decoded
+							}
+						}
+					}
+				}
+			}
+		}
+	}
+	var eval func(ast.Expr) (string, bool)
+	eval = func(expr ast.Expr) (string, bool) {
+		switch value := expr.(type) {
+		case *ast.BasicLit:
+			if value.Kind == token.STRING {
+				decoded, err := strconv.Unquote(value.Value)
+				return decoded, err == nil
+			}
+		case *ast.Ident:
+			decoded, ok := constants[value.Name]
+			return decoded, ok
+		case *ast.BinaryExpr:
+			if value.Op == token.ADD {
+				left, ok := eval(value.X)
+				right, rightOK := eval(value.Y)
+				return left + right, ok && rightOK
+			}
+		}
+		return "", false
+	}
+	unwrapHandler := func(expr ast.Expr) string {
+		if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) == 1 {
+			expr = call.Args[0]
+		}
+		if selector, ok := expr.(*ast.SelectorExpr); ok {
+			return selector.Sel.Name
+		}
+		return ""
+	}
+	protected := func(expr ast.Expr) (string, ast.Expr) {
+		call, ok := expr.(*ast.CallExpr)
+		if !ok || len(call.Args) != 1 {
+			return "", nil
+		}
+		selector, ok := call.Fun.(*ast.SelectorExpr)
+		if !ok {
+			return "", nil
+		}
+		if selector.Sel.Name == "withAgentCapability" || selector.Sel.Name == "withOwnerOrAgentAuth" {
+			return selector.Sel.Name, call.Args[0]
+		}
+		return "", nil
+	}
+	routes := map[string]tokenRouteRegistration{}
+	initialAuthenticators := map[string]int{}
+	add := func(pattern, handler, wrapper, source string) {
+		t.Helper()
+		if _, exists := routes[pattern]; exists {
+			t.Fatalf("duplicate token route %s", pattern)
+		}
+		if pattern == "" || handler == "" {
+			t.Fatal("token route has no reproducible pattern/handler")
+		}
+		routes[pattern] = tokenRouteRegistration{pattern, handler, wrapper, source}
+	}
+	for source, file := range files {
+		for _, declaration := range file.Decls {
+			function, ok := declaration.(*ast.FuncDecl)
+			if !ok || function.Body == nil {
+				continue
+			}
+			ast.Inspect(function.Body, func(node ast.Node) bool {
+				call, ok := node.(*ast.CallExpr)
+				if !ok {
+					return true
+				}
+				method, ok := call.Fun.(*ast.SelectorExpr)
+				if !ok || method.Sel.Name != "Authenticate" {
+					return true
+				}
+				receiver, ok := method.X.(*ast.SelectorExpr)
+				if !ok || receiver.Sel.Name != "agentCapabilities" {
+					return true
+				}
+				key := source + ":" + function.Name.Name
+				if key != "helpers.go:withAgentCapability" && key != "helpers.go:withOwnerOrAgentAuth" {
+					t.Fatalf("Agent token authenticator outside audited wrappers: %s", key)
+				}
+				initialAuthenticators[key]++
+				return true
+			})
+		}
+		handled := map[*ast.CallExpr]bool{}
+		ast.Inspect(file, func(node ast.Node) bool {
+			rangeStmt, ok := node.(*ast.RangeStmt)
+			if !ok {
+				return true
+			}
+			table, ok := rangeStmt.X.(*ast.CompositeLit)
+			if !ok {
+				return true
+			}
+			ast.Inspect(rangeStmt.Body, func(child ast.Node) bool {
+				call, ok := child.(*ast.CallExpr)
+				if !ok || len(call.Args) != 2 {
+					return true
+				}
+				selector, ok := call.Fun.(*ast.SelectorExpr)
+				if !ok || selector.Sel.Name != "Handle" {
+					return true
+				}
+				wrapper, _ := protected(call.Args[1])
+				if wrapper == "" {
+					return true
+				}
+				patternField, ok := call.Args[0].(*ast.SelectorExpr)
+				if !ok || patternField.Sel.Name != "pattern" {
+					return true
+				}
+				handled[call] = true
+				for _, element := range table.Elts {
+					row, ok := element.(*ast.CompositeLit)
+					if !ok || len(row.Elts) != 2 {
+						t.Fatal("unsupported protected route table row")
+					}
+					pattern, ok := eval(row.Elts[0])
+					if !ok {
+						t.Fatal("nonliteral protected route table pattern")
+					}
+					add(pattern, unwrapHandler(row.Elts[1]), wrapper, source)
+				}
+				return true
+			})
+			return true
+		})
+		ast.Inspect(file, func(node ast.Node) bool {
+			call, ok := node.(*ast.CallExpr)
+			if !ok || len(call.Args) != 2 || handled[call] {
+				return true
+			}
+			selector, ok := call.Fun.(*ast.SelectorExpr)
+			if !ok || (selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc") {
+				return true
+			}
+			wrapper, handler := protected(call.Args[1])
+			if wrapper == "" {
+				return true
+			}
+			pattern, ok := eval(call.Args[0])
+			if !ok {
+				t.Fatalf("unsupported token registration syntax in %s", source)
+			}
+			add(pattern, unwrapHandler(handler), wrapper, source)
+			return true
+		})
+	}
+	if initialAuthenticators["helpers.go:withAgentCapability"] != 1 || initialAuthenticators["helpers.go:withOwnerOrAgentAuth"] != 1 {
+		t.Fatalf("Agent authenticators changed: %v", initialAuthenticators)
+	}
+	return routes
+}
+
+func TestEveryAgentTokenRouteHasExplicitPolicy(t *testing.T) {
+	routes := registeredAgentTokenRoutes(t)
+	if len(routes) != len(agentTokenRoutePolicies) {
+		t.Fatalf("registered=%d classified=%d; update explicit policy with any token route change", len(routes), len(agentTokenRoutePolicies))
+	}
+	mcpHandlers := map[string]string{"petMCP": "pet", "agentChannelMCP": "channel", "agentCoreMCP": "turn", "agentKnowledgeMCP": "knowledge", "agentWorkspaceMCP": "workspace", "agentCollabMCP": "collaboration", "agentBrowserMCP": "browser", "agentHardwareMCP": "hardware", "agentDesktopMCP": "desktop", "agentMailMCP": "mail"}
+	validDomains := map[string]bool{"pet": true, "turn": true, "task": true, "attachment": true, "hardware": true, "browser": true, "workspace": true, "desktop": true, "knowledge": true, "skill": true, "channel": true, "collaboration": true, "mail": true}
+	for pattern, registration := range routes {
+		policy, ok := agentTokenRoutePolicies[pattern]
+		if !ok {
+			t.Fatalf("unclassified token route %s", pattern)
+		}
+		if !validDomains[policy.Domain] {
+			t.Fatalf("unknown policy domain at %s", pattern)
+		}
+		if domain, isMCP := mcpHandlers[registration.Handler]; isMCP {
+			if policy.Mode != agentRouteMCP || policy.Access != agentRouteTools || policy.Domain != domain || !strings.HasPrefix(pattern, "POST ") || strings.Contains(pattern, "{") {
+				t.Fatalf("MCP policy mismatch at %s", pattern)
+			}
+		} else if policy.Mode == agentRouteMCP {
+			t.Fatalf("non-MCP handler exempted at %s", pattern)
+		}
+		if policy.Mode != agentRouteMCP && policy.Mode != agentRouteResource && policy.Mode != agentRouteBusiness {
+			t.Fatalf("untyped authority at %s", pattern)
+		}
+		if registration.Wrapper == "withOwnerOrAgentAuth" && pattern != "GET /api/v1/tasks/{id}/attachments/{attachment}" {
+			t.Fatalf("new dual-auth resource needs explicit audit: %s", pattern)
+		}
+	}
+	for pattern := range agentTokenRoutePolicies {
+		if _, ok := routes[pattern]; !ok {
+			t.Fatalf("stale/unregistered token policy %s", pattern)
+		}
+	}
+}
diff --git a/internal/httpapi/attachment_handlers_test.go b/internal/httpapi/attachment_handlers_test.go
index 65069f6..a913427 100644
--- a/internal/httpapi/attachment_handlers_test.go
+++ b/internal/httpapi/attachment_handlers_test.go
@@ -235,16 +235,18 @@ func TestTaskAttachmentDownloadByAgentCapabilityIsScopedToTask(t *testing.T) {
 	}
 
 	capabilities := agentapi.NewCapabilities()
-	tokenA, err := capabilities.Issue(taskA.ID, "main", "turn-a", time.Hour)
+	turnA := createCoreTurn(t, database, taskA, "main", domain.NewID("inbox_batch"))
+	tokenA, err := capabilities.Issue(taskA.ID, "main", turnA.ID, time.Hour)
 	if err != nil {
 		t.Fatal(err)
 	}
-	tokenB, err := capabilities.Issue(taskB.ID, "main", "turn-b", time.Hour)
+	turnB := createCoreTurn(t, database, taskB, "main", domain.NewID("inbox_batch"))
+	tokenB, err := capabilities.Issue(taskB.ID, "main", turnB.ID, time.Hour)
 	if err != nil {
 		t.Fatal(err)
 	}
 	authService := auth.NewService(database, "setup-test", time.Hour)
-	server := httptest.NewServer(New(Config{Store: database, Auth: authService, AgentCapabilities: capabilities}).Handler())
+	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}), AgentCapabilities: capabilities}).Handler())
 	defer server.Close()
 	attachmentURL := func(taskID, attachmentID string) string {
 		return server.URL + "/api/v1/tasks/" + taskID + "/attachments/" + attachmentID
diff --git a/internal/httpapi/helpers.go b/internal/httpapi/helpers.go
index 5ac4059..323ca30 100644
--- a/internal/httpapi/helpers.go
+++ b/internal/httpapi/helpers.go
@@ -61,7 +61,11 @@ func (s *Server) withOwnerOrAgentAuth(next http.Handler) http.Handler {
 		if ok && strings.EqualFold(scheme, "Bearer") && s.agentCapabilities != nil {
 			if claims, err := s.agentCapabilities.Authenticate(strings.TrimSpace(token)); err == nil {
 				ctx := context.WithValue(request.Context(), agentClaimsContextKey{}, claims)
-				next.ServeHTTP(writer, request.WithContext(ctx))
+				request = request.WithContext(ctx)
+				if !s.authorizeAgentDomainRoute(writer, request, claims) {
+					return
+				}
+				next.ServeHTTP(writer, request)
 				return
 			}
 		}
```
