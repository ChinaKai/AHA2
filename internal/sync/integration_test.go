package sync_test

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
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func TestTwoLocalStoresSynchronizeKnowledgeThroughCenter(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := httptest.NewServer(center.Handler())
	defer server.Close()
	makeDevice := func(id, token string) (*store.Store, *secrets.FileStore) {
		dir := t.TempDir()
		db, err := store.Open(ctx, filepath.Join(dir, "aha2.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		secretStore, err := secrets.Open(filepath.Join(dir, "secrets.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := secretStore.PutMany(map[string]string{syncer.DefaultTokenRef: token}); err != nil {
			t.Fatal(err)
		}
		if err := db.PutSyncSettings(ctx, domain.SyncSettings{Scope: "default", Enabled: true, Endpoint: server.URL, DeviceID: id, IntervalSeconds: 60}); err != nil {
			t.Fatal(err)
		}
		if err := center.PutDeviceToken(ctx, id, token); err != nil {
			t.Fatal(err)
		}
		return db, secretStore
	}
	source, sourceSecrets := makeDevice("device-a", "token-a")
	destination, destinationSecrets := makeDevice("device-b", "token-b")
	now := time.Now().UTC()
	entry := domain.KnowledgeEntry{ID: "knowledge-portable", Scope: "global", Type: "practice", Title: "Portable", Body: "sync me", Status: domain.KnowledgeVerified, Confidence: 1, Revision: 1, ContentHash: "hash", CreatedAt: now, UpdatedAt: now}
	if err := source.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err := (syncer.Runner{Store: source, Secrets: sourceSecrets, TokenRef: syncer.DefaultTokenRef}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := (syncer.Runner{Store: destination, Secrets: destinationSecrets, TokenRef: syncer.DefaultTokenRef}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	received, err := destination.Knowledge(ctx, entry.ID)
	if err != nil || received.Body != entry.Body {
		t.Fatalf("received=%#v err=%v", received, err)
	}
}
