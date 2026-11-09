package store

import (
	"context"
	"fmt"
)

// CreatePetCodeReviewLink is only called by the server's review creation flow.
// Links are immutable and local: task input, watches and sync cannot create them.
func (s *Store) CreatePetCodeReviewLink(ctx context.Context, reviewTaskID, sourceTaskID string) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO pet_code_reviews(review_task_id,source_task_id)
		SELECT review.id,source.id FROM tasks review JOIN tasks source ON source.project_id=review.project_id
		WHERE review.id=? AND source.id=? AND review.id<>source.id`, reviewTaskID, sourceTaskID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return fmt.Errorf("invalid code review task link")
	}
	return nil
}

// PetCodeReviewsForSource returns every recorded review, including links whose
// endpoints no longer match, so callers can fail closed when checking approval.
func (s *Store) PetCodeReviewsForSource(ctx context.Context, sourceTaskID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT review_task_id FROM pet_code_reviews WHERE source_task_id=? ORDER BY review_task_id`, sourceTaskID)
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

// PetCodeReviewSource revalidates the endpoints so a stale or invalid link never
// grants the review-task completion exemption.
func (s *Store) PetCodeReviewSource(ctx context.Context, reviewTaskID string) (string, error) {
	var sourceID string
	err := s.db.QueryRowContext(ctx, `SELECT source.id FROM pet_code_reviews link
		JOIN tasks review ON review.id=link.review_task_id
		JOIN tasks source ON source.id=link.source_task_id
		WHERE review.id=? AND review.id<>source.id AND review.project_id=source.project_id`, reviewTaskID).Scan(&sourceID)
	return sourceID, err
}
