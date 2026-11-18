package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// PetCodeReviewLink is one durable source/review association joined with the
// Review Task's current identity. CreatedAt lets callers order several reviews
// of the same source without trusting SQLite's text collation of RFC3339Nano.
type PetCodeReviewLink struct {
	SourceTaskID string
	ReviewTaskID string
	Code         string
	Status       domain.TaskStatus
	CreatedAt    time.Time
	rowid        int64
}

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

// PetCodeReviewLinksBySource returns the links for the given source Tasks,
// grouped by source and oldest first. One statement serves the whole
// work-state briefing; callers choose the newest in-flight review in memory so
// the query count does not grow with the number of waiting Tasks. The project
// equality keeps display on the same trusted link invariant as
// PetCodeReviewSource. SQLite serves the IN lookup from
// idx_pet_code_reviews_source and the caller sorts in memory, so the plan has
// neither a full link scan nor a temporary ORDER BY sort.
func (s *Store) PetCodeReviewLinksBySource(ctx context.Context, sourceTaskIDs []string) (map[string][]PetCodeReviewLink, error) {
	grouped := map[string][]PetCodeReviewLink{}
	if len(sourceTaskIDs) == 0 {
		return grouped, nil
	}
	args := make([]any, len(sourceTaskIDs))
	placeholders := make([]string, len(sourceTaskIDs))
	for i, id := range sourceTaskIDs {
		args[i] = id
		placeholders[i] = "?"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT link.source_task_id,review.id,review.code,review.status,review.created_at,review.rowid
		FROM pet_code_reviews link
		JOIN tasks review ON review.id=link.review_task_id
		JOIN tasks source ON source.id=link.source_task_id AND source.project_id=review.project_id
		WHERE link.source_task_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var link PetCodeReviewLink
		var createdAt string
		if err := rows.Scan(&link.SourceTaskID, &link.ReviewTaskID, &link.Code, &link.Status, &createdAt, &link.rowid); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("invalid review creation time: %w", err)
		}
		link.CreatedAt = at
		grouped[link.SourceTaskID] = append(grouped[link.SourceTaskID], link)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for source, links := range grouped {
		sort.SliceStable(links, func(i, j int) bool {
			if links[i].CreatedAt.Equal(links[j].CreatedAt) {
				return links[i].rowid < links[j].rowid
			}
			return links[i].CreatedAt.Before(links[j].CreatedAt)
		})
		grouped[source] = links
	}
	return grouped, nil
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
