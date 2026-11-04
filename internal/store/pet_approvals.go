package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) CreatePetApproval(ctx context.Context, item domain.PetApproval) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pet_approvals(id,task_id,kind,title,summary,risk,reason,payload_json,status,resolution,decision_reason,created_at,resolved_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.Kind, item.Title, item.Summary, item.Risk, item.Reason, item.Payload,
		item.Status, item.Resolution, item.DecisionReason, timeString(item.CreatedAt), timeString(item.ResolvedAt))
	return err
}

func (s *Store) PetApproval(ctx context.Context, id string) (domain.PetApproval, error) {
	var item domain.PetApproval
	var created, resolved string
	err := s.db.QueryRowContext(ctx, `
		SELECT id,task_id,kind,title,summary,risk,reason,payload_json,status,resolution,decision_reason,created_at,resolved_at
		FROM pet_approvals WHERE id=?`, id).
		Scan(&item.ID, &item.TaskID, &item.Kind, &item.Title, &item.Summary, &item.Risk, &item.Reason, &item.Payload,
			&item.Status, &item.Resolution, &item.DecisionReason, &created, &resolved)
	if err != nil {
		return domain.PetApproval{}, err
	}
	item.CreatedAt, item.ResolvedAt = parseTime(created), parseTime(resolved)
	return item, nil
}

func (s *Store) ListPetApprovals(ctx context.Context, limit int) ([]domain.PetApproval, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,task_id,kind,title,summary,risk,reason,payload_json,status,resolution,decision_reason,created_at,resolved_at
		FROM pet_approvals
		ORDER BY CASE status WHEN 'pending' THEN 0 ELSE 1 END, created_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.PetApproval{}
	for rows.Next() {
		var item domain.PetApproval
		var created, resolved string
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Kind, &item.Title, &item.Summary, &item.Risk, &item.Reason, &item.Payload,
			&item.Status, &item.Resolution, &item.DecisionReason, &created, &resolved); err != nil {
			return nil, err
		}
		item.CreatedAt, item.ResolvedAt = parseTime(created), parseTime(resolved)
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListPendingPetApprovals returns every pending request. The work-state
// briefing uses this full set to avoid reporting a stale pending count and to
// recognize a Task that already has an owner decision waiting.
func (s *Store) ListPendingPetApprovals(ctx context.Context, limit int) ([]domain.PetApproval, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,task_id,kind,title,summary,risk,reason,payload_json,status,resolution,decision_reason,created_at,resolved_at
		FROM pet_approvals WHERE status='pending'
		ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.PetApproval{}
	for rows.Next() {
		var item domain.PetApproval
		var created, resolved string
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Kind, &item.Title, &item.Summary, &item.Risk, &item.Reason, &item.Payload,
			&item.Status, &item.Resolution, &item.DecisionReason, &created, &resolved); err != nil {
			return nil, err
		}
		item.CreatedAt, item.ResolvedAt = parseTime(created), parseTime(resolved)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListPetApprovalsWithTotal(ctx context.Context, limit int) ([]domain.PetApproval, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pet_approvals`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,task_id,kind,title,summary,risk,reason,payload_json,status,resolution,decision_reason,created_at,resolved_at
		FROM pet_approvals
		ORDER BY CASE status WHEN 'pending' THEN 0 ELSE 1 END, created_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []domain.PetApproval{}
	for rows.Next() {
		var item domain.PetApproval
		var created, resolved string
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Kind, &item.Title, &item.Summary, &item.Risk, &item.Reason, &item.Payload,
			&item.Status, &item.Resolution, &item.DecisionReason, &created, &resolved); err != nil {
			return nil, 0, err
		}
		item.CreatedAt, item.ResolvedAt = parseTime(created), parseTime(resolved)
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Store) ResolvePetApproval(ctx context.Context, id, status, resolution string, resolvedAt time.Time, decisionReason ...string) error {
	reason := strings.TrimSpace(resolution)
	if len(decisionReason) > 0 && strings.TrimSpace(decisionReason[0]) != "" {
		reason = strings.TrimSpace(decisionReason[0])
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE pet_approvals SET status=?,resolution=?,decision_reason=?,resolved_at=?
		WHERE id=? AND status='pending'`,
		status, resolution, reason, timeString(resolvedAt), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
