package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var ErrReadOnlySyncMirror = errors.New("synchronized read-only mirror cannot delete owner data")

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
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id=? AND read_only=0`, id); err != nil {
		return err
	}
	return tx.Commit()
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
