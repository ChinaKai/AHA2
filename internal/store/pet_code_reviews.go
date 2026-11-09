package store

import (
	"context"
	"fmt"
	"sort"
	"time"
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

// PetCodeReviewsForSource returns reviews oldest first, with insertion order
// breaking ties. Parse RFC3339Nano: variable fractional digits do not sort as text.
// Cross-project links remain visible so approval checks can fail closed.
func (s *Store) PetCodeReviewsForSource(ctx context.Context, sourceTaskID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT review.id,review.created_at FROM pet_code_reviews link
		JOIN tasks review ON review.id=link.review_task_id
		WHERE link.source_task_id=? ORDER BY review.rowid`, sourceTaskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type reviewTime struct {
		id        string
		createdAt time.Time
	}
	var reviews []reviewTime
	for rows.Next() {
		var id, createdAt string
		if err := rows.Scan(&id, &createdAt); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("invalid review creation time: %w", err)
		}
		reviews = append(reviews, reviewTime{id: id, createdAt: at})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(reviews, func(i, j int) bool { return reviews[i].createdAt.Before(reviews[j].createdAt) })
	ids := make([]string, len(reviews))
	for i, review := range reviews {
		ids[i] = review.id
	}
	return ids, nil
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
