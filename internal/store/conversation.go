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
			round_id,parent_turn_id,attempt,generation,required,title,instruction,context_window,prompt_chars,prompt_snapshot,inbox_batch_id,usage_json,channel_reply_decision
		)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.AgentID, item.Sequence, item.InputMessageID, item.Status, item.WaitingReason,
		item.BackendSessionID, item.RuntimeConfigSnapshotID, timeString(item.QueuedAt), timeString(item.PreparedAt),
		timeString(item.StartedAt), timeString(item.FinishedAt), item.ExitCode, item.Result, item.Error,
		item.RoundID, item.ParentTurnID, item.Attempt, item.Generation, item.Required, item.Title, item.Instruction,
		item.ContextWindow, item.PromptChars, item.PromptSnapshot, item.InboxBatchID, encodeJSON(item.Usage), item.ChannelReplyDecision,
	)
	return err
}

func (s *Store) CreateAgentTurn(ctx context.Context, item domain.Turn) (domain.Turn, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Turn{}, err
	}
	defer tx.Rollback()
	// Recheck the Round inside the write transaction. A caller's optimistic
	// read can race InterruptRound; without this check the INSERT would attach a
	// retry Turn to a Round that is already terminal. Rows without a Round (old
	// fixtures and legacy Turns) keep working because the missing row is not a
	// terminal signal.
	if item.RoundID != "" {
		var status string
		err := tx.QueryRowContext(ctx, `SELECT status FROM task_rounds WHERE id=?`, item.RoundID).Scan(&status)
		switch {
		case err == nil && domain.RoundStatus(status).Terminal():
			return domain.Turn{}, ErrRoundTerminal
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return domain.Turn{}, err
		}
	}
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
	item = normalizeConversationItem(item)
	return insertConversationItem(ctx, s.db, item)
}

// UpsertBackendStreamItem keeps transient assistant output to one placeholder per Turn.
// Explicit progress and routed messages use different route kinds and are not affected.
func (s *Store) UpsertBackendStreamItem(ctx context.Context, item domain.ConversationItem) (domain.ConversationItem, error) {
	item = normalizeConversationItem(item)
	item.RouteKind = "backend_stream"
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ConversationItem{}, err
	}
	defer tx.Rollback()
	var existingID string
	var sequence int64
	err = tx.QueryRowContext(ctx, `
		SELECT id,sequence FROM conversation_items
		WHERE task_id=? AND turn_id=? AND kind='agent_message_update'
		  AND route_kind IN ('','backend_stream')
		ORDER BY sequence LIMIT 1`, item.TaskID, item.TurnID).Scan(&existingID, &sequence)
	if err == nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE conversation_items
			SET round_id=?,agent_id=?,stream_agent_id=?,from_agent_id=?,to_agent_id=?,route_kind=?,
			    category=?,kind=?,summary=?,payload_json=?
			WHERE id=?`,
			item.RoundID, item.AgentID, item.StreamAgentID, item.FromAgentID, item.ToAgentID, item.RouteKind,
			item.Category, item.Kind, item.Summary, encodeJSON(item.Payload), existingID,
		); err != nil {
			return domain.ConversationItem{}, err
		}
		item.ID, item.Sequence = existingID, sequence
	} else if errors.Is(err, sql.ErrNoRows) {
		item, err = insertConversationItem(ctx, tx, item)
		if err != nil {
			return domain.ConversationItem{}, err
		}
	} else {
		return domain.ConversationItem{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM conversation_items
		WHERE task_id=? AND turn_id=? AND kind='agent_message_update'
		  AND route_kind IN ('','backend_stream') AND id<>?`, item.TaskID, item.TurnID, item.ID); err != nil {
		return domain.ConversationItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ConversationItem{}, err
	}
	return item, nil
}

// FinalizeTurnReply atomically replaces backend stream placeholders with one durable reply.
// Repeating the call updates the same records, while identical text from different Turns remains distinct.
func (s *Store) FinalizeTurnReply(ctx context.Context, message domain.Message, item domain.ConversationItem) (domain.ConversationItem, error) {
	item = normalizeConversationItem(item)
	item.RouteKind = "turn_result"
	if message.TaskID == "" || message.TurnID == "" || message.Role != "assistant" || item.TaskID != message.TaskID || item.TurnID != message.TurnID {
		return domain.ConversationItem{}, fmt.Errorf("final Turn reply scope is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ConversationItem{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM conversation_items
		WHERE task_id=? AND turn_id=? AND kind='agent_message_update'
		  AND route_kind IN ('','backend_stream')`, item.TaskID, item.TurnID); err != nil {
		return domain.ConversationItem{}, err
	}
	var messageID string
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM messages WHERE task_id=? AND turn_id=? AND role='assistant'
		ORDER BY created_at,id LIMIT 1`, message.TaskID, message.TurnID).Scan(&messageID)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id,task_id,turn_id,role,sender,content,created_at) VALUES(?,?,?,?,?,?,?)`,
			message.ID, message.TaskID, message.TurnID, message.Role, message.Sender, message.Content, timeString(message.CreatedAt)); err != nil {
			return domain.ConversationItem{}, err
		}
		messageID = message.ID
	} else if err != nil {
		return domain.ConversationItem{}, err
	} else if _, err := tx.ExecContext(ctx, `UPDATE messages SET sender=?,content=?,created_at=? WHERE id=?`,
		message.Sender, message.Content, timeString(message.CreatedAt), messageID); err != nil {
		return domain.ConversationItem{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE task_id=? AND turn_id=? AND role='assistant' AND id<>?`, message.TaskID, message.TurnID, messageID); err != nil {
		return domain.ConversationItem{}, err
	}
	var existingID string
	var sequence int64
	err = tx.QueryRowContext(ctx, `
		SELECT id,sequence FROM conversation_items
		WHERE task_id=? AND turn_id=?
		  AND (route_kind='turn_result' OR (route_kind='' AND kind IN ('agent_message','agent_result')))
		ORDER BY sequence LIMIT 1`, item.TaskID, item.TurnID).Scan(&existingID, &sequence)
	if errors.Is(err, sql.ErrNoRows) {
		item, err = insertConversationItem(ctx, tx, item)
		if err != nil {
			return domain.ConversationItem{}, err
		}
	} else if err != nil {
		return domain.ConversationItem{}, err
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE conversation_items
			SET round_id=?,agent_id=?,stream_agent_id=?,from_agent_id=?,to_agent_id=?,route_kind=?,
			    category=?,kind=?,summary=?,payload_json=?,created_at=?
			WHERE id=?`,
			item.RoundID, item.AgentID, item.StreamAgentID, item.FromAgentID, item.ToAgentID, item.RouteKind,
			item.Category, item.Kind, item.Summary, encodeJSON(item.Payload), timeString(item.CreatedAt), existingID,
		); err != nil {
			return domain.ConversationItem{}, err
		}
		item.ID, item.Sequence = existingID, sequence
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM conversation_items
		WHERE task_id=? AND turn_id=? AND id<>?
		  AND (route_kind='turn_result' OR (route_kind='' AND kind IN ('agent_message','agent_result')))`, item.TaskID, item.TurnID, item.ID); err != nil {
		return domain.ConversationItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ConversationItem{}, err
	}
	return item, nil
}

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

func normalizeConversationItem(item domain.ConversationItem) domain.ConversationItem {
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
	return item
}

type conversationExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertConversationItem(ctx context.Context, database conversationExecer, item domain.ConversationItem) (domain.ConversationItem, error) {
	result, err := database.ExecContext(ctx, `
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

// MergeAgentBatchDispatch adds a later dispatch's agents to the routed-agent card
// already recorded for this parent Turn, and reports whether such a card existed.
//
// A Turn may submit more than one batch of sub agents. The card is identified by
// the parent Turn, so a second batch that inserted its own row would collide with
// the first on that id; because the insert error was discarded, the second batch
// vanished and the card named fewer agents than were actually running. Callers
// merge into the existing card instead, and only insert when there is none.
func (s *Store) MergeAgentBatchDispatch(
	ctx context.Context,
	parentTurnID string,
	collaborationBatchID string,
	agentIDs []string,
	routes []map[string]any,
) (bool, error) {
	if parentTurnID == "" || len(agentIDs) == 0 {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
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
		return false, nil
	}
	if err != nil {
		return false, err
	}
	payload := decodeJSON(payloadJSON, map[string]any{})
	seen := map[string]bool{}
	mergedIDs := make([]any, 0, len(agentIDs))
	if existing, ok := payload["agent_ids"].([]any); ok {
		for _, raw := range existing {
			value := strings.TrimSpace(fmt.Sprint(raw))
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			mergedIDs = append(mergedIDs, value)
		}
	}
	mergedRoutes, _ := payload["agent_routes"].([]any)
	added := false
	for index, agentID := range agentIDs {
		value := strings.TrimSpace(agentID)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		mergedIDs = append(mergedIDs, value)
		if index < len(routes) {
			mergedRoutes = append(mergedRoutes, routes[index])
		}
		added = true
	}
	if !added {
		// The card is already complete for this Turn; nothing to write.
		return true, nil
	}
	payload["agent_ids"] = mergedIDs
	payload["agent_routes"] = mergedRoutes
	var batchIDs []any
	if existing, ok := payload["batch_ids"].([]any); ok {
		batchIDs = append(batchIDs, existing...)
	}
	if collaborationBatchID != "" {
		found := false
		for _, raw := range batchIDs {
			if strings.TrimSpace(fmt.Sprint(raw)) == collaborationBatchID {
				found = true
				break
			}
		}
		if !found {
			batchIDs = append(batchIDs, collaborationBatchID)
		}
	}
	payload["batch_ids"] = batchIDs
	summary := fmt.Sprintf("AHA 已向 %d 个子 Agent 路由任务", len(mergedIDs))
	if _, err := tx.ExecContext(ctx,
		`UPDATE conversation_items SET payload_json=?, summary=? WHERE id=?`,
		encodeJSON(payload), summary, id,
	); err != nil {
		return false, err
	}
	return true, tx.Commit()
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

// conversationCategoryClause turns the conversation filter categories into one
// SQL predicate. Plain categories match the stored category column. "update"
// and "thinking" share the stored "update" category and are split by the
// reasoning kind: "update" alone excludes reasoning rows, "thinking" alone
// selects only them, and both together select every update row. The filter
// is applied in SQL, so LIMIT, has_more, next_before and the incremental
// latest_sequence all describe exactly the rows the caller asked for.
// An empty or fully unknown list returns no predicate (all categories).
func conversationCategoryClause(categories []string, categoryColumn, kindColumn string) (string, []any) {
	plain := make([]string, 0, len(categories))
	seen := map[string]bool{}
	update, thinking := false, false
	for _, category := range categories {
		category = strings.TrimSpace(category)
		if category == "" || seen[category] {
			continue
		}
		seen[category] = true
		switch category {
		case domain.ConversationCategoryUpdate:
			update = true
		case domain.ConversationCategoryThinking:
			thinking = true
		default:
			plain = append(plain, category)
		}
	}
	if update && thinking {
		plain = append(plain, domain.ConversationCategoryUpdate)
	}
	terms := make([]string, 0, 2)
	args := make([]any, 0, len(plain)+2)
	if len(plain) > 0 {
		placeholders := make([]string, 0, len(plain))
		for _, category := range plain {
			placeholders = append(placeholders, "?")
			args = append(args, category)
		}
		terms = append(terms, categoryColumn+" IN ("+strings.Join(placeholders, ",")+")")
	}
	if update != thinking {
		operator := "<>"
		if thinking {
			operator = "="
		}
		terms = append(terms, "("+categoryColumn+"=? AND "+kindColumn+operator+"?)")
		args = append(args, domain.ConversationCategoryUpdate, domain.ConversationKindReasoning)
	}
	if len(terms) == 0 {
		return "", nil
	}
	return "(" + strings.Join(terms, " OR ") + ")", args
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
	where := []string{"item.task_id=?", "item.stream_agent_id=?"}
	if before > 0 {
		where = append(where, "item.sequence<?")
		args = append(args, before)
	}
	if after > 0 {
		where = append(where, "item.sequence>?")
		args = append(args, after)
	}
	if clause, clauseArgs := conversationCategoryClause(categories, "item.category", "item.kind"); clause != "" {
		where = append(where, clause)
		args = append(args, clauseArgs...)
	}
	order := "DESC"
	if after > 0 {
		order = "ASC"
	}
	args = append(args, limit+1)
	// Channel route cards carry their delivery id in the payload. The live
	// outbox state is resolved only for the rows that actually carry one: the
	// card is a small minority of a conversation, and joining every page row
	// through json_extract made the common page pay for a feature it does not
	// use.
	rows, err := s.db.QueryContext(ctx, `
		SELECT item.sequence,item.id,item.task_id,item.round_id,item.turn_id,item.agent_id,item.stream_agent_id,
		       item.from_agent_id,item.to_agent_id,item.route_kind,item.category,item.kind,item.summary,
		       item.payload_json,item.created_at
		FROM conversation_items item
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY item.sequence `+order+` LIMIT ?`, args...)
	if err != nil {
		return domain.ConversationPage{}, err
	}
	defer rows.Close()
	items := make([]domain.ConversationItem, 0, limit+1)
	deliveryIDs := make([]string, 0, 4)
	seenDelivery := map[string]bool{}
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
		if deliveryID := stringValue(item.Payload["delivery_id"]); deliveryID != "" && !seenDelivery[deliveryID] {
			seenDelivery[deliveryID] = true
			deliveryIDs = append(deliveryIDs, deliveryID)
		}
		item.CreatedAt = parseTime(createdAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.ConversationPage{}, err
	}
	if err := rows.Close(); err != nil {
		return domain.ConversationPage{}, err
	}
	if len(deliveryIDs) > 0 {
		states, err := s.channelDeliveryDisplayStates(ctx, deliveryIDs)
		if err != nil {
			return domain.ConversationPage{}, err
		}
		for index := range items {
			deliveryID := stringValue(items[index].Payload["delivery_id"])
			delivery, ok := states[deliveryID]
			if !ok || delivery.State == "" {
				continue
			}
			state := delivery.State
			// "delivered" in the outbox means the provider accepted the message
			// and returned an ID; it is never a client delivery/read receipt.
			if state == "delivered" && delivery.Certainty == "provider_accepted" {
				state = "provider_accepted"
			}
			items[index].Payload["delivery_state"] = state
		}
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	// A replay leaves the card's original row in state='replayed' and appends a
	// new generation. Follow that chain so the card shows the newest attempt.
	for index := range items {
		if stringValue(items[index].Payload["delivery_state"]) != "replayed" {
			continue
		}
		deliveryID := stringValue(items[index].Payload["delivery_id"])
		if deliveryID == "" {
			continue
		}
		state, err := s.channelDeliveryDisplayState(ctx, deliveryID)
		if err != nil {
			return domain.ConversationPage{}, err
		}
		if state != "" {
			items[index].Payload["delivery_state"] = state
		}
	}
	if after == 0 {
		for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
			items[left], items[right] = items[right], items[left]
		}
	}
	page := domain.ConversationPage{Items: items, HasMore: hasMore}
	// Advance only through records represented by this response. A concurrent
	// insert must remain reachable by the caller's next incremental request.
	page.Latest = after
	if len(items) > 0 {
		page.NextBefore = items[0].Sequence
		for _, item := range items {
			if item.Sequence > page.Latest {
				page.Latest = item.Sequence
			}
		}
	}
	return page, nil
}

// channelDeliveryState is the live outbox state for one delivery id. Certainty
// distinguishes provider acceptance from a client receipt.
type channelDeliveryState struct {
	State     string
	Certainty string
}

// channelDeliveryDisplayStates loads the outbox rows for the delivery ids a
// page actually referenced. The previous LEFT JOIN ran json_extract over every
// page row; this reads only the rows that carry a delivery id.
func (s *Store) channelDeliveryDisplayStates(ctx context.Context, deliveryIDs []string) (map[string]channelDeliveryState, error) {
	placeholders := make([]string, 0, len(deliveryIDs))
	args := make([]any, 0, len(deliveryIDs))
	for _, id := range deliveryIDs {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,state,outcome_certainty FROM channel_delivery_outbox
		WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make(map[string]channelDeliveryState, len(deliveryIDs))
	for rows.Next() {
		var id string
		var item channelDeliveryState
		if err := rows.Scan(&id, &item.State, &item.Certainty); err != nil {
			return nil, err
		}
		states[id] = item
	}
	return states, rows.Err()
}

// channelDeliveryDisplayState resolves the newest generation of a delivery
// chain. ReplayChannelDelivery marks the original row "replayed" and inserts a
// new pending row linked by replay_of_id, so a card bound to the original id
// must follow the chain before reporting a final state.
func (s *Store) channelDeliveryDisplayState(ctx context.Context, deliveryID string) (string, error) {
	currentID := strings.TrimSpace(deliveryID)
	for depth := 0; currentID != "" && depth < 32; depth++ {
		var state, certainty string
		err := s.db.QueryRowContext(ctx, `SELECT state,outcome_certainty FROM channel_delivery_outbox WHERE id=?`, currentID).Scan(&state, &certainty)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if state != "replayed" {
			// "delivered" is only provider acceptance, never a client read receipt.
			if state == "delivered" && certainty == "provider_accepted" {
				return "provider_accepted", nil
			}
			return state, nil
		}
		var nextID string
		err = s.db.QueryRowContext(ctx, `
			SELECT id FROM channel_delivery_outbox
			WHERE replay_of_id=?
			ORDER BY replay_generation DESC LIMIT 1`, currentID).Scan(&nextID)
		if errors.Is(err, sql.ErrNoRows) {
			return "replayed", nil
		}
		if err != nil {
			return "", err
		}
		currentID = nextID
	}
	return "replayed", nil
}

func (s *Store) MaxEventSequence(ctx context.Context, taskID string) int64 {
	var value int64
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence),0) FROM events WHERE aggregate_type='task' AND aggregate_id=?`,
		taskID,
	).Scan(&value)
	return value
}
