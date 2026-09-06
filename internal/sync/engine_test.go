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
