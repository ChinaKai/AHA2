package sync_test

import (
	"context"
	"database/sql"
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
	_ "modernc.org/sqlite"
)

func TestExistingCursorUpgradeReplaysCenterBeforePublishingDivergedKnowledge(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := httptest.NewServer(center.Handler())
	defer server.Close()

	type device struct {
		path    string
		store   *store.Store
		secrets *secrets.FileStore
		runner  syncer.Runner
	}
	makeDevice := func(id, token string) device {
		dir := t.TempDir()
		path := filepath.Join(dir, "aha2.db")
		database, err := store.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
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
		return device{path: path, store: database, secrets: secretStore, runner: syncer.Runner{Store: database, Secrets: secretStore, TokenRef: syncer.DefaultTokenRef}}
	}
	source := makeDevice("source", "source-token")
	destination := makeDevice("destination", "destination-token")
	defer source.store.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "shared-project", Name: "Shared", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	for _, database := range []*store.Store{source.store, destination.store} {
		if err := database.CreateProject(ctx, project); err != nil {
			t.Fatal(err)
		}
		if err := database.CreateKnowledge(ctx, domain.KnowledgeEntry{ID: "shared-knowledge", Scope: "project", ProjectID: project.ID, Type: "practice", Title: "Shared", Body: "canonical", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := source.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := destination.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	entry, err := destination.store.Knowledge(ctx, "shared-knowledge")
	if err != nil {
		t.Fatal(err)
	}
	globalRoot, err := destination.store.EnsureKnowledgeRoot(ctx, "global", "")
	if err != nil {
		t.Fatal(err)
	}
	entry.Scope, entry.ProjectID, entry.ParentID = "global", "", globalRoot.ID
	if err := destination.store.UpdateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	state, err := destination.store.SyncState(ctx, "default")
	if err != nil || state.Cursor == "" {
		t.Fatalf("destination did not establish an old cursor: %#v err=%v", state, err)
	}
	if err := destination.store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", destination.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM schema_migrations WHERE version=35; ALTER TABLE sync_state DROP COLUMN replay_required`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	destination.store, err = store.Open(ctx, destination.path)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.store.Close()
	destination.runner.Store = destination.store
	state, err = destination.store.SyncState(ctx, "default")
	if err != nil || !state.ReplayRequired || state.Cursor != "" {
		t.Fatalf("upgrade did not schedule replay: %#v err=%v", state, err)
	}
	if err := destination.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	entry, err = destination.store.Knowledge(ctx, "shared-knowledge")
	if err != nil || entry.Scope != "project" || entry.ProjectID != project.ID || entry.Body != "canonical" {
		t.Fatalf("ordered replay did not restore center state: %#v err=%v", entry, err)
	}
	state, err = destination.store.SyncState(ctx, "default")
	if err != nil || state.ReplayRequired || state.Cursor == "" {
		t.Fatalf("replay did not finish: %#v err=%v", state, err)
	}
	events, _, err := center.Pull(ctx, "audit", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.ObjectType != syncer.TypeKnowledge || event.ObjectID != "shared-knowledge" {
			continue
		}
		var synced domain.KnowledgeEntry
		if err := json.Unmarshal(event.Payload, &synced); err != nil {
			t.Fatal(err)
		}
		if synced.Scope != "project" || synced.ProjectID != project.ID {
			t.Fatalf("diverged local scope was published before replay: %#v", synced)
		}
	}
}
