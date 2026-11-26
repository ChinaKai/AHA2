package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Window reads for the pet workbench "last 7 days" block. Every query bounds
// its rows in SQL by a [from, to) time window, so the work is proportional to
// the rows inside the window rather than to the whole table. Times are stored
// as RFC 3339 UTC strings (timeString), which compare correctly as text.

// The window queries are constants so the store tests can EXPLAIN them.
const (
	petEventCountQuery = `SELECT COUNT(*) FROM pet_events WHERE at>=? AND at<? AND kind=?`

	petApprovalWindowCountsQuery = `SELECT kind,status,COUNT(*) FROM pet_approvals
		WHERE created_at>=? AND created_at<?
		GROUP BY kind,status ORDER BY kind,status`

	petMainTurnWindowFilter = ` FROM turns
		WHERE task_id=? AND agent_id='main' AND started_at<>'' AND started_at>=? AND started_at<?`

	petMainTurnWindowCountQuery = `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN UPPER(SUBSTR(LTRIM(result, ' '||char(9)||char(10)||char(13)),1,4))='NOOP' THEN 1 ELSE 0 END),0)` +
		petMainTurnWindowFilter

	petMainTurnWindowUsageQuery = `SELECT ` + turnTokenUsageColumns + petMainTurnWindowFilter + ` ORDER BY sequence`
)

func petActionEventsQuery(kinds int) string {
	return `SELECT kind,detail FROM pet_events
		WHERE at>=? AND at<? AND actor=? AND kind IN (` + placeholders(kinds) + `)`
}

// PetActionEvent is the part of a pet event the insights count needs.
type PetActionEvent struct {
	Kind   string
	Detail string
}

// ListPetActionEventsInWindow returns events by the given actor whose kind is
// one of kinds and whose time is in [from, to). pet_events has an index on
// `at`, so this is a range scan over the window.
func (s *Store) ListPetActionEventsInWindow(ctx context.Context, actor string, kinds []string, from, to time.Time) ([]PetActionEvent, error) {
	if len(kinds) == 0 {
		return []PetActionEvent{}, nil
	}
	args := []any{timeString(from), timeString(to), actor}
	for _, kind := range kinds {
		args = append(args, kind)
	}
	rows, err := s.db.QueryContext(ctx, petActionEventsQuery(len(kinds)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PetActionEvent{}
	for rows.Next() {
		var item PetActionEvent
		if err := rows.Scan(&item.Kind, &item.Detail); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// CountPetEventsInWindow counts events of one kind in [from, to).
func (s *Store) CountPetEventsInWindow(ctx context.Context, kind string, from, to time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, petEventCountQuery, timeString(from), timeString(to), kind).Scan(&count)
	return count, err
}

// PetApprovalCountsInWindow groups approvals created in [from, to) by kind and
// status. pet_approvals has no index that starts with created_at
// (idx_pet_approvals_status leads with status), so SQLite scans the table;
// the table holds the pet's requests and stays small. Adding an index is a
// schema change and is left out on purpose.
func (s *Store) PetApprovalCountsInWindow(ctx context.Context, from, to time.Time) ([]domain.PetApprovalCount, error) {
	rows, err := s.db.QueryContext(ctx, petApprovalWindowCountsQuery, timeString(from), timeString(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := []domain.PetApprovalCount{}
	for rows.Next() {
		var item domain.PetApprovalCount
		if err := rows.Scan(&item.Kind, &item.Status, &item.Count); err != nil {
			return nil, err
		}
		counts = append(counts, item)
	}
	return counts, rows.Err()
}

// PetTurnWindowCounts is the wake-up count for one Task's Main Agent Turns
// that started in a window.
type PetTurnWindowCounts struct {
	Started int
	Noop    int
}

// CountMainTurnsStartedInWindow counts the Task's Main Agent Turns whose
// started_at is in [from, to), and how many of those replied with NOOP (the
// trimmed result starts with "NOOP", case-insensitive). The filter uses
// idx_turns_agent (task_id, agent_id, sequence) to stay inside this Task's
// Main Turns (checked with EXPLAIN QUERY PLAN in the store tests);
// started_at itself is not indexed, so the window is applied to those rows.
// The pet Task's own Turn count is what bounds the work.
func (s *Store) CountMainTurnsStartedInWindow(ctx context.Context, taskID string, from, to time.Time) (PetTurnWindowCounts, error) {
	var counts PetTurnWindowCounts
	err := s.db.QueryRowContext(ctx, petMainTurnWindowCountQuery, taskID, timeString(from), timeString(to)).Scan(&counts.Started, &counts.Noop)
	return counts, err
}

// ListMainTurnTokenUsageInWindow returns the same partial Turns as
// ListTurnTokenUsageForTasks (ID, TaskID, Sequence, BackendSessionID,
// RuntimeConfigSnapshotID, Usage), limited to the Task's Main Agent Turns
// that started in [from, to).
func (s *Store) ListMainTurnTokenUsageInWindow(ctx context.Context, taskID string, from, to time.Time) ([]domain.Turn, error) {
	rows, err := s.db.QueryContext(ctx, petMainTurnWindowUsageQuery, taskID, timeString(from), timeString(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Turn{}
	for rows.Next() {
		var item domain.Turn
		var usage string
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Sequence, &item.BackendSessionID, &item.RuntimeConfigSnapshotID, &usage); err != nil {
			return nil, err
		}
		item.Usage = decodeJSON(usage, map[string]any{})
		result = append(result, item)
	}
	return result, rows.Err()
}

// PetLastReply is the latest finished Main Agent Turn of a Task, with whether
// the inbox batch it answered contained a server wake.
type PetLastReply struct {
	TurnID     string
	Status     string
	Result     string
	FinishedAt time.Time
	// FromWake is true when the batch the Turn consumed held at least one
	// pet_wake inbox item. Mixed is true when the same batch also held a
	// message from someone else (the Owner, a channel, the pet's delegate).
	FromWake bool
	Mixed    bool
}

// LatestFinishedMainTurnReply returns the Task's most recent Main Agent Turn
// that has finished (finished_at set), plus the source kinds of the inbox
// batch it consumed. Only Turns with a non-empty result are considered:
// a Turn that failed before replying has nothing to show. When wakeOnly is
// true the newest such Turn whose batch held a pet_wake item is returned
// instead. sql.ErrNoRows means there is none.
func (s *Store) LatestFinishedMainTurnReply(ctx context.Context, taskID string, wakeOnly bool) (PetLastReply, error) {
	query := `
		SELECT t.id,t.status,t.result,t.finished_at,
		       EXISTS(SELECT 1 FROM agent_inbox i WHERE t.inbox_batch_id<>'' AND i.batch_id=t.inbox_batch_id AND i.task_id=t.task_id AND i.source_kind=?),
		       EXISTS(SELECT 1 FROM agent_inbox i WHERE t.inbox_batch_id<>'' AND i.batch_id=t.inbox_batch_id AND i.task_id=t.task_id AND i.source_kind<>?)
		FROM turns t
		WHERE t.task_id=? AND t.agent_id='main' AND t.finished_at<>'' AND TRIM(t.result)<>''`
	args := []any{domain.PetWakeSourceKind, domain.PetWakeSourceKind, taskID}
	if wakeOnly {
		query += ` AND t.inbox_batch_id<>'' AND EXISTS(SELECT 1 FROM agent_inbox i WHERE i.batch_id=t.inbox_batch_id AND i.task_id=t.task_id AND i.source_kind=?)`
		args = append(args, domain.PetWakeSourceKind)
	}
	query += ` ORDER BY t.sequence DESC LIMIT 1`
	var reply PetLastReply
	var finished string
	var fromWake, mixed int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&reply.TurnID, &reply.Status, &reply.Result, &finished, &fromWake, &mixed)
	if err != nil {
		return PetLastReply{}, err
	}
	reply.FinishedAt = parseTime(finished)
	reply.FromWake = fromWake != 0
	reply.Mixed = reply.FromWake && mixed != 0
	return reply, nil
}

// IsNoRows reports a missing row from the helpers above without making
// callers import database/sql.
func IsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// ListPetEventsByKind returns the newest events of one kind in any status.
func (s *Store) ListPetEventsByKind(ctx context.Context, kind string, limit int) ([]domain.PetEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,at,device_id,actor,kind,page,detail,status FROM pet_events
		WHERE kind=? ORDER BY at DESC,id DESC LIMIT ?`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.PetEvent{}
	for rows.Next() {
		var item domain.PetEvent
		var at string
		if err := rows.Scan(&item.ID, &at, &item.DeviceID, &item.Actor, &item.Kind, &item.Page, &item.Detail, &item.Status); err != nil {
			return nil, err
		}
		item.At = parseTime(at)
		result = append(result, item)
	}
	return result, rows.Err()
}
