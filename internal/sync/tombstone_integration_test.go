package sync_test

import (
	"context"
	"database/sql"
	"errors"
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

func TestKnowledgeDeleteWinsOverLateOldUpsertAndFullReplay(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := httptest.NewServer(center.Handler())
	defer server.Close()
	type device struct {
		database *store.Store
		runner   syncer.Runner
	}
	makeDevice := func(id, token string) device {
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
		return device{database: database, runner: syncer.Runner{Store: database, Secrets: secretStore, TokenRef: syncer.DefaultTokenRef}}
	}
	deviceA := makeDevice("device-delete-a", "token-delete-a")
	deviceB := makeDevice("device-delete-b", "token-delete-b")
	now := time.Now().UTC()
	root, _ := deviceA.database.EnsureKnowledgeRoot(ctx, "global", "")
	entry := domain.KnowledgeEntry{ID: "delete-wins", Scope: "global", ParentID: root.ID, Slug: "delete-wins", Type: "practice", Title: "Delete wins", Body: "old content", Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now}
	if err := deviceA.database.CreateKnowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	for _, current := range []*syncer.Runner{&deviceA.runner, &deviceB.runner, &deviceA.runner} {
		if err := current.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := deviceB.database.Knowledge(ctx, entry.ID); err != nil {
		t.Fatalf("initial knowledge did not synchronize: %v", err)
	}

	if err := deviceA.database.DeleteKnowledge(ctx, entry.ID); err != nil {
		t.Fatal(err)
	}
	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// B changes same-revision feedback while offline, producing a new business
	// key for old content before it pulls A's later delete.
	if _, err := deviceB.database.FeedbackKnowledge(ctx, entry.ID, "helped", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	for _, current := range []device{deviceA, deviceB} {
		if _, err := current.database.Knowledge(ctx, entry.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("old upsert resurrected deleted knowledge: %v", err)
		}
		tombstone, err := current.database.SyncTombstone(ctx, syncer.TypeKnowledge, entry.ID)
		if err != nil || tombstone.Version != "1" || tombstone.SyncKey == "" {
			t.Fatalf("tombstone=%#v err=%v", tombstone, err)
		}
	}
	events, _, err := center.Pull(ctx, "audit", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	operations := []string{}
	for _, event := range events {
		if event.ObjectID == entry.ID {
			operations = append(operations, event.Operation)
		}
	}
	if len(operations) < 3 || operations[len(operations)-2] != "delete" || operations[len(operations)-1] != "upsert" {
		t.Fatalf("expected late old upsert after delete, operations=%v", operations)
	}

	state, err := deviceB.database.SyncState(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	state.Cursor = ""
	state.UpdatedAt = time.Now().UTC()
	if err := deviceB.database.UpdateSyncState(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceB.database.Knowledge(ctx, entry.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("full replay resurrected deleted knowledge: %v", err)
	}
}

func TestCodexDeleteQuarantinedByOldBuildReplaysAfterUpgrade(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := httptest.NewServer(center.Handler())
	defer server.Close()

	const deviceID = "device-old-codex"
	const token = "old-codex-sync-token"
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
	if err := database.PutSyncSettings(ctx, domain.SyncSettings{
		Scope: "default", Enabled: true, Endpoint: server.URL, DeviceID: deviceID, IntervalSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if err := center.PutDeviceToken(ctx, deviceID, token); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	account := domain.CodexAccount{
		ID: "codex-deleted-before-upgrade", Label: "Stale", Status: "ready",
		CredentialRef: "codex-account/codex-deleted-before-upgrade/auth", CredentialConfigured: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertCodexAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if _, err := center.Push(ctx, "deleting-device", []centersync.Event{{
		EventID: "delete-codex-before-upgrade", ObjectID: account.ID,
		ObjectType: syncer.TypeCodexAccount, Operation: "delete",
		Version: 1, SourceVersion: now.Format(time.RFC3339Nano),
	}}); err != nil {
		t.Fatal(err)
	}

	oldClient := &syncer.Client{
		BaseURL: server.URL, DeviceID: deviceID,
		Credential: func(context.Context) (string, error) { return token, nil },
	}
	oldEngine := &syncer.Engine{Store: database, Remote: oldClient, Scope: "default", DeviceID: deviceID}
	if err := oldEngine.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CodexAccount(ctx, account.ID); err != nil {
		t.Fatalf("old build unexpectedly understood Codex deletion: %v", err)
	}
	conflicts, err := database.SyncConflicts(ctx, "default")
	if err != nil || len(conflicts) != 1 || conflicts[0].ObjectType != syncer.TypeCodexAccount {
		t.Fatalf("old build did not quarantine Codex deletion: %#v err=%v", conflicts, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM schema_migrations WHERE version=72`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state, err := database.SyncState(ctx, "default")
	if err != nil || !state.ReplayRequired || state.Cursor != "" {
		t.Fatalf("Codex tombstone upgrade did not request replay: %#v err=%v", state, err)
	}
	if conflicts, err := database.SyncConflicts(ctx, "default"); err != nil || len(conflicts) != 0 {
		t.Fatalf("Codex quarantine conflict survived upgrade: %#v err=%v", conflicts, err)
	}

	runner := syncer.Runner{Store: database, Secrets: secretStore, TokenRef: syncer.DefaultTokenRef}
	if err := runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CodexAccount(ctx, account.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replayed Codex deletion did not remove the account: %v", err)
	}
	state, err = database.SyncState(ctx, "default")
	if err != nil || state.ReplayRequired || state.Cursor == "" {
		t.Fatalf("Codex tombstone replay did not complete: %#v err=%v", state, err)
	}
}
