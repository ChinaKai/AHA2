package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const knowledgeColumns = `id,scope,project_id,type,title,body,status,branch_scope,product_line_id,evidence_json,confidence,revision,content_hash,verified_commit,helped_count,stale_count,feedback_state,source_task_id,source_turn_id,created_at,updated_at,last_verified_at`

func (s *Store) CreateKnowledge(ctx context.Context, item domain.KnowledgeEntry) error {
	if item.Revision < 1 {
		item.Revision = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_entries(`+knowledgeColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.Scope, item.ProjectID, item.Type, item.Title, item.Body, item.Status, item.BranchScope,
		item.ProductLineID, item.EvidenceJSON, item.Confidence, item.Revision, item.ContentHash, item.VerifiedCommit,
		item.HelpedCount, item.StaleCount, item.FeedbackState, item.SourceTaskID, item.SourceTurnID,
		timeString(item.CreatedAt), timeString(item.UpdatedAt), timeString(item.LastVerifiedAt),
	)
	if err == nil && item.ProjectID != "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE projects SET knowledge_revision=knowledge_revision+1 WHERE id=?`, item.ProjectID)
	}
	return err
}

func scanKnowledge(scanner interface{ Scan(...any) error }) (domain.KnowledgeEntry, error) {
	var item domain.KnowledgeEntry
	var createdAt, updatedAt, verifiedAt string
	err := scanner.Scan(&item.ID, &item.Scope, &item.ProjectID, &item.Type, &item.Title, &item.Body, &item.Status,
		&item.BranchScope, &item.ProductLineID, &item.EvidenceJSON, &item.Confidence, &item.Revision, &item.ContentHash,
		&item.VerifiedCommit, &item.HelpedCount, &item.StaleCount, &item.FeedbackState, &item.SourceTaskID,
		&item.SourceTurnID, &createdAt, &updatedAt, &verifiedAt)
	item.CreatedAt, item.UpdatedAt, item.LastVerifiedAt = parseTime(createdAt), parseTime(updatedAt), parseTime(verifiedAt)
	return item, err
}

func (s *Store) Knowledge(ctx context.Context, id string) (domain.KnowledgeEntry, error) {
	return scanKnowledge(s.db.QueryRowContext(ctx, `SELECT `+knowledgeColumns+` FROM knowledge_entries WHERE id=?`, id))
}

func (s *Store) ListKnowledge(ctx context.Context, scope, projectID string, statuses []domain.KnowledgeStatus) ([]domain.KnowledgeEntry, error) {
	return s.listKnowledge(ctx, scope, projectID, "", false, statuses)
}

func (s *Store) ListApplicableKnowledge(ctx context.Context, projectID, productLineID string, statuses []domain.KnowledgeStatus) ([]domain.KnowledgeEntry, error) {
	return s.listKnowledge(ctx, "project", projectID, productLineID, true, statuses)
}

func (s *Store) listKnowledge(ctx context.Context, scope, projectID, productLineID string, applicableOnly bool, statuses []domain.KnowledgeStatus) ([]domain.KnowledgeEntry, error) {
	query := `SELECT ` + knowledgeColumns + ` FROM knowledge_entries WHERE 1=1`
	args := []any{}
	if scope != "" {
		query += ` AND scope=?`
		args = append(args, scope)
	}
	if projectID != "" {
		query += ` AND project_id=?`
		args = append(args, projectID)
	}
	if applicableOnly {
		query += ` AND (product_line_id='' OR product_line_id=?)`
		args = append(args, productLineID)
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
	query += ` ORDER BY type,title,updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.KnowledgeEntry{}
	for rows.Next() {
		item, err := scanKnowledge(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) MatchingKnowledge(ctx context.Context, scope, projectID, productLineID, kind, title string) (domain.KnowledgeEntry, error) {
	return scanKnowledge(s.db.QueryRowContext(ctx, `SELECT `+knowledgeColumns+` FROM knowledge_entries
		WHERE scope=? AND project_id=? AND product_line_id=? AND type=? AND lower(title)=lower(?) ORDER BY updated_at DESC LIMIT 1`,
		scope, projectID, productLineID, kind, strings.TrimSpace(title)))
}

func (s *Store) UpdateKnowledge(ctx context.Context, item domain.KnowledgeEntry) error {
	if item.Revision < 1 {
		item.Revision = 1
	}
	result, err := s.db.ExecContext(ctx, `UPDATE knowledge_entries SET scope=?,project_id=?,type=?,title=?,body=?,status=?,branch_scope=?,product_line_id=?,evidence_json=?,confidence=?,revision=?,content_hash=?,verified_commit=?,helped_count=?,stale_count=?,feedback_state=?,source_task_id=?,source_turn_id=?,updated_at=?,last_verified_at=? WHERE id=?`,
		item.Scope, item.ProjectID, item.Type, item.Title, item.Body, item.Status, item.BranchScope, item.ProductLineID,
		item.EvidenceJSON, item.Confidence, item.Revision, item.ContentHash, item.VerifiedCommit, item.HelpedCount,
		item.StaleCount, item.FeedbackState, item.SourceTaskID, item.SourceTurnID, timeString(item.UpdatedAt),
		timeString(item.LastVerifiedAt), item.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	if item.ProjectID != "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE projects SET knowledge_revision=knowledge_revision+1 WHERE id=?`, item.ProjectID)
	}
	return nil
}

func (s *Store) DeleteKnowledge(ctx context.Context, id string) error {
	item, _ := s.Knowledge(ctx, id)
	_, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_entries WHERE id=?`, id)
	if err == nil && item.ProjectID != "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE projects SET knowledge_revision=knowledge_revision+1 WHERE id=?`, item.ProjectID)
	}
	return err
}

func (s *Store) VerifyKnowledge(ctx context.Context, id, updatedAt string) error {
	item, err := s.Knowledge(ctx, id)
	if err != nil {
		return err
	}
	item.Status = domain.KnowledgeVerified
	item.Revision++
	item.FeedbackState = ""
	item.LastVerifiedAt = parseTime(updatedAt)
	item.UpdatedAt = item.LastVerifiedAt
	return s.UpdateKnowledge(ctx, item)
}

func (s *Store) FeedbackKnowledge(ctx context.Context, id, kind string, updatedAt string) (domain.KnowledgeEntry, error) {
	item, err := s.Knowledge(ctx, id)
	if err != nil {
		return domain.KnowledgeEntry{}, err
	}
	switch kind {
	case "helped":
		item.HelpedCount++
		item.FeedbackState = "helped"
	case "stale", "wrong":
		item.StaleCount++
		item.FeedbackState = kind
		item.Status = domain.KnowledgeStale
	default:
		return domain.KnowledgeEntry{}, sql.ErrNoRows
	}
	item.UpdatedAt = parseTime(updatedAt)
	if err := s.UpdateKnowledge(ctx, item); err != nil {
		return domain.KnowledgeEntry{}, err
	}
	return item, nil
}
