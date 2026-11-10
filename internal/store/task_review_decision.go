package store

import (
	"context"
	"fmt"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Keep historical Tasks undecided; a completion declaration is never inferred.
func (s *Store) migrateTaskReviewDecision(ctx context.Context) error {
	var applied bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=127)`).Scan(&applied); err != nil {
		return err
	}
	if applied {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `ALTER TABLE tasks ADD COLUMN review_decision_json TEXT NOT NULL DEFAULT '{}'`); err != nil {
		return fmt.Errorf("apply schema v127 review decision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(127,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return err
	}
	return tx.Commit()
}

// CompleteTaskWithReviewDecision writes the completion and its audit declaration
// in the same status update. Owner completion (nil) clears any old declaration.
// The update is guarded by NOT EXISTS(active turn), so it cannot commit while a
// scheduler is creating a Turn for the same Task.
func (s *Store) CompleteTaskWithReviewDecision(ctx context.Context, id string, from domain.TaskStatus, at string, decision *domain.TaskReviewDecision) error {
	return s.updateTaskStatus(ctx, id, from, domain.TaskCompleted, at, at, decision, true)
}
