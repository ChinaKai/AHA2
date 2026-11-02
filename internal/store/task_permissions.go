package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const taskPermissionRequestColumns = `id,task_id,turn_id,inbound_receipt_id,channel_instance_id,channel_conversation_id,channel_action_id,applicant_identity_link_id,applicant_name,applicant_is_bot,action,scope,reason,risk,status,decider_identity_link_id,decider_name,decision_reason,created_at,updated_at,decided_at,expires_at`

func scanTaskPermissionRequest(scanner interface{ Scan(...any) error }) (domain.TaskPermissionRequest, error) {
	var item domain.TaskPermissionRequest
	var created, updated, decided, expires string
	err := scanner.Scan(
		&item.ID, &item.TaskID, &item.TurnID, &item.InboundReceiptID, &item.ChannelInstanceID,
		&item.ChannelConversationID, &item.ChannelActionID, &item.ApplicantIdentityLinkID,
		&item.ApplicantName, &item.ApplicantIsBot, &item.Action, &item.Scope, &item.Reason,
		&item.Risk, &item.Status, &item.DeciderIdentityLinkID, &item.DeciderName,
		&item.DecisionReason, &created, &updated, &decided, &expires,
	)
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	if decided != "" {
		item.DecidedAt = parseTime(decided)
	}
	item.ExpiresAt = parseTime(expires)
	return item, err
}

func (s *Store) CreateTaskPermissionRequest(ctx context.Context, item domain.TaskPermissionRequest) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO task_permission_requests(`+taskPermissionRequestColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.TurnID, item.InboundReceiptID, item.ChannelInstanceID,
		item.ChannelConversationID, item.ChannelActionID, item.ApplicantIdentityLinkID,
		item.ApplicantName, item.ApplicantIsBot, item.Action, item.Scope, item.Reason,
		item.Risk, item.Status, item.DeciderIdentityLinkID, item.DeciderName,
		item.DecisionReason, timeString(item.CreatedAt), timeString(item.UpdatedAt),
		timeString(item.DecidedAt), timeString(item.ExpiresAt),
	)
	return err
}

func (s *Store) TaskPermissionRequest(ctx context.Context, id string) (domain.TaskPermissionRequest, error) {
	return scanTaskPermissionRequest(s.db.QueryRowContext(ctx,
		`SELECT `+taskPermissionRequestColumns+` FROM task_permission_requests WHERE id=?`, id))
}

func (s *Store) ListTaskPermissionRequests(ctx context.Context, taskID string, limit int) ([]domain.TaskPermissionRequest, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	now := time.Now().UTC()
	_, _ = s.db.ExecContext(ctx, `
		UPDATE task_permission_requests SET status='expired',updated_at=?
		WHERE task_id=? AND status='pending' AND expires_at<=?`,
		timeString(now), taskID, timeString(now))
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+taskPermissionRequestColumns+` FROM task_permission_requests
		WHERE task_id=?
		ORDER BY CASE status WHEN 'pending' THEN 0 ELSE 1 END, created_at DESC
		LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.TaskPermissionRequest{}
	for rows.Next() {
		item, scanErr := scanTaskPermissionRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) SetTaskPermissionRequestChannelAction(ctx context.Context, id, actionID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE task_permission_requests SET channel_action_id=?,updated_at=? WHERE id=?`,
		actionID, timeString(at), id)
	return err
}

// ResolveTaskPermissionRequest consumes one pending request. It returns false
// without changing anything when the request is already decided, expired, or
// belongs to another task.
func (s *Store) ResolveTaskPermissionRequest(
	ctx context.Context,
	id, decision, deciderIdentityLinkID, deciderName, reason string,
	at time.Time,
) (domain.TaskPermissionRequest, bool, error) {
	if decision != "approved" && decision != "rejected" {
		return domain.TaskPermissionRequest{}, false, sql.ErrNoRows
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE task_permission_requests
		SET status=?,decider_identity_link_id=?,decider_name=?,decision_reason=?,decided_at=?,updated_at=?
		WHERE id=? AND status='pending' AND expires_at>?`,
		decision, deciderIdentityLinkID, deciderName, reason, timeString(at), timeString(at), id, timeString(at))
	if err != nil {
		return domain.TaskPermissionRequest{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.TaskPermissionRequest{}, false, err
	}
	item, err := s.TaskPermissionRequest(ctx, id)
	if err != nil {
		return domain.TaskPermissionRequest{}, false, err
	}
	return item, affected == 1, nil
}
