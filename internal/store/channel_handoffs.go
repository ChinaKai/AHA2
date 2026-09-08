package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const channelHandoffColumns = `id,instance_id,origin_conversation_id,origin_inbox_id,requester_identity_link_id,owner_conversation_id,summary,details,source_json,state,decision,accepted_action_id,created_task_id,created_at,updated_at,resolved_at`

func scanChannelHandoff(scanner interface{ Scan(...any) error }) (domain.ChannelHandoff, error) {
	var item domain.ChannelHandoff
	var ownerConversation, acceptedAction, createdTask sql.NullString
	var source, created, updated, resolved string
	err := scanner.Scan(&item.ID, &item.InstanceID, &item.OriginConversationID, &item.OriginInboxID, &item.RequesterIdentityLinkID,
		&ownerConversation, &item.Summary, &item.Details, &source, &item.State, &item.Decision, &acceptedAction, &createdTask, &created, &updated, &resolved)
	if ownerConversation.Valid {
		item.OwnerConversationID = ownerConversation.String
	}
	if acceptedAction.Valid {
		item.AcceptedActionID = acceptedAction.String
	}
	if createdTask.Valid {
		item.CreatedTaskID = createdTask.String
	}
	item.Source = decodeJSON(source, map[string]any{})
	item.CreatedAt, item.UpdatedAt, item.ResolvedAt = parseTime(created), parseTime(updated), parseTime(resolved)
	return item, err
}

func (s *Store) CreateChannelHandoff(ctx context.Context, item domain.ChannelHandoff) (domain.ChannelHandoff, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelHandoff{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO channel_handoffs(id,instance_id,origin_conversation_id,origin_inbox_id,requester_identity_link_id,owner_conversation_id,summary,details,source_json,state,decision,accepted_action_id,created_task_id,created_at,updated_at,resolved_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.InstanceID, item.OriginConversationID, item.OriginInboxID, item.RequesterIdentityLinkID, nullableString(item.OwnerConversationID),
		item.Summary, item.Details, encodeJSON(item.Source), item.State, item.Decision, nullableString(item.AcceptedActionID), nullableString(item.CreatedTaskID),
		timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.ResolvedAt))
	if err != nil {
		return domain.ChannelHandoff{}, err
	}
	inserted, _ := result.RowsAffected()
	if inserted == 1 && item.OwnerConversationID != "" {
		payload := map[string]any{"kind": "handoff", "handoff_id": item.ID, "summary": item.Summary, "details": item.Details, "status": item.State}
		if err := enqueueChannelActionDeliveryTx(ctx, tx, item.InstanceID, item.OwnerConversationID, item.ID+":created", "handoff_created", payload, item.CreatedAt); err != nil {
			return domain.ChannelHandoff{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.ChannelHandoff{}, err
	}
	return s.ChannelHandoff(ctx, item.ID)
}

func (s *Store) ChannelHandoff(ctx context.Context, id string) (domain.ChannelHandoff, error) {
	return scanChannelHandoff(s.db.QueryRowContext(ctx, `SELECT `+channelHandoffColumns+` FROM channel_handoffs WHERE id=?`, id))
}

func (s *Store) ChannelHandoffs(ctx context.Context, instanceID string) ([]domain.ChannelHandoff, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+channelHandoffColumns+` FROM channel_handoffs WHERE instance_id=? ORDER BY created_at DESC`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ChannelHandoff{}
	for rows.Next() {
		item, err := scanChannelHandoff(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ResolveChannelHandoff(ctx context.Context, id, actionID, state, decision, taskID string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE channel_handoffs SET state=?,decision=?,accepted_action_id=?,created_task_id=?,updated_at=?,resolved_at=? WHERE id=? AND state IN ('pending_owner','accepted_todo','accepted_task')`,
		state, decision, nullableString(actionID), nullableString(taskID), timeString(at), timeString(at), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrChannelRevision
	}
	return nil
}

func (s *Store) DeliverPendingChannelHandoffs(ctx context.Context, instanceID, ownerConversationID string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,summary,details,state FROM channel_handoffs WHERE instance_id=? AND owner_conversation_id IS NULL AND state IN ('pending_owner','accepted_todo') ORDER BY created_at`, instanceID)
	if err != nil {
		return err
	}
	type pending struct{ id, summary, details, state string }
	items := []pending{}
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.summary, &item.details, &item.state); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `UPDATE channel_handoffs SET owner_conversation_id=?,updated_at=? WHERE id=? AND owner_conversation_id IS NULL`, ownerConversationID, timeString(at), item.id); err != nil {
			return err
		}
		payload := map[string]any{"kind": "handoff", "handoff_id": item.id, "summary": item.summary, "details": item.details, "status": item.state}
		if err := enqueueChannelActionDeliveryTx(ctx, tx, instanceID, ownerConversationID, item.id+":created", "handoff_created", payload, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}
