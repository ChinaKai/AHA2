# 复审 M4 / S2 / S3：设备效果、固定错误码与有界结果

统一固定源码：`9bca3785b3ef1d071faed06041aeb2e6de0139a7`，tree `5d2a491a18fe4df4c9c348c0e491aba87a8537ba`。问题 diff 边界为 `9d01cb63cca51bff23c40dad2c61d55497f2cd3a..9bca3785b3ef1d071faed06041aeb2e6de0139a7` 中下列完整文件；门禁 diff 保留 3 行上下文。所有源码摘录也从该固定提交读取，不来自正在编辑的 worktree。

四种生命周期分别用真实硬件 manager + 127.0.0.1 假 transport 验证，宠物和普通 Task 都执行；没有连接真实设备。假 ADB 走注入执行器计数。拒绝表覆盖非 Main、只读 snapshot、只读资源、渠道 Turn、终态 Task、宠物撤权；同 token 撤权后直连 HTTP 为 403，执行计数不变。固定错误码来自封闭白名单，原 message/凭据/endpoint 不透传。MCP ADB 在一次执行后保留退出码、原字节数及截断标记，输出原始字节总预算 24 KiB；3 MiB 假结果仍返回成功的业务结果封装而非超限错误，防止误触发重试。owner/普通 HTTP 的字节通道保留原行为。

固定提交的 20 个具名定向回归全部执行且退出 0（136.834s，显式核对每个顶层 Test 的 pass，missing=0）；`go vet ./internal/app ./internal/httpapi ./internal/hardware` 退出 0（1.176s）。完整命令和逐项覆盖在 `recheck-9bca378-validation.md`。固定提交的 app/httpapi 全包尚未完成，当前排队；旧 worktree 全包运行期间源码有改动，只作诊断，不是这个提交的通过证据。不能据此合并或声称完整 task-722 已完成。

完整 diff 文件边界：

- `internal/httpapi/agent_handlers.go`
- `internal/httpapi/agent_hardware_mcp_operations.go`
- `internal/httpapi/agent_hardware_effects_test.go`
- `internal/httpapi/agent_hardware_fixture_test.go`

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

### `internal/httpapi/agent_handlers.go` · `agentLoginHardware`

```go
func (s *Server) agentLoginHardware(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if readOnly {
		writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "hardware_read_only", "message": "当前硬件组或 Task 为只读"})
		return
	}
	if s.hardware == nil {
		writeError(writer, http.StatusServiceUnavailable, "hardware_runtime_unavailable")
		return
	}
	var options hardware.LoginOptions
	if err := decodeJSON(request, &options); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	status, err := s.hardware.Login(task.ID, group.ID, transport, options)
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, hardware.ErrReadOnly) {
			code = http.StatusForbidden
		}
		writeJSON(writer, code, map[string]any{"ok": false, "error": "hardware_login_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.hardware.login", "task", task.ID, map[string]any{"hardware_id": group.ID, "transport": transport})
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "status": status})
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

### `internal/hardware/manager.go` · `readLoop`

```go
func (session *terminalSession) readLoop() {
	buffer := make([]byte, 4096)
	for {
		count, err := session.conn.Read(buffer)
		if count > 0 {
			payload := append([]byte(nil), buffer[:count]...)
			if session.codec != nil {
				var reply []byte
				payload, reply = session.codec.Feed(payload)
				if len(reply) > 0 {
					_ = session.writeRaw(reply)
				}
			}
			if text, terminalData := session.decoder.Decode(payload); len(terminalData) > 0 {
				session.record("rx", text, "runtime", terminalData, nil)
				session.maybeAutoLogin(text)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || session.isClosing() {
				break
			}
			session.fail(err)
			break
		}
	}
}
```

### `internal/hardware/manager.go` · `maybeAutoLogin`

```go
func (session *terminalSession) maybeAutoLogin(text string) {
	session.mu.Lock()
	session.loginBuffer = appendLoginBuffer(session.loginBuffer, text)
	lower := strings.ToLower(session.loginBuffer)
	options := session.loginOptions
	username := session.username
	password := session.password
	if matchesLoginPrompt(lower, options.FailurePrompts) {
		session.loginStatus = "failed"
		session.loginError = "hardware reported a login failure"
		session.mu.Unlock()
		return
	}
	if matchesLoginPrompt(lower, options.SuccessPrompts) {
		session.loginStatus = "authenticated"
		session.loginError = ""
		session.mu.Unlock()
		return
	}
	sendUsername := username != "" && !session.usernameSent && matchesLoginPrompt(lower, options.UsernamePrompts)
	sendPassword := password != "" && !session.passwordSent && matchesLoginPrompt(lower, options.PasswordPrompts)
	if password != "" && session.passwordSent && matchesLoginPrompt(lower, options.PasswordPrompts) && session.loginAttempts < options.Retries {
		session.passwordSent = false
		session.loginAttempts++
		sendPassword = true
	}
	if sendUsername {
		session.usernameSent = true
		session.loginStatus = "username_sent"
	}
	if sendPassword {
		session.passwordSent = true
		session.loginStatus = "credentials_sent"
	}
	if sendUsername || sendPassword {
		session.loginBuffer = ""
	}
	ending := loginLineEnding(options.LineEnding)
	session.mu.Unlock()
	if sendUsername {
		session.automaticSend(username+ending, username+displayLineEnding(options.LineEnding))
	}
	if sendPassword {
		session.automaticSend(password+ending, "<password>"+displayLineEnding(options.LineEnding))
	}
}
```

### `internal/hardware/manager.go` · `normalizeLoginOptions`

```go
func normalizeLoginOptions(options LoginOptions) LoginOptions {
	if len(options.UsernamePrompts) == 0 {
		options.UsernamePrompts = []string{"login:", "username:"}
	}
	if len(options.PasswordPrompts) == 0 {
		options.PasswordPrompts = []string{"password:"}
	}
	if len(options.SuccessPrompts) == 0 {
		options.SuccessPrompts = []string{"# ", "$ "}
	}
	if len(options.FailurePrompts) == 0 {
		options.FailurePrompts = []string{"login incorrect", "authentication failed", "access denied"}
	}
	switch options.LineEnding {
	case "cr", "lf", "crlf":
	default:
		options.LineEnding = "crlf"
	}
	if options.TimeoutSeconds < 3 || options.TimeoutSeconds > 300 {
		options.TimeoutSeconds = 30
	}
	if options.Retries < 1 {
		options.Retries = 1
	}
	if options.Retries > 3 {
		options.Retries = 3
	}
	return options
}
```

### `internal/hardware/manager.go` · `Login`

```go
func (manager *Manager) Login(taskID, hardwareID, transport string, options LoginOptions) (domain.HardwareTerminalStatus, error) {
	key := attachmentKey(taskID, hardwareID, transport)
	manager.mu.Lock()
	session := manager.attachments[key]
	manager.mu.Unlock()
	if session == nil {
		return domain.HardwareTerminalStatus{}, ErrNotConnected
	}
	session.mu.Lock()
	if session.protocol == domain.HardwareProtocolSSH {
		status := session.statusForLocked(taskID, hardwareID, false)
		session.mu.Unlock()
		return status, nil
	}
	attachment, ok := session.attachments[key]
	if !ok || session.closing {
		session.mu.Unlock()
		return domain.HardwareTerminalStatus{}, ErrNotConnected
	}
	if attachment.readOnly {
		session.mu.Unlock()
		return domain.HardwareTerminalStatus{}, ErrReadOnly
	}
	if session.username == "" && session.password == "" {
		session.mu.Unlock()
		return domain.HardwareTerminalStatus{}, fmt.Errorf("hardware login credentials are not configured")
	}
	session.loginOptions = normalizeLoginOptions(options)
	session.usernameSent = false
	session.passwordSent = false
	session.loginBuffer = ""
	session.loginStatus = "waiting"
	session.loginError = ""
	session.loginAttempts = 1
	session.loginGeneration++
	generation := session.loginGeneration
	wakeup := session.loginOptions.Wakeup
	ending := loginLineEnding(session.loginOptions.LineEnding)
	timeout := session.loginOptions.TimeoutSeconds
	session.mu.Unlock()
	if wakeup {
		session.automaticSend(ending, "<wakeup>")
	}
	go session.loginTimeout(generation, timeout)
	return session.statusFor(taskID, hardwareID, attachment.readOnly), nil
}
```

### `internal/hardware/manager.go` · `Send`

```go
func (manager *Manager) Send(taskID, hardwareID, transport, data, encoding, source string) error {
	key := attachmentKey(taskID, hardwareID, transport)
	manager.mu.Lock()
	session := manager.attachments[key]
	manager.mu.Unlock()
	if session == nil {
		return ErrNotConnected
	}
	session.mu.RLock()
	item, ok := session.attachments[key]
	session.mu.RUnlock()
	if !ok {
		return ErrNotConnected
	}
	if item.readOnly {
		return ErrReadOnly
	}
	if encoding == "" {
		encoding = "text"
	}
	var payload []byte
	switch encoding {
	case "text":
		payload = []byte(data)
	case "hex":
		if transport != domain.HardwareTransportSerial {
			return fmt.Errorf("hex input is supported for serial only")
		}
		var err error
		payload, err = decodeHex(data)
		if err != nil {
			return err
		}
	case "base64":
		var err error
		payload, err = base64.StdEncoding.DecodeString(data)
		if err != nil {
			return fmt.Errorf("invalid base64 input: %w", err)
		}
	default:
		return fmt.Errorf("unsupported encoding %q", encoding)
	}
	if len(payload) == 0 {
		return fmt.Errorf("hardware input is empty")
	}
	if len(payload) > 64*1024 {
		return fmt.Errorf("hardware input exceeds 64 KiB")
	}
	display := data
	if encoding == "hex" {
		display = strings.ToUpper(hex.EncodeToString(payload))
	} else if encoding == "base64" {
		display = strings.ToValidUTF8(string(payload), "\uFFFD")
	}
	return session.sendPayload(payload, display, source)
}
```

### `internal/hardware/manager.go` · `Disconnect`

```go
func (manager *Manager) Disconnect(taskID, hardwareID, transport string) {
	key := attachmentKey(taskID, hardwareID, transport)
	manager.mu.Lock()
	session := manager.attachments[key]
	if session != nil {
		manager.detachLocked(key, session)
	}
	manager.mu.Unlock()
}
```

### `internal/hardware/manager.go` · `automaticSend`

```go
func (session *terminalSession) automaticSend(value, display string) {
	payload := []byte(value)
	if session.codec != nil {
		payload = session.codec.Encode(payload)
	}
	if session.writeRaw(payload) == nil {
		session.record("tx", display, "auto-login", nil, nil)
	}
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
diff --git a/internal/httpapi/agent_handlers.go b/internal/httpapi/agent_handlers.go
index 6b4fab5..033e1ef 100644
--- a/internal/httpapi/agent_handlers.go
+++ b/internal/httpapi/agent_handlers.go
@@ -178,11 +178,30 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
 		"input_bytes": len(input), "stdout_bytes": len(result.Stdout),
 		"stderr_bytes": len(result.Stderr), "exit_code": result.ExitCode,
 	})
+	stdout, stderr := result.Stdout, result.Stderr
+	stdoutTruncated, stderrTruncated := false, false
+	if operation, _ := request.Context().Value(agentOperationContextKey{}).(string); operation == "hardware_adb_exec" {
+		// The command has already run. Preserve exit/status and an explicit
+		// bounded result instead of turning a successful side effect into a
+		// response-size failure that could provoke an unsafe command retry.
+		const budget = 24 << 10
+		if len(stdout) > budget {
+			stdout = stdout[:budget]
+			stdoutTruncated = true
+		}
+		remaining := budget - len(stdout)
+		if len(stderr) > remaining {
+			stderr = stderr[:remaining]
+			stderrTruncated = true
+		}
+	}
 	writeJSON(writer, http.StatusOK, map[string]any{
 		"ok": true, "endpoint": result.Endpoint, "exit_code": result.ExitCode,
-		"stdout_base64": base64.StdEncoding.EncodeToString(result.Stdout),
-		"stderr_base64": base64.StdEncoding.EncodeToString(result.Stderr),
-		"duration_ms":   result.Duration.Milliseconds(),
+		"stdout_base64":    base64.StdEncoding.EncodeToString(stdout),
+		"stderr_base64":    base64.StdEncoding.EncodeToString(stderr),
+		"stdout_truncated": stdoutTruncated, "stderr_truncated": stderrTruncated,
+		"stdout_bytes": len(result.Stdout), "stderr_bytes": len(result.Stderr),
+		"duration_ms": result.Duration.Milliseconds(),
 	})
 }
 
diff --git a/internal/httpapi/agent_hardware_effects_test.go b/internal/httpapi/agent_hardware_effects_test.go
new file mode 100644
index 0000000..2f70531
--- /dev/null
+++ b/internal/httpapi/agent_hardware_effects_test.go
@@ -0,0 +1,95 @@
+package httpapi
+
+import (
+	"bufio"
+	"context"
+	"encoding/json"
+	"fmt"
+	"github.com/ChinaKai/AHA2/internal/hardware"
+	"net/http/httptest"
+	"strings"
+	"testing"
+	"time"
+)
+
+func TestAgentHardwareFourLifecyclesUseFakeTransportForPetAndOrdinaryTask(t *testing.T) {
+	for _, pet := range []bool{false, true} {
+		t.Run(fmt.Sprintf("pet=%v", pet), func(t *testing.T) {
+			f := newHardwareAuthorizationFixture(t, pet)
+			result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"})
+			if result.Status != 200 || result.IsError || !f.manager.Status(f.task.ID, "board", "network", false).Connected {
+				t.Fatalf("connect=%#v", result)
+			}
+
+			conn := f.connection(t)
+			reader := bufio.NewReader(conn)
+			result = f.mcp(t, "hardware_send", map[string]any{"hardware_id": "board", "data": "FIXTURE-PING\n"})
+			if result.Status != 200 || result.IsError {
+				t.Fatalf("send=%#v", result)
+			}
+			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
+			line, err := reader.ReadString('\n')
+			if err != nil || line != "FIXTURE-PING\n" {
+				t.Fatal("send did not reach fake transport")
+			}
+			result = f.mcp(t, "hardware_login", map[string]any{"hardware_id": "board", "wakeup": false, "line_ending": "lf", "timeout_seconds": 10})
+			if result.Status != 200 || result.IsError || strings.Contains(result.Text, f.secret) {
+				t.Fatalf("login request failed: status=%d error=%v", result.Status, result.IsError)
+			}
+			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
+			_, _ = conn.Write([]byte("login:"))
+			line, err = reader.ReadString('\n')
+			if err != nil || strings.TrimSpace(line) != "fixture-user" {
+				t.Fatal("login username did not reach fake transport")
+			}
+			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
+			_, _ = conn.Write([]byte("password:"))
+			line, err = reader.ReadString('\n')
+			if err != nil || strings.TrimSpace(line) != f.secret {
+				t.Fatal("stored login credential did not reach fake transport")
+			}
+			_, _ = conn.Write([]byte("$ "))
+			deadline := time.Now().Add(3 * time.Second)
+			for f.manager.Status(f.task.ID, "board", "network", false).LoginStatus != "authenticated" {
+				if time.Now().After(deadline) {
+					t.Fatal("fake login did not authenticate")
+				}
+				time.Sleep(10 * time.Millisecond)
+			}
+			page, err := f.h.database.HardwareIOPage(f.h.ctx, f.task.ID, "board", "network", 0, 100)
+			if err != nil {
+				t.Fatal(err)
+			}
+			for _, event := range page.Items {
+				if strings.Contains(event.Data, f.secret) {
+					t.Fatal("credential was recorded in terminal history")
+				}
+			}
+			result = f.mcp(t, "hardware_disconnect", map[string]any{"hardware_id": "board"})
+			if result.Status != 200 || result.IsError || f.manager.Status(f.task.ID, "board", "network", false).Connected {
+				t.Fatalf("disconnect=%#v", result)
+			}
+			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
+			if _, err = reader.ReadByte(); err == nil {
+				t.Fatal("disconnect left fake transport open")
+			}
+		})
+	}
+}
+
+func TestADBMCPLargeOutputIsBoundedAfterExactlyOneExecution(t *testing.T) {
+	f := newHardwareAuthorizationFixture(t, false)
+	server := httptest.NewServer(New(Config{Store: f.h.database, App: f.h.service, AgentCapabilities: f.h.caps, ExecuteADB: func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error) {
+		f.executions.Add(1)
+		return hardware.ADBExecResult{Stdout: []byte(strings.Repeat("x", 3<<20)), Stderr: []byte("tail"), ExitCode: 7}, nil
+	}}).Handler())
+	defer server.Close()
+	result := coreMCPCallTool(t, server.URL+"/api/v1/agent/hardware/mcp", f.token, "hardware_adb_exec", map[string]any{"hardware_id": "device", "args": []string{"shell", "fixture"}})
+	if result.Status != 200 || result.IsError || len(result.Text) > 64<<10 || f.executions.Load() != 1 {
+		t.Fatalf("large result: status=%d isError=%v size=%d executions=%d", result.Status, result.IsError, len(result.Text), f.executions.Load())
+	}
+	var payload map[string]any
+	if json.Unmarshal([]byte(result.Text), &payload) != nil || payload["stdout_truncated"] != true || payload["stderr_truncated"] != true || payload["exit_code"] != float64(7) {
+		t.Fatal("truncation or command status was lost")
+	}
+}
diff --git a/internal/httpapi/agent_hardware_fixture_test.go b/internal/httpapi/agent_hardware_fixture_test.go
new file mode 100644
index 0000000..dd76c4b
--- /dev/null
+++ b/internal/httpapi/agent_hardware_fixture_test.go
@@ -0,0 +1,126 @@
+package httpapi
+
+import (
+	"context"
+	"github.com/ChinaKai/AHA2/internal/auth"
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/hardware"
+	"net"
+	"net/http/httptest"
+	"strconv"
+	"sync/atomic"
+	"testing"
+	"time"
+)
+
+type hardwareAuthorizationFixture struct {
+	h          *coreMCPHarness
+	task       domain.Task
+	turn       domain.Turn
+	token      string
+	server     *httptest.Server
+	manager    *hardware.Manager
+	listener   net.Listener
+	accepted   chan net.Conn
+	accepts    atomic.Int32
+	executions atomic.Int32
+	board      domain.HardwareResource
+	adb        domain.HardwareResource
+	secret     string
+}
+
+func newHardwareAuthorizationFixture(t *testing.T, pet bool) *hardwareAuthorizationFixture {
+	t.Helper()
+	h := newCoreMCPHarness(t)
+	base := createHardwareAPITask(t, h.database)
+	task := base
+	if pet {
+		task = createPetDomainHTTPTask(t, h, base)
+	}
+	listener, err := net.Listen("tcp", "127.0.0.1:0")
+	if err != nil {
+		t.Fatal(err)
+	}
+	t.Cleanup(func() { _ = listener.Close() })
+	f := &hardwareAuthorizationFixture{h: h, task: task, listener: listener, accepted: make(chan net.Conn, 8), secret: "LOGIN-FIXTURE-SECRET-NOT-IN-MODEL"}
+	go func() {
+		for {
+			conn, err := listener.Accept()
+			if err != nil {
+				return
+			}
+			f.accepts.Add(1)
+			f.accepted <- conn
+		}
+	}()
+	now := time.Now().UTC()
+	host, portText, _ := net.SplitHostPort(listener.Addr().String())
+	port, _ := strconv.Atoi(portText)
+	f.board = domain.HardwareResource{ID: domain.NewID("resource"), Description: "Loopback fixture", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: host, Port: port, Protocol: domain.HardwareProtocolRaw}, Username: "fixture-user", CredentialRef: "fixture/login", PasswordConfigured: true, Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now}
+	f.adb = domain.HardwareResource{ID: domain.NewID("resource"), Description: "Fake ADB", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "192.0.2.24", Port: 5555, Protocol: domain.HardwareProtocolADB}, Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now}
+	for _, item := range []domain.HardwareResource{f.board, f.adb} {
+		if err := h.database.UpsertHardwareResource(h.ctx, item); err != nil {
+			t.Fatal(err)
+		}
+	}
+	if err := h.database.ReplaceTaskHardwareBindings(h.ctx, task.ID, []domain.TaskHardwareBinding{{TaskID: task.ID, ID: "board", ResourceID: f.board.ID}, {TaskID: task.ID, ID: "device", ResourceID: f.adb.ID}}); err != nil {
+		t.Fatal(err)
+	}
+	f.manager = hardware.NewManager(h.database)
+	t.Cleanup(func() { _ = f.manager.Close() })
+	secrets := &fakeSecretStore{values: map[string]string{"fixture/login": f.secret}}
+	f.server = httptest.NewServer(New(Config{Store: h.database, App: h.service, Auth: auth.NewService(h.database, "setup-test", time.Hour), AgentCapabilities: h.caps, Hardware: f.manager, Secrets: secrets, ExecuteADB: func(ctx context.Context, _ string, _ []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
+		if ctx.Err() != nil {
+			return hardware.ADBExecResult{}, ctx.Err()
+		}
+		f.executions.Add(1)
+		return hardware.ADBExecResult{Stdout: []byte("fake-adb-result")}, nil
+	}}).Handler())
+	t.Cleanup(f.server.Close)
+	if task.Status == domain.TaskDraft {
+		if err := h.database.UpdateTaskStatus(h.ctx, task.ID, task.Status, domain.TaskPreparing, now.Format(time.RFC3339Nano), ""); err != nil {
+			t.Fatal(err)
+		}
+		if err := h.database.UpdateTaskStatus(h.ctx, task.ID, domain.TaskPreparing, domain.TaskActive, now.Format(time.RFC3339Nano), ""); err != nil {
+			t.Fatal(err)
+		}
+	}
+	f.task.Status = domain.TaskActive
+	if !pet {
+		assistant, err := h.database.EnsureOwnerAssistant(h.ctx, now)
+		if err != nil {
+			t.Fatal(err)
+		}
+		if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, false, false, false, false, false, false, false, false, now); err != nil {
+			t.Fatal(err)
+		}
+	}
+
+	f.turn = createCoreTurn(t, h.database, task, "main", domain.NewID("inbox_batch"))
+	f.token = h.issue(task.ID, "main", f.turn.ID)
+	return f
+}
+
+func (f *hardwareAuthorizationFixture) mcp(t *testing.T, name string, args map[string]any) coreMCPToolCall {
+	t.Helper()
+	return coreMCPCallTool(t, f.server.URL+"/api/v1/agent/hardware/mcp", f.token, name, args)
+}
+
+func (f *hardwareAuthorizationFixture) connection(t *testing.T) net.Conn {
+	t.Helper()
+	select {
+	case conn := <-f.accepted:
+		t.Cleanup(func() { _ = conn.Close() })
+		return conn
+	case <-time.After(3 * time.Second):
+		t.Fatal("fixture did not accept a connection")
+		return nil
+	}
+}
+
+func (f *hardwareAuthorizationFixture) raw(t *testing.T, method, path string, body any) int {
+	t.Helper()
+	response := agentRequest(t, f.server.URL+"/api/v1/agent/hardware"+path, method, f.token, body)
+	defer response.Body.Close()
+	return response.StatusCode
+}
diff --git a/internal/httpapi/agent_hardware_mcp_operations.go b/internal/httpapi/agent_hardware_mcp_operations.go
index 49482cf..8bae229 100644
--- a/internal/httpapi/agent_hardware_mcp_operations.go
+++ b/internal/httpapi/agent_hardware_mcp_operations.go
@@ -28,7 +28,7 @@ func (s *Server) hardwareMCPOperations() []agentMCPOperation {
 		"args":         map[string]any{"type": "array", "items": mcpString(), "minItems": 1, "maxItems": 64},
 		"stdin_base64": map[string]any{"type": "string", "maxLength": 65536}, "timeout_seconds": mcpInteger(1, 120),
 	}, "args")
-	adb.ResultFields = []string{"ok", "exit_code", "stdout_base64", "stderr_base64", "duration_ms"}
+	adb.ResultFields = []string{"ok", "exit_code", "stdout_base64", "stderr_base64", "duration_ms", "stdout_truncated", "stderr_truncated", "stdout_bytes", "stderr_bytes"}
 	return []agentMCPOperation{connect, disconnect, send, login, adb}
 }
 
```
