package store

import (
	"context"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type RecoveryResult struct {
	Turns              int `json:"turns"`
	Tasks              int `json:"tasks"`
	RequeuedInboxItems int `json:"requeued_inbox_items"`
	PetApprovals       int `json:"pet_approvals"`
}

// PetApprovalRestartInterrupted is written to an approval whose background
// execution was cut off by a service restart. The delete transaction rolled
// back with the process, so the row only needs to become retryable again.
const PetApprovalRestartInterrupted = "服务重启导致执行中断，请重试"

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
	requeueResult, err := tx.ExecContext(ctx, `
		UPDATE agent_inbox
		SET status='pending',batch_id='',claimed_at='',recovery_attempts=recovery_attempts+1
		WHERE status='claimed'
		  AND batch_id IN (
			SELECT interrupted.inbox_batch_id
			FROM turns interrupted
			WHERE interrupted.status=? AND interrupted.finished_at=? AND interrupted.inbox_batch_id<>''
		)`,
		domain.TurnInterrupted, finishedAt,
	)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("recover inbox: %w", err)
	}
	liveRequeueResult, err := tx.ExecContext(ctx, `
		UPDATE agent_inbox
		SET status='pending',batch_id='',claimed_at='',delivered_turn_id='',delivered_at='',
		    recovery_attempts=recovery_attempts+1
		WHERE status IN ('claimed_in_turn','delivered_in_turn')
		  AND delivered_turn_id IN (
			SELECT interrupted.id
			FROM turns interrupted
			WHERE interrupted.status=? AND interrupted.finished_at=?
		  )`,
		domain.TurnInterrupted, finishedAt,
	)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("recover live inbox: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE task_agents
		SET status='idle',updated_at=?
		WHERE status IN ('queued','preparing','starting','running','waiting')`,
		finishedAt,
	); err != nil {
		return RecoveryResult{}, fmt.Errorf("recover agents: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE task_rounds
		SET status=?,finished_at=?
		WHERE status IN ('running','waiting')
		  AND NOT EXISTS (
			SELECT 1 FROM agent_inbox
			WHERE agent_inbox.round_id=task_rounds.id AND agent_inbox.status='pending'
		  )`,
		domain.RoundInterrupted, finishedAt,
	); err != nil {
		return RecoveryResult{}, fmt.Errorf("recover rounds: %w", err)
	}
	// A heavy approval executes in one transaction in a background goroutine.
	// A restart kills that goroutine before it can finalize, so the transaction
	// is rolled back and the approval may be retried by the owner.
	approvalResult, err := tx.ExecContext(ctx, `
		UPDATE pet_approvals SET status='pending',resolution=?,decision_reason=?,resolved_at=''
		WHERE status='executing'`,
		PetApprovalRestartInterrupted, PetApprovalRestartInterrupted,
	)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("recover pet approvals: %w", err)
	}
	taskResult, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status=?,updated_at=?
		WHERE status IN (?,?) AND EXISTS (
			SELECT 1 FROM turns WHERE turns.task_id=tasks.id AND turns.status=? AND turns.finished_at=?
		) AND NOT EXISTS (
			SELECT 1 FROM agent_inbox WHERE agent_inbox.task_id=tasks.id AND agent_inbox.status='pending'
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
	requeued, _ := requeueResult.RowsAffected()
	liveRequeued, _ := liveRequeueResult.RowsAffected()
	approvals, _ := approvalResult.RowsAffected()
	return RecoveryResult{
		Turns: int(turns), Tasks: int(tasks),
		RequeuedInboxItems: int(requeued + liveRequeued),
		PetApprovals:       int(approvals),
	}, nil
}
