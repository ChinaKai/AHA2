package sync_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/centersync"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func TestRunOnceDrainsEveryImmediatelyPushableBatch(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := httptest.NewServer(center.Handler())
	defer server.Close()
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
	if err := secretStore.PutMany(map[string]string{syncer.DefaultTokenRef: "batch-token"}); err != nil {
		t.Fatal(err)
	}
	if err := center.PutDeviceToken(ctx, "batch-device", "batch-token"); err != nil {
		t.Fatal(err)
	}
	if err := database.PutSyncSettings(ctx, domain.SyncSettings{Scope: "default", Enabled: true, Endpoint: server.URL, DeviceID: "batch-device", IntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for index := 0; index < 205; index++ {
		id := fmt.Sprintf("batch-knowledge-%03d", index)
		if err := database.CreateKnowledge(ctx, domain.KnowledgeEntry{ID: id, Scope: "global", Slug: id, Type: "practice", Title: id, Body: id, Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := (syncer.Runner{Store: database, Secrets: secretStore, TokenRef: syncer.DefaultTokenRef}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := database.SyncOutboxCount(ctx, "default")
	if err != nil || pending != 0 {
		t.Fatalf("RunOnce left %d immediately pushable items: %v", pending, err)
	}
	events, _, err := center.Pull(ctx, "audit", 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	knowledgeEvents := 0
	for _, event := range events {
		if event.ObjectType == syncer.TypeKnowledge {
			knowledgeEvents++
		}
	}
	if knowledgeEvents < 206 {
		t.Fatalf("center received only %d knowledge events", knowledgeEvents)
	}
}
