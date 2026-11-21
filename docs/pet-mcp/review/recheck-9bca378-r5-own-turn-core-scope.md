# 复审 N2：宠物 core 的当前 Turn 范围与只读状态写入

统一固定源码：`9bca3785b3ef1d071faed06041aeb2e6de0139a7`，tree `5d2a491a18fe4df4c9c348c0e491aba87a8537ba`。问题 diff 边界为 `9d01cb63cca51bff23c40dad2c61d55497f2cd3a..9bca3785b3ef1d071faed06041aeb2e6de0139a7` 中下列完整文件；门禁 diff 保留 3 行上下文。所有源码摘录也从该固定提交读取，不来自正在编辑的 worktree。

core 自己的 Task 元数据、Inbox 和当前 Task 附件收尾不取决于宠物跨项目查看开关，避免撤权后丢失 owner 的停止/纠正消息；它们也不提供跨项目管理权。宠物 task_get/task_list 只返回自身，内部 pet Project 的其他 Task 也被拒绝；旧 AgentProjectTask HTTP 入口同样限制。Inbox 始终按 claims Task/Agent/Turn 领取；附件 ID 先按当前 Task 查询，另一 Task 的附件拒绝且不改其绑定。memory_update 的 HTTP 与 MCP 都要求可写 Main，capability 标志相应校正。普通协作者原 HTTP progress 仅显示自己 Task 的进度，明确保留，不因此获得资源写权限。

固定提交的 20 个具名定向回归全部执行且退出 0（136.834s，显式核对每个顶层 Test 的 pass，missing=0）；`go vet ./internal/app ./internal/httpapi ./internal/hardware` 退出 0（1.176s）。完整命令和逐项覆盖在 `recheck-9bca378-validation.md`。固定提交的 app/httpapi 全包尚未完成，当前排队；旧 worktree 全包运行期间源码有改动，只作诊断，不是这个提交的通过证据。不能据此合并或声称完整 task-722 已完成。

完整 diff 文件边界：

- `internal/app/agent_control.go`
- `internal/app/agent_core.go`
- `internal/httpapi/agent_core_pet_scope_test.go`
- `internal/httpapi/agent_state_handlers.go`

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

### `internal/app/agent_control.go` · `WaitAgentInbox`

```go
func (s *Service) WaitAgentInbox(
	ctx context.Context,
	claims agentapi.Claims,
	after int64,
	timeout time.Duration,
) ([]domain.LiveInboxMessage, int64, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return nil, after, err
	}
	if after < 0 {
		after = 0
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		items, claimErr := s.store.ClaimOwnerInboxForTurn(
			ctx, call.Task.ID, call.Turn.AgentID, call.Turn.RoundID, call.Turn.ID, after, 100,
		)
		if claimErr != nil {
			return nil, after, claimErr
		}
		if len(items) > 0 {
			messages := make([]domain.LiveInboxMessage, 0, len(items))
			for _, item := range items {
				messages = append(messages, liveInboxMessage(item))
				if item.Sequence > after {
					after = item.Sequence
				}
			}
			s.emit(ctx, call.Task.ID, "inbox", call.Turn.ID, "agent_inbox_delivered_in_turn", map[string]any{
				"round_id": call.Turn.RoundID, "turn_id": call.Turn.ID, "agent_id": call.Turn.AgentID,
				"message_count": len(messages), "next_cursor": after,
			})
			return messages, after, nil
		}
		select {
		case <-ctx.Done():
			return nil, after, ctx.Err()
		case <-timer.C:
			return []domain.LiveInboxMessage{}, after, nil
		case <-ticker.C:
		}
	}
}
```

### `internal/app/agent_control.go` · `UpdateAgentMemory`

```go
func (s *Service) UpdateAgentMemory(ctx context.Context, claims agentapi.Claims, update MemoryUpdate) (domain.TaskMemory, error) {
	call, err := s.agentWriteCallContext(ctx, claims)
	if err != nil {
		return domain.TaskMemory{}, err
	}
	memory, err := s.store.TaskMemory(ctx, call.Task.ID)
	if err != nil {
		return domain.TaskMemory{}, err
	}
	if err := s.applyMemoryUpdate(ctx, call.Task, call.Turn, memory, update); err != nil {
		return domain.TaskMemory{}, err
	}
	return s.store.TaskMemory(ctx, call.Task.ID)
}
```

### `internal/app/agent_control.go` · `AgentProjectTask`

```go
func (s *Service) AgentProjectTask(ctx context.Context, claims agentapi.Claims, taskID string) (domain.Task, []domain.Turn, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return domain.Task{}, nil, err
	}
	item, err := s.store.Task(ctx, strings.TrimSpace(taskID))
	if err != nil || item.ProjectID != call.Project.ID || (call.Task.ID == petTaskID && item.ID != call.Task.ID) {
		return domain.Task{}, nil, ErrAgentCallForbidden
	}
	turns, err := s.store.ListTurns(ctx, item.ID)
	return item, turns, err
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

### `internal/app/agent_core.go` · `agentWriteCallContext`

```go
func (s *Service) agentWriteCallContext(ctx context.Context, claims agentapi.Claims) (AgentCallContext, error) {
	call, err := s.AgentCallContext(ctx, claims, true)
	if err != nil {
		return AgentCallContext{}, err
	}
	if !s.AgentWriteAllowed(ctx, call) {
		return AgentCallContext{}, ErrAgentReadOnly
	}
	return call, nil
}
```

### `internal/app/agent_core.go` · `AgentCoreTaskGet`

```go
func (s *Service) AgentCoreTaskGet(ctx context.Context, claims agentapi.Claims, taskID string) (AgentCoreTaskView, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return AgentCoreTaskView{}, err
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		taskID = call.Task.ID
	}
	item, err := s.store.Task(ctx, taskID)
	if err != nil || item.ProjectID != call.Project.ID || (call.Task.ID == petTaskID && item.ID != call.Task.ID) {
		return AgentCoreTaskView{}, ErrAgentCallForbidden
	}
	turns, err := s.store.ListTurns(ctx, item.ID)
	if err != nil {
		return AgentCoreTaskView{}, err
	}
	view := agentCoreTaskView(item)
	view.Turns = make([]AgentCoreTurnView, 0, len(turns))
	for _, turn := range turns {
		view.Turns = append(view.Turns, agentCoreTurnView(turn))
	}
	return view, nil
}
```

### `internal/app/agent_core.go` · `AgentCoreTaskList`

```go
func (s *Service) AgentCoreTaskList(ctx context.Context, claims agentapi.Claims, limit int) ([]AgentCoreTaskView, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	items, err := s.store.ListTasks(ctx, call.Project.ID)
	if err != nil {
		return nil, err
	}
	result := make([]AgentCoreTaskView, 0, min(len(items), limit))
	for _, item := range items {
		if call.Task.ID == petTaskID && item.ID != call.Task.ID {
			continue
		}
		if len(result) == limit {
			break
		}
		result = append(result, agentCoreTaskView(item))
	}
	return result, nil
}
```

### `internal/app/agent_core.go` · `BindAgentAttachments`

```go
func (s *Service) BindAgentAttachments(ctx context.Context, claims agentapi.Claims, message string, attachmentIDs []string) ([]domain.Attachment, error) {
	call, err := s.agentWriteCallContext(ctx, claims)
	if err != nil {
		return nil, err
	}
	message = strings.TrimSpace(message)
	if len([]rune(message)) > 4000 {
		return nil, fmt.Errorf("message exceeds 4000 characters")
	}
	if len(attachmentIDs) == 0 {
		return nil, fmt.Errorf("attachment_ids is required")
	}
	if len(attachmentIDs) > 8 {
		return nil, fmt.Errorf("at most 8 attachments are allowed")
	}
	seen := map[string]bool{}
	for _, id := range attachmentIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return nil, fmt.Errorf("invalid attachment selection")
		}
		seen[id] = true
		item, err := s.store.Attachment(ctx, call.Task.ID, id)
		if err != nil {
			return nil, fmt.Errorf("attachment %s is unavailable", id)
		}
		if item.MessageID != "" {
			return nil, fmt.Errorf("attachment %s is already sent", id)
		}
	}
	summary := message
	if summary == "" {
		summary = "附件"
	}
	item := domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: call.Task.ID, RoundID: call.Turn.RoundID, TurnID: call.Turn.ID,
		AgentID: call.Turn.AgentID, StreamAgentID: call.Turn.AgentID, FromAgentID: call.Turn.AgentID,
		ToAgentID: "owner", RouteKind: "agent_attachment_binding", Category: "update",
		Kind: "agent_message_update", Summary: summary, Payload: map[string]any{"api": true},
		CreatedAt: s.now().UTC(),
	}
	created, err := s.store.AddConversationItemWithAttachments(ctx, item, attachmentIDs)
	if err != nil {
		return nil, err
	}
	attachments, _ := created.Payload["attachments"].([]domain.Attachment)
	s.emitTurn(ctx, call.Turn, "agent_attachment_binding", map[string]any{"attachment_count": len(attachments)})
	return attachments, nil
}
```

### `internal/app/agent_core.go` · `AddAgentCoreProgress`

```go
func (s *Service) AddAgentCoreProgress(ctx context.Context, claims agentapi.Claims, message string) error {
	if _, err := s.agentWriteCallContext(ctx, claims); err != nil {
		return err
	}
	return s.AddAgentProgress(ctx, claims, message, nil)
}
```

### `internal/app/agent_control.go` · `AddAgentProgress`

```go
func (s *Service) AddAgentProgress(ctx context.Context, claims agentapi.Claims, message string, attachmentIDs []string) error {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return err
	}
	message = strings.TrimSpace(message)
	if message == "" && len(attachmentIDs) == 0 {
		return fmt.Errorf("progress message or attachment is required")
	}
	if len([]rune(message)) > 4000 {
		return fmt.Errorf("progress message exceeds 4000 characters")
	}
	item := domain.ConversationItem{
		ID: domain.NewID("conversation"), TaskID: call.Task.ID, RoundID: call.Turn.RoundID, TurnID: call.Turn.ID,
		AgentID: call.Turn.AgentID, StreamAgentID: call.Turn.AgentID, FromAgentID: call.Turn.AgentID, ToAgentID: "owner",
		RouteKind: "agent_progress", Category: "update", Kind: "agent_message_update", Summary: message,
		Payload: map[string]any{"api": true}, CreatedAt: s.now().UTC(),
	}
	_, err = s.store.AddConversationItemWithAttachments(ctx, item, attachmentIDs)
	if err == nil {
		s.emitTurn(ctx, call.Turn, "agent_progress", map[string]any{"message": message})
	}
	return err
}
```

### `internal/store/conversation.go` · `AddConversationItemWithAttachments`

```go
func (s *Store) AddConversationItemWithAttachments(ctx context.Context, item domain.ConversationItem, attachmentIDs []string) (domain.ConversationItem, error) {
	item = normalizeConversationItem(item)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ConversationItem{}, err
	}
	defer tx.Rollback()
	attachments, err := bindAttachmentsTx(ctx, tx, item.TaskID, item.ID, attachmentIDs)
	if err != nil {
		return domain.ConversationItem{}, err
	}
	if item.Payload == nil {
		item.Payload = map[string]any{}
	}
	if len(attachments) > 0 {
		item.Payload["attachments"] = attachments
	}
	item, err = insertConversationItem(ctx, tx, item)
	if err != nil {
		return domain.ConversationItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ConversationItem{}, err
	}
	return item, nil
}
```

### `internal/httpapi/agent_core_mcp.go` · `callAgentCoreMCPTool`

```go
func (s *Server) callAgentCoreMCPTool(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims, call app.AgentCallContext, payload mcpRequest) {
	respond := func(result any) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID, "result": result,
		})
	}
	toolError := func(message string) {
		respond(map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": message}},
		})
	}
	respondJSON := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			toolError("result could not be encoded")
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
		case errors.Is(err, app.ErrAgentReadOnly):
			writeError(writer, http.StatusForbidden, app.ErrCodeTaskReadOnly)
		default:
			toolError(safeCoreToolMessage(err))
		}
	}

	var input struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
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
	known := false
	for _, name := range app.CoreMCPToolNames(true) {
		if name == input.Name {
			known = true
			break
		}
	}
	if !known {
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
		return
	}
	auditResult := "ok"
	started := time.Now()
	defer func() {
		s.audit(request, "agent.core_mcp.call", "task", call.Task.ID, map[string]any{
			"tool": input.Name, "turn_id": call.Turn.ID, "result": auditResult,
			"duration_ms":   time.Since(started).Milliseconds(),
			"argument_keys": coreMCPArgumentKeys(input.Arguments),
		})
	}()
	if app.CoreMCPToolIsWrite(input.Name) {
		if call.Turn.AgentID != "main" {
			auditResult = "denied"
			writeError(writer, http.StatusForbidden, "agent_operation_forbidden")
			return
		}
		if !coreMCPNameIncluded(s.app.AgentCoreMCPToolNames(request.Context(), call), input.Name) {
			auditResult = "denied"
			writeError(writer, http.StatusForbidden, app.ErrCodeTaskReadOnly)
			return
		}
	}

	switch input.Name {
	case "inbox_wait":
		var args struct {
			AfterCursor    int64 `json:"after_cursor"`
			TimeoutSeconds int   `json:"timeout_seconds"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil ||
			args.AfterCursor < 0 || args.TimeoutSeconds < 0 || args.TimeoutSeconds > 30 {
			auditResult = "invalid"
			toolError("invalid inbox_wait arguments")
			return
		}
		messages, next, err := s.app.WaitAgentInbox(
			request.Context(), claims, args.AfterCursor, time.Duration(args.TimeoutSeconds)*time.Second,
		)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"messages": messages, "next_cursor": next})

	case "task_get":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			toolError("invalid task_get arguments")
			return
		}
		view, err := s.app.AgentCoreTaskGet(request.Context(), claims, args.TaskID)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(view)

	case "task_list":
		var args struct {
			Limit int `json:"limit"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil || args.Limit < 0 || args.Limit > 50 {
			auditResult = "invalid"
			toolError("invalid task_list arguments")
			return
		}
		views, err := s.app.AgentCoreTaskList(request.Context(), claims, args.Limit)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"tasks": views})

	case "capabilities":
		if err := decodeCoreToolArguments(input.Arguments, &struct{}{}); err != nil {
			auditResult = "invalid"
			toolError("capabilities takes no arguments")
			return
		}
		respondJSON(map[string]any{"capabilities": s.agentCapabilityMap(request.Context(), claims, call)})

	case "memory_update":
		var args app.MemoryUpdate
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			toolError("invalid memory_update arguments")
			return
		}
		if args.Empty() {
			auditResult = "invalid"
			toolError("memory_update needs at least one field")
			return
		}
		memory, err := s.app.UpdateAgentMemory(request.Context(), claims, args)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"ok": true, "memory": compactTaskMemory(memory)})

	case "progress":
		var args struct {
			Message string `json:"message"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil {
			auditResult = "invalid"
			toolError("invalid progress arguments")
			return
		}
		if err := s.app.AddAgentCoreProgress(request.Context(), claims, args.Message); err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"ok": true})

	case "task_message":
		var args struct {
			TargetTaskID string `json:"target_task_id"`
			Content      string `json:"content"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil ||
			strings.TrimSpace(args.TargetTaskID) == "" ||
			strings.TrimSpace(args.Content) == "" || len([]rune(args.Content)) > 8000 {
			auditResult = "invalid"
			toolError("task_message needs target_task_id and 1-8000 characters of content")
			return
		}
		turn, err := s.app.SubmitPeerTaskMessage(request.Context(), claims, args.TargetTaskID, args.Content)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"ok": true, "target_task_id": args.TargetTaskID, "turn_id": turn.ID})

	case "attachment_bind":
		var args struct {
			AttachmentIDs []string `json:"attachment_ids"`
			Message       string   `json:"message"`
		}
		if err := decodeCoreToolArguments(input.Arguments, &args); err != nil ||
			len(args.AttachmentIDs) == 0 || len(args.AttachmentIDs) > 8 ||
			len([]rune(args.Message)) > 4000 {
			auditResult = "invalid"
			toolError("attachment_bind needs 1-8 attachment_ids and an optional message of at most 4000 characters")
			return
		}
		attachments, err := s.app.BindAgentAttachments(request.Context(), claims, args.Message, args.AttachmentIDs)
		if err != nil {
			auditResult = "error"
			fail(err)
			return
		}
		respondJSON(map[string]any{"ok": true, "attachments": attachments})

	default:
		auditResult = "invalid"
		writeJSON(writer, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": payload.ID,
			"error": map[string]any{"code": -32602, "message": "unknown tool"},
		})
	}
}
```

## 本问题边界的完整 diff

```diff
diff --git a/internal/app/agent_control.go b/internal/app/agent_control.go
index 9dbd114..27702f2 100644
--- a/internal/app/agent_control.go
+++ b/internal/app/agent_control.go
@@ -323,7 +323,7 @@ func (s *Service) SetAgentChannelReplyDecision(ctx context.Context, claims agent
 }
 
 func (s *Service) UpdateAgentMemory(ctx context.Context, claims agentapi.Claims, update MemoryUpdate) (domain.TaskMemory, error) {
-	call, err := s.AgentCallContext(ctx, claims, true)
+	call, err := s.agentWriteCallContext(ctx, claims)
 	if err != nil {
 		return domain.TaskMemory{}, err
 	}
@@ -761,7 +761,7 @@ func (s *Service) AgentProjectTask(ctx context.Context, claims agentapi.Claims,
 		return domain.Task{}, nil, err
 	}
 	item, err := s.store.Task(ctx, strings.TrimSpace(taskID))
-	if err != nil || item.ProjectID != call.Project.ID {
+	if err != nil || item.ProjectID != call.Project.ID || (call.Task.ID == petTaskID && item.ID != call.Task.ID) {
 		return domain.Task{}, nil, ErrAgentCallForbidden
 	}
 	turns, err := s.store.ListTurns(ctx, item.ID)
diff --git a/internal/app/agent_core.go b/internal/app/agent_core.go
index ac6fc2b..0b02132 100644
--- a/internal/app/agent_core.go
+++ b/internal/app/agent_core.go
@@ -65,7 +65,7 @@ func (s *Service) AgentWriteAllowed(ctx context.Context, call AgentCallContext)
 }
 
 func (s *Service) agentSnapshotWritable(ctx context.Context, task domain.Task, snapshotID, agentID string) bool {
-	if agentID != "main" || task.ReadOnly {
+	if agentID != "main" || task.ReadOnly || task.Status.Terminal() {
 		return false
 	}
 	snapshot, err := s.store.RuntimeSnapshot(ctx, snapshotID)
@@ -143,7 +143,7 @@ func (s *Service) AgentCoreTaskGet(ctx context.Context, claims agentapi.Claims,
 		taskID = call.Task.ID
 	}
 	item, err := s.store.Task(ctx, taskID)
-	if err != nil || item.ProjectID != call.Project.ID {
+	if err != nil || item.ProjectID != call.Project.ID || (call.Task.ID == petTaskID && item.ID != call.Task.ID) {
 		return AgentCoreTaskView{}, ErrAgentCallForbidden
 	}
 	turns, err := s.store.ListTurns(ctx, item.ID)
@@ -178,6 +178,9 @@ func (s *Service) AgentCoreTaskList(ctx context.Context, claims agentapi.Claims,
 	}
 	result := make([]AgentCoreTaskView, 0, min(len(items), limit))
 	for _, item := range items {
+		if call.Task.ID == petTaskID && item.ID != call.Task.ID {
+			continue
+		}
 		if len(result) == limit {
 			break
 		}
diff --git a/internal/httpapi/agent_core_pet_scope_test.go b/internal/httpapi/agent_core_pet_scope_test.go
new file mode 100644
index 0000000..0658c82
--- /dev/null
+++ b/internal/httpapi/agent_core_pet_scope_test.go
@@ -0,0 +1,78 @@
+package httpapi
+
+import (
+	"net/http"
+	"strings"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/app"
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+func TestPetCoreKeepsOwnTurnAccessWithoutReadingOtherInternalTasks(t *testing.T) {
+	f := newHardwareAuthorizationFixture(t, true)
+	h := f.h
+	snapshot, err := h.database.RuntimeSnapshot(h.ctx, f.task.RuntimeConfigSnapshotID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	peer, err := h.service.CreateTask(h.ctx, app.CreateTaskInput{ID: domain.NewID("task"), ProjectID: f.task.ProjectID, WorkspaceID: f.task.WorkspaceID, Title: "INTERNAL-PEER-MARKER", Request: "fixture", ModelID: snapshot.ModelID, AllowSystemProject: true, Isolation: "inplace", Filesystem: "workspace-write", Approval: "never", CollaborationMode: "single", MaxAgents: 1})
+	if err != nil {
+		t.Fatal(err)
+	}
+	peerTurn := createCoreTurn(t, h.database, peer, "main", domain.NewID("inbox_batch"))
+	peerToken := h.issue(peer.ID, "main", peerTurn.ID)
+	_, _, _, err = h.database.EnqueueOwnerMessage(h.ctx, domain.Message{ID: domain.NewID("message"), TaskID: peer.ID, Role: "user", Sender: "owner", Content: "PEER-INBOX-MARKER", CreatedAt: time.Now()}, "main", nil)
+	if err != nil {
+		t.Fatal(err)
+	}
+	assistant, _ := h.database.EnsureOwnerAssistant(h.ctx, time.Now())
+	if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, false, false, false, false, false, false, false, false, time.Now()); err != nil {
+		t.Fatal(err)
+	}
+	url := f.server.URL + "/api/v1/agent/core/mcp"
+	for _, op := range []struct {
+		name string
+		args map[string]any
+	}{
+		{"task_get", map[string]any{}},
+		{"task_list", map[string]any{}},
+		{"inbox_wait", map[string]any{"timeout_seconds": 1}},
+	} {
+		result := coreMCPCallTool(t, url, f.token, op.name, op.args)
+		if result.Status != 200 || result.IsError || strings.Contains(result.Text, "INTERNAL-PEER-MARKER") || strings.Contains(result.Text, "PEER-INBOX-MARKER") {
+			t.Fatalf("own-Turn %s escaped scope or was denied", op.name)
+		}
+	}
+	denied := coreMCPCallTool(t, url, f.token, "task_get", map[string]any{"task_id": peer.ID})
+	if denied.Status != http.StatusForbidden {
+		t.Fatal("pet core read another internal Task")
+	}
+	for _, path := range []string{"/project/workspaces", "/project/runtimes", "/tasks/" + peer.ID} {
+		response := agentRequest(t, f.server.URL+"/api/v1/agent"+path, http.MethodGet, f.token, nil)
+		status := response.StatusCode
+		response.Body.Close()
+		if status != 403 {
+			t.Fatalf("pet direct HTTP escaped live/scope gate at %s: %d", path, status)
+		}
+	}
+	response := agentMultipartRequest(t, f.server.URL+"/api/v1/agent/turn/attachments", peerToken, "peer.txt", []byte("peer fixture"))
+	var uploaded struct {
+		Attachment struct {
+			ID string `json:"id"`
+		} `json:"attachment"`
+	}
+	decodeResponse(t, response, &uploaded)
+	if response.StatusCode != http.StatusCreated {
+		t.Fatal("peer attachment fixture upload failed")
+	}
+	denied = coreMCPCallTool(t, url, f.token, "attachment_bind", map[string]any{"attachment_ids": []string{uploaded.Attachment.ID}})
+	if !denied.IsError {
+		t.Fatal("pet bound another Task's attachment")
+	}
+	stored, err := h.database.Attachment(h.ctx, peer.ID, uploaded.Attachment.ID)
+	if err != nil || stored.MessageID != "" {
+		t.Fatal("denied bind changed another Task's attachment")
+	}
+}
diff --git a/internal/httpapi/agent_state_handlers.go b/internal/httpapi/agent_state_handlers.go
index 7de1b2e..1c5e3bd 100644
--- a/internal/httpapi/agent_state_handlers.go
+++ b/internal/httpapi/agent_state_handlers.go
@@ -53,7 +53,7 @@ func (s *Server) agentCapabilityMap(ctx context.Context, claims agentapi.Claims,
 		"desktop_mcp_write":   s.app.AgentDesktopMCPWriteAllowed(ctx, call),
 		"browser_mcp_write":   s.app.AgentBrowserMCPWriteAllowed(ctx, call),
 		"progress":            true, "live_inbox_wait": true, "knowledge_read": true, "knowledge_feedback": true,
-		"memory_update": main, "knowledge_publish": knowledgePublish, "knowledge_contribute_bound": knowledgePublish && boundKnowledgeContribute, "skill_create": main, "skill_update": main,
+		"memory_update": s.app.AgentWriteAllowed(ctx, call), "knowledge_publish": knowledgePublish, "knowledge_contribute_bound": knowledgePublish && boundKnowledgeContribute, "skill_create": main, "skill_update": main,
 		// Reported separately so a caller can tell whether a global skill is
 		// possible before sending one and being refused.
 		"skill_create_global": main && s.app.AgentGlobalSkillAllowed(ctx, call),
```
