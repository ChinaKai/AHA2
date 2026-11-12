package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestOwnedGraphDeletesCreateTombstonesAndBlockReplay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	localTask := createStoreTestTask(t, database)
	if err := database.ClaimLocalWorkspaces(ctx, "device-owner"); err != nil {
		t.Fatal(err)
	}
	localWorkspace, err := database.Workspace(ctx, localTask.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	extraSnapshot, err := database.RuntimeSnapshot(ctx, localTask.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	extraSnapshot.ID = "orphan-runtime-snapshot"
	extraSnapshot.CreatedAt = time.Now().UTC()
	if err := database.CreateRuntimeSnapshot(ctx, extraSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteTaskWithSyncTombstone(ctx, localTask.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Task(ctx, localTask.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("owned task was not deleted: %v", err)
	}
	taskWireID := ownedGraphObjectID("device-owner", localTask.ID)
	if tombstone, err := database.SyncTombstone(ctx, "task", taskWireID); err != nil || tombstone.SyncKey == "" {
		t.Fatalf("task tombstone=%#v err=%v", tombstone, err)
	}
	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, localWorkspace.ID); err != nil {
		t.Fatal(err)
	}
	var snapshotCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_config_snapshots WHERE workspace_id=?`, localWorkspace.ID).Scan(&snapshotCount); err != nil || snapshotCount != 0 {
		t.Fatalf("workspace snapshots=%d err=%v", snapshotCount, err)
	}
	workspaceWireID := ownedGraphObjectID("device-owner", localWorkspace.ID)
	if tombstone, err := database.SyncTombstone(ctx, "workspace", workspaceWireID); err != nil || tombstone.SyncKey == "" {
		t.Fatalf("workspace tombstone=%#v err=%v", tombstone, err)
	}

	project := domain.Project{ID: "remote-delete-project", Name: "Remote", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	remoteWorkspace := domain.Workspace{ID: "sync-workspace", ProjectID: project.ID, Name: "Remote", OwnerDeviceID: "device-remote", ReadOnly: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := database.UpsertSyncedWorkspaceFromSource(ctx, remoteWorkspace, "source-workspace"); err != nil {
		t.Fatal(err)
	}
	remoteTask := domain.Task{ID: "source-task", ProjectID: project.ID, WorkspaceID: "source-workspace", Title: "Remote", Status: domain.TaskWaitingUser, ReadOnly: true, OwnerDeviceID: "device-remote"}
	payload, _ := json.Marshal(remoteTask)
	for _, item := range []RemoteTaskObject{
		{OwnerDeviceID: "device-remote", ObjectType: "task", ObjectID: remoteTask.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: payload},
		{OwnerDeviceID: "device-remote", ObjectType: "conversation", ObjectID: "conversation-one", TaskID: remoteTask.ID, ProjectID: project.ID, Payload: json.RawMessage(`{}`)},
	} {
		if err := database.UpsertRemoteTaskObject(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	protectedLocalTask := createStoreTestTask(t, database)
	if err := database.ApplyRemoteGraphDelete(ctx, "task", "device-remote", protectedLocalTask.ID, "", "remote-cannot-delete-local", "2026-09-06T00:00:00Z", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Task(ctx, protectedLocalTask.ID); err != nil {
		t.Fatalf("remote tombstone deleted owner task: %v", err)
	}
	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, remoteWorkspace.ID); !errors.Is(err, ErrReadOnlySyncMirror) {
		t.Fatalf("read-only remote workspace deletion error=%v", err)
	}
	if _, err := database.Workspace(ctx, remoteWorkspace.ID); err != nil {
		t.Fatalf("read-only workspace was deleted: %v", err)
	}
	if err := database.ApplyRemoteGraphDelete(ctx, "task", "device-remote", remoteTask.ID, "", "remote-task-delete", "2026-09-06T00:00:00Z", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if objects, err := database.RemoteTaskObjects(ctx, "device-remote", remoteTask.ID); err != nil || len(objects) != 0 {
		t.Fatalf("remote task mirror survived delete: %#v err=%v", objects, err)
	}
	if err := database.UpsertRemoteTaskObject(ctx, RemoteTaskObject{OwnerDeviceID: "device-remote", ObjectType: "task", ObjectID: remoteTask.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRemoteTaskObject(ctx, RemoteTaskObject{OwnerDeviceID: "device-remote", ObjectType: "conversation", ObjectID: "late-conversation", TaskID: remoteTask.ID, ProjectID: project.ID, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if found, err := database.RemoteTaskObjectExists(ctx, "device-remote", "task", remoteTask.ID); err != nil || found {
		t.Fatalf("old task replay resurrected mirror: found=%t err=%v", found, err)
	}
	if objects, err := database.RemoteTaskObjects(ctx, "device-remote", remoteTask.ID); err != nil || len(objects) != 0 {
		t.Fatalf("old child replay resurrected task graph: %#v err=%v", objects, err)
	}
	if err := database.ApplyRemoteGraphDelete(ctx, "workspace", "device-remote", "source-workspace", remoteWorkspace.ID, "remote-workspace-delete", "2026-09-06T00:00:01Z", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Workspace(ctx, remoteWorkspace.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("remote workspace mirror survived delete: %v", err)
	}
	if err := database.UpsertSyncedWorkspaceFromSource(ctx, remoteWorkspace, "source-workspace"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Workspace(ctx, remoteWorkspace.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old workspace replay resurrected mirror: %v", err)
	}
}

func TestRetireRemoteWorkspaceMirrorBlocksReplay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "retire-workspace-project", Name: "Retire", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	remote := domain.Workspace{ID: "remote-workspace", ProjectID: project.ID, Name: "Remote", OwnerDeviceID: "retired-device", ReadOnly: true, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertSyncedWorkspaceFromSource(ctx, remote, "source-workspace"); err != nil {
		t.Fatal(err)
	}
	stored, err := database.Workspace(ctx, remote.ID)
	if err != nil || stored.SourceWorkspaceID != "source-workspace" {
		t.Fatalf("stored workspace=%#v err=%v", stored, err)
	}
	retired, synchronized, err := database.RetireRemoteWorkspaceMirror(ctx, remote.ID)
	if err != nil || !synchronized || retired.OwnerDeviceID != "retired-device" {
		t.Fatalf("retired=%#v synchronized=%t err=%v", retired, synchronized, err)
	}
	if _, err := database.Workspace(ctx, remote.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("workspace mirror survived retirement: %v", err)
	}
	if _, err := database.SyncTombstone(ctx, "workspace", ownedGraphObjectID("retired-device", "source-workspace")); err != nil {
		t.Fatalf("workspace graph tombstone missing: %v", err)
	}
	if err := database.UpsertSyncedWorkspaceFromSource(ctx, remote, "source-workspace"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Workspace(ctx, remote.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("workspace replay bypassed retirement: %v", err)
	}

	legacy := domain.Workspace{ID: "legacy-remote-workspace", ProjectID: project.ID, Name: "Legacy", OwnerDeviceID: "legacy-device", ReadOnly: true, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertSyncedWorkspaceFromSource(ctx, legacy, "legacy-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE workspaces SET source_workspace_id='' WHERE id=?`, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if _, synchronized, err := database.RetireRemoteWorkspaceMirror(ctx, legacy.ID); err != nil || synchronized {
		t.Fatalf("legacy synchronized=%t err=%v", synchronized, err)
	}
	if err := database.UpsertSyncedWorkspaceFromSource(ctx, legacy, "legacy-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Workspace(ctx, legacy.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy workspace replay bypassed local suppression: %v", err)
	}

	busy := domain.Workspace{ID: "busy-remote-workspace", ProjectID: project.ID, Name: "Busy", OwnerDeviceID: "busy-device", ReadOnly: true, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertSyncedWorkspaceFromSource(ctx, busy, "busy-source"); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "busy-task", ProjectID: project.ID, WorkspaceID: busy.ID, Title: "Busy", OwnerDeviceID: busy.OwnerDeviceID, ReadOnly: true}
	payload, _ := json.Marshal(task)
	if err := database.UpsertRemoteTaskObject(ctx, RemoteTaskObject{OwnerDeviceID: busy.OwnerDeviceID, ObjectType: "task", ObjectID: task.ID, TaskID: task.ID, ProjectID: project.ID, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.RetireRemoteWorkspaceMirror(ctx, busy.ID); !errors.Is(err, ErrWorkspaceMirrorHasTasks) {
		t.Fatalf("busy workspace retirement error=%v", err)
	}
	if _, err := database.Workspace(ctx, busy.ID); err != nil {
		t.Fatalf("busy workspace was removed: %v", err)
	}
}

func TestRetireRemoteTaskMirrorCreatesOwnerGraphTombstone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "retire-project", Name: "Retire", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "orphan-task", ProjectID: project.ID, WorkspaceID: "orphan-workspace", Title: "Orphan", Status: domain.TaskWaitingUser, OwnerDeviceID: "retired-device", ReadOnly: true, CreatedAt: now, UpdatedAt: now}
	taskPayload, _ := json.Marshal(task)
	for _, item := range []RemoteTaskObject{
		{OwnerDeviceID: "retired-device", ObjectType: "task", ObjectID: task.ID, TaskID: task.ID, ProjectID: project.ID, Payload: taskPayload},
		{OwnerDeviceID: "retired-device", ObjectType: "conversation", ObjectID: "message-one", TaskID: task.ID, ProjectID: project.ID, Payload: json.RawMessage(`{"kind":"message"}`)},
	} {
		if err := database.UpsertRemoteTaskObject(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	publicID := remoteTaskPublicID("retired-device", task.ID)
	retired, err := database.RetireRemoteTaskMirror(ctx, publicID)
	if err != nil {
		t.Fatal(err)
	}
	if retired.SourceTaskID != task.ID || retired.Task.OwnerDeviceID != "retired-device" {
		t.Fatalf("retired identity=%#v", retired)
	}
	if objects, err := database.RemoteTaskObjects(ctx, "retired-device", task.ID); err != nil || len(objects) != 0 {
		t.Fatalf("retired mirror objects=%#v err=%v", objects, err)
	}
	wireID := ownedGraphObjectID("retired-device", task.ID)
	if tombstone, err := database.SyncTombstone(ctx, "task", wireID); err != nil || tombstone.SyncKey == "" {
		t.Fatalf("retired mirror tombstone=%#v err=%v", tombstone, err)
	}
	if _, err := database.RetireRemoteTaskMirror(ctx, publicID); err == nil {
		t.Fatal("retiring an absent mirror unexpectedly succeeded")
	}
}

// A Workspace whose Tasks are gone but whose Backend Sessions, Turns, agents
// and runtime snapshots still reference it must still be deletable. The rows
// come from a legacy schema that removed Tasks without cascading; a live Task
// in another Workspace must keep its own rows.
func TestDeleteWorkspaceWithSyncTombstoneClearsResidualReferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	task := createStoreTestTask(t, database)
	otherTask := createStoreTestTask(t, database)
	if err := database.ClaimLocalWorkspaces(ctx, "device-owner"); err != nil {
		t.Fatal(err)
	}
	workspace, err := database.Workspace(ctx, task.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.RuntimeSnapshot(ctx, task.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	residualSnapshot := snapshot
	residualSnapshot.ID = "residual-runtime-snapshot"
	residualSnapshot.CreatedAt = now
	if err := database.CreateRuntimeSnapshot(ctx, residualSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertBackendSession(ctx, domain.BackendSession{
		ID: "residual-backend-session", TaskID: task.ID, AgentID: "main", WorkspaceID: workspace.ID,
		Backend: "stub", ModelID: snapshot.ModelID, EnvGroupRevision: snapshot.EnvGroupRevision,
		Status: "active", CreatedAt: now, LastUsedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO task_agents(task_id,agent_id,role,status,title,runtime_config_snapshot_id,inherit_main,last_read_sequence,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		task.ID, "residual-agent", "helper", "idle", "Residual", residualSnapshot.ID, 0, 0, timeString(now), timeString(now)); err != nil {
		t.Fatal(err)
	}
	message := domain.Message{ID: "residual-message", TaskID: task.ID, Role: "user", Sender: "owner", Content: "hello", CreatedAt: now}
	if err := database.AddMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateTurn(ctx, domain.Turn{
		ID: "residual-turn", TaskID: task.ID, AgentID: "residual-agent", Sequence: 1,
		InputMessageID: message.ID, Status: domain.TurnSucceeded,
		BackendSessionID:        "residual-backend-session",
		RuntimeConfigSnapshotID: residualSnapshot.ID, QueuedAt: now, FinishedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	otherSnapshot, err := database.RuntimeSnapshot(ctx, otherTask.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertBackendSession(ctx, domain.BackendSession{
		ID: "other-backend-session", TaskID: otherTask.ID, AgentID: "main", WorkspaceID: otherTask.WorkspaceID,
		Backend: "stub", ModelID: otherSnapshot.ModelID, EnvGroupRevision: otherSnapshot.EnvGroupRevision,
		Status: "active", CreatedAt: now, LastUsedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Drop only the Task row, leaving its dependents behind like a database
	// migrated from a schema without cascading foreign keys.
	conn, err := database.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM tasks WHERE id=?`, task.ID); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, workspace.ID); err != nil {
		t.Fatalf("delete workspace with residual references: %v", err)
	}
	if _, err := database.Workspace(ctx, workspace.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("workspace survived delete: %v", err)
	}
	for _, check := range []struct {
		name  string
		query string
	}{
		{"backend sessions", `SELECT COUNT(*) FROM backend_sessions WHERE workspace_id='` + workspace.ID + `'`},
		{"runtime snapshots", `SELECT COUNT(*) FROM runtime_config_snapshots WHERE workspace_id='` + workspace.ID + `'`},
		{"turns", `SELECT COUNT(*) FROM turns WHERE task_id='` + task.ID + `'`},
		{"task agents", `SELECT COUNT(*) FROM task_agents WHERE task_id='` + task.ID + `'`},
	} {
		var count int
		if err := database.db.QueryRowContext(ctx, check.query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s left behind: count=%d err=%v", check.name, count, err)
		}
	}
	if tombstone, err := database.SyncTombstone(ctx, "workspace", ownedGraphObjectID("device-owner", workspace.ID)); err != nil || tombstone.SyncKey == "" {
		t.Fatalf("workspace tombstone=%#v err=%v", tombstone, err)
	}

	if _, err := database.Workspace(ctx, otherTask.WorkspaceID); err != nil {
		t.Fatalf("other workspace was deleted: %v", err)
	}
	if _, err := database.Task(ctx, otherTask.ID); err != nil {
		t.Fatalf("other task was deleted: %v", err)
	}
	var otherSessions, otherSnapshots int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backend_sessions WHERE workspace_id=?`, otherTask.WorkspaceID).Scan(&otherSessions); err != nil || otherSessions != 1 {
		t.Fatalf("other workspace sessions=%d err=%v", otherSessions, err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_config_snapshots WHERE workspace_id=?`, otherTask.WorkspaceID).Scan(&otherSnapshots); err != nil || otherSnapshots != 1 {
		t.Fatalf("other workspace snapshots=%d err=%v", otherSnapshots, err)
	}
}

// A reference held by a live Task in another Workspace must refuse the delete
// and roll back the Tasks that were about to be removed with it.
func TestDeleteWorkspaceWithSyncTombstoneRollsBackSharedSnapshotReference(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	target := createStoreTestTask(t, database)
	sharing := createStoreTestTask(t, database)
	if err := database.ClaimLocalWorkspaces(ctx, "device-owner"); err != nil {
		t.Fatal(err)
	}
	targetWorkspace, err := database.Workspace(ctx, target.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	targetSnapshot, err := database.RuntimeSnapshot(ctx, target.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE tasks SET runtime_config_snapshot_id=? WHERE id=?`, targetSnapshot.ID, sharing.ID); err != nil {
		t.Fatal(err)
	}

	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, targetWorkspace.ID); !errors.Is(err, ErrWorkspaceInUse) {
		t.Fatalf("delete with a shared snapshot error=%v, want ErrWorkspaceInUse", err)
	}
	if _, err := database.Task(ctx, target.ID); err != nil {
		t.Fatalf("half-deleted: target task is gone: %v", err)
	}
	if _, err := database.Workspace(ctx, targetWorkspace.ID); err != nil {
		t.Fatalf("target workspace is gone: %v", err)
	}
	if _, err := database.Task(ctx, sharing.ID); err != nil {
		t.Fatalf("sharing task is gone: %v", err)
	}
	if _, err := database.RuntimeSnapshot(ctx, targetSnapshot.ID); err != nil {
		t.Fatalf("shared snapshot is gone: %v", err)
	}
	for _, wire := range []struct{ objectType, id string }{
		{"task", target.ID},
		{"workspace", targetWorkspace.ID},
	} {
		if _, err := database.SyncTombstone(ctx, wire.objectType, ownedGraphObjectID("device-owner", wire.id)); err == nil {
			t.Fatalf("rolled-back %s tombstone survived", wire.objectType)
		}
	}
}

func TestDeleteWorkspaceWithSyncTombstoneRefusesActiveTurnAndKeepsRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	task := createStoreTestTask(t, database)
	if err := database.ClaimLocalWorkspaces(ctx, "device-owner"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	message := domain.Message{ID: "active-message", TaskID: task.ID, Role: "user", Sender: "owner", Content: "run", CreatedAt: now}
	if err := database.AddMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateTurn(ctx, domain.Turn{
		ID: "active-turn", TaskID: task.ID, AgentID: "main", Sequence: 1,
		InputMessageID: message.ID, Status: domain.TurnRunning,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, QueuedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, task.WorkspaceID); !errors.Is(err, ErrWorkspaceHasActiveTurn) {
		t.Fatalf("delete with an active turn error=%v, want ErrWorkspaceHasActiveTurn", err)
	}
	if _, err := database.Task(ctx, task.ID); err != nil {
		t.Fatalf("task was deleted despite the active turn: %v", err)
	}
	if _, err := database.Workspace(ctx, task.WorkspaceID); err != nil {
		t.Fatalf("workspace was deleted despite the active turn: %v", err)
	}
}

func TestDeleteWorkspaceWithSyncTombstoneDeletesTasksAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	first := createStoreTestTask(t, database)
	second := createStoreTestTask(t, database)
	if _, err := database.db.ExecContext(ctx, `UPDATE tasks SET workspace_id=?, runtime_config_snapshot_id=? WHERE id=?`,
		first.WorkspaceID, first.RuntimeConfigSnapshotID, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.ClaimLocalWorkspaces(ctx, "device-owner"); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, first.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	for _, taskID := range []string{first.ID, second.ID} {
		if _, err := database.Task(ctx, taskID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("task %s survived: %v", taskID, err)
		}
		if _, err := database.SyncTombstone(ctx, "task", ownedGraphObjectID("device-owner", taskID)); err != nil {
			t.Fatalf("task %s tombstone missing: %v", taskID, err)
		}
	}
	if _, err := database.Workspace(ctx, first.WorkspaceID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("workspace survived: %v", err)
	}
	if _, err := database.SyncTombstone(ctx, "workspace", ownedGraphObjectID("device-owner", first.WorkspaceID)); err != nil {
		t.Fatalf("workspace tombstone missing: %v", err)
	}
}
