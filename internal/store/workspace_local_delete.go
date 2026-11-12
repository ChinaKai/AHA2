package store

import (
	"context"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// DeleteWorkspaceLocal removes a writable Workspace from this device without
// emitting a sync tombstone. It is used by factory-reset flows that must not
// change another device's copy of the same logical Workspace.
func (s *Store) DeleteWorkspaceLocal(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var readOnly bool
	if err := tx.QueryRowContext(ctx, `SELECT read_only FROM workspaces WHERE id=?`, id).Scan(&readOnly); err != nil {
		return err
	}
	if readOnly {
		return ErrReadOnlySyncMirror
	}
	if err := purgeWorkspaceDeleteResidueTx(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM runtime_config_snapshots
		WHERE workspace_id=?
		  AND NOT EXISTS(SELECT 1 FROM tasks WHERE tasks.runtime_config_snapshot_id=runtime_config_snapshots.id)
		  AND NOT EXISTS(SELECT 1 FROM task_agents WHERE task_agents.runtime_config_snapshot_id=runtime_config_snapshots.id)
		  AND NOT EXISTS(SELECT 1 FROM turns WHERE turns.runtime_config_snapshot_id=runtime_config_snapshots.id)`, id); err != nil {
		return err
	}
	var inUse bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM tasks WHERE workspace_id=?
		UNION ALL SELECT 1 FROM backend_sessions WHERE workspace_id=?
		UNION ALL SELECT 1 FROM runtime_config_snapshots WHERE workspace_id=?)`, id, id, id).Scan(&inUse); err != nil {
		return err
	}
	if inUse {
		return ErrWorkspaceInUse
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET default_workspace_id='' WHERE default_workspace_id=?`, id); err != nil {
		return err
	}
	if _, err := s.DeleteMailBindingsForScopeInTx(ctx, tx, domain.MailBindingScopeWorkspace, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id=? AND read_only=0`, id); err != nil {
		return err
	}
	return tx.Commit()
}
