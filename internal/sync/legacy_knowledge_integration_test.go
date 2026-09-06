package sync_test

import (
	"context"
	"encoding/json"
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

func TestNewDeviceRecoversProjectKnowledgeAfterLegacyScopeEvent(t *testing.T) {
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
		database, err := store.Open(ctx, filepath.Join(dir, "aha2.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		secretStore, err := secrets.Open(filepath.Join(dir, "secrets.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := secretStore.PutMany(map[string]string{syncer.DefaultTokenRef: token}); err != nil {
			t.Fatal(err)
		}
		if err := database.PutSyncSettings(ctx, domain.SyncSettings{Scope: "default", Enabled: true, Endpoint: server.URL, DeviceID: id, IntervalSeconds: 60}); err != nil {
			t.Fatal(err)
		}
		if err := center.PutDeviceToken(ctx, id, token); err != nil {
			t.Fatal(err)
		}
		return database, secretStore
	}

	source, sourceSecrets := makeDevice("device-source", "token-source")
	destination, destinationSecrets := makeDevice("device-new", "token-new")
	now := time.Now().UTC()
	project := domain.Project{ID: "project-legacy-scope", Name: "Project", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := source.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	entry := domain.KnowledgeEntry{ID: "knowledge-legacy-scope", Scope: "project", ProjectID: project.ID, Type: "practice", Title: "Scoped", Body: "project body", Status: domain.KnowledgeVerified, Confidence: 1, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := source.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}

	legacy := entry
	legacy.ProjectID = ""
	legacy.ParentID = ""
	legacyRaw, _ := json.Marshal(legacy)
	legacyObject := domain.SyncObject{Type: "knowledge", ID: entry.ID, Operation: "upsert", Payload: legacyRaw, RemoteVersion: "1", IdempotencyKey: "knowledge:" + entry.ID + ":1"}
	if _, err := center.Push(ctx, "legacy-device", []centersync.Event{{EventID: legacyObject.IdempotencyKey, ObjectID: entry.ID, ObjectType: "knowledge", Operation: "upsert", Payload: legacyRaw, Version: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := source.MarkSyncApplied(ctx, "default", legacyObject, now); err != nil {
		t.Fatal(err)
	}

	if err := (syncer.Runner{Store: source, Secrets: sourceSecrets, TokenRef: syncer.DefaultTokenRef}).RunOnce(ctx); err != nil {
		t.Fatalf("source correction sync failed: %v", err)
	}
	if err := (syncer.Runner{Store: destination, Secrets: destinationSecrets, TokenRef: syncer.DefaultTokenRef}).RunOnce(ctx); err != nil {
		t.Fatalf("new device first sync failed: %v", err)
	}
	received, err := destination.Knowledge(ctx, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if received.Scope != "project" || received.ProjectID != project.ID || received.Body != entry.Body {
		t.Fatalf("corrected project knowledge was not restored: %#v", received)
	}
	if _, err := destination.Project(ctx, project.ID); err != nil {
		t.Fatalf("project dependency was not applied before corrected knowledge: %v", err)
	}
}
