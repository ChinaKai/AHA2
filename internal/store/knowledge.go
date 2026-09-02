package store

import (
	"context"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) CreateKnowledge(ctx context.Context, item domain.KnowledgeEntry) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_entries(id,scope,project_id,type,title,body,status,branch_scope,evidence_json,confidence,source_task_id,source_turn_id,created_at,updated_at,last_verified_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.Scope, item.ProjectID, item.Type, item.Title, item.Body, item.Status, item.BranchScope,
		item.EvidenceJSON, item.Confidence, item.SourceTaskID, item.SourceTurnID,
		timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.LastVerifiedAt),
	)
	return err
}

func (s *Store) ListKnowledge(ctx context.Context, scope, projectID string, statuses []domain.KnowledgeStatus) ([]domain.KnowledgeEntry, error) {
	query := `SELECT id,scope,project_id,type,title,body,status,branch_scope,evidence_json,confidence,source_task_id,source_turn_id,created_at,updated_at,last_verified_at FROM knowledge_entries WHERE 1=1`
	args := []any{}
	if scope != "" {
		query += ` AND scope=?`
		args = append(args, scope)
	}
	if projectID != "" {
		query += ` AND project_id=?`
		args = append(args, projectID)
	}
	if len(statuses) > 0 {
		query += ` AND status IN (`
		for index, status := range statuses {
			if index > 0 {
				query += `,`
			}
			query += `?`
			args = append(args, status)
		}
		query += `)`
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.KnowledgeEntry{}
	for rows.Next() {
		var item domain.KnowledgeEntry
		var createdAt, updatedAt, verifiedAt string
		if err := rows.Scan(&item.ID, &item.Scope, &item.ProjectID, &item.Type, &item.Title, &item.Body, &item.Status,
			&item.BranchScope, &item.EvidenceJSON, &item.Confidence, &item.SourceTaskID, &item.SourceTurnID,
			&createdAt, &updatedAt, &verifiedAt); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt, item.LastVerifiedAt = parseTime(createdAt), parseTime(updatedAt), parseTime(verifiedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) VerifyKnowledge(ctx context.Context, id, updatedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE knowledge_entries SET status=?,last_verified_at=?,updated_at=? WHERE id=?`,
		domain.KnowledgeVerified, updatedAt, updatedAt, id)
	return err
}
