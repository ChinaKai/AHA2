package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var (
	ErrReadOnlySyncMirror      = errors.New("synchronized read-only mirror cannot delete owner data")
	ErrWorkspaceInUse          = errors.New("workspace is still referenced")
	ErrWorkspaceHasActiveTurn  = errors.New("workspace still has an active turn")
	ErrWorkspaceMirrorHasTasks = errors.New("remote workspace mirror still has tasks")
	ErrProjectHasActiveTurn    = errors.New("project still has an active turn")
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

// purgeWorkspaceDeleteResidueTx removes rows that still block deleting a
// Workspace even though no Turn in it is executing. Backend Sessions retain the
// Workspace they were opened in after their Task moves elsewhere, and legacy
// rows can outlive a Task that was removed without cascading. Only rows owned by
// this Workspace, or orphaned rows that reference one of its snapshots, are
// removed: a live Task in another Workspace keeps its Turns and agents, so the
// following existence check still refuses the delete instead of widening it.
func purgeWorkspaceDeleteResidueTx(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM backend_sessions WHERE workspace_id=?`, workspaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM turns
		WHERE task_id IN (SELECT id FROM tasks WHERE workspace_id=?)
		   OR (NOT EXISTS(SELECT 1 FROM tasks WHERE tasks.id=turns.task_id)
		       AND runtime_config_snapshot_id IN (SELECT id FROM runtime_config_snapshots WHERE workspace_id=?))`,
		workspaceID, workspaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_agents
		WHERE task_id IN (SELECT id FROM tasks WHERE workspace_id=?)
		   OR (NOT EXISTS(SELECT 1 FROM tasks WHERE tasks.id=task_agents.task_id)
		       AND runtime_config_snapshot_id IN (SELECT id FROM runtime_config_snapshots WHERE workspace_id=?))`,
		workspaceID, workspaceID); err != nil {
		return err
	}
	return nil
}

// DeleteWorkspaceWithSyncTombstone deletes a writable Workspace together with
// its Tasks, their sync tombstones and the residual references that would
// otherwise block it, all in one transaction. Refusing on an active Turn or on
// a reference owned by another Workspace rolls the whole change back, so the
// Owner never gets a half-deleted Workspace with Tasks already gone.
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
	// Checked in the same transaction as the delete so a Turn cannot start
	// between the check and the row removal.
	var activeTurn bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM turns JOIN tasks ON tasks.id=turns.task_id
		WHERE tasks.workspace_id=? AND turns.status IN ('queued','preparing','starting','running','waiting'))`, id).Scan(&activeTurn); err != nil {
		return err
	}
	if activeTurn {
		return ErrWorkspaceHasActiveTurn
	}
	now := time.Now().UTC()
	if err := enqueueOwnedGraphDeleteTx(ctx, tx, "workspace", id, ownerDeviceID, now); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE workspace_id=? ORDER BY created_at,id`, id)
	if err != nil {
		return err
	}
	var taskIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			rows.Close()
			return err
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	var attachmentHashes []string
	for _, taskID := range taskIDs {
		hashes, err := s.deleteTaskTx(ctx, tx, taskID, true)
		if err != nil {
			return err
		}
		attachmentHashes = append(attachmentHashes, hashes...)
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
	if err := tx.Commit(); err != nil {
		return err
	}
	s.removeOrphanedAttachmentBlobs(ctx, attachmentHashes)
	return nil
}

// DeleteProjectWithSyncTombstone removes a Project together with its Tasks, its
// Workspaces and its knowledge-library bindings in one transaction. The pet's
// project deletion used to run one transaction per object, so a workspace that
// failed late left the Project half-deleted. Keeping every step in one
// transaction makes a refusal roll the whole graph back.
func (s *Store) DeleteProjectWithSyncTombstone(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var updatedAt string
	if err := tx.QueryRowContext(ctx, `SELECT updated_at FROM projects WHERE id=?`, id).Scan(&updatedAt); err != nil {
		return err
	}
	// Checked inside the transaction so a Turn cannot start between the check
	// and the deletes.
	var activeTurn bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM turns JOIN tasks ON tasks.id=turns.task_id
		WHERE tasks.project_id=? AND turns.status IN ('queued','preparing','starting','running','waiting'))`, id).Scan(&activeTurn); err != nil {
		return err
	}
	if activeTurn {
		return ErrProjectHasActiveTurn
	}
	now := time.Now().UTC()
	rows, err := tx.QueryContext(ctx, `SELECT id,runtime_config_snapshot_id FROM tasks WHERE project_id=? ORDER BY created_at,id`, id)
	if err != nil {
		return err
	}
	type projectTask struct{ id, snapshotID string }
	var tasks []projectTask
	for rows.Next() {
		var task projectTask
		if err := rows.Scan(&task.id, &task.snapshotID); err != nil {
			rows.Close()
			return err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	var attachmentHashes []string
	for _, task := range tasks {
		hashes, err := s.deleteTaskTx(ctx, tx, task.id, true)
		if err != nil {
			return err
		}
		attachmentHashes = append(attachmentHashes, hashes...)
		// The task's snapshot may be shared with another Project; only drop it
		// when nothing references it anymore, matching the old per-task delete.
		if task.snapshotID != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM runtime_config_snapshots
				WHERE id=?
				  AND NOT EXISTS(SELECT 1 FROM tasks WHERE tasks.runtime_config_snapshot_id=runtime_config_snapshots.id)
				  AND NOT EXISTS(SELECT 1 FROM task_agents WHERE task_agents.runtime_config_snapshot_id=runtime_config_snapshots.id)
				  AND NOT EXISTS(SELECT 1 FROM turns WHERE turns.runtime_config_snapshot_id=runtime_config_snapshots.id)`,
				task.snapshotID); err != nil {
				return err
			}
		}
	}
	workspaceRows, err := tx.QueryContext(ctx, `SELECT id,owner_device_id,read_only FROM workspaces WHERE project_id=? ORDER BY id`, id)
	if err != nil {
		return err
	}
	type projectWorkspace struct {
		id            string
		ownerDeviceID string
		readOnly      bool
	}
	var workspaces []projectWorkspace
	for workspaceRows.Next() {
		var workspace projectWorkspace
		if err := workspaceRows.Scan(&workspace.id, &workspace.ownerDeviceID, &workspace.readOnly); err != nil {
			workspaceRows.Close()
			return err
		}
		workspaces = append(workspaces, workspace)
	}
	if err := workspaceRows.Err(); err != nil {
		workspaceRows.Close()
		return err
	}
	workspaceRows.Close()
	for _, workspace := range workspaces {
		if !workspace.readOnly {
			if err := enqueueOwnedGraphDeleteTx(ctx, tx, "workspace", workspace.id, workspace.ownerDeviceID, now); err != nil {
				return err
			}
			if err := purgeWorkspaceDeleteResidueTx(ctx, tx, workspace.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM runtime_config_snapshots
				WHERE workspace_id=?
				  AND NOT EXISTS(SELECT 1 FROM tasks WHERE tasks.runtime_config_snapshot_id=runtime_config_snapshots.id)
				  AND NOT EXISTS(SELECT 1 FROM task_agents WHERE task_agents.runtime_config_snapshot_id=runtime_config_snapshots.id)
				  AND NOT EXISTS(SELECT 1 FROM turns WHERE turns.runtime_config_snapshot_id=runtime_config_snapshots.id)`, workspace.id); err != nil {
				return err
			}
			var inUse bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
				SELECT 1 FROM tasks WHERE workspace_id=?
				UNION ALL SELECT 1 FROM backend_sessions WHERE workspace_id=?
				UNION ALL SELECT 1 FROM runtime_config_snapshots WHERE workspace_id=?)`,
				workspace.id, workspace.id, workspace.id).Scan(&inUse); err != nil {
				return err
			}
			if inUse {
				return ErrWorkspaceInUse
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET default_workspace_id='' WHERE default_workspace_id=?`, workspace.id); err != nil {
			return err
		}
		if _, err := s.DeleteMailBindingsForScopeInTx(ctx, tx, domain.MailBindingScopeWorkspace, workspace.id); err != nil {
			return err
		}
		if workspace.readOnly {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id=?`, workspace.id); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id=? AND read_only=0`, workspace.id); err != nil {
			return err
		}
	}
	bindingRows, err := tx.QueryContext(ctx, `SELECT library_id,updated_at FROM project_knowledge_bindings WHERE project_id=? ORDER BY library_id`, id)
	if err != nil {
		return err
	}
	type projectBinding struct{ libraryID, updatedAt string }
	var bindings []projectBinding
	for bindingRows.Next() {
		var binding projectBinding
		if err := bindingRows.Scan(&binding.libraryID, &binding.updatedAt); err != nil {
			bindingRows.Close()
			return err
		}
		bindings = append(bindings, binding)
	}
	if err := bindingRows.Err(); err != nil {
		bindingRows.Close()
		return err
	}
	bindingRows.Close()
	for _, binding := range bindings {
		affected, err := deleteSharedRow(ctx, tx, "knowledge_binding", binding.libraryID, now)
		if err != nil {
			return err
		}
		if affected != 1 {
			return sql.ErrNoRows
		}
		version := binding.updatedAt
		if version == "" {
			version = timeString(now)
		}
		if err := insertSyncTombstone(ctx, tx, domain.SyncTombstone{
			ObjectType: "knowledge_binding", ObjectID: binding.libraryID, Version: version,
			SyncKey:   fmt.Sprintf("tombstone:v1:knowledge_binding:%s:%d", binding.libraryID, now.UTC().UnixNano()),
			DeletedAt: now,
		}); err != nil {
			return err
		}
	}
	productLines, err := projectProductLines(ctx, tx, id)
	if err != nil {
		return err
	}
	projectTombstone := domain.SyncTombstone{
		ObjectType: "project", ObjectID: id, Version: updatedAt,
		SyncKey: fmt.Sprintf("tombstone:v1:project:%s:%d", id, now.UTC().UnixNano()), DeletedAt: now,
	}
	affected, err := deleteSharedRow(ctx, tx, "project", id, now)
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	if err := insertSyncTombstone(ctx, tx, projectTombstone); err != nil {
		return err
	}
	if err := insertProjectProductLineTombstones(ctx, tx, productLines, projectTombstone.SyncKey, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.removeOrphanedAttachmentBlobs(ctx, attachmentHashes)
	return nil
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
	if _, err := s.DeleteMailBindingsForScopeInTx(ctx, tx, domain.MailBindingScopeWorkspace, item.ID); err != nil {
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
		// Legacy tombstones may not carry a local workspace ID; only clean up
		// bindings when there is a local scope to match.
		if strings.TrimSpace(localWorkspaceID) != "" {
			if _, err := s.DeleteMailBindingsForScopeInTx(ctx, tx, domain.MailBindingScopeWorkspace, localWorkspaceID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id=? AND owner_device_id=? AND read_only=1`, localWorkspaceID, ownerDeviceID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
