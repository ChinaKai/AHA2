# 复审 M3 / S1：资源 read_write 与 owner Web 边界

统一固定源码：`9bca3785b3ef1d071faed06041aeb2e6de0139a7`，tree `5d2a491a18fe4df4c9c348c0e491aba87a8537ba`。问题 diff 边界为 `9d01cb63cca51bff23c40dad2c61d55497f2cd3a..9bca3785b3ef1d071faed06041aeb2e6de0139a7` 中下列完整文件；门禁 diff 保留 3 行上下文。所有源码摘录也从该固定提交读取，不来自正在编辑的 worktree。

Agent connect/disconnect 与 send/login/ADB 一样要求绑定资源 read_write；拒绝发生在 manager 调用前。只读资源的 Agent 新连接、断开已有连接均为 403，且无连接/发送/假 ADB 副作用。task.ReadOnly/snapshot 检查只在 Agent 边界；owner Web 保留原有资源只读与终态策略：owner 可建立只读连接及断开，但只读资源发送仍拒绝。真实 owner cookie/CSRF fixture 与同一只读 snapshot 的 Agent 同时回归。硬件 HTTP 列表对 Agent 只带当前绑定资源，owner Web 列表保留完整资源。此文件中的绑定错误映射属于 S4/S5，定义附后，业务生命周期另由 r4 独立包完整审查。

固定提交的 20 个具名定向回归全部执行且退出 0（136.834s，显式核对每个顶层 Test 的 pass，missing=0）；`go vet ./internal/app ./internal/httpapi ./internal/hardware` 退出 0（1.176s）。完整命令和逐项覆盖在 `recheck-9bca378-validation.md`。固定提交的 app/httpapi 全包尚未完成，当前排队；旧 worktree 全包运行期间源码有改动，只作诊断，不是这个提交的通过证据。不能据此合并或声称完整 task-722 已完成。

完整 diff 文件边界：

- `internal/httpapi/hardware_handlers.go`
- `internal/httpapi/agent_hardware_gate_test.go`

## 完整调用链依赖源码

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

### `internal/httpapi/agent_domain_authorization.go` · `authorizeAgentDomainRoute`

```go
func (s *Server) authorizeAgentDomainRoute(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims) bool {
	path := request.URL.Path
	if strings.HasSuffix(path, "/mcp") {
		return true
	}
	domainName := ""
	for prefix, name := range map[string]string{"/api/v1/agent/hardware": "hardware", "/api/v1/agent/browser": "browser", "/api/v1/agent/processes": "workspace", "/api/v1/agent/project/workspaces": "workspace", "/api/v1/agent/project/runtimes": "workspace", "/api/v1/agent/desktop": "desktop"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			domainName = name
			break
		}
	}
	if domainName == "" {
		return true
	}
	if s.app == nil {
		writeError(writer, http.StatusServiceUnavailable, "agent_api_unavailable")
		return false
	}
	_, err := s.app.AgentDomainCallContext(request.Context(), claims, domainName, request.Method != http.MethodGet)
	if err != nil {
		writeAgentControlError(writer, err)
		return false
	}
	return true
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

### `internal/httpapi/hardware_handlers.go` · `connectHardware`

```go
func (s *Server) connectHardware(writer http.ResponseWriter, request *http.Request) {
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if s.hardware == nil {
		writeError(writer, http.StatusServiceUnavailable, "hardware_runtime_unavailable")
		return
	}
	password := ""
	if group.CredentialRef != "" && s.secrets != nil {
		password, _ = s.secrets.Get(group.CredentialRef)
	}
	status, err := s.hardware.Connect(request.Context(), hardware.ConnectRequest{
		TaskID: task.ID, HardwareID: group.ID, Transport: transport, Group: group,
		Password: password, ReadOnly: readOnly,
	})
	if err != nil {
		s.audit(request, "task.hardware.connect_failed", "task", task.ID, map[string]any{
			"hardware_id": group.ID, "transport": transport, "error": "hardware_connect_failed",
		})
		code := "hardware_connect_failed"
		if hardware.IsUnknownSSHHostKey(err) {
			code = "ssh_host_key_unknown"
		}
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": code, "message": err.Error()})
		return
	}
	s.audit(request, "task.hardware.connect", "task", task.ID, map[string]any{
		"hardware_id": group.ID, "transport": transport,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "status": status})
}
```

### `internal/httpapi/hardware_handlers.go` · `disconnectHardware`

```go
func (s *Server) disconnectHardware(writer http.ResponseWriter, request *http.Request) {
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if s.hardware != nil {
		s.hardware.Disconnect(task.ID, group.ID, transport)
	}
	s.audit(request, "task.hardware.disconnect", "task", task.ID, map[string]any{
		"hardware_id": group.ID, "transport": transport,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "status": disconnectedHardwareStatus(task.ID, group.ID, transport, readOnly),
	})
}
```

### `internal/httpapi/hardware_handlers.go` · `sendHardware`

```go
func (s *Server) sendHardware(writer http.ResponseWriter, request *http.Request) {
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if readOnly {
		writeJSON(writer, http.StatusForbidden, map[string]any{
			"ok": false, "error": "hardware_read_only", "message": "当前硬件组或 Task 为只读",
		})
		return
	}
	if s.hardware == nil {
		writeError(writer, http.StatusServiceUnavailable, "hardware_runtime_unavailable")
		return
	}
	var payload struct {
		Data     string `json:"data"`
		Encoding string `json:"encoding"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	source := "web"
	if claims, ok := agentClaimsFromContext(request.Context()); ok {
		source = "agent:" + claims.AgentID
	}
	if err := s.hardware.Send(task.ID, group.ID, transport, payload.Data, payload.Encoding, source); err != nil {
		status := http.StatusConflict
		if errors.Is(err, hardware.ErrReadOnly) {
			status = http.StatusForbidden
		}
		writeJSON(writer, status, map[string]any{
			"ok": false, "error": "hardware_send_failed", "message": err.Error(),
		})
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true})
}
```

### `internal/httpapi/agent_handlers.go` · `agentConnectHardware`

```go
func (s *Server) agentConnectHardware(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	s.connectHardware(writer, request)
}
```

### `internal/httpapi/agent_handlers.go` · `agentDisconnectHardware`

```go
func (s *Server) agentDisconnectHardware(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	s.disconnectHardware(writer, request)
}
```

### `internal/httpapi/agent_handlers.go` · `agentSendHardware`

```go
func (s *Server) agentSendHardware(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	s.sendHardware(writer, request)
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

### `internal/httpapi/agent_hardware_mcp_operations.go` · `hardwareMCPOperations`

```go
func (s *Server) hardwareMCPOperations() []agentMCPOperation {
	base := func(extra map[string]any) map[string]any {
		fields := map[string]any{"hardware_id": mcpString(), "transport": mcpEnum("serial", "network")}
		for key, value := range extra {
			fields[key] = value
		}
		return fields
	}
	operation := func(name, description string, handler http.HandlerFunc, fields map[string]any, required ...string) agentMCPOperation {
		return agentMCPOperation{
			Tool:    mcpTool{Name: name, Description: description, InputSchema: mcpObject(base(fields), append([]string{"hardware_id"}, required...)...)},
			Handler: handler, Method: http.MethodPost, Write: true,
			PathFields: map[string]string{"hardware_id": "hardware"}, QueryFields: []string{"transport"}, ResultFields: []string{"ok"},
		}
	}
	connect := operation("hardware_connect", "Connect a device already bound to this Task using stored credentials. Requires Main and writable resource access. Unknown SSH host keys require owner confirmation in Web. Read connection state with hardware_list.", s.agentConnectHardware, nil)
	disconnect := operation("hardware_disconnect", "Disconnect one bound device transport. Does not change bindings or credentials.", s.agentDisconnectHardware, nil)
	send := operation("hardware_send", "Send data to a bound writable connected device. No credentials are accepted; use hardware_login for stored-credential login. Read output with hardware_terminal_read.", s.agentSendHardware, map[string]any{"data": map[string]any{"type": "string", "minLength": 1, "maxLength": 65536}, "encoding": mcpEnum("text", "base64")}, "data")
	login := operation("hardware_login", "Start stored-credential login on a bound writable device. Credential values are never accepted or returned. Read connection state and terminal output separately.", s.agentLoginHardware, map[string]any{
		"wakeup": map[string]any{"type": "boolean"}, "line_ending": mcpEnum("lf", "crlf", "cr"),
		"timeout_seconds": mcpInteger(1, 120), "retries": mcpInteger(0, 5),
	})
	adb := operation("hardware_adb_exec", "Run a device-scoped ADB command against a bound writable ADB resource. Host commands, pairing, host-key trust and credentials are excluded. Uses the same command validation, timeout and cancellation as the Agent API.", s.agentADBExec, map[string]any{
		"args":         map[string]any{"type": "array", "items": mcpString(), "minItems": 1, "maxItems": 64},
		"stdin_base64": map[string]any{"type": "string", "maxLength": 65536}, "timeout_seconds": mcpInteger(1, 120),
	}, "args")
	adb.ResultFields = []string{"ok", "exit_code", "stdout_base64", "stderr_base64", "duration_ms", "stdout_truncated", "stderr_truncated", "stdout_bytes", "stderr_bytes"}
	return []agentMCPOperation{connect, disconnect, send, login, adb}
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

### 共享 fixture 完整定义

```go
package httpapi

import (
	"context"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"net"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

type hardwareAuthorizationFixture struct {
	h          *coreMCPHarness
	task       domain.Task
	turn       domain.Turn
	token      string
	server     *httptest.Server
	manager    *hardware.Manager
	listener   net.Listener
	accepted   chan net.Conn
	accepts    atomic.Int32
	executions atomic.Int32
	board      domain.HardwareResource
	adb        domain.HardwareResource
	secret     string
}

func newHardwareAuthorizationFixture(t *testing.T, pet bool) *hardwareAuthorizationFixture {
	t.Helper()
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	task := base
	if pet {
		task = createPetDomainHTTPTask(t, h, base)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	f := &hardwareAuthorizationFixture{h: h, task: task, listener: listener, accepted: make(chan net.Conn, 8), secret: "LOGIN-FIXTURE-SECRET-NOT-IN-MODEL"}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			f.accepts.Add(1)
			f.accepted <- conn
		}
	}()
	now := time.Now().UTC()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	f.board = domain.HardwareResource{ID: domain.NewID("resource"), Description: "Loopback fixture", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: host, Port: port, Protocol: domain.HardwareProtocolRaw}, Username: "fixture-user", CredentialRef: "fixture/login", PasswordConfigured: true, Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now}
	f.adb = domain.HardwareResource{ID: domain.NewID("resource"), Description: "Fake ADB", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "192.0.2.24", Port: 5555, Protocol: domain.HardwareProtocolADB}, Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now}
	for _, item := range []domain.HardwareResource{f.board, f.adb} {
		if err := h.database.UpsertHardwareResource(h.ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.database.ReplaceTaskHardwareBindings(h.ctx, task.ID, []domain.TaskHardwareBinding{{TaskID: task.ID, ID: "board", ResourceID: f.board.ID}, {TaskID: task.ID, ID: "device", ResourceID: f.adb.ID}}); err != nil {
		t.Fatal(err)
	}
	f.manager = hardware.NewManager(h.database)
	t.Cleanup(func() { _ = f.manager.Close() })
	secrets := &fakeSecretStore{values: map[string]string{"fixture/login": f.secret}}
	f.server = httptest.NewServer(New(Config{Store: h.database, App: h.service, Auth: auth.NewService(h.database, "setup-test", time.Hour), AgentCapabilities: h.caps, Hardware: f.manager, Secrets: secrets, ExecuteADB: func(ctx context.Context, _ string, _ []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
		if ctx.Err() != nil {
			return hardware.ADBExecResult{}, ctx.Err()
		}
		f.executions.Add(1)
		return hardware.ADBExecResult{Stdout: []byte("fake-adb-result")}, nil
	}}).Handler())
	t.Cleanup(f.server.Close)
	if task.Status == domain.TaskDraft {
		if err := h.database.UpdateTaskStatus(h.ctx, task.ID, task.Status, domain.TaskPreparing, now.Format(time.RFC3339Nano), ""); err != nil {
			t.Fatal(err)
		}
		if err := h.database.UpdateTaskStatus(h.ctx, task.ID, domain.TaskPreparing, domain.TaskActive, now.Format(time.RFC3339Nano), ""); err != nil {
			t.Fatal(err)
		}
	}
	f.task.Status = domain.TaskActive
	if !pet {
		assistant, err := h.database.EnsureOwnerAssistant(h.ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, false, false, false, false, false, false, false, false, now); err != nil {
			t.Fatal(err)
		}
	}

	f.turn = createCoreTurn(t, h.database, task, "main", domain.NewID("inbox_batch"))
	f.token = h.issue(task.ID, "main", f.turn.ID)
	return f
}

func (f *hardwareAuthorizationFixture) mcp(t *testing.T, name string, args map[string]any) coreMCPToolCall {
	t.Helper()
	return coreMCPCallTool(t, f.server.URL+"/api/v1/agent/hardware/mcp", f.token, name, args)
}

func (f *hardwareAuthorizationFixture) connection(t *testing.T) net.Conn {
	t.Helper()
	select {
	case conn := <-f.accepted:
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	case <-time.After(3 * time.Second):
		t.Fatal("fixture did not accept a connection")
		return nil
	}
}

func (f *hardwareAuthorizationFixture) raw(t *testing.T, method, path string, body any) int {
	t.Helper()
	response := agentRequest(t, f.server.URL+"/api/v1/agent/hardware"+path, method, f.token, body)
	defer response.Body.Close()
	return response.StatusCode
}
```

```go
type HardwareBindingError struct{ Code string }

func (e *HardwareBindingError) Error() string { return e.Code }
```

## 本问题边界的完整 diff

```diff
diff --git a/internal/httpapi/agent_hardware_gate_test.go b/internal/httpapi/agent_hardware_gate_test.go
new file mode 100644
index 0000000..2867618
--- /dev/null
+++ b/internal/httpapi/agent_hardware_gate_test.go
@@ -0,0 +1,261 @@
+package httpapi
+
+import (
+	"bufio"
+	"database/sql"
+	"encoding/json"
+	"fmt"
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"net/http"
+	"net/http/cookiejar"
+	"net/http/httptest"
+	"strings"
+	"testing"
+	"time"
+)
+
+func TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects(t *testing.T) {
+	for _, pet := range []bool{false, true} {
+		t.Run(fmt.Sprintf("pet=%v", pet), func(t *testing.T) {
+			f := newHardwareAuthorizationFixture(t, pet)
+			connected := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"})
+			if connected.IsError || connected.Status != 200 {
+				t.Fatal("fixture connect failed")
+			}
+			conn := f.connection(t)
+			raw, err := sql.Open("sqlite", f.h.dbPath)
+			if err != nil {
+				t.Fatal(err)
+			}
+			defer raw.Close()
+			snapshot, err := f.h.database.RuntimeSnapshot(f.h.ctx, f.task.RuntimeConfigSnapshotID)
+			if err != nil {
+				t.Fatal(err)
+			}
+			readSnapshot := snapshot
+			readSnapshot.ID = domain.NewID("runtime")
+			readSnapshot.PermissionsJSON = `{"filesystem":"read-only","approval":"never"}`
+			if err := f.h.database.CreateRuntimeSnapshot(f.h.ctx, readSnapshot); err != nil {
+				t.Fatal(err)
+			}
+			if _, err := f.h.database.UpsertTaskAgent(f.h.ctx, domain.TaskAgent{TaskID: f.task.ID, AgentID: "sub", Role: "sub", Status: "idle", RuntimeConfigSnapshotID: snapshot.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
+				t.Fatal(err)
+			}
+			reset := func() {
+				t.Helper()
+				if _, err := raw.Exec(`UPDATE turns SET agent_id='main',runtime_config_snapshot_id=?,status='running' WHERE id=?`, snapshot.ID, f.turn.ID); err != nil {
+					t.Fatal(err)
+				}
+				if _, err := raw.Exec(`UPDATE tasks SET status=? WHERE id=?`, string(f.task.Status), f.task.ID); err != nil {
+					t.Fatal(err)
+				}
+				if _, err := raw.Exec(`DELETE FROM agent_inbox WHERE batch_id=?`, f.turn.InboxBatchID); err != nil {
+					t.Fatal(err)
+				}
+				for _, resource := range []domain.HardwareResource{f.board, f.adb} {
+					if err := f.h.database.UpsertHardwareResource(f.h.ctx, resource); err != nil {
+						t.Fatal(err)
+					}
+				}
+				f.token = f.h.issue(f.task.ID, "main", f.turn.ID)
+				if pet {
+					assistant, _ := f.h.database.EnsureOwnerAssistant(f.h.ctx, time.Now())
+					if err := f.h.database.UpdateOwnerPetPermissions(f.h.ctx, assistant.ID, true, true, false, false, false, true, false, false, time.Now()); err != nil {
+						t.Fatal(err)
+					}
+				}
+			}
+			for _, scenario := range []string{"non_main", "read_only_snapshot", "read_only_resource", "channel_turn", "terminal_task", "revoked_pet"} {
+				if scenario == "revoked_pet" && !pet {
+					continue
+				}
+				t.Run(scenario, func(t *testing.T) {
+					reset()
+					defer reset()
+					switch scenario {
+					case "non_main":
+						_, err = raw.Exec(`UPDATE turns SET agent_id='sub' WHERE id=?`, f.turn.ID)
+						f.token = f.h.issue(f.task.ID, "sub", f.turn.ID)
+					case "read_only_snapshot":
+						_, err = raw.Exec(`UPDATE turns SET runtime_config_snapshot_id=? WHERE id=?`, readSnapshot.ID, f.turn.ID)
+					case "read_only_resource":
+						for _, resource := range []domain.HardwareResource{f.board, f.adb} {
+							resource.Access = domain.HardwareAccessReadOnly
+							if err = f.h.database.UpsertHardwareResource(f.h.ctx, resource); err != nil {
+								t.Fatal(err)
+							}
+						}
+					case "terminal_task":
+						_, err = raw.Exec(`UPDATE tasks SET status='completed' WHERE id=?`, f.task.ID)
+					case "revoked_pet":
+						if status := f.raw(t, http.MethodPost, "/device/adb/exec", map[string]any{"args": []string{"shell", "id"}}); status != 200 {
+							t.Fatal("pre-revocation HTTP ADB failed")
+						}
+						assistant, _ := f.h.database.EnsureOwnerAssistant(f.h.ctx, time.Now())
+						err = f.h.database.UpdateOwnerPetPermissions(f.h.ctx, assistant.ID, false, false, false, false, false, false, false, false, time.Now())
+					case "channel_turn":
+						payload := `{"channel_context":{"schema":"aha2.channel.v1","instance_id":"fixture","endpoint":"group_digital_human","conversation_id":"fixture","route":{"mode":"task_route"}}}`
+						_, err = raw.Exec(`INSERT INTO agent_inbox(id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,content,payload_json,status,batch_id,created_at,claimed_at,processed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, domain.NewID("inbox"), f.task.ID, f.turn.RoundID, "main", "channel", "channel_message", "", "", "fixture", payload, "claimed", f.turn.InboxBatchID, time.Now().Format(time.RFC3339Nano), "", "")
+					}
+					if err != nil {
+						t.Fatal(err)
+					}
+					beforeAccepts, beforeExec := f.accepts.Load(), f.executions.Load()
+					for _, op := range []struct {
+						name, path string
+						args       map[string]any
+						body       any
+					}{
+						{"hardware_connect", "/board/connect", map[string]any{"hardware_id": "board"}, map[string]any{}},
+						{"hardware_disconnect", "/board/disconnect", map[string]any{"hardware_id": "board"}, map[string]any{}},
+						{"hardware_send", "/board/send", map[string]any{"hardware_id": "board", "data": "NO-EFFECT\n"}, map[string]any{"data": "NO-EFFECT\n"}},
+						{"hardware_login", "/board/login", map[string]any{"hardware_id": "board", "wakeup": true}, map[string]any{"wakeup": true}},
+						{"hardware_adb_exec", "/device/adb/exec", map[string]any{"hardware_id": "device", "args": []string{"shell", "id"}}, map[string]any{"args": []string{"shell", "id"}}},
+					} {
+						mcp := f.mcp(t, op.name, op.args)
+						if mcp.Status != http.StatusForbidden {
+							t.Fatalf("%s MCP status=%d", op.name, mcp.Status)
+						}
+						if status := f.raw(t, http.MethodPost, op.path, op.body); status != http.StatusForbidden {
+							t.Fatalf("%s HTTP status=%d", op.name, status)
+						}
+					}
+
+					_ = conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
+					buffer := make([]byte, 128)
+					if n, _ := conn.Read(buffer); n != 0 {
+						t.Fatal("denied send/login reached fake transport")
+					}
+					if f.accepts.Load() != beforeAccepts || f.executions.Load() != beforeExec || !f.manager.Status(f.task.ID, "board", "network", false).Connected {
+						t.Fatal("denied operation changed transport state")
+					}
+					if scenario == "channel_turn" || scenario == "revoked_pet" {
+						if status := f.raw(t, http.MethodGet, "", nil); status != 403 {
+							t.Fatalf("HTTP read after revocation status=%d", status)
+						}
+						if result := f.mcp(t, "hardware_list", map[string]any{}); result.Status != 403 {
+							t.Fatal("MCP read after revocation succeeded")
+						}
+					}
+				})
+			}
+		})
+	}
+}
+
+func TestOwnerWebHardwarePolicyIsSeparateFromAgentSnapshot(t *testing.T) {
+	f := newHardwareAuthorizationFixture(t, false)
+	snapshot, err := f.h.database.RuntimeSnapshot(f.h.ctx, f.task.RuntimeConfigSnapshotID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	snapshot.ID = domain.NewID("runtime")
+	snapshot.PermissionsJSON = `{"filesystem":"read-only","approval":"never"}`
+	if err := f.h.database.CreateRuntimeSnapshot(f.h.ctx, snapshot); err != nil {
+		t.Fatal(err)
+	}
+	raw, err := sql.Open("sqlite", f.h.dbPath)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer raw.Close()
+	if _, err := raw.Exec(`UPDATE turns SET runtime_config_snapshot_id=? WHERE id=?`, snapshot.ID, f.turn.ID); err != nil {
+		t.Fatal(err)
+	}
+	if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"}); result.Status != 403 {
+		t.Fatal("read-only Agent connected")
+	}
+	jar, _ := cookiejar.New(nil)
+	client := &http.Client{Jar: jar}
+	csrf := registerOwner(t, client, f.server.URL)
+	base := f.server.URL + "/api/v1/tasks/" + f.task.ID + "/hardware/board"
+	response := requestJSON(t, client, http.MethodPost, base+"/connect", map[string]any{}, csrf)
+	if response.StatusCode != 200 {
+		t.Fatalf("owner connect status=%d", response.StatusCode)
+	}
+	response.Body.Close()
+	conn := f.connection(t)
+	response = requestJSON(t, client, http.MethodPost, base+"/send", map[string]any{"data": "OWNER-FIXTURE\n"}, csrf)
+	if response.StatusCode != http.StatusAccepted {
+		t.Fatalf("owner send status=%d", response.StatusCode)
+	}
+	response.Body.Close()
+	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
+	if line, err := bufio.NewReader(conn).ReadString('\n'); err != nil || line != "OWNER-FIXTURE\n" {
+		t.Fatal("owner send did not reach fixture")
+	}
+	if f.raw(t, http.MethodPost, "/board/disconnect", map[string]any{}) != 403 || !f.manager.Status(f.task.ID, "board", "network", false).Connected {
+		t.Fatal("read-only Agent disconnected owner transport")
+	}
+	response = requestJSON(t, client, http.MethodPost, base+"/disconnect", map[string]any{}, csrf)
+	if response.StatusCode != 200 {
+		t.Fatal("owner disconnect was tightened")
+	}
+	response.Body.Close()
+	if f.manager.Status(f.task.ID, "board", "network", false).Connected {
+		t.Fatal("owner disconnect did not close transport")
+	}
+}
+
+func TestReadOnlyResourceCannotConnectOrDisconnectThroughEitherAgentSurface(t *testing.T) {
+	f := newHardwareAuthorizationFixture(t, false)
+	resource := f.board
+	resource.Access = domain.HardwareAccessReadOnly
+	if err := f.h.database.UpsertHardwareResource(f.h.ctx, resource); err != nil {
+		t.Fatal(err)
+	}
+	for _, path := range []string{"connect", "disconnect"} {
+		if result := f.mcp(t, "hardware_"+path, map[string]any{"hardware_id": "board"}); result.Status != 403 {
+			t.Fatalf("read-only %s MCP status=%d", path, result.Status)
+		}
+		if status := f.raw(t, http.MethodPost, "/board/"+path, map[string]any{}); status != 403 {
+			t.Fatalf("read-only %s HTTP status=%d", path, status)
+		}
+	}
+	if f.accepts.Load() != 0 || f.manager.Status(f.task.ID, "board", "network", false).Connected {
+		t.Fatal("read-only resource opened a transport")
+	}
+	jar, _ := cookiejar.New(nil)
+	client := &http.Client{Jar: jar}
+	csrf := registerOwner(t, client, f.server.URL)
+	base := f.server.URL + "/api/v1/tasks/" + f.task.ID + "/hardware/board"
+	response := requestJSON(t, client, http.MethodPost, base+"/connect", map[string]any{}, csrf)
+	if response.StatusCode != 200 {
+		t.Fatalf("owner read-only connection policy changed: %d", response.StatusCode)
+	}
+	response.Body.Close()
+	_ = f.connection(t)
+	if result := f.mcp(t, "hardware_disconnect", map[string]any{"hardware_id": "board"}); result.Status != 403 {
+		t.Fatal("Agent detached read-only transport")
+	}
+	if f.raw(t, http.MethodPost, "/board/disconnect", map[string]any{}) != 403 || !f.manager.Status(f.task.ID, "board", "network", true).Connected {
+		t.Fatal("HTTP Agent detached read-only transport")
+	}
+	response = requestJSON(t, client, http.MethodPost, base+"/send", map[string]any{"data": "FORBIDDEN"}, csrf)
+	if response.StatusCode != 403 {
+		t.Fatal("owner resource read-only policy was widened")
+	}
+	response.Body.Close()
+	response = requestJSON(t, client, http.MethodPost, base+"/disconnect", map[string]any{}, csrf)
+	if response.StatusCode != 200 {
+		t.Fatal("owner disconnect policy changed")
+	}
+	response.Body.Close()
+}
+
+func TestAgentOperationReturnsOnlyFixedFailureCode(t *testing.T) {
+	request := httptest.NewRequest(http.MethodPost, "/fixed", nil)
+	for _, code := range []string{"ssh_host_key_unknown", "hardware_read_only", "UNTRUSTED-SECRET-MARKER"} {
+		op := agentMCPOperation{Tool: mcpTool{InputSchema: mcpObject(map[string]any{})}, Method: http.MethodPost, Handler: func(w http.ResponseWriter, _ *http.Request) {
+			writeJSON(w, http.StatusForbidden, map[string]any{"error": code, "message": "SECRET-DETAIL", "credential": "SECRET-DETAIL"})
+		}}
+		_, err := runAgentMCPOperation(request, op, json.RawMessage(`{}`))
+		typed, ok := err.(*agentOperationError)
+		if !ok || typed.Status != 403 || strings.Contains(err.Error(), "SECRET") {
+			t.Fatal("unsafe failure projection")
+		}
+		if code != "UNTRUSTED-SECRET-MARKER" && typed.Code != code {
+			t.Fatal("known error lost its classification")
+		}
+	}
+}
diff --git a/internal/httpapi/hardware_handlers.go b/internal/httpapi/hardware_handlers.go
index 12f0f25..a4c8e9e 100644
--- a/internal/httpapi/hardware_handlers.go
+++ b/internal/httpapi/hardware_handlers.go
@@ -320,6 +320,19 @@ func (s *Server) taskHardware(writer http.ResponseWriter, request *http.Request)
 		return
 	}
 	resources, _ := s.store.HardwareResources(request.Context())
+	if _, agent := agentClaimsFromContext(request.Context()); agent {
+		bound := map[string]bool{}
+		for _, group := range groups {
+			bound[group.ResourceID] = true
+		}
+		filtered := make([]domain.HardwareResource, 0, len(groups))
+		for _, resource := range resources {
+			if bound[resource.ID] {
+				filtered = append(filtered, resource)
+			}
+		}
+		resources = filtered
+	}
 	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "groups": groups, "resources": resources})
 }
 
@@ -392,7 +405,12 @@ func (s *Server) updateTaskHardwareBindings(writer http.ResponseWriter, request
 		})
 	}
 	if err := s.app.ReplaceTaskHardwareResourceBindings(request.Context(), taskID, bindings); err != nil {
-		writeError(writer, http.StatusBadRequest, "update_hardware_bindings_failed")
+		var invalid *app.HardwareBindingError
+		if errors.As(err, &invalid) {
+			writeError(writer, http.StatusBadRequest, invalid.Code)
+		} else {
+			writeError(writer, http.StatusBadRequest, "update_hardware_bindings_failed")
+		}
 		return
 	}
 
@@ -724,7 +742,19 @@ func (s *Server) hardwareTarget(
 		})
 		return domain.Task{}, domain.HardwareGroup{}, "", false, false
 	}
-	readOnly := task.Status.Terminal() || task.ReadOnly || !group.Writable()
+	// Owner Web retains its resource/terminal-state policy. Agent-only Task and
+	// snapshot restrictions belong to the shared Agent authorization boundary.
+	readOnly := task.Status.Terminal() || !group.Writable()
+	if claims, agent := agentClaimsFromContext(request.Context()); agent {
+		if _, err := s.app.AgentDomainCallContext(request.Context(), claims, "hardware", request.Method != http.MethodGet); err != nil {
+			writeAgentControlError(writer, err)
+			return domain.Task{}, domain.HardwareGroup{}, "", true, false
+		}
+		if request.Method != http.MethodGet && !group.Writable() {
+			writeError(writer, http.StatusForbidden, "hardware_read_only")
+			return domain.Task{}, domain.HardwareGroup{}, "", true, false
+		}
+	}
 	return task, group, transport, readOnly, true
 }
 
```
