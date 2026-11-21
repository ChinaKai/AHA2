# M2-a：固定 aee3f33 增量复审

源码 `aee3f33e926e1f6345810a5de0f8d1dda77e3b51`，tree `c54bd022f66e9c5900a046d4397284a306e778bf`。本轮完整增量 `7eab7ce74630e742d41e066af25a5e92e2234d6e..aee3f33e926e1f6345810a5de0f8d1dda77e3b51` 共7文件；7eab7ce仅是9bca378后的旧材料存档，不把P1b/P1c或未审P2吞成已通过。本轮不改已获有限APPROVE的r1/r3a/r4/r5/r6源码；M5与P1c仍阻塞，完整Task未完成。所有摘录与diff都从固定提交取得，门禁diff保留3行上下文。新材料先在scratch完整生成与校验，再原子写入版本路径、归档后回报ready；此前所有冻结包不改写。

固定副本6个具名顶层测试逐个核对run/pass，遗漏0，退出0（67.042s）；`go vet ./internal/httpapi`退出0（1.129s）。摘要及完整命令为同版本 `m2a-aee3f33-validation-v1.md`。N3仍未完成：固定9bca378的app全包40m累计超时退出1（359个测试pass事件、0个单用例fail事件，不能当通过）；其httpapi仍在运行。新aee3f33全包尚无退出码。

## 全部token路由分类与前次N5调用链补证

完整diff文件边界：

- `docs/pet-mcp/agent-token-route-policy.tsv`

本边界补齐前次r2缺少的全集和完整函数：两个接收Agent token的认证包装、共享服务层、浏览器/Workspace/桌面MCP根函数、ADB的agentOperationContextKey读取方、硬件最终目标、owner Web认证分支及错误映射。操作context键只由固定MCP操作适配器写入，用于有界输出，不用于制造owner身份。

下方完整TSV与代码策略表的82个注册模式一致，包含withOwnerOrAgentAuth。结构化测试从真实Go AST读取所有受保护Handle/HandleFunc及有限桌面表，核对每条已注册模式与策略一一对应，并检查直接Agent token认证只出现在两个已审包装器；新注册写法无法解析、缺策略或多余策略都会失败。它是M2-a的token入口对账，不冒充P1c全业务路由扫描器；P1c仍REQUEST_CHANGES。
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

### `internal/httpapi/agent_browser_mcp.go` · `agentBrowserMCP`

```go
func (s *Server) agentBrowserMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_browser_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, agentBrowserMCPMaxRequestBytes))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
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
	call, err := s.app.AgentDomainCallContext(request.Context(), claims, "browser", false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	if !s.app.AgentBrowserMCPAllowed(request.Context(), call) {
		writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
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
			"serverInfo":      map[string]any{"name": "aha2-browser", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		respond(map[string]any{"tools": s.browserMCPToolsFor(s.app.AgentBrowserMCPWriteAllowed(request.Context(), call))})
	case "tools/call":
		s.callAgentBrowserMCPTool(writer, request, call, payload)
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		respondError(-32601, "method not found")
	}
}
```

### `internal/httpapi/agent_workspace_mcp.go` · `agentWorkspaceMCP`

```go
func (s *Server) agentWorkspaceMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_workspace_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, agentWorkspaceMCPMaxRequestBytes))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
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
	call, err := s.app.AgentDomainCallContext(request.Context(), claims, "workspace", false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	if !s.app.AgentWorkspaceMCPAllowed(request.Context(), call) {
		writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
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
			"serverInfo":      map[string]any{"name": "aha2-workspace", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		respond(map[string]any{"tools": s.workspaceMCPToolsFor(s.app.AgentWorkspaceMCPWriteAllowed(request.Context(), call))})
	case "tools/call":
		s.callAgentWorkspaceMCPTool(writer, request, claims, call, payload)
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		respondError(-32601, "method not found")
	}
}
```

### `internal/httpapi/agent_desktop_mcp.go` · `agentDesktopMCP`

```go
func (s *Server) agentDesktopMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_desktop_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	call, err := s.app.AgentDomainCallContext(request.Context(), claims, "desktop", false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	if !s.app.AgentDesktopMCPAllowed(request.Context(), call) {
		writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
	if err != nil {
		writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	var payload mcpRequest
	if trimmed := bytes.TrimSpace(body); len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(body, &payload) != nil || len(payload.ID) > 256 {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	respond := func(value any) {
		writeJSON(writer, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": payload.ID, "result": value})
	}
	fail := func(message string) {
		respond(map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": message}}})
	}
	write := s.app.AgentDesktopMCPWriteAllowed(request.Context(), call)
	switch payload.Method {
	case "initialize":
		respond(map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "aha2-desktop", "version": "1.0.0"}})
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		items := []mcpTool{}
		for _, op := range s.desktopMCPOperations() {
			if !op.Write || write {
				items = append(items, op.Tool)
			}
		}
		respond(map[string]any{"tools": items})
	case "tools/call":
		var input struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(payload.Params, &input) != nil {
			fail("invalid tool call")
			return
		}
		if len(input.Arguments) == 0 {
			input.Arguments = json.RawMessage(`{}`)
		}
		for _, op := range s.desktopMCPOperations() {
			if op.Tool.Name != input.Name {
				continue
			}
			if op.Write && !write {
				writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
				return
			}
			result, callErr := runAgentMCPOperation(request, op, input.Arguments)
			s.audit(request, "agent.desktop_mcp.call", "task", call.Task.ID, map[string]any{"tool": input.Name, "turn_id": call.Turn.ID, "argument_keys": coreMCPArgumentKeys(input.Arguments), "success": callErr == nil})
			if callErr != nil {
				fail("desktop operation failed; check sharing, observation and permissions")
				return
			}
			if observation, ok := result["observation"].(map[string]any); ok {
				delete(observation, "capture_error")
				if elements, ok := observation["elements"].([]any); ok {
					for _, raw := range elements {
						if element, ok := raw.(map[string]any); ok {
							delete(element, "value")
						}
					}
				}
			}
			data, encodeErr := json.Marshal(result)
			if encodeErr != nil {
				fail("operation result unavailable")
				return
			}
			respond(map[string]any{"content": []map[string]any{{"type": "text", "text": string(data)}}})
			return
		}
		fail("unknown tool")
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": payload.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
	}
}
```

### `internal/httpapi/agent_handlers.go` · `agentADBExec`

```go
func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if transport != domain.HardwareTransportNetwork || group.Network.Protocol != domain.HardwareProtocolADB {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_exec_unavailable", "message": "当前硬件资源不是 ADB 网络设备"})
		return
	}
	if readOnly {
		writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "hardware_read_only", "message": "ADB 通用执行要求硬件资源和 Task 可读写"})
		return
	}
	var payload struct {
		Args           []string `json:"args"`
		StdinBase64    string   `json:"stdin_base64"`
		TimeoutSeconds int      `json:"timeout_seconds"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if len(payload.Args) == 0 || len(payload.Args) > 64 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_args_invalid", "message": "ADB 参数数量必须在 1 到 64 之间"})
		return
	}
	if strings.HasPrefix(payload.Args[0], "-") || blockedADBHostCommands[strings.ToLower(payload.Args[0])] {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_command_not_allowed", "message": "这里只允许面向当前设备的 ADB 子命令"})
		return
	}
	argumentBytes := 0
	for _, argument := range payload.Args {
		if strings.ContainsRune(argument, '\x00') || len(argument) > 4096 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_args_invalid", "message": "ADB 参数包含无效内容或过长"})
			return
		}
		argumentBytes += len(argument)
	}
	if argumentBytes > 16*1024 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_args_invalid", "message": "ADB 参数总长度超过限制"})
		return
	}
	input := []byte(nil)
	if payload.StdinBase64 != "" {
		var err error
		input, err = base64.StdEncoding.DecodeString(payload.StdinBase64)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_stdin_invalid", "message": "ADB stdin 必须是 base64"})
			return
		}
	}
	if len(input) > 1024*1024 {
		writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]any{"ok": false, "error": "adb_stdin_too_large", "message": "ADB stdin 超过 1 MiB"})
		return
	}
	timeoutSeconds := payload.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = 30
	}
	if timeoutSeconds < 1 || timeoutSeconds > 120 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_timeout_invalid", "message": "ADB 超时时间必须在 1 到 120 秒之间"})
		return
	}
	endpoint := net.JoinHostPort(group.Network.Host, strconv.Itoa(group.Network.Port))
	result, err := s.executeADB(request.Context(), endpoint, payload.Args, input, time.Duration(timeoutSeconds)*time.Second)
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, hardware.ErrADBOutputTooLarge) {
			code = http.StatusRequestEntityTooLarge
		}
		s.audit(request, "task.hardware.adb_exec_failed", "task", task.ID, map[string]any{
			"hardware_id": group.ID, "argument_count": len(payload.Args), "input_bytes": len(input),
		})
		writeJSON(writer, code, map[string]any{"ok": false, "error": "adb_exec_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.hardware.adb_exec", "task", task.ID, map[string]any{
		"hardware_id": group.ID, "argument_count": len(payload.Args),
		"input_bytes": len(input), "stdout_bytes": len(result.Stdout),
		"stderr_bytes": len(result.Stderr), "exit_code": result.ExitCode,
	})
	stdout, stderr := result.Stdout, result.Stderr
	stdoutTruncated, stderrTruncated := false, false
	if operation, _ := request.Context().Value(agentOperationContextKey{}).(string); operation == "hardware_adb_exec" {
		// The command has already run. Preserve exit/status and an explicit
		// bounded result instead of turning a successful side effect into a
		// response-size failure that could provoke an unsafe command retry.
		const budget = 24 << 10
		if len(stdout) > budget {
			stdout = stdout[:budget]
			stdoutTruncated = true
		}
		remaining := budget - len(stdout)
		if len(stderr) > remaining {
			stderr = stderr[:remaining]
			stderrTruncated = true
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "endpoint": result.Endpoint, "exit_code": result.ExitCode,
		"stdout_base64":    base64.StdEncoding.EncodeToString(stdout),
		"stderr_base64":    base64.StdEncoding.EncodeToString(stderr),
		"stdout_truncated": stdoutTruncated, "stderr_truncated": stderrTruncated,
		"stdout_bytes": len(result.Stdout), "stderr_bytes": len(result.Stderr),
		"duration_ms": result.Duration.Milliseconds(),
	})
}
```

### `internal/httpapi/hardware_handlers.go` · `hardwareTarget`

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

### `internal/httpapi/agent_mcp_operation.go` · `runAgentMCPOperation`

```go
func runAgentMCPOperation(request *http.Request, op agentMCPOperation, raw json.RawMessage) (result map[string]any, err error) {
	if request.Context().Err() != nil {
		return nil, errors.New("operation cancelled")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var arguments map[string]any
	if decoder.Decode(&arguments) != nil || validateMCPOperation(arguments, op.Tool.InputSchema) != nil {
		return nil, errors.New("invalid tool arguments")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid tool arguments")
	}
	child := request.Clone(context.WithValue(request.Context(), agentOperationContextKey{}, op.Tool.Name))
	child.Method = op.Method
	child.Header = make(http.Header)
	child.URL = &url.URL{Path: request.URL.Path}
	query := url.Values{}
	for field, pathName := range op.PathFields {
		if value, ok := arguments[field]; ok {
			child.SetPathValue(pathName, fmt.Sprint(value))
			delete(arguments, field)
		}
	}
	for _, field := range op.QueryFields {
		if value, ok := arguments[field]; ok {
			query.Set(field, fmt.Sprint(value))
			delete(arguments, field)
		}
	}
	child.URL.RawQuery = query.Encode()
	body, err := json.Marshal(arguments)
	if err != nil {
		return nil, errors.New("invalid tool arguments")
	}
	child.Header.Set("Content-Type", "application/json")
	child.Body = io.NopCloser(bytes.NewReader(body))
	child.ContentLength = int64(len(body))
	response := &mcpOperationResponse{header: make(http.Header)}
	defer func() {
		if recovered := recover(); recovered != nil {
			if recovered == http.ErrAbortHandler {
				result = nil
				err = errors.New("operation stream interrupted")
				return
			}
			panic(recovered)
		}
	}()
	op.Handler(response, child)
	// Raw runtime errors can contain endpoints or credentials. Only a fixed
	// classification leaves this boundary, and no body is logged here.
	if response.status >= 400 {
		return nil, &agentOperationError{Status: response.status, Code: safeAgentOperationCode(response.body.Bytes())}
	}
	if response.overflow {
		return nil, errors.New("operation response exceeds limit")
	}
	if op.Binary {
		return map[string]any{"media_type": response.header.Get("Content-Type"), "content_base64": base64.StdEncoding.EncodeToString(response.body.Bytes())}, nil
	}
	if json.Unmarshal(response.body.Bytes(), &result) != nil {
		return nil, errors.New("operation result unavailable")
	}
	if op.ResultFields != nil {
		view := map[string]any{}
		for _, key := range op.ResultFields {
			if value, ok := result[key]; ok {
				view[key] = value
			}
		}
		return view, nil
	}
	return result, nil
}
```

### `internal/httpapi/helpers.go` · `withAuth`

```go
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(sessionCookieName)
		if err != nil {
			writeError(writer, http.StatusUnauthorized, "authentication_required")
			return
		}
		session, err := s.auth.Authenticate(request.Context(), cookie.Value)
		if err != nil {
			clearSessionCookie(writer, s.secureCookie)
			writeError(writer, http.StatusUnauthorized, "authentication_required")
			return
		}
		if unsafeMethod(request.Method) {
			if !s.sameOrigin(request) {
				writeError(writer, http.StatusForbidden, "origin_rejected")
				return
			}
			if request.Header.Get("X-CSRF-Token") != session.CSRFToken {
				writeError(writer, http.StatusForbidden, "csrf_rejected")
				return
			}
		}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, session)))
	})
}
```

## 该边界完整diff

```diff
diff --git a/docs/pet-mcp/agent-token-route-policy.tsv b/docs/pet-mcp/agent-token-route-policy.tsv
new file mode 100644
index 0000000..d3e1687
--- /dev/null
+++ b/docs/pet-mcp/agent-token-route-policy.tsv
@@ -0,0 +1,83 @@
+operation	domain	access	authority	handler	source	auth_wrapper
+DELETE /api/v1/agent/browser/grants/{grant}	browser	agentRouteWrite	agentRouteResource	agentBrowserRelease	server.go	agent
+DELETE /api/v1/agent/browser/grants/{grant}/tabs/{tab}	browser	agentRouteWrite	agentRouteResource	agentBrowserCloseTab	server.go	agent
+GET /api/v1/agent/browser	browser	agentRouteRead	agentRouteResource	agentBrowserState	server.go	agent
+GET /api/v1/agent/browser/grants/{grant}/bookmarks	browser	agentRouteRead	agentRouteResource	agentBrowserBookmarks	server.go	agent
+GET /api/v1/agent/browser/grants/{grant}/cookies	browser	agentRouteRead	agentRouteResource	agentBrowserCookies	server.go	agent
+GET /api/v1/agent/browser/grants/{grant}/downloads	browser	agentRouteRead	agentRouteResource	agentBrowserDownloads	server.go	agent
+GET /api/v1/agent/browser/grants/{grant}/downloads/{download}	browser	agentRouteRead	agentRouteResource	agentBrowserDownload	server.go	agent
+GET /api/v1/agent/browser/grants/{grant}/tabs	browser	agentRouteRead	agentRouteResource	agentBrowserTabs	server.go	agent
+GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/frame	browser	agentRouteRead	agentRouteResource	agentBrowserFrame	server.go	agent
+GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/snapshot	browser	agentRouteRead	agentRouteResource	agentBrowserSnapshot	server.go	agent
+GET /api/v1/agent/capabilities	turn	agentRouteRead	agentRouteBusiness	agentCapabilitiesInfo	server.go	agent
+GET /api/v1/agent/channel/catalog	channel	agentRouteRead	agentRouteBusiness	agentChannelCatalog	server.go	agent
+GET /api/v1/agent/channel/contacts	channel	agentRouteRead	agentRouteBusiness	agentTaskChannelContacts	server.go	agent
+GET /api/v1/agent/channel/context	channel	agentRouteRead	agentRouteBusiness	agentChannelContext	server.go	agent
+GET /api/v1/agent/desktop	desktop	agentRouteRead	agentRouteResource	desktopStatus	desktop_handlers.go	agent
+GET /api/v1/agent/desktop/observation	desktop	agentRouteRead	agentRouteResource	desktopObserve	desktop_handlers.go	agent
+GET /api/v1/agent/hardware	hardware	agentRouteRead	agentRouteResource	agentHardware	server.go	agent
+GET /api/v1/agent/hardware/{hardware}/terminal	hardware	agentRouteRead	agentRouteResource	agentHardwareTerminal	server.go	agent
+GET /api/v1/agent/knowledge	knowledge	agentRouteRead	agentRouteBusiness	agentKnowledge	server.go	agent
+GET /api/v1/agent/knowledge/guide	knowledge	agentRouteRead	agentRouteBusiness	agentKnowledgeGuide	server.go	agent
+GET /api/v1/agent/knowledge/{knowledge}	knowledge	agentRouteRead	agentRouteBusiness	agentKnowledgeEntry	server.go	agent
+GET /api/v1/agent/mail/accounts	mail	agentRouteRead	agentRouteBusiness	agentMailAccounts	server.go	agent
+GET /api/v1/agent/mail/messages/{id}	mail	agentRouteRead	agentRouteBusiness	agentMailMessageStatus	server.go	agent
+GET /api/v1/agent/mail/templates	mail	agentRouteRead	agentRouteBusiness	agentMailTemplates	server.go	agent
+GET /api/v1/agent/mail/templates/{id}	mail	agentRouteRead	agentRouteBusiness	agentMailTemplateDetail	server.go	agent
+GET /api/v1/agent/processes	workspace	agentRouteRead	agentRouteResource	agentProcesses	server.go	agent
+GET /api/v1/agent/processes/{name}	workspace	agentRouteRead	agentRouteResource	agentProcessStatus	server.go	agent
+GET /api/v1/agent/project/runtimes	workspace	agentRouteRead	agentRouteResource	agentProjectRuntimes	server.go	agent
+GET /api/v1/agent/project/workspaces	workspace	agentRouteRead	agentRouteResource	agentProjectWorkspaces	server.go	agent
+GET /api/v1/agent/skills	skill	agentRouteRead	agentRouteBusiness	agentSkills	server.go	agent
+GET /api/v1/agent/skills/{skill}	skill	agentRouteRead	agentRouteBusiness	agentSkill	server.go	agent
+GET /api/v1/agent/tasks/{task}	task	agentRouteRead	agentRouteBusiness	agentTaskStatus	server.go	agent
+GET /api/v1/tasks/{id}/attachments/{attachment}	attachment	agentRouteRead	agentRouteBusiness	taskAttachmentContent	server.go	owner_or_agent
+PATCH /api/v1/agent/turn/memory	turn	agentRouteWrite	agentRouteBusiness	updateAgentMemory	server.go	agent
+POST /api/v1/agent/browser/grants/{grant}/cookies	browser	agentRouteWrite	agentRouteResource	agentBrowserImportCookies	server.go	agent
+POST /api/v1/agent/browser/grants/{grant}/tabs	browser	agentRouteWrite	agentRouteResource	agentBrowserOpenTab	server.go	agent
+POST /api/v1/agent/browser/grants/{grant}/tabs/current	browser	agentRouteWrite	agentRouteResource	agentBrowserBindCurrent	server.go	agent
+POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/actions	browser	agentRouteWrite	agentRouteResource	agentBrowserAction	server.go	agent
+POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/activate	browser	agentRouteWrite	agentRouteResource	agentBrowserActivate	server.go	agent
+POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/files	browser	agentRouteWrite	agentRouteResource	agentBrowserUpload	server.go	agent
+POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/navigate	browser	agentRouteWrite	agentRouteResource	agentBrowserNavigate	server.go	agent
+POST /api/v1/agent/browser/mcp	browser	agentRouteTools	agentRouteMCP	agentBrowserMCP	server.go	agent
+POST /api/v1/agent/channel/actions/preview	channel	agentRouteRead	agentRouteBusiness	previewAgentChannelAction	server.go	agent
+POST /api/v1/agent/channel/handoffs	channel	agentRouteWrite	agentRouteBusiness	createAgentChannelHandoff	server.go	agent
+POST /api/v1/agent/channel/mcp	channel	agentRouteTools	agentRouteMCP	agentChannelMCP	server.go	agent
+POST /api/v1/agent/channel/messages	channel	agentRouteWrite	agentRouteBusiness	sendAgentTaskChannelMessage	server.go	agent
+POST /api/v1/agent/channel/replies	channel	agentRouteWrite	agentRouteBusiness	sendAgentChannelReply	server.go	agent
+POST /api/v1/agent/channel/reply-decision	channel	agentRouteBookkeeping	agentRouteBusiness	setAgentChannelReplyDecision	server.go	agent
+POST /api/v1/agent/collab/mcp	collaboration	agentRouteTools	agentRouteMCP	agentCollabMCP	server.go	agent
+POST /api/v1/agent/collaboration/batches	collaboration	agentRouteWrite	agentRouteBusiness	submitAgentCollaboration	server.go	agent
+POST /api/v1/agent/collaboration/batches/{batch}/wait	collaboration	agentRouteWrite	agentRouteBusiness	waitAgentCollaboration	server.go	agent
+POST /api/v1/agent/core/mcp	turn	agentRouteTools	agentRouteMCP	agentCoreMCP	server.go	agent
+POST /api/v1/agent/desktop/actions	desktop	agentRouteWrite	agentRouteResource	desktopAct	desktop_handlers.go	agent
+POST /api/v1/agent/desktop/control	desktop	agentRouteWrite	agentRouteResource	desktopControl	desktop_handlers.go	agent
+POST /api/v1/agent/desktop/mcp	desktop	agentRouteTools	agentRouteMCP	agentDesktopMCP	server.go	agent
+POST /api/v1/agent/desktop/stop	desktop	agentRouteWrite	agentRouteResource	agentDesktopStop	server.go	agent
+POST /api/v1/agent/hardware/mcp	hardware	agentRouteTools	agentRouteMCP	agentHardwareMCP	server.go	agent
+POST /api/v1/agent/hardware/{hardware}/adb/exec	hardware	agentRouteWrite	agentRouteResource	agentADBExec	server.go	agent
+POST /api/v1/agent/hardware/{hardware}/connect	hardware	agentRouteWrite	agentRouteResource	agentConnectHardware	server.go	agent
+POST /api/v1/agent/hardware/{hardware}/disconnect	hardware	agentRouteWrite	agentRouteResource	agentDisconnectHardware	server.go	agent
+POST /api/v1/agent/hardware/{hardware}/login	hardware	agentRouteWrite	agentRouteResource	agentLoginHardware	server.go	agent
+POST /api/v1/agent/hardware/{hardware}/send	hardware	agentRouteWrite	agentRouteResource	agentSendHardware	server.go	agent
+POST /api/v1/agent/knowledge/candidates	knowledge	agentRouteWrite	agentRouteBusiness	submitAgentKnowledge	server.go	agent
+POST /api/v1/agent/knowledge/entries	knowledge	agentRouteWrite	agentRouteBusiness	submitAgentKnowledgeEntry	server.go	agent
+POST /api/v1/agent/knowledge/feedback	knowledge	agentRouteWrite	agentRouteBusiness	submitAgentKnowledgeFeedbackEntry	server.go	agent
+POST /api/v1/agent/knowledge/mcp	knowledge	agentRouteTools	agentRouteMCP	agentKnowledgeMCP	server.go	agent
+POST /api/v1/agent/knowledge/{knowledge}/feedback	knowledge	agentRouteWrite	agentRouteBusiness	submitAgentKnowledgeFeedback	server.go	agent
+POST /api/v1/agent/mail/mcp	mail	agentRouteTools	agentRouteMCP	agentMailMCP	server.go	agent
+POST /api/v1/agent/mail/messages	mail	agentRouteWrite	agentRouteBusiness	agentMailSend	server.go	agent
+POST /api/v1/agent/mail/preview	mail	agentRouteRead	agentRouteBusiness	agentMailPreview	server.go	agent
+POST /api/v1/agent/processes	workspace	agentRouteWrite	agentRouteResource	startAgentProcess	server.go	agent
+POST /api/v1/agent/processes/{name}/stop	workspace	agentRouteWrite	agentRouteResource	stopAgentProcess	server.go	agent
+POST /api/v1/agent/skills	skill	agentRouteWrite	agentRouteBusiness	createAgentSkill	server.go	agent
+POST /api/v1/agent/tasks	task	agentRouteWrite	agentRouteBusiness	createAgentTask	server.go	agent
+POST /api/v1/agent/turn/attachments	turn	agentRouteBookkeeping	agentRouteBusiness	uploadAgentAttachment	server.go	agent
+POST /api/v1/agent/turn/inbox/ack	turn	agentRouteBookkeeping	agentRouteBusiness	ackAgentInbox	server.go	agent
+POST /api/v1/agent/turn/inbox/claim	turn	agentRouteBookkeeping	agentRouteBusiness	claimAgentInbox	server.go	agent
+POST /api/v1/agent/turn/inbox/wait	turn	agentRouteBookkeeping	agentRouteBusiness	waitAgentInbox	server.go	agent
+POST /api/v1/agent/turn/messages	turn	agentRouteBookkeeping	agentRouteBusiness	addAgentProgressMessage	server.go	agent
+POST /api/v1/agent/workspace/mcp	workspace	agentRouteTools	agentRouteMCP	agentWorkspaceMCP	server.go	agent
+POST /api/v1/pet/mcp	pet	agentRouteTools	agentRouteMCP	petMCP	server.go	agent
+PUT /api/v1/agent/skills/{skill}	skill	agentRouteWrite	agentRouteBusiness	updateAgentSkill	server.go	agent
```

## 实际注册声明全集（直接条目与完整桌面注册函数）

```go
	mux.Handle("POST /api/v1/pet/mcp", s.withAgentCapability(http.HandlerFunc(s.petMCP)))
	mux.Handle("GET /api/v1/tasks/{id}/attachments/{attachment}", s.withOwnerOrAgentAuth(http.HandlerFunc(s.taskAttachmentContent)))
	mux.Handle("GET /api/v1/agent/hardware", s.withAgentCapability(http.HandlerFunc(s.agentHardware)))
	mux.Handle("GET /api/v1/agent/hardware/{hardware}/terminal", s.withAgentCapability(http.HandlerFunc(s.agentHardwareTerminal)))
	mux.Handle("POST /api/v1/agent/hardware/{hardware}/connect", s.withAgentCapability(http.HandlerFunc(s.agentConnectHardware)))
	mux.Handle("POST /api/v1/agent/hardware/{hardware}/disconnect", s.withAgentCapability(http.HandlerFunc(s.agentDisconnectHardware)))
	mux.Handle("POST /api/v1/agent/hardware/{hardware}/send", s.withAgentCapability(http.HandlerFunc(s.agentSendHardware)))
	mux.Handle("POST /api/v1/agent/hardware/{hardware}/login", s.withAgentCapability(http.HandlerFunc(s.agentLoginHardware)))
	mux.Handle(agentADBExecRoute, s.withAgentCapability(http.HandlerFunc(s.agentADBExec)))
	mux.Handle("GET /api/v1/agent/processes", s.withAgentCapability(http.HandlerFunc(s.agentProcesses)))
	mux.Handle("POST /api/v1/agent/processes", s.withAgentCapability(http.HandlerFunc(s.startAgentProcess)))
	mux.Handle("GET /api/v1/agent/processes/{name}", s.withAgentCapability(http.HandlerFunc(s.agentProcessStatus)))
	mux.Handle("POST /api/v1/agent/processes/{name}/stop", s.withAgentCapability(http.HandlerFunc(s.stopAgentProcess)))
	mux.Handle("GET /api/v1/agent/capabilities", s.withAgentCapability(http.HandlerFunc(s.agentCapabilitiesInfo)))
	mux.Handle("GET /api/v1/agent/browser", s.withAgentCapability(http.HandlerFunc(s.agentBrowserState)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/tabs", s.withAgentCapability(http.HandlerFunc(s.agentBrowserTabs)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs", s.withAgentCapability(http.HandlerFunc(s.agentBrowserOpenTab)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs/current", s.withAgentCapability(http.HandlerFunc(s.agentBrowserBindCurrent)))
	mux.Handle("DELETE /api/v1/agent/browser/grants/{grant}/tabs/{tab}", s.withAgentCapability(http.HandlerFunc(s.agentBrowserCloseTab)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/activate", s.withAgentCapability(http.HandlerFunc(s.agentBrowserActivate)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/navigate", s.withAgentCapability(http.HandlerFunc(s.agentBrowserNavigate)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/snapshot", s.withAgentCapability(http.HandlerFunc(s.agentBrowserSnapshot)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/tabs/{tab}/frame", s.withAgentCapability(http.HandlerFunc(s.agentBrowserFrame)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/actions", s.withAgentCapability(http.HandlerFunc(s.agentBrowserAction)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/tabs/{tab}/files", s.withAgentCapability(http.HandlerFunc(s.agentBrowserUpload)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/downloads", s.withAgentCapability(http.HandlerFunc(s.agentBrowserDownloads)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/downloads/{download}", s.withAgentCapability(http.HandlerFunc(s.agentBrowserDownload)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/cookies", s.withAgentCapability(http.HandlerFunc(s.agentBrowserCookies)))
	mux.Handle("POST /api/v1/agent/browser/grants/{grant}/cookies", s.withAgentCapability(http.HandlerFunc(s.agentBrowserImportCookies)))
	mux.Handle("GET /api/v1/agent/browser/grants/{grant}/bookmarks", s.withAgentCapability(http.HandlerFunc(s.agentBrowserBookmarks)))
	mux.Handle("DELETE /api/v1/agent/browser/grants/{grant}", s.withAgentCapability(http.HandlerFunc(s.agentBrowserRelease)))
	mux.Handle("PATCH /api/v1/agent/turn/memory", s.withAgentCapability(http.HandlerFunc(s.updateAgentMemory)))
	mux.Handle("POST /api/v1/agent/turn/messages", s.withAgentCapability(http.HandlerFunc(s.addAgentProgressMessage)))
	mux.Handle(waitAgentInboxRoute, s.withAgentCapability(http.HandlerFunc(s.waitAgentInbox)))
	mux.Handle("POST /api/v1/agent/turn/inbox/claim", s.withAgentCapability(http.HandlerFunc(s.claimAgentInbox)))
	mux.Handle("POST /api/v1/agent/turn/inbox/ack", s.withAgentCapability(http.HandlerFunc(s.ackAgentInbox)))
	mux.Handle("POST /api/v1/agent/channel/reply-decision", s.withAgentCapability(http.HandlerFunc(s.setAgentChannelReplyDecision)))
	mux.Handle("POST /api/v1/agent/turn/attachments", s.withAgentCapability(http.HandlerFunc(s.uploadAgentAttachment)))
	mux.Handle("GET /api/v1/agent/knowledge", s.withAgentCapability(http.HandlerFunc(s.agentKnowledge)))
	mux.Handle("GET /api/v1/agent/knowledge/guide", s.withAgentCapability(http.HandlerFunc(s.agentKnowledgeGuide)))
	mux.Handle("GET /api/v1/agent/knowledge/{knowledge}", s.withAgentCapability(http.HandlerFunc(s.agentKnowledgeEntry)))
	mux.Handle("POST /api/v1/agent/knowledge/candidates", s.withAgentCapability(http.HandlerFunc(s.submitAgentKnowledge)))
	mux.Handle("POST /api/v1/agent/knowledge/entries", s.withAgentCapability(http.HandlerFunc(s.submitAgentKnowledgeEntry)))
	mux.Handle("POST /api/v1/agent/knowledge/feedback", s.withAgentCapability(http.HandlerFunc(s.submitAgentKnowledgeFeedbackEntry)))
	mux.Handle("POST /api/v1/agent/knowledge/{knowledge}/feedback", s.withAgentCapability(http.HandlerFunc(s.submitAgentKnowledgeFeedback)))
	mux.Handle("POST /api/v1/agent/collaboration/batches", s.withAgentCapability(http.HandlerFunc(s.submitAgentCollaboration)))
	mux.Handle(waitAgentCollaborationRoute, s.withAgentCapability(http.HandlerFunc(s.waitAgentCollaboration)))
	mux.Handle("GET /api/v1/agent/skills", s.withAgentCapability(http.HandlerFunc(s.agentSkills)))
	mux.Handle("POST /api/v1/agent/skills", s.withAgentCapability(http.HandlerFunc(s.createAgentSkill)))
	mux.Handle("GET /api/v1/agent/skills/{skill}", s.withAgentCapability(http.HandlerFunc(s.agentSkill)))
	mux.Handle("PUT /api/v1/agent/skills/{skill}", s.withAgentCapability(http.HandlerFunc(s.updateAgentSkill)))
	mux.Handle("GET /api/v1/agent/project/workspaces", s.withAgentCapability(http.HandlerFunc(s.agentProjectWorkspaces)))
	mux.Handle("GET /api/v1/agent/project/runtimes", s.withAgentCapability(http.HandlerFunc(s.agentProjectRuntimes)))
	mux.Handle("GET /api/v1/agent/channel/context", s.withAgentCapability(http.HandlerFunc(s.agentChannelContext)))
	mux.Handle("GET /api/v1/agent/channel/catalog", s.withAgentCapability(http.HandlerFunc(s.agentChannelCatalog)))
	mux.Handle("POST /api/v1/agent/channel/actions/preview", s.withAgentCapability(http.HandlerFunc(s.previewAgentChannelAction)))
	mux.Handle("POST /api/v1/agent/channel/handoffs", s.withAgentCapability(http.HandlerFunc(s.createAgentChannelHandoff)))
	mux.Handle("GET /api/v1/agent/channel/contacts", s.withAgentCapability(http.HandlerFunc(s.agentTaskChannelContacts)))
	mux.Handle("POST /api/v1/agent/channel/messages", s.withAgentCapability(http.HandlerFunc(s.sendAgentTaskChannelMessage)))
	mux.Handle("POST /api/v1/agent/channel/replies", s.withAgentCapability(http.HandlerFunc(s.sendAgentChannelReply)))
	mux.Handle("POST /api/v1/agent/channel/mcp", s.withAgentCapability(http.HandlerFunc(s.agentChannelMCP)))
	mux.Handle("POST /api/v1/agent/core/mcp", s.withAgentCapability(http.HandlerFunc(s.agentCoreMCP)))
	mux.Handle("POST /api/v1/agent/knowledge/mcp", s.withAgentCapability(http.HandlerFunc(s.agentKnowledgeMCP)))
	mux.Handle("POST /api/v1/agent/workspace/mcp", s.withAgentCapability(http.HandlerFunc(s.agentWorkspaceMCP)))
	mux.Handle("POST /api/v1/agent/collab/mcp", s.withAgentCapability(http.HandlerFunc(s.agentCollabMCP)))
	mux.Handle("POST /api/v1/agent/browser/mcp", s.withAgentCapability(http.HandlerFunc(s.agentBrowserMCP)))
	mux.Handle("POST /api/v1/agent/hardware/mcp", s.withAgentCapability(http.HandlerFunc(s.agentHardwareMCP)))
	mux.Handle("POST /api/v1/agent/desktop/mcp", s.withAgentCapability(http.HandlerFunc(s.agentDesktopMCP)))
	mux.Handle("POST /api/v1/agent/desktop/stop", s.withAgentCapability(http.HandlerFunc(s.agentDesktopStop)))
	mux.Handle("GET /api/v1/agent/mail/accounts", s.withAgentCapability(http.HandlerFunc(s.agentMailAccounts)))
	mux.Handle("GET /api/v1/agent/mail/templates", s.withAgentCapability(http.HandlerFunc(s.agentMailTemplates)))
	mux.Handle("GET /api/v1/agent/mail/templates/{id}", s.withAgentCapability(http.HandlerFunc(s.agentMailTemplateDetail)))
	mux.Handle("POST /api/v1/agent/mail/preview", s.withAgentCapability(http.HandlerFunc(s.agentMailPreview)))
	mux.Handle("POST /api/v1/agent/mail/messages", s.withAgentCapability(http.HandlerFunc(s.agentMailSend)))
	mux.Handle("GET /api/v1/agent/mail/messages/{id}", s.withAgentCapability(http.HandlerFunc(s.agentMailMessageStatus)))
	mux.Handle("POST /api/v1/agent/mail/mcp", s.withAgentCapability(http.HandlerFunc(s.agentMailMCP)))
	mux.Handle("POST /api/v1/agent/tasks", s.withAgentCapability(http.HandlerFunc(s.createAgentTask)))
	mux.Handle("GET /api/v1/agent/tasks/{task}", s.withAgentCapability(http.HandlerFunc(s.agentTaskStatus)))
```

```go
func (s *Server) registerDesktopRoutes(mux *http.ServeMux) {
	prefix := "/api/v1/tasks/{id}/desktop"
	for _, route := range []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"GET " + prefix, s.desktopStatus},
		{"GET " + prefix + "/windows", s.desktopWindows},
		{"GET " + prefix + "/targets", s.desktopTargets},
		{"POST " + prefix + "/session", s.desktopOpen},
		{"POST " + prefix + "/control", s.desktopControl},
		{"POST " + prefix + "/stop", s.desktopStop},
		{"POST " + prefix + "/switch", s.desktopSwitch},
		{"GET " + prefix + "/observation", s.desktopObserve},
		{"GET " + prefix + "/stream", s.desktopStream},
		{"POST " + prefix + "/actions", s.desktopAct},
	} {
		mux.Handle(route.pattern, s.withAuth(route.handler))
	}
	for _, route := range []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"GET /api/v1/agent/desktop", s.desktopStatus},
		{"GET /api/v1/agent/desktop/observation", s.desktopObserve},
		{"POST /api/v1/agent/desktop/control", s.desktopControl},
		{"POST /api/v1/agent/desktop/actions", s.desktopAct},
	} {
		mux.Handle(route.pattern, s.withAgentCapability(route.handler))
	}
}
```

### 注册常量与操作 context 类型

```go
const (
	waitAgentInboxRoute         = "POST /api/v1/agent/turn/inbox/wait"
	waitAgentCollaborationRoute = "POST /api/v1/agent/collaboration/batches/{batch}/wait"
	agentADBExecRoute           = "POST /api/v1/agent/hardware/{hardware}/adb/exec"
)
type agentOperationContextKey struct{}
```
