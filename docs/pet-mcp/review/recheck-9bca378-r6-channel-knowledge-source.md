# 复审 N2 / N5：渠道批次、运行时排除与知识单次解析

统一固定源码：`9bca3785b3ef1d071faed06041aeb2e6de0139a7`，tree `5d2a491a18fe4df4c9c348c0e491aba87a8537ba`。问题 diff 边界为 `9d01cb63cca51bff23c40dad2c61d55497f2cd3a..9bca3785b3ef1d071faed06041aeb2e6de0139a7` 中下列完整文件；门禁 diff 保留 3 行上下文。所有源码摘录也从该固定提交读取，不来自正在编辑的 worktree。

实际 scheduler 在创建 Turn 时生成 InboxBatchID，ClaimInboxAndCreateTurn 要求非空并将实际领取项写入该批次。新测试使用真正的 enqueue/claim 路径核对持久 batch，再证明宠物渠道批次不能注入/调用 knowledge、workspace、browser、hardware、desktop；HTTP 对照也验证。既有空 batch 兼容读取仍会检查活动 Task 渠道路由，不能把空字符串当作授权证明。宠物知识反馈去掉先读详情、再解析写入的两步；现在只由反馈服务解析一次 read source/entry ID，然后原样传到所选 vault 或 legacy 写入。模型不拿内部路径；知识 MCP 错误日志改为固定分类，避免回显输入 ID 或底层诊断。

固定提交的 20 个具名定向回归全部执行且退出 0（136.834s，显式核对每个顶层 Test 的 pass，missing=0）；`go vet ./internal/app ./internal/httpapi ./internal/hardware` 退出 0（1.176s）。完整命令和逐项覆盖在 `recheck-9bca378-validation.md`。固定提交的 app/httpapi 全包尚未完成，当前排队；旧 worktree 全包运行期间源码有改动，只作诊断，不是这个提交的通过证据。不能据此合并或声称完整 task-722 已完成。

完整 diff 文件边界：

- `internal/app/agent_knowledge_mcp.go`
- `internal/app/agent_pet_channel_scope_test.go`
- `internal/httpapi/agent_knowledge_mcp.go`
- `internal/httpapi/agent_pet_channel_authorization_test.go`

## 完整调用链依赖源码

### `internal/app/agents.go` · `scheduleAgent`

```go
func (s *Service) scheduleAgent(ctx context.Context, taskID, agentID string) (domain.Turn, bool, error) {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	items, err := s.store.PendingAgentInbox(ctx, taskID, agentID)
	if err != nil || len(items) == 0 {
		return domain.Turn{}, false, err
	}
	// A pending assignment whose member is already linked to a child Turn was
	// claimed before a restart and requeued by recovery. Reconcile it before
	// the active-Turn check so an active child Turn keeps its assignment and a
	// terminal one cannot force a second Turn or an unstartable service.
	items, err = s.reconcilePendingCollaborationAssignments(ctx, taskID, agentID, items)
	if err != nil {
		return domain.Turn{}, false, err
	}
	if len(items) == 0 {
		return domain.Turn{}, false, nil
	}
	if s.scheduleSnapshotHook != nil {
		s.scheduleSnapshotHook()
	}
	if _, err := s.store.ActiveTurnForAgent(ctx, taskID, agentID); err == nil {
		return domain.Turn{}, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.Turn{}, false, err
	}
	agent, err := s.store.TaskAgent(ctx, taskID, agentID)
	if err != nil {
		return domain.Turn{}, false, err
	}
	round, err := s.store.Round(ctx, items[0].RoundID)
	if err != nil {
		return domain.Turn{}, false, err
	}
	if round.Status.Terminal() {
		_ = s.store.CancelPendingInboxForRound(ctx, round.ID, s.now().UTC())
		return domain.Turn{}, false, nil
	}
	generation := 0
	if latest, latestErr := s.store.LatestTurnForAgent(ctx, taskID, agentID); latestErr == nil {
		generation = latest.Generation + 1
	} else if !errors.Is(latestErr, sql.ErrNoRows) {
		return domain.Turn{}, false, latestErr
	}
	// Load the inbox template outside the claim transaction so the batch can be
	// re-rendered inside it from the items the scheduler actually claimed.
	templates, err := s.prompts.TemplatesForLanguage(ctx, s.promptLanguage(ctx))
	if err != nil {
		return domain.Turn{}, false, err
	}
	now := s.now().UTC()
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: taskID, RoundID: round.ID, AgentID: agentID,
		Attempt: 1, Generation: generation, Required: true,
		Title: agent.Title, InputMessageID: round.InputMessageID,
		Status: domain.TurnQueued, RuntimeConfigSnapshotID: agent.RuntimeConfigSnapshotID,
		InboxBatchID: domain.NewID("inbox_batch"), QueuedAt: now,
	}
	render := func(claimed []domain.AgentInboxItem) (domain.Turn, error) {
		for _, item := range claimed {
			if item.SourceTurnID != "" {
				turn.ParentTurnID = item.SourceTurnID
			}
		}
		instruction, renderErr := prompt.RenderInboxBatchContent(templates, inboxTemplateItems(claimed))
		if renderErr != nil {
			return domain.Turn{}, renderErr
		}
		turn.Instruction = instruction
		return turn, nil
	}
	var claimed []domain.AgentInboxItem
	turn, claimed, err = s.store.ClaimInboxAndCreateTurn(ctx, turn, items, render)
	if err != nil {
		if errors.Is(err, store.ErrActiveTurn) {
			return domain.Turn{}, false, nil
		}
		return domain.Turn{}, false, err
	}
	if len(claimed) == 0 {
		// The running Turn's live inbox delivered every message in the
		// snapshot before this scheduler claimed it. The submit has already
		// succeeded and no follow-up Turn (or duplicate delivery) is owed.
		return domain.Turn{}, false, nil
	}
	s.emitTurn(ctx, turn, "turn_queued", map[string]any{
		"sequence": turn.Sequence, "inbox_batch_id": turn.InboxBatchID, "inbox_count": len(claimed),
	})
	s.startTurn(turn)
	return turn, true, nil
}
```

### `internal/store/agents.go` · `ClaimInboxAndCreateTurn`

```go
func (s *Store) ClaimInboxAndCreateTurn(
	ctx context.Context,
	turn domain.Turn,
	items []domain.AgentInboxItem,
	render func([]domain.AgentInboxItem) (domain.Turn, error),
) (domain.Turn, []domain.AgentInboxItem, error) {
	if len(items) == 0 || turn.InboxBatchID == "" {
		return domain.Turn{}, nil, fmt.Errorf("inbox batch is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Turn{}, nil, err
	}
	defer tx.Rollback()
	// Status is read after BEGIN IMMEDIATE acquired the write lock. A concurrent
	// CompleteTask/Cancel/Block therefore either commits before this check and
	// refuses the Turn, or waits for this claim and sees the new active Turn.
	var taskStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id=?`, turn.TaskID).Scan(&taskStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Turn{}, nil, ErrTaskNotRunnable
		}
		return domain.Turn{}, nil, err
	}
	switch domain.TaskStatus(taskStatus) {
	case domain.TaskCompleted, domain.TaskCancelled, domain.TaskBlocked:
		return domain.Turn{}, nil, ErrTaskNotRunnable
	}
	var active int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM turns
		WHERE task_id=? AND agent_id=? AND status IN ('queued','preparing','starting','running','waiting')`,
		turn.TaskID, turn.AgentID,
	).Scan(&active); err != nil {
		return domain.Turn{}, nil, err
	}
	if active > 0 {
		return domain.Turn{}, nil, ErrActiveTurn
	}
	claimedAt := timeString(turn.QueuedAt)
	claimed := make([]domain.AgentInboxItem, 0, len(items))
	for _, item := range items {
		result, err := tx.ExecContext(ctx, `
			UPDATE agent_inbox
			SET status='claimed',batch_id=?,claimed_at=?
			WHERE id=? AND task_id=? AND target_agent_id=? AND status='pending'`,
			turn.InboxBatchID, claimedAt, item.ID, turn.TaskID, turn.AgentID,
		)
		if err != nil {
			return domain.Turn{}, nil, err
		}
		count, _ := result.RowsAffected()
		if count == 1 {
			claimed = append(claimed, item)
			continue
		}
		delivered, err := inboxItemDeliveredByLiveInboxTx(ctx, tx, turn.TaskID, turn.AgentID, item.ID)
		if err != nil {
			return domain.Turn{}, nil, err
		}
		if !delivered {
			return domain.Turn{}, nil, fmt.Errorf("inbox item changed concurrently")
		}
	}
	if len(claimed) == 0 {
		return domain.Turn{}, nil, nil
	}
	if render != nil {
		turn, err = render(claimed)
		if err != nil {
			return domain.Turn{}, nil, err
		}
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence),0)+1 FROM turns WHERE task_id=?`,
		turn.TaskID,
	).Scan(&turn.Sequence); err != nil {
		return domain.Turn{}, nil, err
	}
	if err := insertTurnTx(ctx, tx, turn); err != nil {
		return domain.Turn{}, nil, err
	}
	for _, item := range claimed {
		if item.MessageID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE messages SET turn_id=? WHERE id=?`, turn.ID, item.MessageID); err != nil {
				return domain.Turn{}, nil, err
			}
		}
		batchID := strings.TrimSpace(fmt.Sprint(item.Payload["collaboration_batch_id"]))
		if batchID == "" || batchID == "<nil>" {
			continue
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE collaboration_members
			SET child_turn_id=?,status=?,updated_at=?
			WHERE batch_id=? AND agent_id=? AND child_turn_id=''`,
			turn.ID, domain.CollaborationMemberRunning, claimedAt, batchID, turn.AgentID,
		)
		if err != nil {
			return domain.Turn{}, nil, err
		}
		linked, _ := result.RowsAffected()
		if linked != 1 {
			return domain.Turn{}, nil, fmt.Errorf("collaboration member %s/%s is missing or already linked", batchID, turn.AgentID)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE task_agents SET status='queued',updated_at=?
		WHERE task_id=? AND agent_id=?`,
		claimedAt, turn.TaskID, turn.AgentID,
	); err != nil {
		return domain.Turn{}, nil, err
	}
	return turn, claimed, tx.Commit()
}
```

### `internal/store/channel_inbound.go` · `InboxBatchHasChannelContext`

```go
func (s *Store) InboxBatchHasChannelContext(ctx context.Context, batchID string) (bool, error) {
	if batchID == "" {
		return false, nil
	}
	var invalid, channel int64
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(CASE WHEN json_valid(payload_json) AND json_type(payload_json)='object' THEN 0 ELSE 1 END),0),
       COALESCE(SUM(CASE WHEN json_valid(payload_json) AND json_type(payload_json)='object'
                          AND json_type(payload_json,'$.channel_context') IS NOT NULL THEN 1 ELSE 0 END),0)
FROM agent_inbox WHERE batch_id=?`, batchID).Scan(&invalid, &channel)
	if err != nil {
		return false, err
	}
	if invalid > 0 {
		return false, errors.New("inbox batch contains a malformed payload")
	}
	return channel > 0, nil
}
```

### `internal/store/channel_inbound.go` · `ChannelContextForInboxBatch`

```go
func (s *Store) ChannelContextForInboxBatch(ctx context.Context, batchID string) (map[string]any, error) {
	if batchID == "" {
		return nil, sql.ErrNoRows
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM agent_inbox WHERE batch_id=? ORDER BY sequence`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result map[string]any
	var latest map[string]any
	for rows.Next() {
		var payloadJSON string
		if err := rows.Scan(&payloadJSON); err != nil {
			return nil, err
		}
		payload := decodeJSON(payloadJSON, map[string]any{})
		value, ok := payload["channel_context"].(map[string]any)
		if !ok || len(value) == 0 {
			return nil, sql.ErrNoRows
		}
		stable := map[string]any{}
		for _, key := range []string{"schema", "instance_id", "provider", "endpoint", "conversation_id", "route"} {
			if child, exists := value[key]; exists {
				stable[key] = child
			}
		}
		latest = value
		if result == nil {
			result = stable
			continue
		}
		if encodeJSON(result) != encodeJSON(stable) {
			return nil, errors.New("mixed channel contexts in one inbox batch")
		}
	}
	if result == nil {
		return nil, sql.ErrNoRows
	}
	if latest != nil {
		for _, key := range []string{"actor", "mentions", "inbound_receipt_id", "chat_display_name", "reply_context"} {
			if child, exists := latest[key]; exists {
				result[key] = child
			}
		}
	}
	return result, rows.Err()
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

### `internal/app/agent_knowledge_mcp.go` · `AgentKnowledgeMCPAllowed`

```go
func (s *Service) AgentKnowledgeMCPAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.knowledgeMCPInjected(ctx, call.Task, call.Turn.InboxBatchID)
}
```

### `internal/app/agent_knowledge_mcp.go` · `SubmitAgentKnowledgeMCPFeedback`

```go
func (s *Service) SubmitAgentKnowledgeMCPFeedback(ctx context.Context, claims agentapi.Claims, entryID, kind string) (AgentKnowledgeMCPFeedbackResult, error) {
	if call, err := s.AgentCallContext(ctx, claims, true); err == nil && call.Task.ID == petTaskID {
		if !s.AgentKnowledgeMCPAllowed(ctx, call) || !s.AgentWriteAllowed(ctx, call) {
			return AgentKnowledgeMCPFeedbackResult{}, ErrAgentCallForbidden
		}
		if _, err := s.petKnowledgeWritable(ctx); err != nil {
			return AgentKnowledgeMCPFeedbackResult{}, ErrAgentCallForbidden
		}
		if kind != "helped" && kind != "stale" && kind != "wrong" {
			return AgentKnowledgeMCPFeedbackResult{}, ErrAgentCallForbidden
		}
		result, err := s.feedbackKnowledge(ctx, entryID, kind, "pet")
		if err != nil {
			return AgentKnowledgeMCPFeedbackResult{}, err
		}
		return AgentKnowledgeMCPFeedbackResult{Source: result.Source, Feedback: &AgentKnowledgeMCPFeedback{Kind: kind, ID: result.Entry.ID, Scope: result.Entry.Scope, Status: string(result.Entry.Status), Revision: result.Entry.Revision}}, nil
	}
	call, err := s.agentKnowledgeMCPWriteContext(ctx, claims)
	if err != nil {
		return AgentKnowledgeMCPFeedbackResult{}, err
	}
	entryID = strings.TrimSpace(entryID)
	items, err := s.ApplicableAgentKnowledge(ctx, claims)
	if err != nil {
		return AgentKnowledgeMCPFeedbackResult{}, err
	}
	readable := false
	for _, item := range items {
		if item.ID == entryID && knowledgeMCPDomainVisible(call, item) {
			readable = true
			break
		}
	}
	if !readable {
		// Feedback may only target something this Turn can actually read. This
		// also turns a cross-Project ID into the same not-found result as an
		// unknown ID instead of revealing that the entry exists elsewhere.
		return AgentKnowledgeMCPFeedbackResult{}, ErrKnowledgeMCPEntryNotFound
	}
	result, err := s.SubmitAgentKnowledgeFeedbackEntry(ctx, claims, AgentKnowledgeFeedbackInput{
		EntryID: entryID, Kind: kind,
	})
	if err != nil {
		return AgentKnowledgeMCPFeedbackResult{}, err
	}
	view := AgentKnowledgeMCPFeedbackResult{
		Source: result.Source, Warning: result.Warning, Knowledge: result.Knowledge,
	}
	if result.Feedback != nil {
		view.Feedback = &AgentKnowledgeMCPFeedback{
			Kind: result.Feedback.Kind, ID: result.Feedback.ID, Scope: result.Feedback.Scope,
			Status: result.Feedback.Status, Revision: result.Feedback.Revision, Queued: result.Feedback.Queued,
		}
	}
	return view, nil
}
```

### `internal/app/knowledge_routes.go` · `resolveKnowledgeFeedbackTarget`

```go
func (s *Service) resolveKnowledgeFeedbackTarget(ctx context.Context, entryID string) (knowledgeFeedbackTarget, error) {
	var target knowledgeFeedbackTarget
	id := strings.TrimSpace(entryID)
	if id == "" {
		return target, &knowledgevault.WriteError{Code: knowledgevault.WriteErrorValidation, Message: "entry_id is required"}
	}
	vaultEntry, vaultFound, err := s.vaultEntryByID(ctx, id)
	if err != nil {
		return target, err
	}
	target.InVault = vaultFound
	if vaultFound {
		target.VaultEntry = vaultEntry
		target.Scope, target.ProjectID = vaultEntry.Scope, vaultEntry.ProjectID
	} else {
		legacy, legacyErr := s.store.Knowledge(ctx, id)
		if legacyErr != nil {
			if errors.Is(legacyErr, sql.ErrNoRows) {
				return target, &knowledgevault.WriteError{
					Code:    knowledgevault.WriteErrorNotFound,
					Message: "knowledge entry " + id + " was not found",
				}
			}
			return target, legacyErr
		}
		target.LegacyEntry = legacy
		target.Scope, target.ProjectID = legacy.Scope, legacy.ProjectID
	}
	useVault, routeErr := s.knowledgeWriteRoute(target.Scope, target.ProjectID)
	if routeErr != nil {
		return target, routeErr
	}
	if useVault && !target.InVault {
		// The entry exists only in the legacy tables while this scope reads the
		// vault. Writing that row would update knowledge the reader cannot see.
		return target, ErrKnowledgeReadSourceMismatch
	}
	if !useVault && target.InVault {
		// The write follows the legacy source. The vault copy must not be
		// updated, so the same ID has to exist in the legacy tables as well.
		legacy, legacyErr := s.store.Knowledge(ctx, id)
		if legacyErr != nil {
			return target, &knowledgevault.WriteError{
				Code:    knowledgevault.WriteErrorNotFound,
				Message: "knowledge entry " + id + " is not available from the current read source",
			}
		}
		target.LegacyEntry = legacy
	}
	target.Vault = useVault
	return target, nil
}
```

### `internal/app/knowledge_routes.go` · `feedbackKnowledge`

```go
func (s *Service) feedbackKnowledge(ctx context.Context, id, kind, writer string) (KnowledgeFeedbackWriteResult, error) {
	target, err := s.resolveKnowledgeFeedbackTarget(ctx, id)
	if err != nil {
		return KnowledgeFeedbackWriteResult{}, err
	}
	if target.Vault {
		result, err := s.knowledgeVault.KnowledgeFeedback(ctx, knowledgevault.FeedbackRequest{
			Scope: target.Scope, ProjectID: target.ProjectID, AllowGlobal: true,
			EntryID: target.VaultEntry.ID, Kind: kind, Writer: writer,
		})
		if err != nil {
			return KnowledgeFeedbackWriteResult{}, err
		}
		entry := applyVaultFeedbackResult(vaultEntryThatNeedsMapping(target.VaultEntry), result, kind)
		return KnowledgeFeedbackWriteResult{Entry: entry, Source: "vault"}, nil
	}
	entry, err := s.store.FeedbackKnowledge(ctx, target.LegacyEntry.ID, kind, timeString(s.now().UTC()))
	if err != nil {
		return KnowledgeFeedbackWriteResult{}, err
	}
	warning := s.vaultFallbackWarning(target.ProjectID)
	if target.Scope == "global" {
		warning = s.vaultGlobalFallbackWarning()
	}
	return KnowledgeFeedbackWriteResult{Entry: entry, Source: "legacy", Warning: warning}, nil
}
```

### `internal/app/pet_knowledge.go` · `petKnowledgeReadable`

```go
func (s *Service) petKnowledgeReadable(ctx context.Context) (domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowViewTasks && !assistant.PetAllowManageKnowledge {
		return domain.OwnerAssistant{}, fmt.Errorf("owner 未开启查看知识信息权限")
	}
	return assistant, nil
}
```

### `internal/app/pet_knowledge.go` · `petKnowledgeWritable`

```go
func (s *Service) petKnowledgeWritable(ctx context.Context) (domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowManageKnowledge {
		return domain.OwnerAssistant{}, fmt.Errorf("owner 未开启管理知识/Skill/Prompt 权限")
	}
	return assistant, nil
}
```

### `internal/app/mcp_runtime.go` · `knowledgeMCPInjected`

```go
func (s *Service) knowledgeMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if task.ID == petTaskID {
		_, err := s.petKnowledgeReadable(ctx)
		return task.ProjectID == petProjectID && err == nil && !s.agentMCPChannelBound(ctx, task.ID, inboxBatchID)
	}
	if task.ID == petTaskID || task.ProjectID == petProjectID || !knowledgeEnabled(domain.Project{}, task) {
		return false
	}
	return !s.agentMCPChannelBound(ctx, task.ID, inboxBatchID)
}
```

### `internal/app/mcp_runtime.go` · `workspaceMCPInjected`

```go
func (s *Service) workspaceMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if !s.petDomainReadable(ctx, task) {
		return false
	}
	workspaceID := strings.TrimSpace(task.WorkspaceID)
	if workspaceID == "" {
		return false
	}
	item, err := s.store.Workspace(ctx, workspaceID)
	if err != nil || item.ProjectID != task.ProjectID {
		return false
	}
	return !s.agentMCPChannelBound(ctx, task.ID, inboxBatchID)
}
```

### `internal/app/mcp_runtime.go` · `browserMCPInjected`

```go
func (s *Service) browserMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if !s.petDomainReadable(ctx, task) {
		return false
	}
	if s.agentMCPChannelBound(ctx, task.ID, inboxBatchID) {
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

### `internal/app/agent_desktop_mcp.go` · `desktopMCPInjected`

```go
func (s *Service) desktopMCPInjected(ctx context.Context, task domain.Task, agentID, batchID string) bool {
	return agentID == "main" && s.petDomainReadable(ctx, task) && !s.agentMCPChannelBound(ctx, task.ID, batchID) && s.desktopMCPAvailable != nil && s.desktopMCPAvailable(task.ID)
}
```

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

## 本问题边界的完整 diff

```diff
diff --git a/internal/app/agent_knowledge_mcp.go b/internal/app/agent_knowledge_mcp.go
index 071831b..29dec17 100644
--- a/internal/app/agent_knowledge_mcp.go
+++ b/internal/app/agent_knowledge_mcp.go
@@ -485,9 +485,6 @@ func (s *Service) SubmitAgentKnowledgeMCPFeedback(ctx context.Context, claims ag
 		if _, err := s.petKnowledgeWritable(ctx); err != nil {
 			return AgentKnowledgeMCPFeedbackResult{}, ErrAgentCallForbidden
 		}
-		if _, err := s.PetKnowledgeDetail(ctx, entryID); err != nil {
-			return AgentKnowledgeMCPFeedbackResult{}, ErrKnowledgeMCPEntryNotFound
-		}
 		if kind != "helped" && kind != "stale" && kind != "wrong" {
 			return AgentKnowledgeMCPFeedbackResult{}, ErrAgentCallForbidden
 		}
diff --git a/internal/app/agent_pet_channel_scope_test.go b/internal/app/agent_pet_channel_scope_test.go
new file mode 100644
index 0000000..95b7695
--- /dev/null
+++ b/internal/app/agent_pet_channel_scope_test.go
@@ -0,0 +1,73 @@
+package app
+
+import (
+	"database/sql"
+	"github.com/ChinaKai/AHA2/internal/agentapi"
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"testing"
+	"time"
+)
+
+func TestPetChannelBatchExcludesEverySharedDomainAfterRealInboxClaim(t *testing.T) {
+	f := newPetTaskFixture(t)
+	task := f.createPetDraft(t, "channel scope fixture")
+	assistant, err := f.database.EnsureOwnerAssistant(f.ctx, f.createdAt)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if err := f.database.UpdateOwnerPetPermissions(f.ctx, assistant.ID, true, true, false, false, false, true, false, false, f.createdAt); err != nil {
+		t.Fatal(err)
+	}
+
+	f.service.SetDesktopMCPAvailability(func(string) bool { return true })
+	seedHardwareMCPGroup(t, f.ctx, f.database, task)
+	seedBrowserMCPGrant(t, f.ctx, f.database, task, "pet-channel-fixture", "active")
+	message := domain.Message{ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner", Content: "fixture channel input", CreatedAt: time.Now()}
+	round, inbox, _, err := f.database.EnqueueOwnerMessage(f.ctx, message, "main", nil)
+	if err != nil {
+		t.Fatal(err)
+	}
+	raw, err := sql.Open("sqlite", f.databasePath)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer raw.Close()
+	payload := `{"channel_context":{"schema":"aha2.channel.v1","instance_id":"fixture","endpoint":"group_digital_human","conversation_id":"fixture","route":{"mode":"task_route"}}}`
+	if _, err := raw.Exec(`UPDATE agent_inbox SET payload_json=?,source_agent_id='channel',source_kind='channel_message' WHERE id=?`, payload, inbox.ID); err != nil {
+		t.Fatal(err)
+	}
+	items, err := f.database.PendingAgentInbox(f.ctx, task.ID, "main")
+	if err != nil {
+		t.Fatal(err)
+	}
+	turn := domain.Turn{ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main", InputMessageID: message.ID, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, Status: domain.TurnRunning, InboxBatchID: domain.NewID("inbox_batch"), QueuedAt: time.Now()}
+	turn, claimed, err := f.database.ClaimInboxAndCreateTurn(f.ctx, turn, items, func([]domain.AgentInboxItem) (domain.Turn, error) { return turn, nil })
+	if err != nil || turn.InboxBatchID == "" || len(claimed) != 1 {
+		t.Fatalf("real Inbox claim failed: %v", err)
+	}
+	stored, err := f.database.Turn(f.ctx, turn.ID)
+	if err != nil || stored.InboxBatchID != turn.InboxBatchID {
+		t.Fatal("Inbox batch identity was lost")
+	}
+	servers, _ := f.service.mcpRuntimeForTurn(f.ctx, task, "main", "http://127.0.0.1:8766", stored.InboxBatchID)
+	for _, name := range []string{knowledgeMCPName, workspaceMCPName, browserMCPName, hardwareMCPName, desktopMCPName, collabMCPName} {
+		if hasMCPServer(servers, name) {
+			t.Fatalf("pet channel Turn received %s", name)
+		}
+	}
+	claims := agentapi.Claims{TaskID: task.ID, AgentID: "main", TurnID: turn.ID}
+	call, err := f.service.AgentCallContext(f.ctx, claims, false)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if f.service.AgentKnowledgeMCPAllowed(f.ctx, call) {
+		t.Fatal("pet channel Turn can call knowledge domain")
+	}
+	for _, domainName := range []string{"workspace", "browser", "hardware", "desktop"} {
+		for _, write := range []bool{false, true} {
+			if _, err := f.service.AgentDomainCallContext(f.ctx, claims, domainName, write); err == nil {
+				t.Fatalf("pet channel Turn can call %s write=%v", domainName, write)
+			}
+		}
+	}
+}
diff --git a/internal/httpapi/agent_knowledge_mcp.go b/internal/httpapi/agent_knowledge_mcp.go
index c5a2ac2..8601181 100644
--- a/internal/httpapi/agent_knowledge_mcp.go
+++ b/internal/httpapi/agent_knowledge_mcp.go
@@ -113,8 +113,8 @@ func (s *Server) callAgentKnowledgeMCPTool(writer http.ResponseWriter, request *
 		// Tool text is model-visible. Internal errors can carry host paths
 		// (guide/entry reads); operators get the detail in the server log and
 		// the model gets only the stable classification.
-		log.Printf("agent knowledge mcp tool %s failed: %v", input.Name, err)
 		code, message := knowledgeMCPErrorCode(err)
+		log.Printf("agent knowledge mcp tool %s failed: %s", input.Name, code)
 		respond(map[string]any{
 			"isError": true,
 			"content": []map[string]any{{"type": "text", "text": code + ": " + message}},
diff --git a/internal/httpapi/agent_pet_channel_authorization_test.go b/internal/httpapi/agent_pet_channel_authorization_test.go
new file mode 100644
index 0000000..45ae7c6
--- /dev/null
+++ b/internal/httpapi/agent_pet_channel_authorization_test.go
@@ -0,0 +1,62 @@
+package httpapi
+
+import (
+	"database/sql"
+	"net/http"
+	"net/http/httptest"
+	"testing"
+	"time"
+
+	"github.com/ChinaKai/AHA2/internal/browser"
+	"github.com/ChinaKai/AHA2/internal/browser/browsertest"
+	"github.com/ChinaKai/AHA2/internal/desktop"
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+func TestPetChannelTurnRejectsDomainDiscoveryAndDirectHTTPCalls(t *testing.T) {
+	f := newHardwareAuthorizationFixture(t, true)
+	seededBrowserGrant(t, f.h.database, f.task, "pet-channel-http", "active")
+	manager := desktop.New(&desktopFixture{})
+	t.Cleanup(func() { _ = manager.Close() })
+	server := httptest.NewServer(New(Config{Store: f.h.database, App: f.h.service, AgentCapabilities: f.h.caps, Hardware: f.manager, Browser: browser.NewService(f.h.database, browsertest.New(), "local"), Desktop: manager, ManagedProcesses: f.h.processes}).Handler())
+	t.Cleanup(server.Close)
+	for _, name := range []string{"knowledge", "workspace", "browser", "hardware", "desktop"} {
+		status, _, _ := coreMCPToolsList(t, server.URL+"/api/v1/agent/"+name+"/mcp", f.token)
+		if status != http.StatusOK {
+			t.Fatalf("ordinary pet %s discovery status=%d", name, status)
+		}
+	}
+	raw, err := sql.Open("sqlite", f.h.dbPath)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer raw.Close()
+	payload := `{"channel_context":{"schema":"aha2.channel.v1","instance_id":"fixture","endpoint":"group_digital_human","conversation_id":"fixture","route":{"mode":"task_route"}}}`
+	if _, err := raw.Exec(`INSERT INTO agent_inbox(id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,content,payload_json,status,batch_id,created_at,claimed_at,processed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, domain.NewID("inbox"), f.task.ID, f.turn.RoundID, "main", "channel", "channel_message", "", "", "fixture", payload, "claimed", f.turn.InboxBatchID, time.Now().Format(time.RFC3339Nano), "", ""); err != nil {
+		t.Fatal(err)
+	}
+	tools := map[string]string{"knowledge": "knowledge_guide", "workspace": "workspace_list", "browser": "browser_status", "hardware": "hardware_list", "desktop": "desktop_status"}
+	for name, tool := range tools {
+		url := server.URL + "/api/v1/agent/" + name + "/mcp"
+		status, _, _ := coreMCPToolsList(t, url, f.token)
+		if status != 403 {
+			t.Fatalf("channel pet discovers %s: %d", name, status)
+		}
+		result := coreMCPCallTool(t, url, f.token, tool, map[string]any{})
+		if result.Status != 403 {
+			t.Fatalf("channel pet calls %s: %d", name, result.Status)
+		}
+	}
+	for _, path := range []string{"/hardware", "/browser", "/processes", "/desktop"} {
+		response := agentRequest(t, server.URL+"/api/v1/agent"+path, http.MethodGet, f.token, nil)
+		status := response.StatusCode
+		response.Body.Close()
+		if status != 403 {
+			t.Fatalf("channel pet direct %s status=%d", path, status)
+		}
+	}
+	status, names, _ := coreMCPToolsList(t, server.URL+"/api/v1/agent/core/mcp", f.token)
+	if status != 200 || !coreMCPNameIncluded(names, "inbox_wait") {
+		t.Fatal("channel gate removed essential own-Turn Inbox access")
+	}
+}
```
