package store

import (
	"context"
	"time"
)

// petDeskBatchSize keeps every IN list far below SQLite's bound-variable
// limit, so a desk read over several thousand Tasks stays a few statements.
const petDeskBatchSize = 400

// TaskStateCard is the "now" part of a Task's memory: the short status the
// Agent wrote, what comes next and what waits on the Owner. It leaves out the
// goal history and constraints, which a list view never shows.
type TaskStateCard struct {
	TaskID          string
	Status          string
	StatusUpdatedAt time.Time
	NextSteps       []string
	OpenQuestions   []string
}

// TaskStateCards reads the state cards of many Tasks at once. A Task that
// never wrote a card is absent from the result.
func (s *Store) TaskStateCards(ctx context.Context, taskIDs []string) (map[string]TaskStateCard, error) {
	result := make(map[string]TaskStateCard, len(taskIDs))
	for start := 0; start < len(taskIDs); start += petDeskBatchSize {
		end := start + petDeskBatchSize
		if end > len(taskIDs) {
			end = len(taskIDs)
		}
		batch := taskIDs[start:end]
		args := make([]any, len(batch))
		for index, id := range batch {
			args[index] = id
		}
		if err := s.taskStateCardBatch(ctx, args, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) taskStateCardBatch(ctx context.Context, args []any, result map[string]TaskStateCard) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id,status,status_updated_at,next_steps_json,open_questions_json
		FROM task_memory WHERE task_id IN (`+placeholders(len(args))+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item TaskStateCard
		var statusUpdatedAt, nextSteps, openQuestions string
		if err := rows.Scan(&item.TaskID, &item.Status, &statusUpdatedAt, &nextSteps, &openQuestions); err != nil {
			return err
		}
		item.StatusUpdatedAt = parseTime(statusUpdatedAt)
		item.NextSteps = decodeJSON(nextSteps, []string{})
		item.OpenQuestions = decodeJSON(openQuestions, []string{})
		result[item.TaskID] = item
	}
	return rows.Err()
}

// MainTurnBrief is the latest Main Turn of a Task, reduced to what a list
// needs to tell "still running", "finished cleanly" and "stopped with an
// error" apart.
type MainTurnBrief struct {
	TaskID     string
	Status     string
	Error      string
	QueuedAt   time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

// LatestMainTurnBriefs returns the newest Main Turn of each given Task. It
// selects the same row as LatestTurnForAgent(task, "main") for every Task in
// one statement per batch, so the query count does not grow with the list.
func (s *Store) LatestMainTurnBriefs(ctx context.Context, taskIDs []string) (map[string]MainTurnBrief, error) {
	result := make(map[string]MainTurnBrief, len(taskIDs))
	for start := 0; start < len(taskIDs); start += petDeskBatchSize {
		end := start + petDeskBatchSize
		if end > len(taskIDs) {
			end = len(taskIDs)
		}
		batch := taskIDs[start:end]
		args := make([]any, len(batch))
		for index, id := range batch {
			args[index] = id
		}
		if err := s.latestMainTurnBriefBatch(ctx, args, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) latestMainTurnBriefBatch(ctx context.Context, args []any, result map[string]MainTurnBrief) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.task_id,t.status,t.error,t.queued_at,t.started_at,t.finished_at
		FROM turns t
		WHERE t.agent_id='main' AND t.task_id IN (`+placeholders(len(args))+`)
		  AND t.sequence=(SELECT MAX(sequence) FROM turns WHERE task_id=t.task_id AND agent_id='main')`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item MainTurnBrief
		var queuedAt, startedAt, finishedAt string
		if err := rows.Scan(&item.TaskID, &item.Status, &item.Error, &queuedAt, &startedAt, &finishedAt); err != nil {
			return err
		}
		item.QueuedAt = parseTime(queuedAt)
		item.StartedAt = parseTime(startedAt)
		item.FinishedAt = parseTime(finishedAt)
		result[item.TaskID] = item
	}
	return rows.Err()
}
