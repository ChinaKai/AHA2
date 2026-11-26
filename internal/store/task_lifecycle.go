package store

import (
	"context"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// As in the pet UI, target means the string task_id in the payload.
const petApprovalTargetSQL = `CASE WHEN json_valid(payload_json)
	THEN CASE WHEN json_type(payload_json,'$.task_id')='text'
	          THEN trim(json_extract(payload_json,'$.task_id'), char(9)||char(10)||char(13)||' ') END END`

// TaskLifecycleApprovals reads only fields displayed in the owner timeline.
func (s *Store) TaskLifecycleApprovals(ctx context.Context, taskID string) ([]domain.PetApproval, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,title,summary,status,decision_reason,created_at,resolved_at
		FROM pet_approvals WHERE `+petApprovalTargetSQL+`=?
		ORDER BY rowid`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.PetApproval
	for rows.Next() {
		var item domain.PetApproval
		var created, resolved string
		if err := rows.Scan(&item.ID, &item.Title, &item.Summary, &item.Status, &item.DecisionReason, &created, &resolved); err != nil {
			return nil, err
		}
		item.CreatedAt, item.ResolvedAt = parseTime(created), parseTime(resolved)
		result = append(result, item)
	}
	return result, rows.Err()
}

// TaskLifecycleMainTurns reads the timestamps of all Main Turns for the
// statistics. It fetches short result/error excerpts only for the most recent
// showLimit rows. The separate token projection still includes every agent.
func (s *Store) TaskLifecycleMainTurns(ctx context.Context, taskID string, showLimit int) ([]domain.Turn, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH visible AS (
			SELECT id FROM turns WHERE task_id=? AND agent_id='main'
			ORDER BY sequence DESC LIMIT ?
		)
		SELECT t.id,t.sequence,t.status,t.started_at,t.finished_at,
		       CASE WHEN v.id IS NULL THEN '' ELSE substr(t.result,1,200) END,
		       CASE WHEN v.id IS NULL THEN '' ELSE substr(t.error,1,200) END
		FROM turns t LEFT JOIN visible v ON v.id=t.id
		WHERE t.task_id=? AND t.agent_id='main' ORDER BY t.sequence DESC`,
		taskID, showLimit, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Turn
	for rows.Next() {
		var item domain.Turn
		var started, finished string
		if err := rows.Scan(&item.ID, &item.Sequence, &item.Status, &started, &finished, &item.Result, &item.Error); err != nil {
			return nil, err
		}
		item.StartedAt, item.FinishedAt = parseTime(started), parseTime(finished)
		result = append(result, item)
	}
	return result, rows.Err()
}

type TaskLifecycleSub struct {
	AgentID    string    `json:"agent_id"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// TaskLifecycleSubs includes the latest batch even after Main consumes its
// results. Joining its members with their linked Turn avoids loading unrelated
// historical Sub executions.
func (s *Store) TaskLifecycleSubs(ctx context.Context, taskID string) ([]TaskLifecycleSub, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.agent_id,COALESCE(NULLIF(t.title,''),a.title,''),m.status,
		       COALESCE(t.started_at,''),COALESCE(t.finished_at,'')
		FROM collaboration_members m
		JOIN collaboration_batches b ON b.id=m.batch_id
		LEFT JOIN task_agents a ON a.task_id=b.task_id AND a.agent_id=m.agent_id
		LEFT JOIN turns t ON t.id=m.child_turn_id AND t.task_id=b.task_id AND t.agent_id=m.agent_id
		WHERE b.id=(SELECT id FROM collaboration_batches WHERE task_id=?
		            ORDER BY rtrim(created_at,'Z') DESC,rowid DESC LIMIT 1)
		ORDER BY m.agent_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []TaskLifecycleSub{}
	for rows.Next() {
		var item TaskLifecycleSub
		var started, finished string
		if err := rows.Scan(&item.AgentID, &item.Title, &item.Status, &started, &finished); err != nil {
			return nil, err
		}
		item.StartedAt, item.FinishedAt = parseTime(started), parseTime(finished)
		result = append(result, item)
	}
	return result, rows.Err()
}

type TaskLifecycleReviewResult struct {
	TaskID     string
	Code       string
	Status     domain.TaskStatus
	Result     string
	TurnStatus domain.TurnStatus
	FinishedAt time.Time
}

// TaskLifecycleReviews reads the same latest review Turn used by the completion
// gate. Only a valid link within the source project can contribute a result.
func (s *Store) TaskLifecycleReviews(ctx context.Context, taskID string) ([]TaskLifecycleReviewResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT review.id,review.code,review.status,COALESCE(t.result,''),
		       COALESCE(t.status,''),COALESCE(t.finished_at,'')
		FROM pet_code_reviews link
		JOIN tasks review ON review.id=link.review_task_id
		JOIN tasks source ON source.id=link.source_task_id AND source.project_id=review.project_id
		LEFT JOIN turns t ON t.id=(SELECT id FROM turns WHERE task_id=review.id AND agent_id='main'
		                          ORDER BY sequence DESC LIMIT 1)
		WHERE source.id=? ORDER BY rtrim(review.created_at,'Z'),review.rowid`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []TaskLifecycleReviewResult
	for rows.Next() {
		var item TaskLifecycleReviewResult
		var finished string
		if err := rows.Scan(&item.TaskID, &item.Code, &item.Status, &item.Result, &item.TurnStatus, &finished); err != nil {
			return nil, err
		}
		item.FinishedAt = parseTime(finished)
		result = append(result, item)
	}
	return result, rows.Err()
}
