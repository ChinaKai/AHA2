package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestSyncPersistenceAndAck(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	settings := domain.SyncSettings{Scope: "default", Enabled: true, Endpoint: "https://sync.example", DeviceID: "device-1", IntervalSeconds: 60, UpdatedAt: now}
	if err := database.PutSyncSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	got, err := database.SyncSettings(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if got.Endpoint != settings.Endpoint || !got.Enabled {
		t.Fatalf("unexpected settings: %#v", got)
	}
	item := domain.SyncOutboxItem{ID: "out-1", Scope: "default", Object: domain.SyncObject{Type: "note", ID: "n1", Operation: "upsert", Payload: json.RawMessage(`{"title":"one"}`), IdempotencyKey: "key-1"}}
	if err := database.EnqueueSync(ctx, item); err != nil {
		t.Fatal(err)
	}
	if err := database.EnqueueSync(ctx, item); err != nil {
		t.Fatal(err)
	}
	pending, err := database.PendingSync(ctx, "default", 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending items", len(pending))
	}
	if err := database.AckSync(ctx, "default", []string{"out-1"}, now); err != nil {
		t.Fatal(err)
	}
	pending, err = database.PendingSync(ctx, "default", 10, now)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after ack: %d, %v", len(pending), err)
	}
	state, err := database.SyncState(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastPushAt.Equal(now) {
		t.Fatalf("last push = %v", state.LastPushAt)
	}
}

func TestSyncAppliedAndConflicts(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	object := domain.SyncObject{Type: "note", ID: "n1", IdempotencyKey: "remote-1", RemoteVersion: "2"}
	if err := database.MarkSyncApplied(ctx, "default", object, now); err != nil {
		t.Fatal(err)
	}
	applied, err := database.SyncWasApplied(ctx, "remote-1")
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if err := database.AddSyncConflict(ctx, domain.SyncConflict{Scope: "default", ObjectType: "note", ObjectID: "n1", LocalPayload: json.RawMessage(`{}`), RemotePayload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	conflicts, err := database.SyncConflicts(ctx, "default")
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("conflicts=%d err=%v", len(conflicts), err)
	}
}
