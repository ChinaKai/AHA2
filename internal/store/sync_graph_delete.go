package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var (
	ErrReadOnlySyncMirror      = errors.New("synchronized read-only mirror cannot delete owner data")
	ErrWorkspaceInUse          = errors.New("workspace is still referenced")
	ErrWorkspaceMirrorHasTasks = errors.New("remote workspace mirror still has tasks")
)

func ownedGraphObjectID(ownerDeviceID, sourceID string) string {
	return ownerDeviceID + ":" + sourceID
}

func enqueueOwnedGraphDeleteTx(ctx context.Context, tx *sql.Tx, objectType, sourceID, ownerDeviceID string, deletedAt time.Time) error {
	if ownerDeviceID == "" {
		return nil
	}
	if objectType != "task" && objectType != "workspace" {
		return fmt.Errorf("unsupported owned graph tombstone type %q", objectType)
	}
	wireID := ownedGraphObjectID(ownerDeviceID, sourceID)
	version := timeString(deletedAt)
	syncKey := fmt.Sprintf("graph-delete:v1:%s:%s:%d", objectType, wireID, deletedAt.UTC().UnixNano())
	return insertSyncTombstone(ctx, tx, domain.SyncTombstone{ObjectType: objectType, ObjectID: wireID, Version: version, SyncKey: syncKey, DeletedAt: deletedAt.UTC()})
}

func (s *Store) DeleteWorkspaceWithSyncTombstone(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ownerDeviceID string
	var readOnly bool
	if err := tx.QueryRowContext(ctx, `SELECT owner_device_id,read_only FROM workspaces WHERE id=?`, id).Scan(&ownerDeviceID, &readOnly); err != nil {
		return err
	}
	if readOnly {
		return ErrReadOnlySyncMirror
	}
	now := time.Now().UTC()
	if err := enqueueOwnedGraphDeleteTx(ctx, tx, "workspace", id, ownerDeviceID, now); err != nil {
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id=? AND read_only=0`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RetireRemoteWorkspaceMirror removes a read-only Workspace mirror without
// touching the source Workspace. Legacy mirrors may not retain their source ID;
// the local suppression row still prevents a later replay on this device.
func (s *Store) RetireRemoteWorkspaceMirror(ctx context.Context, id string) (domain.Workspace, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Workspace{}, false, err
	}
	defer tx.Rollback()
	item, err := scanWorkspace(tx.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces WHERE id=?`, id))
	if err != nil {
		return domain.Workspace{}, false, err
	}
	if !item.ReadOnly || item.OwnerDeviceID == "" {
		return domain.Workspace{}, false, ErrReadOnlySyncMirror
	}
	var hasTasks bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM sync_remote_task_objects
		WHERE owner_device_id=? AND object_type='task'
		  AND (json_extract(payload_json,'$.workspace_id')=? OR (?<>'' AND json_extract(payload_json,'$.workspace_id')=?)))`,
		item.OwnerDeviceID, item.ID, item.SourceWorkspaceID, item.SourceWorkspaceID).Scan(&hasTasks); err != nil {
		return domain.Workspace{}, false, err
	}
	if hasTasks {
		return domain.Workspace{}, false, ErrWorkspaceMirrorHasTasks
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO retired_workspace_mirrors(owner_device_id,local_workspace_id,source_workspace_id,retired_at)
		VALUES(?,?,?,?) ON CONFLICT(owner_device_id,local_workspace_id) DO UPDATE SET source_workspace_id=excluded.source_workspace_id,retired_at=excluded.retired_at`,
		item.OwnerDeviceID, item.ID, item.SourceWorkspaceID, timeString(now)); err != nil {
		return domain.Workspace{}, false, err
	}
	synchronized := item.SourceWorkspaceID != ""
	if synchronized {
		if err := enqueueOwnedGraphDeleteTx(ctx, tx, "workspace", item.SourceWorkspaceID, item.OwnerDeviceID, now); err != nil {
			return domain.Workspace{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET default_workspace_id='' WHERE default_workspace_id=?`, item.ID); err != nil {
		return domain.Workspace{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id=? AND owner_device_id=? AND read_only=1`, item.ID, item.OwnerDeviceID); err != nil {
		return domain.Workspace{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Workspace{}, false, err
	}
	return item, synchronized, nil
}

// RetireRemoteTaskMirror removes an explicitly selected read-only task mirror
// and creates a graph tombstone using the original owner/source identity. This
// is intentionally separate from normal task deletion: it is an escape hatch
// for orphaned mirrors whose source device has been retired or wiped.
func (s *Store) RetireRemoteTaskMirror(ctx context.Context, publicID string) (RemoteTaskMirror, error) {
	mirror, err := s.RemoteTaskDetailMirror(ctx, publicID)
	if err != nil {
		return RemoteTaskMirror{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RemoteTaskMirror{}, err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM sync_remote_task_objects
		WHERE owner_device_id=? AND object_type='task' AND task_id=?)`,
		mirror.Task.OwnerDeviceID, mirror.SourceTaskID).Scan(&exists); err != nil {
		return RemoteTaskMirror{}, err
	}
	if !exists {
		return RemoteTaskMirror{}, sql.ErrNoRows
	}
	now := time.Now().UTC()
	if err := enqueueOwnedGraphDeleteTx(ctx, tx, "task", mirror.SourceTaskID, mirror.Task.OwnerDeviceID, now); err != nil {
		return RemoteTaskMirror{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_remote_task_objects WHERE owner_device_id=? AND task_id=?`, mirror.Task.OwnerDeviceID, mirror.SourceTaskID); err != nil {
		return RemoteTaskMirror{}, err
	}
	if err := tx.Commit(); err != nil {
		return RemoteTaskMirror{}, err
	}
	return mirror, nil
}

func (s *Store) RemoteGraphTombstoned(ctx context.Context, objectType, wireID string) (bool, error) {
	var found bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sync_tombstones WHERE object_type=? AND object_id=?)`, objectType, wireID).Scan(&found)
	return found, err
}

func (s *Store) ApplyRemoteGraphDelete(ctx context.Context, objectType, ownerDeviceID, sourceID, localWorkspaceID, syncKey, version string, deletedAt time.Time) error {
	if ownerDeviceID == "" || sourceID == "" || (objectType != "task" && objectType != "workspace") {
		return fmt.Errorf("remote graph tombstone identity is invalid")
	}
	if syncKey == "" {
		syncKey = fmt.Sprintf("graph-delete:remote:%s:%s:%d", objectType, ownedGraphObjectID(ownerDeviceID, sourceID), deletedAt.UTC().UnixNano())
	}
	if version == "" {
		version = timeString(deletedAt)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	wireID := ownedGraphObjectID(ownerDeviceID, sourceID)
	if err := insertSyncTombstone(ctx, tx, domain.SyncTombstone{ObjectType: objectType, ObjectID: wireID, Version: version, SyncKey: syncKey, DeletedAt: deletedAt.UTC()}); err != nil {
		return err
	}
	if objectType == "task" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sync_remote_task_objects WHERE owner_device_id=? AND task_id=?`, ownerDeviceID, sourceID); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id=? AND owner_device_id=? AND read_only=1`, localWorkspaceID, ownerDeviceID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
