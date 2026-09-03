package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const taskAgentColumns = `
	a.task_id,a.agent_id,a.role,a.status,a.title,a.runtime_config_snapshot_id,a.inherit_main,
	a.created_at,a.updated_at,
	(SELECT COUNT(*) FROM conversation_items c
	 WHERE c.task_id=a.task_id AND c.stream_agent_id=a.agent_id AND c.sequence>a.last_read_sequence)`

func scanTaskAgent(scanner interface{ Scan(...any) error }) (domain.TaskAgent, error) {
	var item domain.TaskAgent
	var inheritMain int
	var createdAt, updatedAt string
	err := scanner.Scan(
		&item.TaskID, &item.AgentID, &item.Role, &item.Status, &item.Title,
		&item.RuntimeConfigSnapshotID, &inheritMain, &createdAt, &updatedAt, &item.UnreadCount,
	)
	item.InheritMain = inheritMain != 0
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) TaskAgent(ctx context.Context, taskID, agentID string) (domain.TaskAgent, error) {
	return scanTaskAgent(s.db.QueryRowContext(ctx, `
		SELECT `+taskAgentColumns+` FROM task_agents a
		WHERE a.task_id=? AND a.agent_id=?`,
		taskID, agentID,
	))
}

func (s *Store) ListTaskAgents(ctx context.Context, taskID string) ([]domain.TaskAgent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+taskAgentColumns+` FROM task_agents a
		WHERE a.task_id=?
		ORDER BY CASE WHEN a.agent_id='main' THEN 0 ELSE 1 END,a.created_at,a.agent_id`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.TaskAgent
	for rows.Next() {
		item, err := scanTaskAgent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) TaskAgentCount(ctx context.Context, taskID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_agents WHERE task_id=?`, taskID).Scan(&count)
	return count, err
}

func (s *Store) UpsertTaskAgent(ctx context.Context, item domain.TaskAgent) (bool, error) {
	now := item.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO task_agents(
			task_id,agent_id,role,status,title,runtime_config_snapshot_id,inherit_main,last_read_sequence,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(task_id,agent_id) DO UPDATE SET
			role=excluded.role,title=excluded.title,runtime_config_snapshot_id=excluded.runtime_config_snapshot_id,
			inherit_main=excluded.inherit_main,updated_at=excluded.updated_at`,
		item.TaskID, item.AgentID, item.Role, item.Status, item.Title, item.RuntimeConfigSnapshotID,
		boolInt(item.InheritMain), 0, timeString(item.CreatedAt), timeString(now),
	)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count > 0, nil
}

func (s *Store) UpdateTaskAgentStatus(ctx context.Context, taskID, agentID, status string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE task_agents SET status=?,updated_at=? WHERE task_id=? AND agent_id=?`,
		status, timeString(updatedAt), taskID, agentID,
	)
	return err
}

func (s *Store) UpdateTaskAgentSnapshot(
	ctx context.Context,
	taskID string,
	agentID string,
	snapshotID string,
	inheritMain bool,
	updatedAt time.Time,
) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE task_agents
		SET runtime_config_snapshot_id=?,inherit_main=?,updated_at=?
		WHERE task_id=? AND agent_id=?`,
		snapshotID, boolInt(inheritMain), timeString(updatedAt), taskID, agentID,
	)
	return err
}

func (s *Store) UpdateInheritedTaskAgentSnapshots(ctx context.Context, taskID, snapshotID string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE task_agents
		SET runtime_config_snapshot_id=?,updated_at=?
		WHERE task_id=? AND agent_id<>'main' AND inherit_main=1`,
		snapshotID, timeString(updatedAt), taskID,
	)
	return err
}

func (s *Store) MarkAgentConversationRead(ctx context.Context, taskID, agentID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE task_agents
		SET last_read_sequence=COALESCE((
			SELECT MAX(sequence) FROM conversation_items
			WHERE task_id=? AND stream_agent_id=?
		),last_read_sequence)
		WHERE task_id=? AND agent_id=?`,
		taskID, agentID, taskID, agentID,
	)
	return err
}

func (s *Store) CloseBackendSessionsForAgent(ctx context.Context, taskID, agentID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE backend_sessions SET status='superseded'
		WHERE task_id=? AND agent_id=? AND status='active'`,
		taskID, agentID,
	)
	return err
}

func (s *Store) RotateAgentBackendSession(
	ctx context.Context,
	taskID string,
	agentID string,
	status string,
	handoff *domain.AgentSessionHandoff,
) (domain.BackendSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.BackendSession{}, err
	}
	defer tx.Rollback()
	session, err := scanBackendSession(tx.QueryRowContext(ctx, `
		SELECT `+backendSessionColumns+` FROM backend_sessions
		WHERE task_id=? AND agent_id=? AND status='active'
		ORDER BY last_used_at DESC LIMIT 1`,
		taskID, agentID,
	))
	if err != nil {
		return domain.BackendSession{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE backend_sessions SET status=? WHERE id=? AND status='active'`,
		status, session.ID,
	); err != nil {
		return domain.BackendSession{}, err
	}
	if handoff != nil {
		handoff.SourceBackendSessionID = session.ID
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_session_handoffs(
				id,task_id,agent_id,source_backend_session_id,mode,summary,status,created_at,consumed_at
			) VALUES(?,?,?,?,?,?,?,?,?)`,
			handoff.ID, handoff.TaskID, handoff.AgentID, handoff.SourceBackendSessionID,
			handoff.Mode, handoff.Summary, handoff.Status, timeString(handoff.CreatedAt), "",
		); err != nil {
			return domain.BackendSession{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.BackendSession{}, err
	}
	session.Status = status
	return session, nil
}

func scanAgentSessionHandoff(scanner interface{ Scan(...any) error }) (domain.AgentSessionHandoff, error) {
	var item domain.AgentSessionHandoff
	var createdAt, consumedAt string
	err := scanner.Scan(
		&item.ID, &item.TaskID, &item.AgentID, &item.SourceBackendSessionID,
		&item.Mode, &item.Summary, &item.Status, &createdAt, &consumedAt,
	)
	item.CreatedAt = parseTime(createdAt)
	item.ConsumedAt = parseTime(consumedAt)
	return item, err
}

func (s *Store) PendingAgentSessionHandoff(ctx context.Context, taskID, agentID string) (domain.AgentSessionHandoff, error) {
	return scanAgentSessionHandoff(s.db.QueryRowContext(ctx, `
		SELECT id,task_id,agent_id,source_backend_session_id,mode,summary,status,created_at,consumed_at
		FROM agent_session_handoffs
		WHERE task_id=? AND agent_id=? AND status='pending'
		ORDER BY created_at DESC LIMIT 1`,
		taskID, agentID,
	))
}

func (s *Store) ConsumeAgentSessionHandoff(ctx context.Context, id string, consumedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE agent_session_handoffs SET status='consumed',consumed_at=?
		WHERE id=? AND status='pending'`,
		timeString(consumedAt), id,
	)
	return err
}

func (s *Store) EnqueueOwnerMessage(
	ctx context.Context,
	message domain.Message,
	targetAgentID string,
) (domain.TaskRound, domain.AgentInboxItem, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM task_agents WHERE task_id=? AND agent_id=?)`,
		message.TaskID, targetAgentID,
	).Scan(&exists); err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, err
	}
	if !exists {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, sql.ErrNoRows
	}
	round, err := scanRound(tx.QueryRowContext(ctx, `
		SELECT `+roundColumns+` FROM task_rounds
		WHERE task_id=? AND status IN ('running','waiting')
		ORDER BY sequence DESC LIMIT 1`,
		message.TaskID,
	))
	createdRound := false
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages(id,task_id,turn_id,role,sender,content,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		message.ID, message.TaskID, "", message.Role, message.Sender, message.Content, timeString(message.CreatedAt),
	); err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, err
	}
	if errors.Is(err, sql.ErrNoRows) || round.ID == "" {
		round = domain.TaskRound{
			ID: domain.NewID("round"), TaskID: message.TaskID, InputMessageID: message.ID,
			Status: domain.RoundRunning, CreatedAt: message.CreatedAt, StartedAt: message.CreatedAt,
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(sequence),0)+1 FROM task_rounds WHERE task_id=?`,
			message.TaskID,
		).Scan(&round.Sequence); err != nil {
			return domain.TaskRound{}, domain.AgentInboxItem{}, false, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO task_rounds(id,task_id,sequence,input_message_id,status,created_at,started_at,finished_at)
			VALUES(?,?,?,?,?,?,?,?)`,
			round.ID, round.TaskID, round.Sequence, round.InputMessageID, round.Status,
			timeString(round.CreatedAt), timeString(round.StartedAt), "",
		); err != nil {
			return domain.TaskRound{}, domain.AgentInboxItem{}, false, err
		}
		createdRound = true
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
			category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		domain.NewID("conversation"), message.TaskID, round.ID, "", "owner", targetAgentID,
		"owner", targetAgentID, "owner_message", "chat", "user_message", message.Content, "{}",
		timeString(message.CreatedAt),
	); err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, err
	}
	inbox := domain.AgentInboxItem{
		ID: domain.NewID("inbox"), TaskID: message.TaskID, RoundID: round.ID,
		TargetAgentID: targetAgentID, SourceAgentID: "owner", SourceKind: "owner_message",
		MessageID: message.ID, Content: message.Content, Status: "pending", CreatedAt: message.CreatedAt,
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO agent_inbox(
			id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
			content,payload_json,status,batch_id,created_at,claimed_at,processed_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		inbox.ID, inbox.TaskID, inbox.RoundID, inbox.TargetAgentID, inbox.SourceAgentID, inbox.SourceKind,
		"", inbox.MessageID, inbox.Content, "{}", inbox.Status, "", timeString(inbox.CreatedAt), "", "",
	)
	if err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, err
	}
	inbox.Sequence, err = result.LastInsertId()
	if err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.TaskRound{}, domain.AgentInboxItem{}, false, err
	}
	return round, inbox, createdRound, nil
}

func (s *Store) EnqueueAgentRoute(ctx context.Context, item domain.AgentInboxItem) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO agent_inbox(
			id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
			content,payload_json,status,batch_id,created_at,claimed_at,processed_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.RoundID, item.TargetAgentID, item.SourceAgentID, item.SourceKind,
		item.SourceTurnID, item.MessageID, item.Content, encodeJSON(item.Payload), "pending", "",
		timeString(item.CreatedAt), "", "",
	)
	if err != nil {
		return false, err
	}
	inserted, _ := result.RowsAffected()
	if inserted == 0 {
		return false, tx.Commit()
	}
	category := "update"
	kind := "agent_result_routed"
	if item.SourceKind == "assignment" {
		kind = "agent_assignment"
	}
	if item.SourceKind == "main_followup" {
		kind = "agent_main_followup"
	}
	if item.SourceKind == "agent_error" {
		category = "error"
		kind = "agent_error_routed"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
			category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		domain.NewID("conversation"), item.TaskID, item.RoundID, item.SourceTurnID, item.SourceAgentID,
		item.TargetAgentID, item.SourceAgentID, item.TargetAgentID, item.SourceKind,
		category, kind, item.Content, encodeJSON(item.Payload), timeString(item.CreatedAt),
	); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func scanInbox(scanner interface{ Scan(...any) error }) (domain.AgentInboxItem, error) {
	var item domain.AgentInboxItem
	var payload, createdAt, claimedAt, processedAt string
	err := scanner.Scan(
		&item.Sequence, &item.ID, &item.TaskID, &item.RoundID, &item.TargetAgentID,
		&item.SourceAgentID, &item.SourceKind, &item.SourceTurnID, &item.MessageID,
		&item.Content, &payload, &item.Status, &item.BatchID, &createdAt, &claimedAt, &processedAt,
	)
	item.Payload = decodeJSON(payload, map[string]any{})
	item.CreatedAt = parseTime(createdAt)
	item.ClaimedAt = parseTime(claimedAt)
	item.ProcessedAt = parseTime(processedAt)
	return item, err
}

const inboxColumns = `
	sequence,id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
	content,payload_json,status,batch_id,created_at,claimed_at,processed_at`

func (s *Store) PendingAgentInbox(ctx context.Context, taskID, agentID string) ([]domain.AgentInboxItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+inboxColumns+` FROM agent_inbox
		WHERE task_id=? AND target_agent_id=? AND status='pending'
		ORDER BY sequence`,
		taskID, agentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.AgentInboxItem
	for rows.Next() {
		item, err := scanInbox(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) PendingInboxAgents(ctx context.Context, taskID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT target_agent_id FROM agent_inbox
		WHERE task_id=? AND status='pending'
		ORDER BY target_agent_id`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			return nil, err
		}
		result = append(result, agentID)
	}
	return result, rows.Err()
}

type PendingAgent struct {
	TaskID  string
	AgentID string
}

func (s *Store) AllPendingAgents(ctx context.Context) ([]PendingAgent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT task_id,target_agent_id FROM agent_inbox
		WHERE status='pending'
		ORDER BY task_id,target_agent_id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []PendingAgent
	for rows.Next() {
		var item PendingAgent
		if err := rows.Scan(&item.TaskID, &item.AgentID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) PendingInboxCount(ctx context.Context, taskID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM agent_inbox WHERE task_id=? AND status='pending'`,
		taskID,
	).Scan(&count)
	return count, err
}

func (s *Store) SubAgentFanInState(ctx context.Context, roundID string) (active int, terminalUnrouted int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN turn.status IN ('queued','preparing','starting','running','waiting') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE
				WHEN turn.status IN ('succeeded','failed','interrupted','blocked')
				 AND NOT EXISTS (
					SELECT 1 FROM agent_inbox inbox
					WHERE inbox.source_turn_id=turn.id
					  AND inbox.target_agent_id='main'
					  AND inbox.source_kind IN ('agent_result','agent_error')
				 )
				THEN 1 ELSE 0 END),0)
		FROM turns turn
		WHERE turn.round_id=? AND turn.agent_id<>'main'
		  AND turn.sequence=(
			SELECT MAX(latest.sequence) FROM turns latest
			WHERE latest.round_id=turn.round_id AND latest.agent_id=turn.agent_id
		  )`,
		roundID,
	).Scan(&active, &terminalUnrouted)
	return active, terminalUnrouted, err
}

func (s *Store) ActiveTurnForAgent(ctx context.Context, taskID, agentID string) (domain.Turn, error) {
	return scanTurn(s.db.QueryRowContext(ctx, `
		SELECT `+turnColumns+` FROM turns
		WHERE task_id=? AND agent_id=? AND status IN ('queued','preparing','starting','running','waiting')
		ORDER BY sequence DESC LIMIT 1`,
		taskID, agentID,
	))
}

func (s *Store) LatestTurnForAgent(ctx context.Context, taskID, agentID string) (domain.Turn, error) {
	return scanTurn(s.db.QueryRowContext(ctx, `
		SELECT `+turnColumns+` FROM turns
		WHERE task_id=? AND agent_id=?
		ORDER BY sequence DESC LIMIT 1`,
		taskID, agentID,
	))
}

func (s *Store) ClaimInboxAndCreateTurn(
	ctx context.Context,
	turn domain.Turn,
	items []domain.AgentInboxItem,
) (domain.Turn, error) {
	if len(items) == 0 || turn.InboxBatchID == "" {
		return domain.Turn{}, fmt.Errorf("inbox batch is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Turn{}, err
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM turns
		WHERE task_id=? AND agent_id=? AND status IN ('queued','preparing','starting','running','waiting')`,
		turn.TaskID, turn.AgentID,
	).Scan(&active); err != nil {
		return domain.Turn{}, err
	}
	if active > 0 {
		return domain.Turn{}, ErrActiveTurn
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence),0)+1 FROM turns WHERE task_id=?`,
		turn.TaskID,
	).Scan(&turn.Sequence); err != nil {
		return domain.Turn{}, err
	}
	if err := insertTurnTx(ctx, tx, turn); err != nil {
		return domain.Turn{}, err
	}
	claimedAt := timeString(turn.QueuedAt)
	for _, item := range items {
		result, err := tx.ExecContext(ctx, `
			UPDATE agent_inbox
			SET status='claimed',batch_id=?,claimed_at=?
			WHERE id=? AND task_id=? AND target_agent_id=? AND status='pending'`,
			turn.InboxBatchID, claimedAt, item.ID, turn.TaskID, turn.AgentID,
		)
		if err != nil {
			return domain.Turn{}, err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return domain.Turn{}, fmt.Errorf("inbox item changed concurrently")
		}
		if item.MessageID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE messages SET turn_id=? WHERE id=?`, turn.ID, item.MessageID); err != nil {
				return domain.Turn{}, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE task_agents SET status='queued',updated_at=?
		WHERE task_id=? AND agent_id=?`,
		claimedAt, turn.TaskID, turn.AgentID,
	); err != nil {
		return domain.Turn{}, err
	}
	return turn, tx.Commit()
}

func (s *Store) MarkInboxBatchProcessed(ctx context.Context, batchID string, processedAt time.Time) error {
	if batchID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE agent_inbox SET status='processed',processed_at=?
		WHERE batch_id=? AND status='claimed'`,
		timeString(processedAt), batchID,
	)
	return err
}

func (s *Store) CancelPendingInboxForRound(ctx context.Context, roundID string, cancelledAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE agent_inbox
		SET status='cancelled',processed_at=?
		WHERE round_id=? AND status IN ('pending','claimed')`,
		timeString(cancelledAt), roundID,
	)
	return err
}
