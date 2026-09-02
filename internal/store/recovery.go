package store

import (
	"context"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type RecoveryResult struct {
	Turns int `json:"turns"`
	Tasks int `json:"tasks"`
}

func (s *Store) RecoverInterrupted(ctx context.Context, now time.Time) (RecoveryResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryResult{}, err
	}
	defer tx.Rollback()
	finishedAt := timeString(now)
	turnResult, err := tx.ExecContext(ctx, `
		UPDATE turns SET status=?,finished_at=?,exit_code=130,error='AHA service restarted during this turn'
		WHERE status IN (?,?,?,?,?)`,
		domain.TurnInterrupted, finishedAt,
		domain.TurnQueued, domain.TurnPreparing, domain.TurnStarting, domain.TurnRunning, domain.TurnWaiting,
	)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("recover turns: %w", err)
	}
	taskResult, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status=?,updated_at=?
		WHERE status IN (?,?) AND EXISTS (
			SELECT 1 FROM turns WHERE turns.task_id=tasks.id AND turns.status=? AND turns.finished_at=?
		)`,
		domain.TaskWaitingUser, finishedAt, domain.TaskActive, domain.TaskPreparing, domain.TurnInterrupted, finishedAt,
	)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("recover tasks: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RecoveryResult{}, err
	}
	turns, _ := turnResult.RowsAffected()
	tasks, _ := taskResult.RowsAffected()
	return RecoveryResult{Turns: int(turns), Tasks: int(tasks)}, nil
}
