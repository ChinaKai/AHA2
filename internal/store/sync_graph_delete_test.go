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
	if err := database.DeleteRuntimeSnapshot(ctx, localTask.RuntimeConfigSnapshotID); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteWorkspaceWithSyncTombstone(ctx, localWorkspace.ID); err != nil {
		t.Fatal(err)
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
