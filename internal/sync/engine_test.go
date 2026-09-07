package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type fakeRemote struct {
	pull   PullResponse
	pushed PushRequest
}

type pagedRemote struct {
	responses map[string]PullResponse
	cursors   []string
}

func (f *pagedRemote) Push(_ context.Context, request PushRequest) (PushResponse, error) {
	keys := make([]string, len(request.Objects))
	for index, object := range request.Objects {
		keys[index] = object.IdempotencyKey
	}
	return PushResponse{AckedKeys: keys}, nil
}

func (f *pagedRemote) Pull(_ context.Context, _ string, cursor, _ string, _ int) (PullResponse, error) {
	f.cursors = append(f.cursors, cursor)
	return f.responses[cursor], nil
}

func TestEngineRecordsCenterConflictOnceWithoutSuppressingLocalExport(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	remoteObject := domain.SyncObject{
		Type: "note", ID: "conflicted", Operation: "upsert", Payload: json.RawMessage(`{"remote":true}`),
		IdempotencyKey: "note:conflicted:remote", EventID: "center:conflicted", RemoteVersion: "2",
	}
	remote := &fakeRemote{pull: PullResponse{Cursor: "2", Objects: []domain.SyncObject{remoteObject}}}
	engine := Engine{Store: database, Remote: remote, Scope: "default", DeviceID: "device"}
	engine.Register("note", func(context.Context, domain.SyncObject) error {
		return &ConflictError{LocalPayload: json.RawMessage(`{"local":true}`), LocalVersion: "1", Cause: fmt.Errorf("%w: test", ErrConflict)}
	})
	if err := engine.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	conflicts, err := database.SyncConflicts(ctx, "default")
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("conflicts=%#v err=%v", conflicts, err)
	}
	deliveryApplied, _ := database.SyncWasApplied(ctx, remoteObject.EventID)
	businessApplied, _ := database.SyncWasApplied(ctx, remoteObject.IdempotencyKey)
	if !deliveryApplied || businessApplied {
		t.Fatalf("delivery applied=%t business applied=%t", deliveryApplied, businessApplied)
	}
}

func (f *fakeRemote) Push(_ context.Context, r PushRequest) (PushResponse, error) {
	f.pushed = r
	keys := make([]string, len(r.Objects))
	for i, o := range r.Objects {
		keys[i] = o.IdempotencyKey
	}
	return PushResponse{AckedKeys: keys}, nil
}
func (f *fakeRemote) Pull(context.Context, string, string, string, int) (PullResponse, error) {
	return f.pull, nil
}

func TestEnginePushPullAndIdempotency(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)
	if err := database.EnqueueSync(ctx, domain.SyncOutboxItem{ID: "out", Scope: "default", Object: domain.SyncObject{Type: "note", ID: "local", Operation: "upsert", Payload: json.RawMessage(`{}`), IdempotencyKey: "local-key"}}); err != nil {
		t.Fatal(err)
	}
	remoteObject := domain.SyncObject{Type: "note", ID: "remote", Operation: "upsert", Payload: json.RawMessage(`{"x":1}`), IdempotencyKey: "remote-key", EventID: "center:event-2", RemoteVersion: "2"}
	if err := database.MarkSyncApplied(ctx, "default", remoteObject, now); err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{pull: PullResponse{Cursor: "cursor-2", Objects: []domain.SyncObject{remoteObject}}}
	engine := Engine{Store: database, Remote: remote, Scope: "default", DeviceID: "device", Now: func() time.Time { return now }}
	calls := 0
	engine.Register("note", func(context.Context, domain.SyncObject) error { calls++; return nil })
	if err := engine.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if len(remote.pushed.Objects) != 1 {
		t.Fatalf("pushed=%d", len(remote.pushed.Objects))
	}
	if err := engine.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("handler called %d times", calls)
	}
	deliveryApplied, err := database.SyncWasApplied(ctx, "center:event-2")
	if err != nil || !deliveryApplied {
		t.Fatalf("center delivery applied=%t err=%v", deliveryApplied, err)
	}
	state, err := database.SyncState(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if state.Cursor != "cursor-2" {
		t.Fatalf("cursor=%q", state.Cursor)
	}
}

func TestEnginePullOrdersKnowledgeAcrossPages(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	parent := domain.KnowledgeEntry{
		ID: "parent", Scope: "global", ParentID: "root", Slug: "parent", Type: "overview",
		Title: "Parent", Body: "parent", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	child := domain.KnowledgeEntry{
		ID: "child", Scope: "global", ParentID: parent.ID, Slug: "child", Type: "practice",
		Title: "Child", Body: "child", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	object := func(entry domain.KnowledgeEntry, event string) domain.SyncObject {
		payload, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return domain.SyncObject{Type: TypeKnowledge, ID: entry.ID, Operation: "upsert", Payload: payload, IdempotencyKey: event, EventID: event}
	}
	remote := &pagedRemote{responses: map[string]PullResponse{
		"":       {Cursor: "page-1", HasMore: true, Objects: []domain.SyncObject{object(child, "event-child")}},
		"page-1": {Cursor: "page-2", Objects: []domain.SyncObject{object(parent, "event-parent")}},
	}}
	engine := Engine{Store: database, Remote: remote, Scope: "default", DeviceID: "device", BatchSize: 1}
	applied := map[string]bool{"root": true}
	order := []string{}
	engine.Register(TypeKnowledge, func(_ context.Context, synced domain.SyncObject) error {
		var entry domain.KnowledgeEntry
		if err := json.Unmarshal(synced.Payload, &entry); err != nil {
			return err
		}
		if entry.ParentID != "" && !applied[entry.ParentID] {
			return waitForDependency(fmt.Errorf("parent %s has not been applied", entry.ParentID))
		}
		applied[entry.ID] = true
		order = append(order, entry.ID)
		return nil
	})
	if err := engine.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != parent.ID || order[1] != child.ID {
		t.Fatalf("apply order=%v", order)
	}
	if len(remote.cursors) != 2 || remote.cursors[0] != "" || remote.cursors[1] != "page-1" {
		t.Fatalf("pull cursors=%v", remote.cursors)
	}
	state, err := database.SyncState(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if state.Cursor != "page-2" {
		t.Fatalf("cursor=%q", state.Cursor)
	}
}

func TestEnginePullPreservesSnapshotsForTheSameObject(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	remote := &fakeRemote{pull: PullResponse{Cursor: "2", Objects: []domain.SyncObject{
		{Type: "note", ID: "same", Operation: "upsert", Payload: json.RawMessage(`{"value":"old"}`), IdempotencyKey: "old", EventID: "event-1"},
		{Type: "note", ID: "same", Operation: "upsert", Payload: json.RawMessage(`{"value":"new"}`), IdempotencyKey: "new", EventID: "event-2"},
	}}}
	engine := Engine{Store: database, Remote: remote, Scope: "default", DeviceID: "device"}
	values := []string{}
	engine.Register("note", func(_ context.Context, object domain.SyncObject) error {
		var payload map[string]string
		if err := json.Unmarshal(object.Payload, &payload); err != nil {
			return err
		}
		values = append(values, payload["value"])
		return nil
	})
	if err := engine.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0] != "old" || values[1] != "new" {
		t.Fatalf("applied snapshots=%v", values)
	}
}

func TestEnginePullAppliesProjectKnowledgeHierarchyAcrossPages(t *testing.T) {
	ctx, source, destination := context.Background(), businessStore(t), businessStore(t)
	now := time.Now().UTC()
	project := domain.Project{ID: "project-cross-page", Name: "Cross page", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := source.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, err := source.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	parent := domain.KnowledgeEntry{
		ID: "knowledge-parent-cross-page", Scope: "project", ProjectID: project.ID, ParentID: root.ID, Slug: "parent",
		Type: "overview", Title: "Parent", Body: "parent", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	child := domain.KnowledgeEntry{
		ID: "knowledge-child-cross-page", Scope: "project", ProjectID: project.ID, ParentID: parent.ID, Slug: "child",
		Type: "practice", Title: "Child", Body: "child", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := source.CreateKnowledge(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := source.CreateKnowledge(ctx, child); err != nil {
		t.Fatal(err)
	}

	exported, err := ExportBusinessObjectsForDevice(ctx, source, "source")
	if err != nil {
		t.Fatal(err)
	}
	selected := map[string]domain.SyncObject{}
	for _, object := range exported {
		if object.ID == project.ID || object.ID == root.ID || object.ID == parent.ID || object.ID == child.ID {
			selected[object.ID] = object
		}
	}
	for _, id := range []string{project.ID, root.ID, parent.ID, child.ID} {
		if selected[id].ID == "" {
			t.Fatalf("missing exported dependency %s", id)
		}
	}
	remote := &pagedRemote{responses: map[string]PullResponse{
		"": {Cursor: "page-1", HasMore: true, Objects: []domain.SyncObject{selected[child.ID]}},
		"page-1": {Cursor: "page-2", Objects: []domain.SyncObject{
			selected[project.ID], selected[root.ID], selected[parent.ID],
		}},
	}}
	engine := Engine{Store: destination, Remote: remote, Scope: "default", DeviceID: "destination", BatchSize: 1}
	RegisterBusinessHandlers(&engine, destination)
	if err := engine.Pull(ctx); err != nil {
		t.Fatalf("cross-page knowledge pull failed: %v", err)
	}
	received, err := destination.Knowledge(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if received.Scope != "project" || received.ProjectID != project.ID || received.ParentID != parent.ID {
		t.Fatalf("child hierarchy changed during sync: %#v", received)
	}
}

func TestEngineUsesRemoteVersionWhenOlderCenterOmitsEventID(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	remoteObject := domain.SyncObject{
		Type: "note", ID: "same-object", Operation: "upsert", Payload: json.RawMessage(`{"value":"corrected"}`),
		IdempotencyKey: "note:same-object:same-producer-key", RemoteVersion: "7",
	}
	if err := database.MarkSyncApplied(ctx, "default", remoteObject, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{pull: PullResponse{Cursor: "7", Objects: []domain.SyncObject{remoteObject}}}
	engine := Engine{Store: database, Remote: remote, Scope: "default", DeviceID: "device"}
	calls := 0
	engine.Register("note", func(context.Context, domain.SyncObject) error { calls++; return nil })
	if err := engine.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("older-center delivery was applied %d times", calls)
	}
	deliveryKey := "center-object:note:same-object:7"
	applied, err := database.SyncWasApplied(ctx, deliveryKey)
	if err != nil || !applied {
		t.Fatalf("fallback delivery key applied=%t err=%v", applied, err)
	}
}
