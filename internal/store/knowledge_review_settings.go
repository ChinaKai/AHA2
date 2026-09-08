package store

import (
	"context"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) KnowledgeReviewSettings(ctx context.Context) (domain.KnowledgeReviewSettings, error) {
	var item domain.KnowledgeReviewSettings
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT auto_approve,updated_at FROM knowledge_review_settings WHERE id=1`).
		Scan(&item.AutoApprove, &updatedAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) UpdateKnowledgeReviewSettings(ctx context.Context, item domain.KnowledgeReviewSettings) (domain.KnowledgeReviewSettings, error) {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_review_settings(id,auto_approve,updated_at) VALUES(1,?,?)
		ON CONFLICT(id) DO UPDATE SET auto_approve=excluded.auto_approve,updated_at=excluded.updated_at`,
		boolInt(item.AutoApprove), timeString(item.UpdatedAt),
	)
	return item, err
}
