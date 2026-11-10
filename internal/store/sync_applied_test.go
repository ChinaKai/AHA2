package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func syncAppliedTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func markTestKey(t *testing.T, database *Store, key string) {
	t.Helper()
	if err := database.MarkSyncApplied(context.Background(), "default", domain.SyncObject{Type: "test", ID: key, IdempotencyKey: key}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func TestSyncAppliedKeysBatchesAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	database := syncAppliedTestStore(t)
	keys := make([]string, syncAppliedLookupBatch*2+25)
	for index := range keys {
		keys[index] = fmt.Sprintf("applied-%04d", index)
	}
	for index, key := range keys {
		if index%2 == 0 {
			markTestKey(t, database, key)
		}
	}
	found, err := database.SyncAppliedKeys(ctx, append(append([]string{}, keys...), keys[0], ""))
	if err != nil {
		t.Fatal(err)
	}
	want := (len(keys) + 1) / 2
	if len(found) != want {
		t.Fatalf("found=%d want=%d", len(found), want)
	}
	if _, ok := found[keys[0]]; !ok {
		t.Fatalf("first applied key missing")
	}
	if _, ok := found[keys[1]]; ok {
		t.Fatalf("unapplied key reported present")
	}
	empty, err := database.SyncAppliedKeys(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty lookup=%#v err=%v", empty, err)
	}
}

func TestConversationSyncMetaIndexIsMigrated(t *testing.T) {
	database := syncAppliedTestStore(t)
	var indexSQL string
	var versionExists bool
	if err := database.db.QueryRowContext(context.Background(), `SELECT COALESCE((SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_conversation_sync_meta'),'')`).Scan(&indexSQL); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=128)`).Scan(&versionExists); err != nil {
		t.Fatal(err)
	}
	if indexSQL == "" || !versionExists {
		t.Fatalf("index=%q version128=%t", indexSQL, versionExists)
	}
	if !strings.Contains(indexSQL, "created_at") {
		t.Fatalf("conversation identity index lost the version column: %s", indexSQL)
	}
	var plan string
	if err := database.db.QueryRowContext(context.Background(), `EXPLAIN QUERY PLAN SELECT id,created_at FROM conversation_items WHERE task_id IN ('a','b')`).Scan(new(int), new(int), new(int), &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_conversation_sync_meta") {
		t.Fatalf("conversation identity lookup does not use the covering index: %s", plan)
	}
}

func TestForEachSyncAppliedKeyRangeUsesExactPrefix(t *testing.T) {
	ctx := context.Background()
	database := syncAppliedTestStore(t)
	for _, key := range []string{
		"conversation:device-a:one:2026-11-10T00:00:00Z",
		"conversation:device-a:two:2026-11-10T00:00:01Z",
		"conversation:device-b:three:2026-11-10T00:00:02Z",
		"skill:device-a:four",
	} {
		markTestKey(t, database, key)
	}
	var found []string
	if err := database.ForEachSyncAppliedKeyRange(ctx, "conversation:device-a:", func(key string) error {
		found = append(found, key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found=%#v", found)
	}
	for _, key := range found {
		if key != "conversation:device-a:one:2026-11-10T00:00:00Z" && key != "conversation:device-a:two:2026-11-10T00:00:01Z" {
			t.Fatalf("unexpected key %q", key)
		}
	}
}
