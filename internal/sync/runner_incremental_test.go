package sync

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/centersync"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func centerEvents(t *testing.T, center *centersync.Store) []centersync.Event {
	t.Helper()
	events, _, err := center.Pull(context.Background(), "audit", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func eventCountForObject(events []centersync.Event, objectID string) int {
	count := 0
	for _, event := range events {
		if event.ObjectID == objectID {
			count++
		}
	}
	return count
}

func TestRunOnceNoChangeDoesNotReapplyOrRepush(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	centerServer := httptest.NewServer(center.Handler())
	defer centerServer.Close()

	dir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(dir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := secretStore.PutMany(map[string]string{DefaultTokenRef: "incremental-token"}); err != nil {
		t.Fatal(err)
	}
	if err := center.PutDeviceToken(ctx, "incremental-device", "incremental-token"); err != nil {
		t.Fatal(err)
	}
	if err := database.PutSyncSettings(ctx, domain.SyncSettings{Scope: "default", Enabled: true, Endpoint: centerServer.URL, DeviceID: "incremental-device", IntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	task := seedSyncExportTask(t, database)
	now := time.Now().UTC()
	for index, id := range []string{"conversation_incremental_a", "conversation_incremental_b"} {
		if _, err := database.AddConversationItem(ctx, domain.ConversationItem{
			ID: id, TaskID: task.ID,
			Category: "chat", Kind: "user_message", Summary: "incremental", CreatedAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.CreateKnowledge(ctx, domain.KnowledgeEntry{
		ID: "knowledge-incremental", Scope: "global", Type: "practice", Title: "Incremental",
		Body: "body", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	runner := Runner{Store: database, Secrets: secretStore, TokenRef: DefaultTokenRef}
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	initial := centerEvents(t, center)
	if len(initial) == 0 {
		t.Fatal("first run pushed nothing")
	}

	preview, err := runner.Preview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Upserts != 0 || preview.Deletes != 0 || preview.Pending != 0 {
		t.Fatalf("unchanged preview=%#v", preview)
	}
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	unchanged := centerEvents(t, center)
	if len(unchanged) != len(initial) {
		t.Fatalf("unchanged run pushed %d extra events", len(unchanged)-len(initial))
	}

	if err := database.CreateKnowledge(ctx, domain.KnowledgeEntry{
		ID: "knowledge-incremental-2", Scope: "global", Type: "practice", Title: "Incremental 2",
		Body: "body", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddConversationItem(ctx, domain.ConversationItem{
		ID: "conversation_incremental_c", TaskID: task.ID, Category: "chat", Kind: "user_message",
		Summary: "new message", CreatedAt: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	preview, err = runner.Preview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Upserts != 2 {
		t.Fatalf("changed preview=%#v", preview)
	}
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	changed := centerEvents(t, center)
	if len(changed) != len(unchanged)+2 {
		t.Fatalf("incremental run pushed %d events, want 2", len(changed)-len(unchanged))
	}
	if count := eventCountForObject(changed, "knowledge-incremental-2"); count != 1 {
		t.Fatalf("new knowledge pushed %d times", count)
	}
	if count := eventCountForObject(changed, "incremental-device:conversation_incremental_c"); count != 1 {
		t.Fatalf("new conversation pushed %d times", count)
	}
}
