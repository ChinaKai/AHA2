package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) CreateTask(ctx context.Context, item domain.Task) error {
	normalizeTaskCollaboration(&item)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	code, err := nextTaskCodeTx(ctx, tx)
	if err != nil {
		return err
	}
	item.Code = code
	_, err = tx.ExecContext(ctx, `
		INSERT INTO tasks(id,code,project_id,workspace_id,title,original_request,current_goal,status,target_branch,base_commit,task_branch,isolation,worktree_dir,task_workspace_path,runtime_config_snapshot_id,collaboration_mode,max_agents,knowledge_policy,skill_ids_json,agent_capabilities_json,created_at,updated_at,completed_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.Code, item.ProjectID, item.WorkspaceID, item.Title, item.OriginalRequest, item.CurrentGoal, item.Status,
		item.TargetBranch, item.BaseCommit, item.TaskBranch, item.Isolation, item.WorktreeDir, item.TaskWorkspacePath, item.RuntimeConfigSnapshotID,
		item.CollaborationMode, item.MaxAgents, item.KnowledgePolicy, encodeJSON(item.SkillIDs), encodeJSON(item.AgentCapabilities),
		timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.CompletedAt),
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO task_memory(task_id,current_goal,updated_at) VALUES(?,?,?)`, item.ID, item.CurrentGoal, timeString(item.CreatedAt))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_agents(task_id,agent_id,role,status,title,runtime_config_snapshot_id,inherit_main,last_read_sequence,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		item.ID, "main", "main", "idle", "Main agent", item.RuntimeConfigSnapshotID, 0, 0,
		timeString(item.CreatedAt), timeString(item.UpdatedAt),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CreateTaskWithSnapshot(ctx context.Context, snapshot domain.RuntimeConfigSnapshot, item domain.Task) (domain.Task, error) {
	normalizeTaskCollaboration(&item)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Task{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO runtime_config_snapshots(id,workspace_id,backend,backend_version,model_id,wire_model,env_group_id,env_group_revision,codex_account_id,proxy_enabled,reasoning_effort,permissions_json,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		snapshot.ID, snapshot.WorkspaceID, snapshot.Backend, snapshot.BackendVersion, snapshot.ModelID,
		snapshot.WireModel, snapshot.EnvGroupID, snapshot.EnvGroupRevision, snapshot.CodexAccountID, boolInt(snapshot.ProxyEnabled), snapshot.ReasoningEffort,
		snapshot.PermissionsJSON, timeString(snapshot.CreatedAt),
	); err != nil {
		return domain.Task{}, err
	}
	code, err := nextTaskCodeTx(ctx, tx)
	if err != nil {
		return domain.Task{}, err
	}
	item.Code = code
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tasks(id,code,project_id,workspace_id,title,original_request,current_goal,status,target_branch,base_commit,task_branch,isolation,worktree_dir,task_workspace_path,runtime_config_snapshot_id,collaboration_mode,max_agents,knowledge_policy,skill_ids_json,agent_capabilities_json,created_at,updated_at,completed_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.Code, item.ProjectID, item.WorkspaceID, item.Title, item.OriginalRequest, item.CurrentGoal, item.Status,
		item.TargetBranch, item.BaseCommit, item.TaskBranch, item.Isolation, item.WorktreeDir, item.TaskWorkspacePath, item.RuntimeConfigSnapshotID,
		item.CollaborationMode, item.MaxAgents, item.KnowledgePolicy, encodeJSON(item.SkillIDs), encodeJSON(item.AgentCapabilities),
		timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.CompletedAt),
	); err != nil {
		return domain.Task{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_memory(task_id,current_goal,updated_at) VALUES(?,?,?)`, item.ID, item.CurrentGoal, timeString(item.CreatedAt)); err != nil {
		return domain.Task{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_agents(task_id,agent_id,role,status,title,runtime_config_snapshot_id,inherit_main,last_read_sequence,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		item.ID, "main", "main", "idle", "Main agent", item.RuntimeConfigSnapshotID, 0, 0,
		timeString(item.CreatedAt), timeString(item.UpdatedAt),
	); err != nil {
		return domain.Task{}, err
	}
	return item, tx.Commit()
}

func normalizeTaskCollaboration(item *domain.Task) {
	if item.CollaborationMode == "" {
		item.CollaborationMode = "auto"
	}
	if item.MaxAgents < 1 {
		item.MaxAgents = 3
	}
	if item.KnowledgePolicy == "" {
		item.KnowledgePolicy = "inherit"
	}
	if item.AgentCapabilities == nil {
		item.AgentCapabilities = map[string]bool{}
	}
}

// nextTaskCodeTx computes the next human-friendly code (task-001, task-002, ...)
// inside an open transaction so concurrent task creation cannot collide.
func nextTaskCodeTx(ctx context.Context, tx *sql.Tx) (string, error) {
	var max int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(CAST(substr(code, 6) AS INTEGER)), 0) FROM tasks WHERE code LIKE 'task-%'`).Scan(&max); err != nil {
		return "", err
	}
	return fmt.Sprintf("task-%03d", max+1), nil
}

// BackfillTaskCodes assigns sequential codes to any tasks that predate the code
// column (returned true if any were changed).
func (s *Store) BackfillTaskCodes(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE code='' ORDER BY created_at,id`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		code, err := nextTaskCodeTx(ctx, tx)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET code=? WHERE id=?`, code, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func scanTask(scanner interface{ Scan(...any) error }) (domain.Task, error) {
	var item domain.Task
	var skillIDsJSON, agentCapabilitiesJSON, createdAt, updatedAt, completedAt string
	err := scanner.Scan(
		&item.ID, &item.Code, &item.ProjectID, &item.WorkspaceID, &item.Title, &item.OriginalRequest, &item.CurrentGoal,
		&item.Status, &item.TargetBranch, &item.BaseCommit, &item.TaskBranch, &item.Isolation, &item.WorktreeDir, &item.TaskWorkspacePath,
		&item.RuntimeConfigSnapshotID, &item.CollaborationMode, &item.MaxAgents, &item.KnowledgePolicy, &skillIDsJSON, &agentCapabilitiesJSON, &createdAt, &updatedAt, &completedAt,
	)
	item.SkillIDs = decodeJSON(skillIDsJSON, []string{})
	item.AgentCapabilities = decodeJSON(agentCapabilitiesJSON, map[string]bool{})
	item.CreatedAt, item.UpdatedAt, item.CompletedAt = parseTime(createdAt), parseTime(updatedAt), parseTime(completedAt)
	return item, err
}

const taskColumns = `id,code,project_id,workspace_id,title,original_request,current_goal,status,target_branch,base_commit,task_branch,isolation,worktree_dir,task_workspace_path,runtime_config_snapshot_id,collaboration_mode,max_agents,knowledge_policy,skill_ids_json,agent_capabilities_json,created_at,updated_at,completed_at`

func (s *Store) Task(ctx context.Context, id string) (domain.Task, error) {
	return scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id=?`, id))
}

func (s *Store) UpdateTaskAgentCapabilities(ctx context.Context, taskID string, capabilities map[string]bool, updatedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET agent_capabilities_json=?,updated_at=? WHERE id=?`, encodeJSON(capabilities), updatedAt, taskID)
	return err
}

func (s *Store) ListTasks(ctx context.Context, projectID string) ([]domain.Task, error) {
	query := `SELECT ` + taskColumns + ` FROM tasks`
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id=?`
		args = append(args, projectID)
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Task{}
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdateTaskStatus(ctx context.Context, id string, from, to domain.TaskStatus, updatedAt, completedAt string) error {
	if err := domain.ValidateTaskTransition(from, to); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE tasks SET status=?,updated_at=?,completed_at=? WHERE id=? AND status=?`, to, updatedAt, completedAt, id, from)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("task status changed concurrently")
	}
	return nil
}

func (s *Store) UpdateTaskGoal(ctx context.Context, id, goal, updatedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET current_goal=?,updated_at=? WHERE id=?`, goal, updatedAt, id)
	return err
}

func (s *Store) UpdateTaskTitle(ctx context.Context, id, title, updatedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET title=?,updated_at=? WHERE id=?`, title, updatedAt, id)
	return err
}

func (s *Store) UpdateTaskCollaboration(ctx context.Context, id, mode string, maxAgents int, updatedAt string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET collaboration_mode=?,max_agents=?,updated_at=? WHERE id=?`,
		mode, maxAgents, updatedAt, id,
	)
	return err
}

func (s *Store) UpdateTaskKnowledgePolicy(ctx context.Context, id, policy, updatedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET knowledge_policy=?,updated_at=? WHERE id=?`, policy, updatedAt, id)
	return err
}

func (s *Store) UpdateTaskSkills(ctx context.Context, id string, skillIDs []string, updatedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET skill_ids_json=?,updated_at=? WHERE id=?`, encodeJSON(skillIDs), updatedAt, id)
	return err
}

func (s *Store) UpdateTaskRuntimeSnapshot(ctx context.Context, id, snapshotID, updatedAt string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET runtime_config_snapshot_id=?,updated_at=? WHERE id=?`,
		snapshotID, updatedAt, id,
	)
	return err
}

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	return s.deleteTask(ctx, id, false)
}

func (s *Store) DeleteTaskWithSyncTombstone(ctx context.Context, id string) error {
	return s.deleteTask(ctx, id, true)
}

func (s *Store) deleteTask(ctx context.Context, id string, tombstone bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, _ := tx.QueryContext(ctx, `SELECT DISTINCT sha256 FROM attachments WHERE task_id=?`, id)
	var hashes []string
	if rows != nil {
		for rows.Next() {
			var hash string
			if rows.Scan(&hash) == nil {
				hashes = append(hashes, hash)
			}
		}
		rows.Close()
	}
	if tombstone {
		var ownerDeviceID string
		if err := tx.QueryRowContext(ctx, `SELECT workspace.owner_device_id FROM tasks task JOIN workspaces workspace ON workspace.id=task.workspace_id WHERE task.id=?`, id).Scan(&ownerDeviceID); err != nil {
			return err
		}
		if err := enqueueOwnedGraphDeleteTx(ctx, tx, "task", id, ownerDeviceID, time.Now().UTC()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id=?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, hash := range hashes {
		var references int
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE sha256=?`, hash).Scan(&references)
		if references == 0 && len(hash) == 64 {
			_ = os.Remove(filepath.Join(s.dataDir, "attachments", "blobs", hash[:2], hash))
		}
	}
	return nil
}

func (s *Store) DeleteRuntimeSnapshot(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM runtime_config_snapshots WHERE id=?`, id)
	return err
}

func (s *Store) UpdateTaskWorkspace(ctx context.Context, id, path, baseCommit, branch, updatedAt string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET task_workspace_path=?,base_commit=?,task_branch=?,updated_at=? WHERE id=?`,
		path, baseCommit, branch, updatedAt, id,
	)
	return err
}

func (s *Store) AddMessage(ctx context.Context, item domain.Message) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO messages(id,task_id,turn_id,role,sender,content,created_at) VALUES(?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.TurnID, item.Role, item.Sender, item.Content, timeString(item.CreatedAt))
	return err
}

func (s *Store) LinkMessageTurn(ctx context.Context, messageID, turnID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET turn_id=? WHERE id=?`, turnID, messageID)
	return err
}

func (s *Store) ListMessages(ctx context.Context, taskID string) ([]domain.Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,task_id,turn_id,role,sender,content,created_at FROM messages WHERE task_id=? ORDER BY created_at,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Message
	for rows.Next() {
		var item domain.Message
		var createdAt string
		if err := rows.Scan(&item.ID, &item.TaskID, &item.TurnID, &item.Role, &item.Sender, &item.Content, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(createdAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CreateTurn(ctx context.Context, item domain.Turn) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertTurnTx(ctx, tx, item); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ActiveTurn(ctx context.Context, taskID string) (domain.Turn, error) {
	return scanTurn(s.db.QueryRowContext(ctx, `
		SELECT `+turnColumns+` FROM turns
		WHERE task_id=? AND status IN ('queued','preparing','starting','running','waiting')
		ORDER BY sequence DESC LIMIT 1`,
		taskID,
	))
}

func scanTurn(scanner interface{ Scan(...any) error }) (domain.Turn, error) {
	var item domain.Turn
	var queuedAt, preparedAt, startedAt, finishedAt string
	var contextReadyAt, sessionReadyAt, firstEventAt, lastActivityAt, stalledAt, backendFinishedAt string
	var exitCode sql.NullInt64
	var required int
	var usage string
	err := scanner.Scan(
		&item.ID, &item.TaskID, &item.AgentID, &item.Sequence, &item.InputMessageID, &item.Status,
		&item.WaitingReason, &item.BackendSessionID, &item.RuntimeConfigSnapshotID,
		&queuedAt, &preparedAt, &startedAt, &finishedAt, &exitCode, &item.Result, &item.Error,
		&item.RoundID, &item.ParentTurnID, &item.Attempt, &item.Generation, &required, &item.Title, &item.Instruction,
		&item.ContextWindow, &item.PromptChars, &item.PromptSnapshot, &item.InboxBatchID, &usage,
		&contextReadyAt, &sessionReadyAt, &firstEventAt, &lastActivityAt, &stalledAt, &backendFinishedAt,
	)
	item.QueuedAt, item.PreparedAt = parseTime(queuedAt), parseTime(preparedAt)
	item.StartedAt, item.FinishedAt = parseTime(startedAt), parseTime(finishedAt)
	item.ContextReadyAt, item.SessionReadyAt = parseTime(contextReadyAt), parseTime(sessionReadyAt)
	item.FirstEventAt, item.LastActivityAt = parseTime(firstEventAt), parseTime(lastActivityAt)
	item.StalledAt, item.BackendFinishedAt = parseTime(stalledAt), parseTime(backendFinishedAt)
	item.QueuedAtMS = unixMilli(item.QueuedAt)
	item.PreparedAtMS = unixMilli(item.PreparedAt)
	item.StartedAtMS = unixMilli(item.StartedAt)
	item.FinishedAtMS = unixMilli(item.FinishedAt)
	item.ContextReadyAtMS = unixMilli(item.ContextReadyAt)
	item.SessionReadyAtMS = unixMilli(item.SessionReadyAt)
	item.FirstEventAtMS = unixMilli(item.FirstEventAt)
	item.LastActivityAtMS = unixMilli(item.LastActivityAt)
	item.StalledAtMS = unixMilli(item.StalledAt)
	item.BackendFinishedAtMS = unixMilli(item.BackendFinishedAt)
	item.Required = required != 0
	item.Usage = decodeJSON(usage, map[string]any{})
	if exitCode.Valid {
		value := int(exitCode.Int64)
		item.ExitCode = &value
	}
	return item, err
}

const turnColumns = `id,task_id,agent_id,sequence,input_message_id,status,waiting_reason,backend_session_id,runtime_config_snapshot_id,queued_at,prepared_at,started_at,finished_at,exit_code,result,error,round_id,parent_turn_id,attempt,generation,required,title,instruction,context_window,prompt_chars,prompt_snapshot,inbox_batch_id,usage_json,context_ready_at,session_ready_at,first_event_at,last_activity_at,stalled_at,backend_finished_at`

func (s *Store) Turn(ctx context.Context, id string) (domain.Turn, error) {
	return scanTurn(s.db.QueryRowContext(ctx, `SELECT `+turnColumns+` FROM turns WHERE id=?`, id))
}

func (s *Store) ListTurns(ctx context.Context, taskID string) ([]domain.Turn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+turnColumns+` FROM turns WHERE task_id=? ORDER BY sequence`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Turn
	for rows.Next() {
		item, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) PreviousTurnUsage(ctx context.Context, turn domain.Turn) (map[string]any, error) {
	previous, err := s.PreviousTurnForSession(ctx, turn)
	if err != nil {
		return nil, err
	}
	return previous.Usage, nil
}

func (s *Store) PreviousTurnForSession(ctx context.Context, turn domain.Turn) (domain.Turn, error) {
	if turn.BackendSessionID == "" {
		return domain.Turn{}, sql.ErrNoRows
	}
	return scanTurn(s.db.QueryRowContext(ctx, `
		SELECT `+turnColumns+` FROM turns
		WHERE task_id=? AND agent_id=? AND sequence<? AND backend_session_id=?
		  AND status IN ('succeeded','failed','interrupted','blocked')
		ORDER BY sequence DESC LIMIT 1`,
		turn.TaskID, turn.AgentID, turn.Sequence, turn.BackendSessionID,
	))
}

func (s *Store) UpdateTurn(ctx context.Context, item domain.Turn, from domain.TurnStatus) error {
	if err := domain.ValidateTurnTransition(from, item.Status); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE turns SET status=?,waiting_reason=?,backend_session_id=?,prepared_at=?,started_at=?,finished_at=?,exit_code=?,result=?,error=?,context_window=?,prompt_chars=?,prompt_snapshot=?,usage_json=?,context_ready_at=?,session_ready_at=?,first_event_at=?,last_activity_at=?,stalled_at=?,backend_finished_at=?
		WHERE id=? AND status=?`,
		item.Status, item.WaitingReason, item.BackendSessionID, timeString(item.PreparedAt), timeString(item.StartedAt),
		timeString(item.FinishedAt), item.ExitCode, item.Result, item.Error, item.ContextWindow, item.PromptChars,
		item.PromptSnapshot, encodeJSON(item.Usage), timeString(item.ContextReadyAt), timeString(item.SessionReadyAt),
		timeString(item.FirstEventAt), timeString(item.LastActivityAt), timeString(item.StalledAt), timeString(item.BackendFinishedAt), item.ID, from,
	)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("turn status changed concurrently")
	}
	return nil
}

func (s *Store) UpsertBackendSession(ctx context.Context, item domain.BackendSession) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO backend_sessions(id,task_id,agent_id,workspace_id,backend,model_id,env_group_revision,codex_account_id,provider_session_id,status,context_usage_json,created_at,last_used_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET provider_session_id=excluded.provider_session_id,status=excluded.status,
			context_usage_json=excluded.context_usage_json,last_used_at=excluded.last_used_at`,
		item.ID, item.TaskID, item.AgentID, item.WorkspaceID, item.Backend, item.ModelID, item.EnvGroupRevision,
		item.CodexAccountID, item.ProviderSession, item.Status, item.ContextUsageJSON, timeString(item.CreatedAt), timeString(item.LastUsedAt),
	)
	return err
}

const backendSessionColumns = `id,task_id,agent_id,workspace_id,backend,model_id,env_group_revision,codex_account_id,provider_session_id,status,context_usage_json,created_at,last_used_at`

func scanBackendSession(scanner interface{ Scan(...any) error }) (domain.BackendSession, error) {
	var item domain.BackendSession
	var createdAt, lastUsedAt string
	err := scanner.Scan(
		&item.ID, &item.TaskID, &item.AgentID, &item.WorkspaceID, &item.Backend, &item.ModelID,
		&item.EnvGroupRevision, &item.CodexAccountID, &item.ProviderSession, &item.Status, &item.ContextUsageJSON,
		&createdAt, &lastUsedAt,
	)
	item.CreatedAt, item.LastUsedAt = parseTime(createdAt), parseTime(lastUsedAt)
	return item, err
}

func (s *Store) ReusableBackendSession(ctx context.Context, taskID, agentID, workspaceID, backend, modelID string, envRevision int, codexAccountID string) (domain.BackendSession, error) {
	return scanBackendSession(s.db.QueryRowContext(ctx, `
		SELECT `+backendSessionColumns+`
		FROM backend_sessions
		WHERE task_id=? AND agent_id=? AND workspace_id=? AND backend=? AND model_id=? AND env_group_revision=? AND codex_account_id=?
		  AND status='active'
		ORDER BY last_used_at DESC LIMIT 1`,
		taskID, agentID, workspaceID, backend, modelID, envRevision, codexAccountID,
	))
}

func (s *Store) ListBackendSessionsForAgent(ctx context.Context, taskID, agentID string) ([]domain.BackendSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+backendSessionColumns+`
		FROM backend_sessions
		WHERE task_id=? AND agent_id=?
		ORDER BY created_at`,
		taskID, agentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.BackendSession
	for rows.Next() {
		item, err := scanBackendSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ListBackendSessionsForTask(ctx context.Context, taskID string) ([]domain.BackendSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+backendSessionColumns+`
		FROM backend_sessions
		WHERE task_id=?
		ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.BackendSession
	for rows.Next() {
		item, err := scanBackendSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) TaskMemory(ctx context.Context, taskID string) (domain.TaskMemory, error) {
	var item domain.TaskMemory
	var decisions, facts, excluded, progress, verification, nextActions, extra, updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT task_id,current_goal,decisions_json,facts_json,excluded_json,progress_json,verification_json,next_actions_json,extra_json,updated_at
		FROM task_memory WHERE task_id=?`, taskID).
		Scan(&item.TaskID, &item.CurrentGoal, &decisions, &facts, &excluded, &progress, &verification, &nextActions, &extra, &updatedAt)
	item.Decisions = decodeJSON(decisions, []string{})
	item.Facts = decodeJSON(facts, []string{})
	item.Excluded = decodeJSON(excluded, []string{})
	item.Progress = decodeJSON(progress, []string{})
	item.Verification = decodeJSON(verification, []string{})
	item.NextActions = decodeJSON(nextActions, []string{})
	item.Extra = decodeJSON(extra, map[string]any{})
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) UpsertTaskMemory(ctx context.Context, item domain.TaskMemory) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO task_memory(task_id,current_goal,decisions_json,facts_json,excluded_json,progress_json,verification_json,next_actions_json,extra_json,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(task_id) DO UPDATE SET current_goal=excluded.current_goal,decisions_json=excluded.decisions_json,
			facts_json=excluded.facts_json,excluded_json=excluded.excluded_json,progress_json=excluded.progress_json,
			verification_json=excluded.verification_json,next_actions_json=excluded.next_actions_json,
			extra_json=excluded.extra_json,updated_at=excluded.updated_at`,
		item.TaskID, item.CurrentGoal, encodeJSON(item.Decisions), encodeJSON(item.Facts), encodeJSON(item.Excluded),
		encodeJSON(item.Progress), encodeJSON(item.Verification), encodeJSON(item.NextActions), encodeJSON(item.Extra),
		timeString(item.UpdatedAt),
	)
	return err
}
