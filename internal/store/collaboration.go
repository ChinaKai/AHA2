package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) CreateCollaborationBatch(ctx context.Context, batch domain.CollaborationBatch, members []domain.CollaborationMember) error {
	if strings.TrimSpace(batch.ID) == "" || strings.TrimSpace(batch.TaskID) == "" ||
		strings.TrimSpace(batch.RoundID) == "" || strings.TrimSpace(batch.ParentTurnID) == "" {
		return fmt.Errorf("collaboration batch identity is required")
	}
	if len(members) == 0 {
		return fmt.Errorf("collaboration batch requires members")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO collaboration_batches(
			id,task_id,round_id,parent_turn_id,integration_instructions,
			status,delivered_cursor,created_at,updated_at,consumed_at
		) VALUES(?,?,?,?,?,?,?,?,?,'')`,
		batch.ID, batch.TaskID, batch.RoundID, batch.ParentTurnID, batch.IntegrationInstructions,
		domain.CollaborationBatchPending, batch.DeliveredCursor,
		timeString(batch.CreatedAt), timeString(batch.UpdatedAt),
	); err != nil {
		return err
	}
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO collaboration_members(
				batch_id,agent_id,required,child_turn_id,status,created_at,updated_at
			) VALUES(?,?,?,?,?,?,?)`,
			batch.ID, member.AgentID, boolInt(member.Required), member.ChildTurnID,
			domain.CollaborationMemberQueued, timeString(member.CreatedAt), timeString(member.UpdatedAt),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CollaborationBatch(ctx context.Context, id string) (domain.CollaborationBatch, error) {
	var item domain.CollaborationBatch
	var createdAt, updatedAt, consumedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id,task_id,round_id,parent_turn_id,integration_instructions,status,
		       delivered_cursor,created_at,updated_at,consumed_at
		FROM collaboration_batches WHERE id=?`, id).Scan(
		&item.ID, &item.TaskID, &item.RoundID, &item.ParentTurnID,
		&item.IntegrationInstructions, &item.Status, &item.DeliveredCursor,
		&createdAt, &updatedAt, &consumedAt,
	)
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	item.ConsumedAt = parseTime(consumedAt)
	return item, err
}

func (s *Store) CollaborationMembers(ctx context.Context, batchID string) ([]domain.CollaborationMember, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT batch_id,agent_id,required,child_turn_id,status,created_at,updated_at
		FROM collaboration_members WHERE batch_id=? ORDER BY agent_id`,
		batchID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.CollaborationMember
	for rows.Next() {
		var item domain.CollaborationMember
		var required int
		var createdAt, updatedAt string
		if err := rows.Scan(
			&item.BatchID, &item.AgentID, &required, &item.ChildTurnID,
			&item.Status, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		item.Required = required != 0
		item.CreatedAt = parseTime(createdAt)
		item.UpdatedAt = parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) LinkCollaborationMemberTurn(ctx context.Context, batchID, agentID, childTurnID string, updatedAt time.Time) error {
	if batchID == "" || agentID == "" || childTurnID == "" {
		return nil
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE collaboration_members
		SET child_turn_id=?,status=?,updated_at=?
		WHERE batch_id=? AND agent_id=? AND child_turn_id=''`,
		childTurnID, domain.CollaborationMemberRunning, timeString(updatedAt),
		batchID, agentID,
	)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return fmt.Errorf("collaboration member %s/%s is missing or already linked", batchID, agentID)
	}
	return nil
}

func (s *Store) RecordCollaborationResult(ctx context.Context, turn domain.Turn, status, content string) (string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	var batchID, agentID string
	if err := tx.QueryRowContext(ctx, `
		SELECT batch_id,agent_id FROM collaboration_members WHERE child_turn_id=?`,
		turn.ID,
	).Scan(&batchID, &agentID); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO collaboration_events(
			id,batch_id,child_turn_id,agent_id,status,content,created_at
		) VALUES(?,?,?,?,?,?,?)`,
		"collaboration_event_"+turn.ID, batchID, turn.ID, agentID, status, content, timeString(now),
	)
	if err != nil {
		return "", false, err
	}
	inserted, _ := result.RowsAffected()
	if inserted == 1 && turn.TaskID != "" && turn.RoundID != "" {
		category, kind := "update", "collaboration_result_routed"
		if status != string(domain.TurnSucceeded) {
			category, kind = "error", "collaboration_error_routed"
		}
		if _, err := insertConversationItem(ctx, tx, domain.ConversationItem{
			ID:     domain.CollaborationResultConversationID(turn.ID),
			TaskID: turn.TaskID, RoundID: turn.RoundID, TurnID: turn.ID,
			AgentID: agentID, StreamAgentID: "main", FromAgentID: agentID, ToAgentID: "main",
			RouteKind: "collaboration_result", Category: category, Kind: kind, Summary: content,
			Payload: map[string]any{
				"batch_id": batchID, "parent_turn_id": turn.ParentTurnID,
				"child_turn_id": turn.ID, "agent_id": agentID, "status": status,
				"attempt": turn.Attempt, "result": turn.Result, "error": turn.Error,
			},
			CreatedAt: now,
		}); err != nil {
			return "", false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE collaboration_members SET status=?,updated_at=?
		WHERE batch_id=? AND agent_id=?`,
		status, timeString(now), batchID, agentID,
	); err != nil {
		return "", false, err
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM collaboration_members member
		WHERE member.batch_id=? AND (
			member.status IN ('queued','running')
			OR NOT EXISTS (
				SELECT 1 FROM collaboration_events event
				WHERE event.batch_id=member.batch_id AND event.child_turn_id=member.child_turn_id
			)
		)`,
		batchID,
	).Scan(&pending); err != nil {
		return "", false, err
	}
	batchStatus := domain.CollaborationBatchPending
	if pending == 0 {
		batchStatus = domain.CollaborationBatchComplete
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE collaboration_batches SET status=?,updated_at=? WHERE id=?`,
		batchStatus, timeString(now), batchID,
	); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return batchID, inserted == 1, nil
}

func (s *Store) CollaborationProgress(ctx context.Context, batchID string, after int64) (domain.CollaborationProgress, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.CollaborationProgress{}, err
	}
	defer tx.Rollback()
	var batch domain.CollaborationBatch
	var createdAt, updatedAt, consumedAt string
	if err := tx.QueryRowContext(ctx, `
		SELECT id,task_id,round_id,parent_turn_id,integration_instructions,status,
		       delivered_cursor,created_at,updated_at,consumed_at
		FROM collaboration_batches WHERE id=?`, batchID).Scan(
		&batch.ID, &batch.TaskID, &batch.RoundID, &batch.ParentTurnID,
		&batch.IntegrationInstructions, &batch.Status, &batch.DeliveredCursor,
		&createdAt, &updatedAt, &consumedAt,
	); err != nil {
		return domain.CollaborationProgress{}, err
	}
	batch.CreatedAt = parseTime(createdAt)
	batch.UpdatedAt = parseTime(updatedAt)
	batch.ConsumedAt = parseTime(consumedAt)
	rows, err := tx.QueryContext(ctx, `
		SELECT sequence,id,batch_id,child_turn_id,agent_id,status,content,created_at
		FROM collaboration_events WHERE batch_id=? AND sequence>? ORDER BY sequence`,
		batchID, after,
	)
	if err != nil {
		return domain.CollaborationProgress{}, err
	}
	progress := domain.CollaborationProgress{
		BatchID: batch.ID, RoundID: batch.RoundID, ParentTurnID: batch.ParentTurnID,
		Instructions: batch.IntegrationInstructions, Status: batch.Status,
		DeliveredCursor: batch.DeliveredCursor, NextCursor: after,
		Results: []domain.CollaborationEvent{}, PendingAgentIDs: []string{},
	}
	for rows.Next() {
		var event domain.CollaborationEvent
		var createdAt string
		if err := rows.Scan(
			&event.Sequence, &event.ID, &event.BatchID, &event.ChildTurnID,
			&event.AgentID, &event.Status, &event.Content, &createdAt,
		); err != nil {
			return domain.CollaborationProgress{}, err
		}
		event.CreatedAt = parseTime(createdAt)
		progress.Results = append(progress.Results, event)
		if event.Sequence > progress.NextCursor {
			progress.NextCursor = event.Sequence
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.CollaborationProgress{}, err
	}
	if err := rows.Close(); err != nil {
		return domain.CollaborationProgress{}, err
	}
	pendingRows, err := tx.QueryContext(ctx, `
		SELECT member.agent_id FROM collaboration_members member
		WHERE member.batch_id=? AND (
			member.status IN ('queued','running')
			OR NOT EXISTS (
				SELECT 1 FROM collaboration_events event
				WHERE event.batch_id=member.batch_id AND event.child_turn_id=member.child_turn_id
			)
		) ORDER BY member.agent_id`,
		batchID,
	)
	if err != nil {
		return domain.CollaborationProgress{}, err
	}
	defer pendingRows.Close()
	for pendingRows.Next() {
		var agentID string
		if err := pendingRows.Scan(&agentID); err != nil {
			return domain.CollaborationProgress{}, err
		}
		progress.PendingAgentIDs = append(progress.PendingAgentIDs, agentID)
	}
	if err := pendingRows.Err(); err != nil {
		return domain.CollaborationProgress{}, err
	}
	if err := pendingRows.Close(); err != nil {
		return domain.CollaborationProgress{}, err
	}
	progress.Complete = len(progress.PendingAgentIDs) == 0
	if progress.Complete {
		progress.Status = domain.CollaborationBatchComplete
	} else {
		progress.Status = domain.CollaborationBatchPending
	}
	if err := tx.Commit(); err != nil {
		return domain.CollaborationProgress{}, err
	}
	return progress, nil
}

func (s *Store) MarkCollaborationDelivered(ctx context.Context, batchID string, cursor int64, status string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE collaboration_batches
		SET delivered_cursor=CASE WHEN delivered_cursor<? THEN ? ELSE delivered_cursor END,
		    status=?,updated_at=?
		WHERE id=?`,
		cursor, cursor, status, timeString(updatedAt), batchID,
	)
	return err
}

func (s *Store) CollaborationBatchesForParentTurn(ctx context.Context, parentTurnID string) ([]domain.CollaborationBatch, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM collaboration_batches
		WHERE parent_turn_id=? AND status<>? ORDER BY created_at,id`,
		parentTurnID, domain.CollaborationBatchConsumed,
	)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var result []domain.CollaborationBatch
	for _, id := range ids {
		item, err := s.CollaborationBatch(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) OpenCollaborationBatchIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM collaboration_batches
		WHERE status<>? ORDER BY created_at,id`,
		domain.CollaborationBatchConsumed,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) OpenCollaborationBatchCountForRound(ctx context.Context, roundID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM collaboration_batches
		WHERE round_id=? AND status<>?`,
		roundID, domain.CollaborationBatchConsumed,
	).Scan(&count)
	return count, err
}

func (s *Store) MarkCollaborationConsumed(ctx context.Context, batchID string, consumedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE collaboration_batches
		SET status=?,consumed_at=?,updated_at=? WHERE id=?`,
		domain.CollaborationBatchConsumed, timeString(consumedAt), timeString(consumedAt), batchID,
	)
	return err
}

func (s *Store) MarkCollaborationFallback(ctx context.Context, batchID string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE collaboration_batches SET status=?,updated_at=? WHERE id=?`,
		domain.CollaborationBatchFallback, timeString(updatedAt), batchID,
	)
	return err
}

func (s *Store) CollaborationBatchIDsForInbox(ctx context.Context, inboxBatchID string) ([]string, error) {
	if inboxBatchID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT payload_json FROM agent_inbox
		WHERE batch_id=? AND source_kind='collaboration_fallback'`,
		inboxBatchID,
	)
	if err != nil {
		return nil, err
	}
	var batchIDs []string
	seen := map[string]bool{}
	for rows.Next() {
		var payloadJSON string
		if err := rows.Scan(&payloadJSON); err != nil {
			rows.Close()
			return nil, err
		}
		payload := decodeJSON(payloadJSON, map[string]any{})
		if rawBatchIDs, ok := payload["batch_ids"].([]any); ok {
			for _, rawBatchID := range rawBatchIDs {
				batchID := strings.TrimSpace(fmt.Sprint(rawBatchID))
				if batchID != "" && batchID != "<nil>" && !seen[batchID] {
					seen[batchID] = true
					batchIDs = append(batchIDs, batchID)
				}
			}
		}
		if batchID := strings.TrimSpace(fmt.Sprint(payload["batch_id"])); batchID != "" && batchID != "<nil>" && !seen[batchID] {
			batchIDs = append(batchIDs, batchID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return batchIDs, nil
}
