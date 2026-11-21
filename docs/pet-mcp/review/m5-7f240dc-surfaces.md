# M5：存储凭据回显与登录状态机（固定 7f240dc）

源码 `7f240dcd2a12a479b3954dc842961790a5a5b2de`，tree `309b9d42b5db5ec8734f27edd1c96b722f46b993`；增量 `9bed44d..7f240dc` 共 18 个文件，全部在 `internal/hardware/` 与 `internal/httpapi/`。`9bed44d` 是 M2-a 材料提交，本包不改 M2-a 与已投审包。下方 diff 与函数全文都从固定提交取得，diff 保留 3 行上下文，不截断；两个包按目录切分，合计覆盖本轮全部增量。

固定副本（git archive，源码不改）验证：

- `go test -json ./internal/hardware -count=1 -race -timeout=15m` → 退出 0，5.4s，具名用例 37 个全部 run/pass，遗漏 0
- `go test -json ./internal/httpapi -run 'Hardware|ADB|Login|Terminal|Credential|HostKey' -count=1 -timeout=40m` → 退出 0，274.3s，具名用例 41 个全部 run/pass，遗漏 0
- `go vet ./internal/hardware ./internal/httpapi` → 退出 0，1.1s

测试只用 fake 设备、loopback 监听、随机生成的 fixture 凭据和本地假 adb 脚本，不连接真实硬件、不读真实凭据。

## B. 历史读取边界、固定错误码与 ADB 执行状态

- 全部终端历史出口（owner HTTP、Agent HTTP、MCP `hardware_terminal_read`、WebSocket 回放）改走唯一的 `hardwareHistoryPage`；凭据在服务端内部按资源的 CredentialRef 解析，不成为工具输入或响应字段。store 中只有这一个读取方（`HardwareIOPage` 的唯一调用者）。`group.TaskID` 来自绑定行 `b.task_id`，与原先传入的当前 Task ID 相同。
- 登录/发送失败返回固定且互不混淆的状态码（未连接、只读、未配置凭据、登录参数越界、预算耗尽、编码无效），MCP 只透出白名单代码。MCP 登录 schema 收紧到 manager 实际接受的范围（timeout 3–300、retries 1–3），新增 `cancel`。
- ADB：输出超限与到达截止时间都返回 `may_have_executed: true` 和独立代码（`adb_output_limit_may_have_executed`、`adb_timeout_may_have_executed`），提示调用方先查设备状态，不要盲目重试；截止前的普通失败仍是 `adb_exec_failed`。连接一直挂到截止时间也按"可能已执行"报告（此处无法区分，取保守侧）。MCP 有界结果为 stderr 预留最多 6 KiB。这里没有改 `internal/hardware/adb.go`，避免与并行 task726 的 ADB 改动重叠。

本包对应固定副本具名用例（41 个，全部 run/pass）：`TestADBDeadlineReportsPossibleExecutionOnHTTPAndMCP`, `TestADBMCPLargeOutputIsBoundedAfterExactlyOneExecution`, `TestADBPairingFailureMessagesAreActionable`, `TestAgentADBExecUsesTaskDeviceAndReturnsBinary`, `TestAgentHardwareFourLifecyclesUseFakeTransportForPetAndOrdinaryTask`, `TestAgentHardwareMCPBoundsOversizedRequestID`, `TestAgentHardwareMCPListOnlyExposesBoundNonSensitiveFields`, `TestAgentHardwareMCPOnlyAuditsVerifiedIdentifiers`, `TestAgentHardwareMCPRejectsChannelBoundTurns`, `TestAgentHardwareMCPRequiresCapabilityAndDomainGate`, `TestAgentHardwareMCPTerminalCursorNeverRewinds`, `TestAgentHardwareMCPTerminalReadBoundsCompleteResponse`, `TestAgentHardwareMCPTerminalReadEnforcesResponseBudget`, `TestAgentHardwareMCPTerminalReadPagingAndCaps`, `TestAgentHardwareMCPToolsListSchemas`, `TestAgentHardwareSharedAuthorizationRejectsWritesBeforeEffects`, `TestCreateTaskClonesLocalSourceHardware`, `TestFitHardwareMCPTerminalResponseRejectsOversizedEnvelope`, `TestHardwareADBPairUsesSeparatePortAndDoesNotPersistCode`, `TestHardwareAPIConfigNetworkTerminalAndPermissions`, `TestHardwareConnectFailureIsAudited`, `TestHardwareIORetentionEndpoints`, `TestHardwareResourcesAreSystemLevelAndBindToTasks`, `TestHardwareSSHHostKeyRequiresExplicitFingerprintConfirmation`, `TestHardwareTerminalWebSocketStreamsRawBytes`, `TestKnowledgeVaultImportEndpointLocalAndCredentialSafety`, `TestLocalOwnerIngestRetryFingerprintAndTerminalTask`, `TestLoginAndSendFailuresHaveFixedDistinctStateCodes`, `TestMailAccountCRUDDoesNotEchoCredential`, `TestMailBindingsRejectTerminalTaskButAllowDelete`, `TestMailOwnerMutationsAreAuditedWithoutCredential`, `TestModelHealthCheckMissingModelAndCredential`, `TestOwnerWebHardwarePolicyIsSeparateFromAgentSnapshot`, `TestPasswordRecoveryRotatesSessionsAndCredentials`, `TestPetHardwareMCPExecUsesBoundResourceAndLivePermissions`, `TestRealADBOutputLimitReportsPossibleExecutionWithoutRetry`, `TestRetainedCredentialFragmentsStayMaskedAcrossPagedHistorySurfaces`, `TestStoredLoginEchoCannotReachHistoryHTTPMCPOrWebSocket`, `TestTaskMailOptionsAreOwnerScopedAndCredentialFree`, `TestWorkspaceSSHCredentialsStayInSecretStore`, `TestWorkspaceSSHHostKeyRequiresExplicitFingerprintConfirmation`

反向验证（提交前工作区）：临时关闭页首接续遮蔽，`TestRetainedCredentialFragmentsStayMaskedAcrossPagedHistorySurfaces` 在 MCP 逐条分页（50 页）时露出凭据片段而失败；恢复后通过。

完整 diff 文件边界：

- `internal/httpapi/agent_handlers.go`
- `internal/httpapi/agent_hardware_effects_test.go`
- `internal/httpapi/agent_hardware_fixture_test.go`
- `internal/httpapi/agent_hardware_mcp.go`
- `internal/httpapi/agent_hardware_mcp_operations.go`
- `internal/httpapi/agent_mcp_operation.go`
- `internal/httpapi/hardware_adb_execution_state_test.go`（新文件）
- `internal/httpapi/hardware_error_codes.go`（新文件）
- `internal/httpapi/hardware_handlers.go`
- `internal/httpapi/hardware_history.go`（新文件）
- `internal/httpapi/hardware_login_security_test.go`（新文件）
- `internal/httpapi/hardware_websocket.go`

## 必要调用链（固定提交全文）

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

### `internal/httpapi/hardware_handlers.go` · `hardwareTerminal`

```go
func (s *Server) hardwareTerminal(writer http.ResponseWriter, request *http.Request) {
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	page, err := s.hardwareHistoryPage(
		request.Context(), group, transport,
		queryInt(request, "after", 0), int(queryInt(request, "limit", 500)),
	)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "hardware_io_failed")
		return
	}
	status := disconnectedHardwareStatus(task.ID, group.ID, transport, readOnly)
	if s.hardware != nil {
		status = s.hardware.Status(task.ID, group.ID, transport, readOnly)
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "group": group, "transport": transport, "status": status, "stream": page,
	})
}
```

### `internal/httpapi/agent_handlers.go` · `agentHardwareTerminal`

```go
func (s *Server) agentHardwareTerminal(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	s.hardwareTerminal(writer, request)
}
```

### `internal/httpapi/agent_hardware_mcp.go` · `agentHardwareMCPTerminalRead`

```go
// agentHardwareMCPTerminalRead mirrors the HTTP terminal handler: the same
// bound-group lookup, transport default and IO page query. Unbound hardware,
// another Task's hardware and a cross-Project hardware ID all return the same
// hardware_group_not_found, so the tool cannot probe existence. The second
// return value is the canonical bound group ID for the audit row.
func (s *Server) agentHardwareMCPTerminalRead(ctx context.Context, call app.AgentCallContext, requestID json.RawMessage, hardwareID, transport string, after int64, limit int) (agentHardwareMCPTerminal, string, error) {
	group, err := s.store.HardwareGroup(ctx, call.Task.ID, hardwareID)
	if errors.Is(err, sql.ErrNoRows) {
		return agentHardwareMCPTerminal{}, "", &hardwareMCPError{
			code: "hardware_group_not_found", message: "hardware group is not bound to this Task",
		}
	}
	if err != nil {
		return agentHardwareMCPTerminal{}, "", err
	}
	if transport == "" {
		if group.Supports(domain.HardwareTransportSerial) {
			transport = domain.HardwareTransportSerial
		} else {
			transport = domain.HardwareTransportNetwork
		}
	}
	if !group.Supports(transport) {
		return agentHardwareMCPTerminal{}, "", &hardwareMCPError{
			code: "hardware_transport_invalid", message: "hardware group does not configure that transport",
		}
	}
	page, err := s.hardwareHistoryPage(ctx, group, transport, after, limit)
	if err != nil {
		return agentHardwareMCPTerminal{}, "", &hardwareMCPError{
			code: "hardware_io_failed", message: "terminal output could not be read",
		}
	}
	view := agentHardwareMCPTerminalView(group.ID, transport, after, page)
	view, err = fitHardwareMCPTerminalResponse(requestID, view, after)
	if err != nil {
		return agentHardwareMCPTerminal{}, "", err
	}
	return view, group.ID, nil
}
```

### `internal/httpapi/hardware_websocket.go` · `hardwareTerminalWebSocket`

```go
func (s *Server) hardwareTerminalWebSocket(writer http.ResponseWriter, request *http.Request) {
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if s.hardware == nil {
		writeError(writer, http.StatusServiceUnavailable, "hardware_runtime_unavailable")
		return
	}
	status := s.hardware.Status(task.ID, group.ID, transport, readOnly)
	if !status.Connected {
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "hardware_not_connected", "message": "请先连接硬件终端",
		})
		return
	}
	live, unsubscribe, err := s.hardware.Subscribe(task.ID, group.ID, transport)
	if err != nil {
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "hardware_subscribe_failed", "message": err.Error(),
		})
		return
	}
	defer unsubscribe()
	afterSequence := queryInt(request, "after", 0)
	if afterSequence < 0 {
		afterSequence = 0
	}
	page, err := s.hardwareHistoryPage(request.Context(), group, transport, afterSequence, 1000)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "hardware_io_failed")
		return
	}
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		InsecureSkipVerify: !s.originValidationEnabled(),
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "")
	connection.SetReadLimit(64 * 1024)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := writeHardwareWSJSON(ctx, connection, map[string]any{
		"type": "ready", "status": status, "latest_sequence": page.LatestSequence,
	}); err != nil {
		return
	}
	for _, item := range page.Items {
		if item.Direction != "rx" || item.Data == "" {
			continue
		}
		if err := writeHardwareWS(ctx, connection, websocket.MessageBinary, []byte(item.Data)); err != nil {
			return
		}
	}
	latestSequence := max(afterSequence, page.LatestSequence)
	incoming := make(chan hardwareWSRead, 1)
	go func() {
		for {
			messageType, data, readErr := connection.Read(ctx)
			select {
			case incoming <- hardwareWSRead{messageType: messageType, data: data, err: readErr}:
			case <-ctx.Done():
				return
			}
			if readErr != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-live:
			switch event.Type {
			case "output":
				if event.Sequence > 0 && event.Sequence <= latestSequence {
					continue
				}
				if event.Sequence > latestSequence {
					latestSequence = event.Sequence
				}
				if err := writeHardwareWS(ctx, connection, websocket.MessageBinary, event.Data); err != nil {
					return
				}
			case "status":
				if err := writeHardwareWSJSON(ctx, connection, map[string]any{"type": "status", "status": event.Status}); err != nil {
					return
				}
				if !event.Status.Connected {
					return
				}
			}
		case message := <-incoming:
			if message.err != nil {
				code := websocket.CloseStatus(message.err)
				if code == websocket.StatusNormalClosure || code == websocket.StatusGoingAway {
					return
				}
				return
			}
			if message.messageType == websocket.MessageBinary {
				if err := s.hardware.SendRaw(task.ID, group.ID, transport, message.data, "websocket"); err != nil {
					_ = writeHardwareWSJSON(ctx, connection, map[string]any{"type": "error", "message": err.Error()})
				}
				continue
			}
			var control hardwareWSMessage
			if err := json.Unmarshal(message.data, &control); err != nil {
				_ = writeHardwareWSJSON(ctx, connection, map[string]any{"type": "error", "message": "invalid control message"})
				continue
			}
			switch control.Type {
			case "input":
				if err := s.hardware.SendRaw(task.ID, group.ID, transport, []byte(control.Data), "websocket"); err != nil {
					_ = writeHardwareWSJSON(ctx, connection, map[string]any{"type": "error", "message": err.Error()})
				}
			case "resize":
				if err := s.hardware.Resize(task.ID, group.ID, transport, control.Cols, control.Rows); err != nil {
					_ = writeHardwareWSJSON(ctx, connection, map[string]any{"type": "error", "message": err.Error()})
				}
			case "close":
				return
			default:
				_ = writeHardwareWSJSON(ctx, connection, map[string]any{"type": "error", "message": "unknown control message"})
			}
		}
	}
}
```

### `internal/store/hardware_io.go` · `HardwareIOPage`

```go
// HardwareIOPage keeps the SQLite-era contract: with after>0 it returns the
// next limit events in sequence order; otherwise the newest limit events.
// LatestSequence is the last returned sequence (or the stream maximum when
// nothing is returned) and HasMore reports newer events beyond the page.
func (s *Store) HardwareIOPage(
	_ context.Context,
	taskID, hardwareID, transport string,
	after int64,
	limit int,
) (domain.HardwareIOPage, error) {
	if limit < 1 {
		limit = 500
	}
	if limit > 2000 {
		limit = 2000
	}
	page := domain.HardwareIOPage{Items: make([]domain.HardwareIOEvent, 0)}
	if s.hardwareIO == nil {
		return page, errors.New("hardware io store is not open")
	}
	key := hardwareio.StreamKey{TaskID: taskID, HardwareID: hardwareID, Transport: transport}
	var records []hardwareio.Record
	var maximum int64
	var err error
	if after > 0 {
		records, maximum, err = s.hardwareIO.After(key, after, limit)
	} else {
		records, maximum, err = s.hardwareIO.Latest(key, limit)
	}
	if err != nil {
		return domain.HardwareIOPage{}, err
	}
	for _, record := range records {
		page.Items = append(page.Items, domain.HardwareIOEvent{
			Sequence: record.Sequence, ID: record.ID, TaskID: record.TaskID, HardwareID: record.HardwareID,
			Transport: record.Transport, Direction: record.Direction, Data: record.Data,
			Encoding: record.Encoding, Source: record.Source, CreatedAt: parseTime(record.CreatedAt),
		})
		page.LatestSequence = record.Sequence
	}
	if maximum > 0 {
		if page.LatestSequence == 0 {
			page.LatestSequence = maximum
		}
		page.HasMore = maximum > page.LatestSequence
	}
	return page, nil
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
	if request.Context().Err() != nil {
		writeError(writer, http.StatusConflict, "operation_cancelled")
		return
	}
	status, err := s.hardware.Login(task.ID, group.ID, transport, options)
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, hardware.ErrReadOnly) {
			code = http.StatusForbidden
		}
		writeJSON(writer, code, map[string]any{"ok": false, "error": hardwarePublicErrorCode(err, "hardware_login_failed"), "message": err.Error()})
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
	timeout := time.Duration(timeoutSeconds) * time.Second
	startedAt := time.Now()
	result, err := s.executeADB(request.Context(), endpoint, payload.Args, input, timeout)
	if err != nil {
		code := http.StatusConflict
		errorCode := hardwarePublicErrorCode(err, "adb_exec_failed")
		// Output overflow and the command deadline both end a command that may
		// already have changed the device; a caller must not retry blindly. A
		// connection that hangs until the deadline is also reported this way:
		// it cannot be told apart from a started command here.
		mayHaveExecuted := errors.Is(err, hardware.ErrADBOutputTooLarge)
		if mayHaveExecuted {
			code = http.StatusRequestEntityTooLarge
		} else if time.Since(startedAt) >= timeout || errors.Is(request.Context().Err(), context.DeadlineExceeded) {
			mayHaveExecuted, errorCode = true, "adb_timeout_may_have_executed"
		}
		s.audit(request, "task.hardware.adb_exec_failed", "task", task.ID, map[string]any{
			"hardware_id": group.ID, "argument_count": len(payload.Args), "input_bytes": len(input), "may_have_executed": mayHaveExecuted,
		})
		writeJSON(writer, code, map[string]any{"ok": false, "error": errorCode, "may_have_executed": mayHaveExecuted, "message": err.Error()})
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
		// Keep diagnostic stderr even when stdout fills its entire allowance.
		reservedStderr := len(stderr)
		if reservedStderr > 6<<10 {
			reservedStderr = 6 << 10
		}
		stdoutBudget := budget - reservedStderr
		if len(stdout) > stdoutBudget {
			stdout = stdout[:stdoutBudget]
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

## 该边界完整 diff

```diff
diff --git a/internal/httpapi/agent_handlers.go b/internal/httpapi/agent_handlers.go
index 033e1ef..9f813bc 100644
--- a/internal/httpapi/agent_handlers.go
+++ b/internal/httpapi/agent_handlers.go
@@ -1,6 +1,7 @@
 package httpapi
 
 import (
+	"context"
 	"encoding/base64"
 	"errors"
 	"fmt"
@@ -75,13 +76,17 @@ func (s *Server) agentLoginHardware(writer http.ResponseWriter, request *http.Re
 		writeError(writer, http.StatusBadRequest, "invalid_json")
 		return
 	}
+	if request.Context().Err() != nil {
+		writeError(writer, http.StatusConflict, "operation_cancelled")
+		return
+	}
 	status, err := s.hardware.Login(task.ID, group.ID, transport, options)
 	if err != nil {
 		code := http.StatusConflict
 		if errors.Is(err, hardware.ErrReadOnly) {
 			code = http.StatusForbidden
 		}
-		writeJSON(writer, code, map[string]any{"ok": false, "error": "hardware_login_failed", "message": err.Error()})
+		writeJSON(writer, code, map[string]any{"ok": false, "error": hardwarePublicErrorCode(err, "hardware_login_failed"), "message": err.Error()})
 		return
 	}
 	s.audit(request, "task.hardware.login", "task", task.ID, map[string]any{"hardware_id": group.ID, "transport": transport})
@@ -161,16 +166,26 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
 		return
 	}
 	endpoint := net.JoinHostPort(group.Network.Host, strconv.Itoa(group.Network.Port))
-	result, err := s.executeADB(request.Context(), endpoint, payload.Args, input, time.Duration(timeoutSeconds)*time.Second)
+	timeout := time.Duration(timeoutSeconds) * time.Second
+	startedAt := time.Now()
+	result, err := s.executeADB(request.Context(), endpoint, payload.Args, input, timeout)
 	if err != nil {
 		code := http.StatusConflict
-		if errors.Is(err, hardware.ErrADBOutputTooLarge) {
+		errorCode := hardwarePublicErrorCode(err, "adb_exec_failed")
+		// Output overflow and the command deadline both end a command that may
+		// already have changed the device; a caller must not retry blindly. A
+		// connection that hangs until the deadline is also reported this way:
+		// it cannot be told apart from a started command here.
+		mayHaveExecuted := errors.Is(err, hardware.ErrADBOutputTooLarge)
+		if mayHaveExecuted {
 			code = http.StatusRequestEntityTooLarge
+		} else if time.Since(startedAt) >= timeout || errors.Is(request.Context().Err(), context.DeadlineExceeded) {
+			mayHaveExecuted, errorCode = true, "adb_timeout_may_have_executed"
 		}
 		s.audit(request, "task.hardware.adb_exec_failed", "task", task.ID, map[string]any{
-			"hardware_id": group.ID, "argument_count": len(payload.Args), "input_bytes": len(input),
+			"hardware_id": group.ID, "argument_count": len(payload.Args), "input_bytes": len(input), "may_have_executed": mayHaveExecuted,
 		})
-		writeJSON(writer, code, map[string]any{"ok": false, "error": "adb_exec_failed", "message": err.Error()})
+		writeJSON(writer, code, map[string]any{"ok": false, "error": errorCode, "may_have_executed": mayHaveExecuted, "message": err.Error()})
 		return
 	}
 	s.audit(request, "task.hardware.adb_exec", "task", task.ID, map[string]any{
@@ -185,8 +200,14 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
 		// bounded result instead of turning a successful side effect into a
 		// response-size failure that could provoke an unsafe command retry.
 		const budget = 24 << 10
-		if len(stdout) > budget {
-			stdout = stdout[:budget]
+		// Keep diagnostic stderr even when stdout fills its entire allowance.
+		reservedStderr := len(stderr)
+		if reservedStderr > 6<<10 {
+			reservedStderr = 6 << 10
+		}
+		stdoutBudget := budget - reservedStderr
+		if len(stdout) > stdoutBudget {
+			stdout = stdout[:stdoutBudget]
 			stdoutTruncated = true
 		}
 		remaining := budget - len(stdout)
diff --git a/internal/httpapi/agent_hardware_effects_test.go b/internal/httpapi/agent_hardware_effects_test.go
index 2f70531..43c9c55 100644
--- a/internal/httpapi/agent_hardware_effects_test.go
+++ b/internal/httpapi/agent_hardware_effects_test.go
@@ -4,10 +4,13 @@ import (
 	"bufio"
 	"context"
 	"encoding/json"
+	"errors"
 	"fmt"
 	"github.com/ChinaKai/AHA2/internal/hardware"
+	"net/http"
 	"net/http/httptest"
 	"strings"
+	"sync/atomic"
 	"testing"
 	"time"
 )
@@ -89,7 +92,48 @@ func TestADBMCPLargeOutputIsBoundedAfterExactlyOneExecution(t *testing.T) {
 		t.Fatalf("large result: status=%d isError=%v size=%d executions=%d", result.Status, result.IsError, len(result.Text), f.executions.Load())
 	}
 	var payload map[string]any
-	if json.Unmarshal([]byte(result.Text), &payload) != nil || payload["stdout_truncated"] != true || payload["stderr_truncated"] != true || payload["exit_code"] != float64(7) {
+	if json.Unmarshal([]byte(result.Text), &payload) != nil || payload["stdout_truncated"] != true || payload["stderr_truncated"] != false || payload["stderr_base64"] != "dGFpbA==" || payload["exit_code"] != float64(7) {
 		t.Fatal("truncation or command status was lost")
 	}
 }
+
+// A command that reaches its deadline may already have changed the device.
+// Both the Agent HTTP body and the MCP error code must say so, and a failure
+// before the deadline keeps the ordinary code, so callers can tell them apart.
+func TestADBDeadlineReportsPossibleExecutionOnHTTPAndMCP(t *testing.T) {
+	f := newHardwareAuthorizationFixture(t, false)
+	var hang atomic.Bool
+	server := httptest.NewServer(New(Config{Store: f.h.database, App: f.h.service, AgentCapabilities: f.h.caps, ExecuteADB: func(ctx context.Context, _ string, _ []string, _ []byte, timeout time.Duration) (hardware.ADBExecResult, error) {
+		f.executions.Add(1)
+		if !hang.Load() {
+			return hardware.ADBExecResult{}, errors.New("fixture connection refused")
+		}
+		timer := time.NewTimer(timeout)
+		defer timer.Stop()
+		select {
+		case <-ctx.Done():
+		case <-timer.C:
+		}
+		return hardware.ADBExecResult{}, errors.New("fixture command timed out")
+	}}).Handler())
+	defer server.Close()
+	args := map[string]any{"hardware_id": "device", "args": []string{"shell", "fixture"}, "timeout_seconds": 1}
+	result := coreMCPCallTool(t, server.URL+"/api/v1/agent/hardware/mcp", f.token, "hardware_adb_exec", args)
+	if !result.IsError || !strings.Contains(result.Text, "adb_exec_failed") || strings.Contains(result.Text, "may_have_executed") {
+		t.Fatal("early failure was reported as a possibly executed command")
+	}
+	hang.Store(true)
+	result = coreMCPCallTool(t, server.URL+"/api/v1/agent/hardware/mcp", f.token, "hardware_adb_exec", args)
+	if !result.IsError || !strings.Contains(result.Text, "adb_timeout_may_have_executed") {
+		t.Fatal("deadline was not reported as possibly executed over MCP")
+	}
+	response := agentRequest(t, server.URL+"/api/v1/agent/hardware/device/adb/exec", http.MethodPost, f.token, map[string]any{"args": []string{"shell", "fixture"}, "timeout_seconds": 1})
+	defer response.Body.Close()
+	var body map[string]any
+	if json.NewDecoder(response.Body).Decode(&body) != nil || body["error"] != "adb_timeout_may_have_executed" || body["may_have_executed"] != true {
+		t.Fatal("deadline was not reported as possibly executed over Agent HTTP")
+	}
+	if f.executions.Load() != 3 {
+		t.Fatal("deadline classification retried or skipped a command")
+	}
+}
diff --git a/internal/httpapi/agent_hardware_fixture_test.go b/internal/httpapi/agent_hardware_fixture_test.go
index dd76c4b..5fda264 100644
--- a/internal/httpapi/agent_hardware_fixture_test.go
+++ b/internal/httpapi/agent_hardware_fixture_test.go
@@ -2,6 +2,8 @@ package httpapi
 
 import (
 	"context"
+	"crypto/rand"
+	"encoding/hex"
 	"github.com/ChinaKai/AHA2/internal/auth"
 	"github.com/ChinaKai/AHA2/internal/domain"
 	"github.com/ChinaKai/AHA2/internal/hardware"
@@ -42,7 +44,12 @@ func newHardwareAuthorizationFixture(t *testing.T, pet bool) *hardwareAuthorizat
 		t.Fatal(err)
 	}
 	t.Cleanup(func() { _ = listener.Close() })
-	f := &hardwareAuthorizationFixture{h: h, task: task, listener: listener, accepted: make(chan net.Conn, 8), secret: "LOGIN-FIXTURE-SECRET-NOT-IN-MODEL"}
+	f := &hardwareAuthorizationFixture{h: h, task: task, listener: listener, accepted: make(chan net.Conn, 8), secret: ""}
+	var secretBytes [24]byte
+	if _, err := rand.Read(secretBytes[:]); err != nil {
+		t.Fatal("fixture secret generation failed")
+	}
+	f.secret = hex.EncodeToString(secretBytes[:])
 	go func() {
 		for {
 			conn, err := listener.Accept()
diff --git a/internal/httpapi/agent_hardware_mcp.go b/internal/httpapi/agent_hardware_mcp.go
index 7eca953..a408b57 100644
--- a/internal/httpapi/agent_hardware_mcp.go
+++ b/internal/httpapi/agent_hardware_mcp.go
@@ -409,7 +409,7 @@ func (s *Server) agentHardwareMCPTerminalRead(ctx context.Context, call app.Agen
 			code: "hardware_transport_invalid", message: "hardware group does not configure that transport",
 		}
 	}
-	page, err := s.store.HardwareIOPage(ctx, call.Task.ID, group.ID, transport, after, limit)
+	page, err := s.hardwareHistoryPage(ctx, group, transport, after, limit)
 	if err != nil {
 		return agentHardwareMCPTerminal{}, "", &hardwareMCPError{
 			code: "hardware_io_failed", message: "terminal output could not be read",
diff --git a/internal/httpapi/agent_hardware_mcp_operations.go b/internal/httpapi/agent_hardware_mcp_operations.go
index 8bae229..9bebe78 100644
--- a/internal/httpapi/agent_hardware_mcp_operations.go
+++ b/internal/httpapi/agent_hardware_mcp_operations.go
@@ -20,11 +20,12 @@ func (s *Server) hardwareMCPOperations() []agentMCPOperation {
 	connect := operation("hardware_connect", "Connect a device already bound to this Task using stored credentials. Requires Main and writable resource access. Unknown SSH host keys require owner confirmation in Web. Read connection state with hardware_list.", s.agentConnectHardware, nil)
 	disconnect := operation("hardware_disconnect", "Disconnect one bound device transport. Does not change bindings or credentials.", s.agentDisconnectHardware, nil)
 	send := operation("hardware_send", "Send data to a bound writable connected device. No credentials are accepted; use hardware_login for stored-credential login. Read output with hardware_terminal_read.", s.agentSendHardware, map[string]any{"data": map[string]any{"type": "string", "minLength": 1, "maxLength": 65536}, "encoding": mcpEnum("text", "base64")}, "data")
-	login := operation("hardware_login", "Start stored-credential login on a bound writable device. Credential values are never accepted or returned. Read connection state and terminal output separately.", s.agentLoginHardware, map[string]any{
+	login := operation("hardware_login", "Start stored-credential login on a bound writable device. Credential values are never accepted or returned. Authenticated or pending explicit attempts are idempotent. Failure/timeout/cancel disarms; retries is the explicit-attempt budget for this connection (default 1, maximum 3). Read connection state and sanitized terminal output separately.", s.agentLoginHardware, map[string]any{
+		"cancel": map[string]any{"type": "boolean", "description": "Disarm a pending login without disconnecting; an authenticated session stays authenticated."},
 		"wakeup": map[string]any{"type": "boolean"}, "line_ending": mcpEnum("lf", "crlf", "cr"),
-		"timeout_seconds": mcpInteger(1, 120), "retries": mcpInteger(0, 5),
+		"timeout_seconds": mcpInteger(3, 300), "retries": mcpInteger(1, 3),
 	})
-	adb := operation("hardware_adb_exec", "Run a device-scoped ADB command against a bound writable ADB resource. Host commands, pairing, host-key trust and credentials are excluded. Uses the same command validation, timeout and cancellation as the Agent API.", s.agentADBExec, map[string]any{
+	adb := operation("hardware_adb_exec", "Run a device-scoped ADB command against a bound writable ADB resource. Host commands, pairing, host-key trust and credentials are excluded. Uses the same command validation, timeout and cancellation as the Agent API. adb_output_limit_may_have_executed and adb_timeout_may_have_executed mean the command may already have run: check device state before any retry.", s.agentADBExec, map[string]any{
 		"args":         map[string]any{"type": "array", "items": mcpString(), "minItems": 1, "maxItems": 64},
 		"stdin_base64": map[string]any{"type": "string", "maxLength": 65536}, "timeout_seconds": mcpInteger(1, 120),
 	}, "args")
diff --git a/internal/httpapi/agent_mcp_operation.go b/internal/httpapi/agent_mcp_operation.go
index d674638..2cb2fdc 100644
--- a/internal/httpapi/agent_mcp_operation.go
+++ b/internal/httpapi/agent_mcp_operation.go
@@ -42,7 +42,7 @@ func safeAgentOperationCode(body []byte) string {
 	}
 	_ = json.Unmarshal(body, &failure)
 	switch failure.Error {
-	case "hardware_read_only", "hardware_runtime_unavailable", "hardware_group_not_found", "hardware_transport_invalid", "hardware_connect_failed", "hardware_send_failed", "hardware_login_failed", "ssh_host_key_unknown", "adb_exec_unavailable", "adb_command_not_allowed", "adb_args_invalid", "adb_stdin_invalid", "adb_timeout_invalid", "adb_exec_failed", "managed_process_not_found", "managed_process_already_running", "managed_process_start_failed", "browser_capability_denied", "browser_grant_forbidden", "browser_grant_revoked", "browser_grant_expired", "browser_tab_not_found", "agent_operation_forbidden", "agent_turn_inactive", "task_read_only":
+	case "hardware_not_connected", "hardware_credentials_not_configured", "hardware_login_options_invalid", "hardware_login_retry_limit", "hardware_encoding_invalid", "adb_output_limit_may_have_executed", "adb_timeout_may_have_executed", "hardware_read_only", "hardware_runtime_unavailable", "hardware_group_not_found", "hardware_transport_invalid", "hardware_connect_failed", "hardware_send_failed", "hardware_login_failed", "ssh_host_key_unknown", "adb_exec_unavailable", "adb_command_not_allowed", "adb_args_invalid", "adb_stdin_invalid", "adb_timeout_invalid", "adb_exec_failed", "managed_process_not_found", "managed_process_already_running", "managed_process_start_failed", "browser_capability_denied", "browser_grant_forbidden", "browser_grant_revoked", "browser_grant_expired", "browser_tab_not_found", "agent_operation_forbidden", "agent_turn_inactive", "task_read_only":
 		return failure.Error
 	default:
 		return "operation_failed"
diff --git a/internal/httpapi/hardware_adb_execution_state_test.go b/internal/httpapi/hardware_adb_execution_state_test.go
new file mode 100644
index 0000000..8792764
--- /dev/null
+++ b/internal/httpapi/hardware_adb_execution_state_test.go
@@ -0,0 +1,46 @@
+//go:build !windows
+
+package httpapi
+
+import (
+	"net/http/httptest"
+	"os"
+	"path/filepath"
+	"strings"
+	"testing"
+)
+
+func TestRealADBOutputLimitReportsPossibleExecutionWithoutRetry(t *testing.T) {
+	f := newHardwareAuthorizationFixture(t, false)
+	directory := t.TempDir()
+	executable := filepath.Join(directory, "adb-fixture")
+	countFile := filepath.Join(directory, "executions")
+	script := `#!/bin/sh
+if [ "$1" = "mdns" ]; then exit 0; fi
+if [ "$1" = "connect" ]; then printf 'connected\n'; exit 0; fi
+if [ "$1" = "-s" ] && [ "$3" = "get-state" ]; then printf 'device\n'; exit 0; fi
+if [ "$1" = "-s" ] && [ "$3" = "shell" ]; then
+  printf 'executed\n' >> "$AHA_TEST_ADB_EXECUTIONS"
+  head -c 16777217 /dev/zero
+  exit 0
+fi
+exit 2
+`
+	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
+		t.Fatal(err)
+	}
+	t.Setenv("AHA2_ADB_PATH", executable)
+	t.Setenv("AHA_TEST_ADB_EXECUTIONS", countFile)
+	// Deliberately use the real ExecuteADB implementation, including its own
+	// output bound. Only the executable is a local fixture, never a real device.
+	server := httptest.NewServer(New(Config{Store: f.h.database, App: f.h.service, AgentCapabilities: f.h.caps}).Handler())
+	defer server.Close()
+	result := coreMCPCallTool(t, server.URL+"/api/v1/agent/hardware/mcp", f.token, "hardware_adb_exec", map[string]any{"hardware_id": "device", "args": []string{"shell", "fixture"}, "timeout_seconds": 10})
+	if !result.IsError || !strings.Contains(result.Text, "adb_output_limit_may_have_executed") {
+		t.Fatal("actual executor limit did not distinguish possibly executed command")
+	}
+	data, err := os.ReadFile(countFile)
+	if err != nil || string(data) != "executed\n" {
+		t.Fatal("output limit caused missing/duplicate command execution")
+	}
+}
diff --git a/internal/httpapi/hardware_error_codes.go b/internal/httpapi/hardware_error_codes.go
new file mode 100644
index 0000000..de51c11
--- /dev/null
+++ b/internal/httpapi/hardware_error_codes.go
@@ -0,0 +1,27 @@
+package httpapi
+
+import (
+	"errors"
+	"github.com/ChinaKai/AHA2/internal/hardware"
+)
+
+func hardwarePublicErrorCode(err error, fallback string) string {
+	switch {
+	case errors.Is(err, hardware.ErrNotConnected):
+		return "hardware_not_connected"
+	case errors.Is(err, hardware.ErrReadOnly):
+		return "hardware_read_only"
+	case errors.Is(err, hardware.ErrLoginCredentialsNotConfigured):
+		return "hardware_credentials_not_configured"
+	case errors.Is(err, hardware.ErrLoginOptionsInvalid):
+		return "hardware_login_options_invalid"
+	case errors.Is(err, hardware.ErrLoginRetryLimit):
+		return "hardware_login_retry_limit"
+	case errors.Is(err, hardware.ErrHardwareEncodingInvalid):
+		return "hardware_encoding_invalid"
+	case errors.Is(err, hardware.ErrADBOutputTooLarge):
+		return "adb_output_limit_may_have_executed"
+	default:
+		return fallback
+	}
+}
diff --git a/internal/httpapi/hardware_handlers.go b/internal/httpapi/hardware_handlers.go
index a4c8e9e..7d7ab95 100644
--- a/internal/httpapi/hardware_handlers.go
+++ b/internal/httpapi/hardware_handlers.go
@@ -551,8 +551,8 @@ func (s *Server) hardwareTerminal(writer http.ResponseWriter, request *http.Requ
 	if !ok {
 		return
 	}
-	page, err := s.store.HardwareIOPage(
-		request.Context(), task.ID, group.ID, transport,
+	page, err := s.hardwareHistoryPage(
+		request.Context(), group, transport,
 		queryInt(request, "after", 0), int(queryInt(request, "limit", 500)),
 	)
 	if err != nil {
@@ -701,7 +701,7 @@ func (s *Server) sendHardware(writer http.ResponseWriter, request *http.Request)
 			status = http.StatusForbidden
 		}
 		writeJSON(writer, status, map[string]any{
-			"ok": false, "error": "hardware_send_failed", "message": err.Error(),
+			"ok": false, "error": hardwarePublicErrorCode(err, "hardware_send_failed"), "message": err.Error(),
 		})
 		return
 	}
diff --git a/internal/httpapi/hardware_history.go b/internal/httpapi/hardware_history.go
new file mode 100644
index 0000000..c041b55
--- /dev/null
+++ b/internal/httpapi/hardware_history.go
@@ -0,0 +1,23 @@
+package httpapi
+
+import (
+	"context"
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/ChinaKai/AHA2/internal/hardware"
+)
+
+// Every terminal history/replay surface uses this boundary, including retained
+// records written before stream sanitization was installed. Secret values are
+// resolved internally and never become tool inputs or response fields.
+func (s *Server) hardwareHistoryPage(ctx context.Context, group domain.HardwareGroup, transport string, after int64, limit int) (domain.HardwareIOPage, error) {
+	page, err := s.store.HardwareIOPage(ctx, group.TaskID, group.ID, transport, after, limit)
+	if err != nil {
+		return page, err
+	}
+	if group.CredentialRef != "" && s.secrets != nil {
+		if secret, ok := s.secrets.Get(group.CredentialRef); ok {
+			return hardware.RedactHistory(page, secret), nil
+		}
+	}
+	return page, nil
+}
diff --git a/internal/httpapi/hardware_login_security_test.go b/internal/httpapi/hardware_login_security_test.go
new file mode 100644
index 0000000..17e04e8
--- /dev/null
+++ b/internal/httpapi/hardware_login_security_test.go
@@ -0,0 +1,334 @@
+package httpapi
+
+import (
+	"bufio"
+	"bytes"
+	"context"
+	"encoding/json"
+	"io"
+	"net/http"
+	"net/http/cookiejar"
+	"net/url"
+	"os"
+	"path/filepath"
+	"strconv"
+	"strings"
+	"sync"
+	"sync/atomic"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"github.com/coder/websocket"
+)
+
+func TestStoredLoginEchoCannotReachHistoryHTTPMCPOrWebSocket(t *testing.T) {
+	f := newHardwareAuthorizationFixture(t, true)
+	result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"})
+	if result.Status != 200 || result.IsError {
+		t.Fatal("fake connection failed")
+	}
+	peer := f.connection(t)
+	var writes sync.Mutex
+	write := func(data []byte) {
+		writes.Lock()
+		defer writes.Unlock()
+		_ = peer.SetWriteDeadline(time.Now().Add(5 * time.Second))
+		_, _ = peer.Write(data)
+	}
+	var sends atomic.Int32
+	go func() {
+		reader := bufio.NewReader(peer)
+		for {
+			line, err := reader.ReadString('\n')
+			if err != nil {
+				return
+			}
+			if strings.TrimSpace(line) == f.secret {
+				sends.Add(1)
+				cut := len(f.secret) / 2
+				write([]byte("echo:" + f.secret[:cut]))
+				write([]byte(f.secret[cut:] + "\n$ \n"))
+			}
+		}
+	}()
+	result = f.mcp(t, "hardware_login", map[string]any{"hardware_id": "board", "timeout_seconds": 3, "retries": 3})
+	if result.Status != 200 || result.IsError {
+		t.Fatal("legitimate login failed")
+	}
+	write([]byte("password:"))
+	deadline := time.Now().Add(5 * time.Second)
+	for f.manager.Status(f.task.ID, "board", "network", false).LoginStatus != "authenticated" {
+		if time.Now().After(deadline) {
+			t.Fatal("fake login did not authenticate")
+		}
+		time.Sleep(time.Millisecond)
+	}
+	result = f.mcp(t, "hardware_login", map[string]any{"hardware_id": "board", "wakeup": true, "retries": 3})
+	if result.Status != 200 || result.IsError {
+		t.Fatal("authenticated idempotent login failed")
+	}
+	write([]byte("password:\nAFTER-AUTH-MARKER\n"))
+	var joined string
+	for {
+		page, err := f.h.database.HardwareIOPage(f.h.ctx, f.task.ID, "board", "network", 0, 2000)
+		if err != nil {
+			t.Fatal(err)
+		}
+		var data strings.Builder
+		for _, event := range page.Items {
+			data.WriteString(event.Data)
+		}
+		joined = data.String()
+		if strings.Contains(joined, f.secret) {
+			t.Fatal("saved credential persisted in history")
+		}
+		if strings.Contains(joined, "AFTER-AUTH-MARKER") {
+			break
+		}
+		if time.Now().After(deadline) {
+			t.Fatal("fake terminal marker not recorded")
+		}
+		time.Sleep(time.Millisecond)
+	}
+	if sends.Load() != 1 || !strings.Contains(joined, "••••") {
+		t.Fatal("credential replay was sent or echo was not masked")
+	}
+	root := filepath.Join(f.h.database.DataDir(), "hardware-io")
+	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
+		if walkErr != nil {
+			return walkErr
+		}
+		if entry.IsDir() {
+			return nil
+		}
+		data, err := os.ReadFile(path)
+		if err != nil {
+			return err
+		}
+		if bytes.Contains(data, []byte(f.secret)) {
+			t.Fatal("credential exists in persisted terminal file")
+		}
+		return nil
+	}); err != nil {
+		t.Fatal("could not inspect isolated terminal persistence")
+	}
+	response := agentRequest(t, f.server.URL+"/api/v1/agent/hardware/board/terminal?transport=network", http.MethodGet, f.token, nil)
+	data, err := io.ReadAll(response.Body)
+	response.Body.Close()
+	if err != nil || response.StatusCode != 200 || bytes.Contains(data, []byte(f.secret)) {
+		t.Fatal("Agent HTTP terminal exposed credential")
+	}
+	result = f.mcp(t, "hardware_terminal_read", map[string]any{"hardware_id": "board", "transport": "network"})
+	if result.Status != 200 || result.IsError || strings.Contains(result.Text, f.secret) {
+		t.Fatal("MCP terminal exposed credential")
+	}
+	jar, _ := cookiejar.New(nil)
+	owner := &http.Client{Jar: jar}
+	_ = registerOwner(t, owner, f.server.URL)
+	parsed, _ := url.Parse(f.server.URL)
+	header := http.Header{}
+	for _, cookie := range jar.Cookies(parsed) {
+		header.Add("Cookie", cookie.String())
+	}
+	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
+	defer cancel()
+	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/tasks/"+f.task.ID+"/hardware/board/terminal/ws?transport=network", &websocket.DialOptions{HTTPHeader: header})
+	if err != nil {
+		t.Fatal("owner terminal WebSocket fixture failed")
+	}
+	defer connection.Close(websocket.StatusNormalClosure, "")
+	write([]byte("LIVE-ECHO:" + f.secret[:len(f.secret)/2]))
+	write([]byte(f.secret[len(f.secret)/2:] + "\nWS-LIVE-SAFE-MARKER\n"))
+	var stream bytes.Buffer
+	for {
+		_, data, err := connection.Read(ctx)
+		if err != nil {
+			t.Fatal("terminal replay/live marker did not arrive")
+		}
+		if bytes.Contains(data, []byte(f.secret)) {
+			t.Fatal("WebSocket frame exposed credential")
+		}
+		stream.Write(data)
+		if bytes.Contains(stream.Bytes(), []byte("WS-LIVE-SAFE-MARKER")) {
+			break
+		}
+	}
+	if bytes.Contains(stream.Bytes(), []byte(f.secret)) {
+		t.Fatal("credential spans WebSocket frames")
+	}
+	result = f.mcp(t, "hardware_terminal_read", map[string]any{"hardware_id": "board", "transport": "network"})
+	if result.Status != 200 || result.IsError || strings.Contains(result.Text, f.secret) {
+		t.Fatal("live echo reached retained MCP history")
+	}
+
+}
+
+func TestLoginAndSendFailuresHaveFixedDistinctStateCodes(t *testing.T) {
+	f := newHardwareAuthorizationFixture(t, false)
+	for _, operation := range []string{"hardware_login", "hardware_send"} {
+		args := map[string]any{"hardware_id": "board"}
+		if operation == "hardware_send" {
+			args["data"] = "fixture"
+		}
+		result := f.mcp(t, operation, args)
+		if !result.IsError || !strings.Contains(result.Text, "hardware_not_connected") {
+			t.Fatal("unconnected operation lost its fixed classification")
+		}
+	}
+	if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"}); result.IsError {
+		t.Fatal("fake connection failed")
+	}
+	_ = f.connection(t)
+	result := f.mcp(t, "hardware_send", map[string]any{"hardware_id": "board", "data": "!invalid!", "encoding": "base64"})
+	if !result.IsError || !strings.Contains(result.Text, "hardware_encoding_invalid") {
+		t.Fatal("invalid input was confused with connection state")
+	}
+	for _, args := range []map[string]any{{"hardware_id": "board", "timeout_seconds": 1}, {"hardware_id": "board", "retries": 5}} {
+		result := f.mcp(t, "hardware_login", args)
+		if !result.IsError {
+			t.Fatal("schema accepted login options outside manager semantics")
+		}
+	}
+	result = f.mcp(t, "hardware_login", map[string]any{"hardware_id": "board", "timeout_seconds": 3, "retries": 3})
+	if result.IsError {
+		t.Fatal("valid bounded login options rejected")
+	}
+	result = f.mcp(t, "hardware_login", map[string]any{"hardware_id": "board", "cancel": true})
+	if result.IsError || f.manager.Status(f.task.ID, "board", "network", false).LoginStatus != "cancelled" {
+		t.Fatal("explicit login cancellation did not disarm")
+	}
+}
+
+// Retained records written before stream sanitization (and stored credentials
+// typed as single keystrokes) are read through every history surface with
+// cursors and limits that split the credential. The fixture text contains no
+// lowercase hex, so any such byte in returned terminal data is a leak.
+func TestRetainedCredentialFragmentsStayMaskedAcrossPagedHistorySurfaces(t *testing.T) {
+	f := newHardwareAuthorizationFixture(t, true)
+	leaked := func(value string) bool { return strings.ContainsAny(value, "0123456789abcdef") }
+	appendEvent := func(direction, data string) int64 {
+		sequence, err := f.h.database.AppendHardwareIO(f.h.ctx, domain.HardwareIOEvent{ID: domain.NewID("hardware_io"), TaskID: f.task.ID, HardwareID: "board", Transport: "network", Direction: direction, Data: data, Encoding: "text", Source: "runtime", CreatedAt: time.Now().UTC()})
+		if err != nil {
+			t.Fatal(err)
+		}
+		return sequence
+	}
+	// after=0 means "newest page", so paging starts at an earlier event.
+	start := appendEvent("system", "BOOT")
+	first := appendEvent("rx", "PRE:"+f.secret[:16])
+	appendEvent("rx", f.secret[16:32])
+	appendEvent("rx", f.secret[32:]+"\nPOST\n")
+	for index := range f.secret {
+		appendEvent("tx", f.secret[index:index+1])
+	}
+	type item struct {
+		Sequence  int64  `json:"sequence"`
+		Direction string `json:"direction"`
+		Data      string `json:"data"`
+	}
+	jar, _ := cookiejar.New(nil)
+	owner := &http.Client{Jar: jar}
+	_ = registerOwner(t, owner, f.server.URL)
+	readers := map[string]func(after int64) ([]item, bool){
+		"agent_http": func(after int64) ([]item, bool) {
+			response := agentRequest(t, f.server.URL+"/api/v1/agent/hardware/board/terminal?transport=network&limit=1&after="+strconv.FormatInt(after, 10), http.MethodGet, f.token, nil)
+			defer response.Body.Close()
+			var body struct {
+				Stream struct {
+					Items   []item `json:"items"`
+					HasMore bool   `json:"has_more"`
+				} `json:"stream"`
+			}
+			if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&body) != nil {
+				t.Fatal("Agent HTTP history read failed")
+			}
+			return body.Stream.Items, body.Stream.HasMore
+		},
+		"owner_http": func(after int64) ([]item, bool) {
+			response, err := owner.Get(f.server.URL + "/api/v1/tasks/" + f.task.ID + "/hardware/board/terminal?transport=network&limit=1&after=" + strconv.FormatInt(after, 10))
+			if err != nil {
+				t.Fatal(err)
+			}
+			defer response.Body.Close()
+			var body struct {
+				Stream struct {
+					Items   []item `json:"items"`
+					HasMore bool   `json:"has_more"`
+				} `json:"stream"`
+			}
+			if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&body) != nil {
+				t.Fatal("owner HTTP history read failed")
+			}
+			return body.Stream.Items, body.Stream.HasMore
+		},
+		"mcp": func(after int64) ([]item, bool) {
+			result := f.mcp(t, "hardware_terminal_read", map[string]any{"hardware_id": "board", "transport": "network", "after": after, "limit": 1})
+			var body struct {
+				Items     []item `json:"items"`
+				Truncated bool   `json:"truncated"`
+			}
+			if result.Status != 200 || result.IsError || json.Unmarshal([]byte(result.Text), &body) != nil {
+				t.Fatal("MCP history read failed")
+			}
+			return body.Items, body.Truncated
+		},
+	}
+	for name, read := range readers {
+		var rx, tx strings.Builder
+		after, pages := int64(0), 0
+		for after = start; ; pages++ {
+			if pages > 80 {
+				t.Fatal(name + " paging did not finish")
+			}
+			items, more := read(after)
+			for _, event := range items {
+				if event.Direction == "rx" {
+					rx.WriteString(event.Data)
+				} else {
+					tx.WriteString(event.Data)
+				}
+				after = event.Sequence
+			}
+			if !more || len(items) == 0 {
+				break
+			}
+		}
+		if leaked(rx.String()) || leaked(tx.String()) {
+			t.Fatalf("%s paged history revealed part of a retained credential (pages=%d)", name, pages)
+		}
+		if !strings.HasPrefix(rx.String(), "PRE:") || !strings.Contains(rx.String(), "\nPOST\n") || !strings.Contains(tx.String(), "••••") {
+			t.Fatalf("%s paged history lost ordinary output (pages=%d rx=%q tx=%q)", name, pages, rx.String(), tx.String())
+		}
+	}
+	if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"}); result.Status != 200 || result.IsError {
+		t.Fatal("fake connection failed")
+	}
+	_ = f.connection(t)
+	parsed, _ := url.Parse(f.server.URL)
+	header := http.Header{}
+	for _, cookie := range jar.Cookies(parsed) {
+		header.Add("Cookie", cookie.String())
+	}
+	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
+	defer cancel()
+	// Replay starts after the first fragment, so it opens mid-credential.
+	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/tasks/"+f.task.ID+"/hardware/board/terminal/ws?transport=network&after="+strconv.FormatInt(first, 10), &websocket.DialOptions{HTTPHeader: header})
+	if err != nil {
+		t.Fatal("owner terminal WebSocket fixture failed")
+	}
+	defer connection.Close(websocket.StatusNormalClosure, "")
+	var replay bytes.Buffer
+	for !bytes.Contains(replay.Bytes(), []byte("\nPOST\n")) {
+		kind, data, err := connection.Read(ctx)
+		if err != nil {
+			t.Fatal("WebSocket replay did not arrive")
+		}
+		if kind == websocket.MessageBinary {
+			replay.Write(data)
+		}
+	}
+	if leaked(replay.String()) {
+		t.Fatal("WebSocket replay opened with a retained credential tail")
+	}
+}
diff --git a/internal/httpapi/hardware_websocket.go b/internal/httpapi/hardware_websocket.go
index e7f5fe2..2517080 100644
--- a/internal/httpapi/hardware_websocket.go
+++ b/internal/httpapi/hardware_websocket.go
@@ -51,7 +51,7 @@ func (s *Server) hardwareTerminalWebSocket(writer http.ResponseWriter, request *
 	if afterSequence < 0 {
 		afterSequence = 0
 	}
-	page, err := s.store.HardwareIOPage(request.Context(), task.ID, group.ID, transport, afterSequence, 1000)
+	page, err := s.hardwareHistoryPage(request.Context(), group, transport, afterSequence, 1000)
 	if err != nil {
 		writeError(writer, http.StatusInternalServerError, "hardware_io_failed")
 		return
```
