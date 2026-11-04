package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const petTaskWatchColumns = `task_id,reason,created_at,updated_at,last_status,last_turn_id,last_notified_status,last_notified_turn_id,ignored_at`

func scanPetTaskWatch(scanner interface{ Scan(...any) error }) (domain.PetTaskWatch, error) {
	var item domain.PetTaskWatch
	var created, updated, ignoredAt string
	err := scanner.Scan(
		&item.TaskID, &item.Reason, &created, &updated, &item.LastStatus, &item.LastTurnID,
		&item.LastNotifiedStatus, &item.LastNotifiedTurnID, &ignoredAt,
	)
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	if strings.TrimSpace(ignoredAt) != "" {
		item.Ignored = true
		item.IgnoredAt = parseTime(ignoredAt)
	}
	return item, err
}

// UpsertPetTaskWatch creates or updates a real follow. Ignored rows are never
// overwritten: the conditional update closes the check-then-upsert race where a
// concurrent ignore could otherwise be turned into a follow (or vice versa).
func (s *Store) UpsertPetTaskWatch(ctx context.Context, taskID, reason string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pet_task_watches(task_id,reason,created_at,updated_at)
		VALUES(?,?,?,?)
		ON CONFLICT(task_id) DO UPDATE SET reason=excluded.reason,updated_at=excluded.updated_at
		WHERE pet_task_watches.ignored_at=''`,
		taskID, reason, timeString(at), timeString(at))
	return err
}

func (s *Store) PetTaskWatch(ctx context.Context, taskID string) (domain.PetTaskWatch, error) {
	return scanPetTaskWatch(s.db.QueryRowContext(ctx,
		`SELECT `+petTaskWatchColumns+` FROM pet_task_watches WHERE task_id=?`, taskID))
}

func (s *Store) PetTaskWatches(ctx context.Context) ([]domain.PetTaskWatch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+petTaskWatchColumns+` FROM pet_task_watches ORDER BY updated_at DESC,task_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.PetTaskWatch{}
	for rows.Next() {
		item, err := scanPetTaskWatch(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) DeletePetTaskWatch(ctx context.Context, taskID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pet_task_watches WHERE task_id=?`, taskID)
	return err
}

// IgnorePetTaskWatch marks a Task ignored without changing whether it is
// followed. When no row exists the insert also writes ignored_at, so creating
// an ignore-only row is one atomic statement and no notification window opens
// between "watch created" and "watch ignored". ignoreOnlyReason is used only on
// insert; an existing real follow keeps its reason.
func (s *Store) IgnorePetTaskWatch(ctx context.Context, taskID, ignoreOnlyReason string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pet_task_watches(task_id,reason,created_at,updated_at,ignored_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(task_id) DO UPDATE SET ignored_at=excluded.ignored_at,updated_at=excluded.updated_at`,
		taskID, ignoreOnlyReason, timeString(at), timeString(at), timeString(at))
	return err
}

// RestorePetTaskWatch clears an ignored watch. An ignore-only row is deleted so
// the Task returns to the unfollowed list; a real follow is only un-ignored.
// The DELETE is scoped by reason, so it can never remove a follow that raced in
// between the Owner's ignore and restore actions.
func (s *Store) RestorePetTaskWatch(ctx context.Context, taskID, ignoreOnlyReason string, at time.Time) (removed, found bool, err error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM pet_task_watches WHERE task_id=? AND reason=?`, taskID, ignoreOnlyReason)
	if err != nil {
		return false, false, err
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		return true, true, nil
	}
	result, err = s.db.ExecContext(ctx, `
		UPDATE pet_task_watches SET ignored_at='',updated_at=? WHERE task_id=?`,
		timeString(at), taskID)
	if err != nil {
		return false, false, err
	}
	affected, _ := result.RowsAffected()
	return false, affected > 0, nil
}

func (s *Store) MarkPetTaskWatchNotified(ctx context.Context, taskID, status, turnID string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE pet_task_watches
		SET last_status=?,last_turn_id=?,last_notified_status=?,last_notified_turn_id=?,updated_at=?
		WHERE task_id=?`,
		status, turnID, status, turnID, timeString(at), taskID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}
