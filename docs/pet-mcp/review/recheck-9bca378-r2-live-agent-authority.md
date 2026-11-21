# 复审 M2：MCP 与直连 HTTP 共用实时 Agent 授权

统一固定源码：`9bca3785b3ef1d071faed06041aeb2e6de0139a7`，tree `5d2a491a18fe4df4c9c348c0e491aba87a8537ba`。问题 diff 边界为 `9d01cb63cca51bff23c40dad2c61d55497f2cd3a..9bca3785b3ef1d071faed06041aeb2e6de0139a7` 中下列完整文件；门禁 diff 保留 3 行上下文。所有源码摘录也从该固定提交读取，不来自正在编辑的 worktree。

认证后的普通 Agent HTTP 资源族先调用 AgentDomainCallContext；MCP 根 handler 使用同一个服务层边界，写分发还会检查当前 Main/snapshot/宠物权限，硬件最终目标也重查。普通 Task 不套宠物管理开关；普通非 Main 保留共享资源读取，设备/进程/桌面写必须 Main。展示进度的旧 HTTP 协作者能力不扩成资源写能力。统一的是实时 Turn、角色、只读、渠道和权限，不把有无活跃 grant 的注入条件冒充授权：合法普通 Agent 的空浏览器状态仍可读，grant 寻址仍由原 service 报 revoked/forbidden，不能执行失效 grant。旧 HTTP fixture 改为真实持久 Turn 和实际 App，保留原状态/错误断言，没有削弱为接受任意拒绝。Task.ReadOnly 是远端镜像等视图元数据，SQLite tasks 没有该列：本批以策略/注入单测验证该标志，并用真实只读 snapshot 的 HTTP/owner 对照验证实际入口，不伪造数据库字段。

固定提交的 20 个具名定向回归全部执行且退出 0（136.834s，显式核对每个顶层 Test 的 pass，missing=0）；`go vet ./internal/app ./internal/httpapi ./internal/hardware` 退出 0（1.176s）。完整命令和逐项覆盖在 `recheck-9bca378-validation.md`。固定提交的 app/httpapi 全包尚未完成，当前排队；旧 worktree 全包运行期间源码有改动，只作诊断，不是这个提交的通过证据。不能据此合并或声称完整 task-722 已完成。

完整 diff 文件边界：

- `internal/app/agent_domain_authorization.go`
- `internal/app/agent_hardware_metadata_test.go`
- `internal/httpapi/agent_domain_authorization.go`
- `internal/httpapi/helpers.go`
- `internal/httpapi/agent_browser_mcp.go`
- `internal/httpapi/agent_desktop_mcp.go`
- `internal/httpapi/agent_hardware_mcp.go`
- `internal/httpapi/agent_workspace_mcp.go`
- `internal/httpapi/agent_handlers_test.go`
- `internal/httpapi/browser_handlers_test.go`
- `internal/httpapi/agent_mcp_operation.go`

## 完整调用链依赖源码

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

### `internal/httpapi/agent_handlers.go` · `bindAgentTask`

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

### `internal/httpapi/agent_hardware_mcp.go` · `agentHardwareMCP`

```go
func (s *Server) agentHardwareMCP(writer http.ResponseWriter, request *http.Request) {
	if agentCoreMCPForwarded(request) {
		writeError(writer, http.StatusForbidden, "agent_hardware_mcp_forwarded")
		return
	}
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, agentHardwareMCPMaxRequestBytes))
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
	call, err := s.app.AgentDomainCallContext(request.Context(), claims, "hardware", false)
	if err != nil {
		writeAgentControlError(writer, err)
		return
	}
	if !s.app.AgentHardwareMCPAllowed(request.Context(), call) {
		writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
		return
	}
	// The id is echoed into every JSON-RPC response. Reject an oversized one
	// before any tool work with a bounded error that does not echo it.
	if len(payload.ID) > agentHardwareMCPMaxRequestIDBytes {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": nil,
			"error": map[string]any{"code": -32600, "message": "request id too large"},
		})
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
			"serverInfo":      map[string]any{"name": "aha2-hardware", "version": "1.0.0"},
		})
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		writer.WriteHeader(http.StatusAccepted)
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		respond(map[string]any{"tools": s.hardwareMCPToolsFor(s.app.AgentHardwareMCPWriteAllowed(request.Context(), call))})
	case "tools/call":
		s.callAgentHardwareMCPTool(writer, request, call, payload)
	default:
		if strings.HasPrefix(payload.Method, "notifications/") {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		respondError(-32601, "method not found")
	}
}
```

### `internal/httpapi/agent_hardware_mcp.go` · `callAgentHardwareMCPTool`

```go
func (s *Server) callAgentHardwareMCPTool(writer http.ResponseWriter, request *http.Request, call app.AgentCallContext, payload mcpRequest) {
	respond := func(result any) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID, "result": result,
		})
	}
	var input struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	// hardwareMCPError is a stable classification with a fixed public message.
	// Store errors, host details and terminal output stay in the server log.
	hardwareMCPError := func(code, message string) {
		respond(map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": code + ": " + message}},
		})
	}
	toolError := func(err error) {
		var operation *agentOperationError
		if errors.As(err, &operation) && operation.Status == http.StatusForbidden {
			writeError(writer, http.StatusForbidden, operation.Code)
			return
		}
		code, message := hardwareMCPErrorCode(err)
		// Only the fixed classification is safe to log: store and host errors
		// can carry terminal content, device paths or credentials.
		log.Printf("agent hardware mcp tool %s failed: %s", input.Name, code)
		hardwareMCPError(code, message)
	}
	invalidToolError := func(message string) {
		respond(map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": "invalid_arguments: " + message}},
		})
	}
	respondJSON := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			toolError(errors.New("result could not be encoded"))
			return
		}
		respond(map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(data)}},
		})
	}
	fail := func(err error) {
		switch {
		case errors.Is(err, app.ErrAgentCallForbidden):
			writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
		case errors.Is(err, app.ErrAgentTurnInactive):
			writeError(writer, http.StatusConflict, "agent_turn_inactive")
		default:
			toolError(err)
		}
	}

	if err := json.Unmarshal(payload.Params, &input); err != nil || strings.TrimSpace(input.Name) == "" {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "invalid tool call"},
		})
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Arguments) == 0 {
		input.Arguments = json.RawMessage(`{}`)
	}
	if !app.HardwareMCPToolIsKnown(input.Name) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
		return
	}
	auditResult := "ok"
	started := time.Now()
	auditIDs := map[string]any{}
	defer func() {
		data := map[string]any{
			"tool": input.Name, "turn_id": call.Turn.ID, "result": auditResult,
			"duration_ms": time.Since(started).Milliseconds(), "argument_keys": coreMCPArgumentKeys(input.Arguments),
		}
		for key, value := range auditIDs {
			data[key] = value
		}
		s.audit(request, "agent.hardware_mcp.call", "task", call.Task.ID, data)
	}()

	if !app.HardwareMCPToolIsRead(input.Name) {
		if !s.app.AgentHardwareMCPWriteAllowed(request.Context(), call) {
			auditResult = "denied"
			writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
			return
		}
		for _, operation := range s.hardwareMCPOperations() {
			if operation.Tool.Name != input.Name {
				continue
			}
			result, err := runAgentMCPOperation(request, operation, input.Arguments)
			if err != nil {
				auditResult = "error"
				toolError(err)
				return
			}
			respondJSON(result)
			return
		}
	}

	switch input.Name {
	case "hardware_list":
		if err := decodeCoreToolArguments(input.Arguments, &struct{}{}); err != nil {
			auditResult = "invalid"
			invalidToolError("hardware_list takes no arguments")
			return
		}
		result, err := s.agentHardwareMCPList(request.Context(), call)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(result)

	case "hardware_terminal_read":
		var args struct {
			HardwareID string `json:"hardware_id"`
			Transport  string `json:"transport"`
			After      int64  `json:"after"`
			Limit      int    `json:"limit"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			invalidToolError("hardware_terminal_read accepts hardware_id, transport, after and limit")
			return
		}
		hardwareID := strings.TrimSpace(args.HardwareID)
		if !agentHardwareMCPIDPattern.MatchString(hardwareID) {
			auditResult = "invalid"
			invalidToolError("hardware_id must be a bound hardware ID")
			return
		}
		transport := strings.TrimSpace(args.Transport)
		if transport != "" && transport != domain.HardwareTransportSerial && transport != domain.HardwareTransportNetwork {
			auditResult = "invalid"
			invalidToolError("transport must be serial or network")
			return
		}
		if args.After < 0 {
			auditResult = "invalid"
			invalidToolError("after must be a non-negative cursor")
			return
		}
		limit := args.Limit
		if limit == 0 {
			limit = agentHardwareMCPDefaultLimit
		}
		if limit < 1 || limit > agentHardwareMCPMaxLimit {
			auditResult = "invalid"
			invalidToolError("limit must be between 1 and 1000")
			return
		}
		result, canonicalID, err := s.agentHardwareMCPTerminalRead(request.Context(), call, payload.ID, hardwareID, transport, args.After, limit)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		auditIDs["hardware_id"] = canonicalID
		respondJSON(result)

	default:
		auditResult = "invalid"
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
	}
}
```

### `internal/app/agent_workspace_mcp.go` · `AgentWorkspaceMCPAllowed`

```go
func (s *Service) AgentWorkspaceMCPAllowed(ctx context.Context, call AgentCallContext) bool {

	return s.workspaceMCPInjected(ctx, call.Task, call.Turn.InboxBatchID)
}
```

### `internal/app/agent_workspace_mcp.go` · `AgentWorkspaceMCPWriteAllowed`

```go
func (s *Service) AgentWorkspaceMCPWriteAllowed(ctx context.Context, call AgentCallContext) bool {
	if !s.AgentWorkspaceMCPAllowed(ctx, call) || !s.hardwareMCPSnapshotWritable(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID) {
		return false
	}
	workspace, err := s.store.Workspace(ctx, call.Task.WorkspaceID)
	return err == nil && !workspace.ReadOnly
}
```

### `internal/app/agent_browser_mcp.go` · `AgentBrowserMCPAllowed`

```go
func (s *Service) AgentBrowserMCPAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.browserMCPInjected(ctx, call.Task, call.Turn.InboxBatchID)
}
```

### `internal/app/agent_browser_mcp.go` · `AgentBrowserMCPWriteAllowed`

```go
func (s *Service) AgentBrowserMCPWriteAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.AgentBrowserMCPAllowed(ctx, call) && s.hardwareMCPSnapshotWritable(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
}
```

### `internal/app/agent_desktop_mcp.go` · `AgentDesktopMCPAllowed`

```go
func (s *Service) AgentDesktopMCPAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.desktopMCPInjected(ctx, call.Task, call.Turn.AgentID, call.Turn.InboxBatchID)
}
```

### `internal/app/agent_desktop_mcp.go` · `AgentDesktopMCPWriteAllowed`

```go
func (s *Service) AgentDesktopMCPWriteAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.AgentDesktopMCPAllowed(ctx, call) && s.hardwareMCPSnapshotWritable(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
}
```

### `internal/agentapi/capability.go` · `Authenticate`

```go
func (c *Capabilities) Authenticate(token string) (Claims, error) {
	if token == "" {
		return Claims{}, ErrInvalidCapability
	}
	hash := sha256.Sum256([]byte(token))
	c.mu.Lock()
	defer c.mu.Unlock()
	claims, ok := c.tokens[hash]
	if !ok || !claims.ExpiresAt.After(c.now().UTC()) {
		delete(c.tokens, hash)
		return Claims{}, ErrInvalidCapability
	}
	return claims, nil
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

### `internal/httpapi/agent_mcp_operation.go` · `validateMCPOperation`

```go
func validateMCPOperation(value any, schema map[string]any) error {
	fail := errors.New("invalid tool arguments")
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fail
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			return fail
		}
		for key, child := range object {
			spec, ok := properties[key].(map[string]any)
			if !ok {
				spec, ok = schema["additionalProperties"].(map[string]any)
				if !ok {
					return fail
				}
			}
			if err := validateMCPOperation(child, spec); err != nil {
				return fail
			}
		}
		if max, ok := schema["maxProperties"].(int); ok && len(object) > max {
			return fail
		}
		for _, key := range schema["required"].([]string) {
			if _, ok := object[key]; !ok {
				return fail
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fail
		}
		if min, ok := schema["minLength"].(int); ok && len([]rune(text)) < min {
			return fail
		}
		if max, ok := schema["maxLength"].(int); ok && len([]rune(text)) > max {
			return fail
		}
		if values, ok := schema["enum"].([]string); ok {
			found := false
			for _, item := range values {
				if text == item {
					found = true
				}
			}
			if !found {
				return fail
			}
		}
	case "integer", "number":
		number, ok := value.(json.Number)
		if !ok {
			return fail
		}
		var n float64
		if schema["type"] == "integer" {
			i, err := number.Int64()
			if err != nil {
				return fail
			}
			n = float64(i)
		} else {
			var err error
			n, err = number.Float64()
			if err != nil {
				return fail
			}
		}
		if min, ok := schema["minimum"].(int); ok && n < float64(min) {
			return fail
		}
		if max, ok := schema["maximum"].(int); ok && n > float64(max) {
			return fail
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fail
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fail
		}
		if min, ok := schema["minItems"].(int); ok && len(items) < min {
			return fail
		}
		if max, ok := schema["maxItems"].(int); ok && len(items) > max {
			return fail
		}
		child, ok := schema["items"].(map[string]any)
		if !ok {
			return fail
		}
		for _, item := range items {
			if validateMCPOperation(item, child) != nil {
				return fail
			}
		}
	default:
		return fail
	}
	return nil
}
```

### 对应注册声明（完整硬件/资源族条目）

```go
	mux.Handle("GET /api/v1/hardware/serial-ports", s.withAuth(http.HandlerFunc(s.hardwareSerialPorts)))
	mux.Handle("GET /api/v1/tasks/{id}/hardware/{hardware}/terminal", s.withAuth(http.HandlerFunc(s.hardwareTerminal)))
	mux.Handle("GET /api/v1/tasks/{id}/hardware/{hardware}/terminal/ws", s.withAuth(http.HandlerFunc(s.hardwareTerminalWebSocket)))
	mux.Handle("POST /api/v1/tasks/{id}/hardware/{hardware}/connect", s.withAuth(http.HandlerFunc(s.connectHardware)))
	mux.Handle("PUT /api/v1/tasks/{id}/hardware-bindings", s.withAuth(http.HandlerFunc(s.updateTaskHardwareBindings)))
	mux.Handle("GET /api/v1/tasks/{id}/hardware/{hardware}/host-key", s.withAuth(http.HandlerFunc(s.hardwareSSHHostKey)))
	mux.Handle("POST /api/v1/tasks/{id}/hardware/{hardware}/host-key/trust", s.withAuth(http.HandlerFunc(s.trustHardwareSSHHostKey)))
	mux.Handle("POST /api/v1/tasks/{id}/hardware/{hardware}/disconnect", s.withAuth(http.HandlerFunc(s.disconnectHardware)))
	mux.Handle("POST /api/v1/tasks/{id}/hardware/{hardware}/send", s.withAuth(http.HandlerFunc(s.sendHardware)))
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
	mux.Handle("GET /api/v1/agent/project/workspaces", s.withAgentCapability(http.HandlerFunc(s.agentProjectWorkspaces)))
	mux.Handle("GET /api/v1/agent/project/runtimes", s.withAgentCapability(http.HandlerFunc(s.agentProjectRuntimes)))
	mux.Handle("POST /api/v1/agent/browser/mcp", s.withAgentCapability(http.HandlerFunc(s.agentBrowserMCP)))
	mux.Handle("POST /api/v1/agent/hardware/mcp", s.withAgentCapability(http.HandlerFunc(s.agentHardwareMCP)))
	mux.Handle("POST /api/v1/agent/desktop/mcp", s.withAgentCapability(http.HandlerFunc(s.agentDesktopMCP)))
	mux.Handle("POST /api/v1/agent/desktop/stop", s.withAgentCapability(http.HandlerFunc(s.agentDesktopStop)))
```

```go
const (
	agentADBExecRoute           = "POST /api/v1/agent/hardware/{hardware}/adb/exec"
)
```

## 本问题边界的完整 diff

```diff
diff --git a/internal/app/agent_domain_authorization.go b/internal/app/agent_domain_authorization.go
new file mode 100644
index 0000000..681883d
--- /dev/null
+++ b/internal/app/agent_domain_authorization.go
@@ -0,0 +1,54 @@
+package app
+
+import (
+	"context"
+	"github.com/ChinaKai/AHA2/internal/agentapi"
+)
+
+// AgentDomainCallContext is the authorization boundary shared by the Agent
+// HTTP routes and their MCP facades. A valid token alone is not a live grant.
+// Ordinary collaborators retain domain reads; device/process/control writes
+// require Main and an explicit writable snapshot. Pet permissions apply only
+// to the genuine pet Task, never to ordinary Tasks.
+func (s *Service) AgentDomainCallContext(ctx context.Context, claims agentapi.Claims, domainName string, write bool) (AgentCallContext, error) {
+	call, err := s.AgentCallContext(ctx, claims, false)
+	if err != nil {
+		return AgentCallContext{}, err
+	}
+	allowed := false
+	// Discovery additionally needs a binding/active session/backend. Status
+	// reads can safely report an empty or unsupported state; grant-addressed
+	// handlers still enforce the actual binding, grant status and capability.
+	// Never replace their resource authorization with tools/list visibility.
+	readAllowed := s.petDomainReadable(ctx, call.Task) && !s.agentMCPChannelBound(ctx, call.Task.ID, call.Turn.InboxBatchID)
+	writeAllowed := false
+	if write {
+		writeAllowed = readAllowed && s.hardwareMCPSnapshotWritable(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
+	}
+	switch domainName {
+	case "hardware":
+		allowed = readAllowed
+		if write {
+			allowed = s.AgentHardwareMCPWriteAllowed(ctx, call)
+		}
+	case "browser":
+		allowed = readAllowed
+		if write {
+			allowed = writeAllowed
+		}
+	case "workspace":
+		allowed = s.AgentWorkspaceMCPAllowed(ctx, call)
+		if write {
+			allowed = s.AgentWorkspaceMCPWriteAllowed(ctx, call)
+		}
+	case "desktop":
+		allowed = readAllowed && call.Turn.AgentID == "main"
+		if write {
+			allowed = writeAllowed
+		}
+	}
+	if !allowed {
+		return AgentCallContext{}, ErrAgentCallForbidden
+	}
+	return call, nil
+}
diff --git a/internal/app/agent_hardware_metadata_test.go b/internal/app/agent_hardware_metadata_test.go
new file mode 100644
index 0000000..097ebba
--- /dev/null
+++ b/internal/app/agent_hardware_metadata_test.go
@@ -0,0 +1,23 @@
+package app
+
+import (
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"testing"
+)
+
+func TestHardwarePolicyReadOnlyTaskMetadataKeepsReadsAndDeniesWrites(t *testing.T) {
+	ctx, _, service, task, _ := newStreamCompatFixture(t)
+	seedHardwareMCPGroup(t, ctx, service.store, task)
+	call := AgentCallContext{Task: task, Project: domain.Project{ID: task.ProjectID}, Turn: domain.Turn{AgentID: "main", RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID}}
+	if !service.AgentHardwareMCPWriteAllowed(ctx, call) {
+		t.Fatal("fixture writable Main was denied")
+	}
+	call.Task.ReadOnly = true
+	if !service.AgentHardwareMCPAllowed(ctx, call) || service.AgentHardwareMCPWriteAllowed(ctx, call) {
+		t.Fatal("read-only Task metadata did not preserve read/write separation")
+	}
+	_, allowed := service.mcpRuntimeForTurn(ctx, call.Task, " main ", "http://127.0.0.1:8766", "")
+	if len(mcpDomainTools(allowed, hardwareMCPName)) != 2 {
+		t.Fatal("read-only Task metadata received device I/O tools")
+	}
+}
diff --git a/internal/httpapi/agent_browser_mcp.go b/internal/httpapi/agent_browser_mcp.go
index 6321678..7d99d84 100644
--- a/internal/httpapi/agent_browser_mcp.go
+++ b/internal/httpapi/agent_browser_mcp.go
@@ -64,7 +64,7 @@ func (s *Server) agentBrowserMCP(writer http.ResponseWriter, request *http.Reque
 		writeError(writer, http.StatusBadRequest, "invalid_json")
 		return
 	}
-	call, err := s.app.AgentCallContext(request.Context(), claims, false)
+	call, err := s.app.AgentDomainCallContext(request.Context(), claims, "browser", false)
 	if err != nil {
 		writeAgentControlError(writer, err)
 		return
@@ -459,6 +459,10 @@ func (s *Server) agentBrowserMCPGrant(ctx context.Context, taskID, grantID strin
 // The message is browser.PublicMessage, which already hides host diagnostics;
 // page text, element names, titles and URLs never enter an error.
 func browserMCPErrorCode(err error) (string, string) {
+	var operation *agentOperationError
+	if errors.As(err, &operation) {
+		return operation.Code, operation.Code
+	}
 	var typed *browser.Error
 	if errors.As(err, &typed) {
 		code := strings.TrimSpace(typed.Code)
diff --git a/internal/httpapi/agent_desktop_mcp.go b/internal/httpapi/agent_desktop_mcp.go
index 4af7ff3..029cbc9 100644
--- a/internal/httpapi/agent_desktop_mcp.go
+++ b/internal/httpapi/agent_desktop_mcp.go
@@ -57,7 +57,7 @@ func (s *Server) agentDesktopMCP(writer http.ResponseWriter, request *http.Reque
 		writeError(writer, http.StatusUnauthorized, "agent_capability_required")
 		return
 	}
-	call, err := s.app.AgentCallContext(request.Context(), claims, false)
+	call, err := s.app.AgentDomainCallContext(request.Context(), claims, "desktop", false)
 	if err != nil {
 		writeAgentControlError(writer, err)
 		return
diff --git a/internal/httpapi/agent_domain_authorization.go b/internal/httpapi/agent_domain_authorization.go
new file mode 100644
index 0000000..b1893a2
--- /dev/null
+++ b/internal/httpapi/agent_domain_authorization.go
@@ -0,0 +1,38 @@
+package httpapi
+
+import (
+	"net/http"
+	"strings"
+
+	"github.com/ChinaKai/AHA2/internal/agentapi"
+)
+
+// authorizeAgentDomainRoute runs after token authentication. Its finite path
+// families are the existing Agent APIs, never owner Web routes. MCP dispatch
+// determines read/write from the named tool and uses the same app boundary.
+func (s *Server) authorizeAgentDomainRoute(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims) bool {
+	path := request.URL.Path
+	if strings.HasSuffix(path, "/mcp") {
+		return true
+	}
+	domainName := ""
+	for prefix, name := range map[string]string{"/api/v1/agent/hardware": "hardware", "/api/v1/agent/browser": "browser", "/api/v1/agent/processes": "workspace", "/api/v1/agent/project/workspaces": "workspace", "/api/v1/agent/project/runtimes": "workspace", "/api/v1/agent/desktop": "desktop"} {
+		if path == prefix || strings.HasPrefix(path, prefix+"/") {
+			domainName = name
+			break
+		}
+	}
+	if domainName == "" {
+		return true
+	}
+	if s.app == nil {
+		writeError(writer, http.StatusServiceUnavailable, "agent_api_unavailable")
+		return false
+	}
+	_, err := s.app.AgentDomainCallContext(request.Context(), claims, domainName, request.Method != http.MethodGet)
+	if err != nil {
+		writeAgentControlError(writer, err)
+		return false
+	}
+	return true
+}
diff --git a/internal/httpapi/agent_handlers_test.go b/internal/httpapi/agent_handlers_test.go
index bc2abb6..71edfb3 100644
--- a/internal/httpapi/agent_handlers_test.go
+++ b/internal/httpapi/agent_handlers_test.go
@@ -72,10 +72,11 @@ func TestAgentAPIUsesTurnCapabilityAndTaskScope(t *testing.T) {
 	processes := managedprocess.NewManager()
 	defer processes.Close()
 	server := httptest.NewServer(New(Config{
-		Store: database, AgentCapabilities: capabilities, ManagedProcesses: processes,
+		Store: database, App: app.NewService(database, nil, app.StubExecutor{}), AgentCapabilities: capabilities, ManagedProcesses: processes,
 	}).Handler())
 	defer server.Close()
-	token, err := capabilities.Issue(task.ID, "main", "turn-1", time.Hour)
+	turn := createCoreTurn(t, database, task, "main", domain.NewID("inbox_batch"))
+	token, err := capabilities.Issue(task.ID, "main", turn.ID, time.Hour)
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -128,7 +129,7 @@ func TestAgentADBExecUsesTaskDeviceAndReturnsBinary(t *testing.T) {
 	}
 	capabilities := agentapi.NewCapabilities()
 	server := httptest.NewServer(New(Config{
-		Store: database, AgentCapabilities: capabilities,
+		Store: database, App: app.NewService(database, nil, app.StubExecutor{}), AgentCapabilities: capabilities,
 		ExecuteADB: func(_ context.Context, endpoint string, args []string, input []byte, timeout time.Duration) (hardware.ADBExecResult, error) {
 			if endpoint != "192.0.2.90:5555" {
 				t.Fatalf("unexpected ADB endpoint %q", endpoint)
@@ -144,7 +145,8 @@ func TestAgentADBExecUsesTaskDeviceAndReturnsBinary(t *testing.T) {
 		},
 	}).Handler())
 	defer server.Close()
-	token, err := capabilities.Issue(task.ID, "main", "turn-adb-exec", time.Hour)
+	turn := createCoreTurn(t, database, task, "main", domain.NewID("inbox_batch"))
+	token, err := capabilities.Issue(task.ID, "main", turn.ID, time.Hour)
 	if err != nil {
 		t.Fatal(err)
 	}
diff --git a/internal/httpapi/agent_hardware_mcp.go b/internal/httpapi/agent_hardware_mcp.go
index efb680c..7eca953 100644
--- a/internal/httpapi/agent_hardware_mcp.go
+++ b/internal/httpapi/agent_hardware_mcp.go
@@ -76,7 +76,7 @@ func (s *Server) agentHardwareMCP(writer http.ResponseWriter, request *http.Requ
 		writeError(writer, http.StatusBadRequest, "invalid_json")
 		return
 	}
-	call, err := s.app.AgentCallContext(request.Context(), claims, false)
+	call, err := s.app.AgentDomainCallContext(request.Context(), claims, "hardware", false)
 	if err != nil {
 		writeAgentControlError(writer, err)
 		return
@@ -148,6 +148,11 @@ func (s *Server) callAgentHardwareMCPTool(writer http.ResponseWriter, request *h
 		})
 	}
 	toolError := func(err error) {
+		var operation *agentOperationError
+		if errors.As(err, &operation) && operation.Status == http.StatusForbidden {
+			writeError(writer, http.StatusForbidden, operation.Code)
+			return
+		}
 		code, message := hardwareMCPErrorCode(err)
 		// Only the fixed classification is safe to log: store and host errors
 		// can carry terminal content, device paths or credentials.
@@ -556,6 +561,10 @@ func (e *hardwareMCPError) Error() string {
 }
 
 func hardwareMCPErrorCode(err error) (string, string) {
+	var operation *agentOperationError
+	if errors.As(err, &operation) {
+		return operation.Code, operation.Code
+	}
 	if errors.Is(err, errHardwareMCPResponseTooLarge) {
 		return "hardware_response_too_large", "response exceeds the size budget"
 	}
diff --git a/internal/httpapi/agent_mcp_operation.go b/internal/httpapi/agent_mcp_operation.go
index 924b044..d674638 100644
--- a/internal/httpapi/agent_mcp_operation.go
+++ b/internal/httpapi/agent_mcp_operation.go
@@ -2,6 +2,7 @@ package httpapi
 
 import (
 	"bytes"
+	"context"
 	"encoding/base64"
 	"encoding/json"
 	"errors"
@@ -25,6 +26,29 @@ type agentMCPOperation struct {
 	Binary       bool
 }
 
+type agentOperationContextKey struct{}
+type agentOperationError struct {
+	Status int
+	Code   string
+}
+
+func (e *agentOperationError) Error() string { return e.Code }
+
+// Codes are copied only from this fixed vocabulary, never from an upstream
+// message, response header or arbitrary provider error.
+func safeAgentOperationCode(body []byte) string {
+	var failure struct {
+		Error string `json:"error"`
+	}
+	_ = json.Unmarshal(body, &failure)
+	switch failure.Error {
+	case "hardware_read_only", "hardware_runtime_unavailable", "hardware_group_not_found", "hardware_transport_invalid", "hardware_connect_failed", "hardware_send_failed", "hardware_login_failed", "ssh_host_key_unknown", "adb_exec_unavailable", "adb_command_not_allowed", "adb_args_invalid", "adb_stdin_invalid", "adb_timeout_invalid", "adb_exec_failed", "managed_process_not_found", "managed_process_already_running", "managed_process_start_failed", "browser_capability_denied", "browser_grant_forbidden", "browser_grant_revoked", "browser_grant_expired", "browser_tab_not_found", "agent_operation_forbidden", "agent_turn_inactive", "task_read_only":
+		return failure.Error
+	default:
+		return "operation_failed"
+	}
+}
+
 func mcpObject(properties map[string]any, required ...string) map[string]any {
 	if required == nil {
 		required = []string{}
@@ -193,7 +217,7 @@ func runAgentMCPOperation(request *http.Request, op agentMCPOperation, raw json.
 	if decoder.Decode(new(any)) != io.EOF {
 		return nil, errors.New("invalid tool arguments")
 	}
-	child := request.Clone(request.Context())
+	child := request.Clone(context.WithValue(request.Context(), agentOperationContextKey{}, op.Tool.Name))
 	child.Method = op.Method
 	child.Header = make(http.Header)
 	child.URL = &url.URL{Path: request.URL.Path}
@@ -233,7 +257,7 @@ func runAgentMCPOperation(request *http.Request, op agentMCPOperation, raw json.
 	// Raw runtime errors can contain endpoints or credentials. Only a fixed
 	// classification leaves this boundary, and no body is logged here.
 	if response.status >= 400 {
-		return nil, errors.New("operation failed; check resource permissions and state")
+		return nil, &agentOperationError{Status: response.status, Code: safeAgentOperationCode(response.body.Bytes())}
 	}
 	if response.overflow {
 		return nil, errors.New("operation response exceeds limit")
diff --git a/internal/httpapi/agent_workspace_mcp.go b/internal/httpapi/agent_workspace_mcp.go
index 0623ea3..2815bcf 100644
--- a/internal/httpapi/agent_workspace_mcp.go
+++ b/internal/httpapi/agent_workspace_mcp.go
@@ -55,7 +55,7 @@ func (s *Server) agentWorkspaceMCP(writer http.ResponseWriter, request *http.Req
 		writeError(writer, http.StatusBadRequest, "invalid_json")
 		return
 	}
-	call, err := s.app.AgentCallContext(request.Context(), claims, false)
+	call, err := s.app.AgentDomainCallContext(request.Context(), claims, "workspace", false)
 	if err != nil {
 		writeAgentControlError(writer, err)
 		return
diff --git a/internal/httpapi/browser_handlers_test.go b/internal/httpapi/browser_handlers_test.go
index 6920547..1fa8e85 100644
--- a/internal/httpapi/browser_handlers_test.go
+++ b/internal/httpapi/browser_handlers_test.go
@@ -3,6 +3,7 @@ package httpapi
 import (
 	"bytes"
 	"context"
+	"database/sql"
 	"encoding/json"
 	"errors"
 	"io"
@@ -292,7 +293,17 @@ func TestBrowserAPINeverReturnsHostDiagnosticPaths(t *testing.T) {
 
 func (f *browserAPIFixture) agentToken(t *testing.T, taskID string) string {
 	t.Helper()
-	token, err := f.agents.Issue(taskID, "main", "turn-1", time.Hour)
+	turn, err := f.store.ActiveTurnForAgent(context.Background(), taskID, "main")
+	if errors.Is(err, sql.ErrNoRows) {
+		task, readErr := f.store.Task(context.Background(), taskID)
+		if readErr != nil {
+			t.Fatal(readErr)
+		}
+		turn = createCoreTurn(t, f.store, task, "main", domain.NewID("inbox_batch"))
+	} else if err != nil {
+		t.Fatal(err)
+	}
+	token, err := f.agents.Issue(taskID, "main", turn.ID, time.Hour)
 	if err != nil {
 		t.Fatal(err)
 	}
diff --git a/internal/httpapi/helpers.go b/internal/httpapi/helpers.go
index 62d8f40..5ac4059 100644
--- a/internal/httpapi/helpers.go
+++ b/internal/httpapi/helpers.go
@@ -44,7 +44,11 @@ func (s *Server) withAgentCapability(next http.Handler) http.Handler {
 			return
 		}
 		ctx := context.WithValue(request.Context(), agentClaimsContextKey{}, claims)
-		next.ServeHTTP(writer, request.WithContext(ctx))
+		request = request.WithContext(ctx)
+		if !s.authorizeAgentDomainRoute(writer, request, claims) {
+			return
+		}
+		next.ServeHTTP(writer, request)
 	})
 }
 
```
