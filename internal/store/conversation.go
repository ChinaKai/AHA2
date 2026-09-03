package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) CreateMessageRoundAndTurn(
	ctx context.Context,
	message domain.Message,
	round domain.TaskRound,
	turn domain.Turn,
) (domain.TaskRound, domain.Turn, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.TaskRound{}, domain.Turn{}, err
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM task_rounds
		WHERE task_id=? AND status IN ('running','waiting')`,
		round.TaskID,
	).Scan(&active); err != nil {
		return domain.TaskRound{}, domain.Turn{}, err
	}
	if active > 0 {
		return domain.TaskRound{}, domain.Turn{}, ErrActiveTurn
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM task_rounds WHERE task_id=?`, round.TaskID).
		Scan(&round.Sequence); err != nil {
		return domain.TaskRound{}, domain.Turn{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM turns WHERE task_id=?`, turn.TaskID).
		Scan(&turn.Sequence); err != nil {
		return domain.TaskRound{}, domain.Turn{}, err
	}
	turn.RoundID = round.ID
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id,task_id,turn_id,role,sender,content,created_at) VALUES(?,?,?,?,?,?,?)`,
		message.ID, message.TaskID, turn.ID, message.Role, message.Sender, message.Content, timeString(message.CreatedAt)); err != nil {
		return domain.TaskRound{}, domain.Turn{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_rounds(id,task_id,sequence,input_message_id,status,created_at,started_at,finished_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		round.ID, round.TaskID, round.Sequence, round.InputMessageID, round.Status,
		timeString(round.CreatedAt), timeString(round.StartedAt), timeString(round.FinishedAt)); err != nil {
		return domain.TaskRound{}, domain.Turn{}, err
	}
	if err := insertTurnTx(ctx, tx, turn); err != nil {
		return domain.TaskRound{}, domain.Turn{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
			category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		domain.NewID("conversation"), message.TaskID, round.ID, turn.ID, message.Sender,
		turn.AgentID, message.Sender, turn.AgentID, "owner_message",
		"chat", "user_message", message.Content, "{}", timeString(message.CreatedAt)); err != nil {
		return domain.TaskRound{}, domain.Turn{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.TaskRound{}, domain.Turn{}, err
	}
	return round, turn, nil
}

func insertTurnTx(ctx context.Context, tx *sql.Tx, item domain.Turn) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO turns(
			id,task_id,agent_id,sequence,input_message_id,status,waiting_reason,backend_session_id,
			runtime_config_snapshot_id,queued_at,prepared_at,started_at,finished_at,exit_code,result,error,
			round_id,parent_turn_id,attempt,generation,required,title,instruction,context_window,prompt_chars,prompt_snapshot,inbox_batch_id,usage_json
		)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.AgentID, item.Sequence, item.InputMessageID, item.Status, item.WaitingReason,
		item.BackendSessionID, item.RuntimeConfigSnapshotID, timeString(item.QueuedAt), timeString(item.PreparedAt),
		timeString(item.StartedAt), timeString(item.FinishedAt), item.ExitCode, item.Result, item.Error,
		item.RoundID, item.ParentTurnID, item.Attempt, item.Generation, item.Required, item.Title, item.Instruction,
		item.ContextWindow, item.PromptChars, item.PromptSnapshot, item.InboxBatchID, encodeJSON(item.Usage),
	)
	return err
}

func (s *Store) CreateAgentTurn(ctx context.Context, item domain.Turn) (domain.Turn, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Turn{}, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM turns WHERE task_id=?`, item.TaskID).
		Scan(&item.Sequence); err != nil {
		return domain.Turn{}, err
	}
	if err := insertTurnTx(ctx, tx, item); err != nil {
		return domain.Turn{}, err
	}
	return item, tx.Commit()
}

func scanRound(scanner interface{ Scan(...any) error }) (domain.TaskRound, error) {
	var item domain.TaskRound
	var createdAt, startedAt, finishedAt string
	err := scanner.Scan(
		&item.ID, &item.TaskID, &item.Sequence, &item.InputMessageID, &item.Status,
		&createdAt, &startedAt, &finishedAt,
	)
	item.CreatedAt = parseTime(createdAt)
	item.StartedAt = parseTime(startedAt)
	item.FinishedAt = parseTime(finishedAt)
	item.CreatedAtMS = unixMilli(item.CreatedAt)
	item.StartedAtMS = unixMilli(item.StartedAt)
	item.FinishedAtMS = unixMilli(item.FinishedAt)
	return item, err
}

const roundColumns = `id,task_id,sequence,input_message_id,status,created_at,started_at,finished_at`

func (s *Store) Round(ctx context.Context, id string) (domain.TaskRound, error) {
	return scanRound(s.db.QueryRowContext(ctx, `SELECT `+roundColumns+` FROM task_rounds WHERE id=?`, id))
}

func (s *Store) LatestRound(ctx context.Context, taskID string) (domain.TaskRound, error) {
	return scanRound(s.db.QueryRowContext(ctx, `
		SELECT `+roundColumns+` FROM task_rounds WHERE task_id=? ORDER BY sequence DESC LIMIT 1`,
		taskID,
	))
}

func (s *Store) ActiveRound(ctx context.Context, taskID string) (domain.TaskRound, error) {
	return scanRound(s.db.QueryRowContext(ctx, `
		SELECT `+roundColumns+` FROM task_rounds
		WHERE task_id=? AND status IN ('running','waiting') ORDER BY sequence DESC LIMIT 1`,
		taskID,
	))
}

func (s *Store) UpdateRound(ctx context.Context, item domain.TaskRound, from domain.RoundStatus) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE task_rounds SET status=?,started_at=?,finished_at=? WHERE id=? AND status=?`,
		item.Status, timeString(item.StartedAt), timeString(item.FinishedAt), item.ID, from,
	)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("round status changed concurrently")
	}
	return nil
}

func (s *Store) TurnsForRound(ctx context.Context, roundID string) ([]domain.Turn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+turnColumns+` FROM turns WHERE round_id=? ORDER BY sequence`, roundID)
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

func (s *Store) AddConversationItem(ctx context.Context, item domain.ConversationItem) (domain.ConversationItem, error) {
	if item.StreamAgentID == "" {
		item.StreamAgentID = item.AgentID
		if item.StreamAgentID == "" || item.StreamAgentID == "owner" {
			item.StreamAgentID = "main"
		}
	}
	if item.FromAgentID == "" {
		item.FromAgentID = item.AgentID
	}
	if item.ToAgentID == "" {
		item.ToAgentID = item.StreamAgentID
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
			category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.RoundID, item.TurnID, item.AgentID, item.StreamAgentID,
		item.FromAgentID, item.ToAgentID, item.RouteKind, item.Category, item.Kind,
		item.Summary, encodeJSON(item.Payload), timeString(item.CreatedAt),
	)
	if err != nil {
		return domain.ConversationItem{}, err
	}
	item.Sequence, err = result.LastInsertId()
	return item, err
}

func (s *Store) UpdateAgentBatchRouteStatus(
	ctx context.Context,
	parentTurnID string,
	agentID string,
	turn domain.Turn,
) error {
	if parentTurnID == "" || agentID == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id, payloadJSON string
	err = tx.QueryRowContext(ctx, `
		SELECT id,payload_json FROM conversation_items
		WHERE turn_id=? AND kind='agent_batch_dispatched'
		LIMIT 1`,
		parentTurnID,
	).Scan(&id, &payloadJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	payload := decodeJSON(payloadJSON, map[string]any{})
	rawRoutes, _ := payload["agent_routes"].([]any)
	changed := false
	for _, rawRoute := range rawRoutes {
		route, ok := rawRoute.(map[string]any)
		if !ok || fmt.Sprint(route["agent_id"]) != agentID {
			continue
		}
		route["status"] = string(turn.Status)
		route["turn_id"] = turn.ID
		route["attempt"] = turn.Attempt
		route["queued_at"] = timeString(turn.QueuedAt)
		route["started_at"] = timeString(turn.StartedAt)
		route["finished_at"] = timeString(turn.FinishedAt)
		changed = true
		break
	}
	if !changed {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversation_items SET payload_json=? WHERE id=?`, encodeJSON(payload), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ConversationPage(
	ctx context.Context,
	taskID string,
	before int64,
	after int64,
	limit int,
	categories []string,
) (domain.ConversationPage, error) {
	return s.ConversationPageForAgent(ctx, taskID, "main", before, after, limit, categories)
}

func (s *Store) ConversationPageForAgent(
	ctx context.Context,
	taskID string,
	agentID string,
	before int64,
	after int64,
	limit int,
	categories []string,
) (domain.ConversationPage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{taskID, agentID}
	where := []string{"task_id=?", "stream_agent_id=?"}
	if before > 0 {
		where = append(where, "sequence<?")
		args = append(args, before)
	}
	if after > 0 {
		where = append(where, "sequence>?")
		args = append(args, after)
	}
	if len(categories) > 0 {
		placeholders := make([]string, 0, len(categories))
		for _, category := range categories {
			placeholders = append(placeholders, "?")
			args = append(args, category)
		}
		where = append(where, "category IN ("+strings.Join(placeholders, ",")+")")
	}
	order := "DESC"
	if after > 0 {
		order = "ASC"
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence,id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,route_kind,
		       category,kind,summary,payload_json,created_at
		FROM conversation_items
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY sequence `+order+` LIMIT ?`, args...)
	if err != nil {
		return domain.ConversationPage{}, err
	}
	defer rows.Close()
	items := make([]domain.ConversationItem, 0, limit+1)
	for rows.Next() {
		var item domain.ConversationItem
		var payload, createdAt string
		if err := rows.Scan(
			&item.Sequence, &item.ID, &item.TaskID, &item.RoundID, &item.TurnID, &item.AgentID,
			&item.StreamAgentID, &item.FromAgentID, &item.ToAgentID, &item.RouteKind,
			&item.Category, &item.Kind, &item.Summary, &payload, &createdAt,
		); err != nil {
			return domain.ConversationPage{}, err
		}
		item.Payload = decodeJSON(payload, map[string]any{})
		item.CreatedAt = parseTime(createdAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.ConversationPage{}, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if after == 0 {
		for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
			items[left], items[right] = items[right], items[left]
		}
	}
	page := domain.ConversationPage{Items: items, HasMore: hasMore}
	if len(items) > 0 {
		page.NextBefore = items[0].Sequence
	}
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence),0) FROM conversation_items
		WHERE task_id=? AND stream_agent_id=?`,
		taskID, agentID,
	).
		Scan(&page.Latest)
	return page, nil
}

func (s *Store) MaxEventSequence(ctx context.Context, taskID string) int64 {
	var value int64
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence),0) FROM events WHERE aggregate_type='task' AND aggregate_id=?`,
		taskID,
	).Scan(&value)
	return value
}
