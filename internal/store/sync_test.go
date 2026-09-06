package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestSyncSettingsDeviceNameLegacyMigration(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TABLE sync_settings(scope TEXT PRIMARY KEY,device_id TEXT NOT NULL); INSERT INTO sync_settings(scope,device_id) VALUES('default','legacy-device')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(schemaV30); err != nil {
		t.Fatal(err)
	}
	var id, name string
	if err := raw.QueryRow(`SELECT device_id,device_name FROM sync_settings WHERE scope='default'`).Scan(&id, &name); err != nil {
		t.Fatal(err)
	}
	if id != "legacy-device" || name != "legacy-device" {
		t.Fatalf("id=%q name=%q", id, name)
	}
}

func TestSyncPersistenceAndAck(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	settings := domain.SyncSettings{Scope: "default", Enabled: true, Endpoint: "https://sync.example", DeviceID: "dev_immutable", DeviceName: "Laptop", IntervalSeconds: 60, UpdatedAt: now}
	if err := database.PutSyncSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	got, err := database.SyncSettings(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if got.Endpoint != settings.Endpoint || got.DeviceID != "dev_immutable" || got.DeviceName != "Laptop" || !got.Enabled {
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

func TestSchemaV35RequiresOneTimeOrderedSyncReplay(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateSyncState(ctx, domain.SyncState{Scope: "default", Cursor: "42", LastPullAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM schema_migrations WHERE version=35; ALTER TABLE sync_state DROP COLUMN replay_required`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := database.SyncState(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if state.Cursor != "" || !state.ReplayRequired || !state.LastPullAt.IsZero() {
		t.Fatalf("migration did not require a clean replay: %#v", state)
	}
	if err := database.CompleteSyncReplay(ctx, "default"); err != nil {
		t.Fatal(err)
	}
	state, err = database.SyncState(ctx, "default")
	if err != nil || state.ReplayRequired {
		t.Fatalf("replay completion was not persisted: %#v err=%v", state, err)
	}
	if err := database.UpdateSyncState(ctx, domain.SyncState{Scope: "default", Cursor: "84"}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state, err = database.SyncState(ctx, "default")
	if err != nil || state.Cursor != "84" || state.ReplayRequired {
		t.Fatalf("schema v35 replay repeated unexpectedly: %#v err=%v", state, err)
	}
}
