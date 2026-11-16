package sync_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strconv"
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

type skillSyncDevice struct {
	database *store.Store
	runner   syncer.Runner
}

func newSkillSyncDevice(t *testing.T, center *centersync.Store, endpoint, id, token string) skillSyncDevice {
	t.Helper()
	ctx := context.Background()
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
	if err := database.PutSyncSettings(ctx, domain.SyncSettings{Scope: "default", Enabled: true, Endpoint: endpoint, DeviceID: id, IntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if err := center.PutDeviceToken(ctx, id, token); err != nil {
		t.Fatal(err)
	}
	return skillSyncDevice{database: database, runner: syncer.Runner{Store: database, Secrets: secretStore, TokenRef: syncer.DefaultTokenRef}}
}

func peerSkillEvent(t *testing.T, eventID, id string, version int, body string) centersync.Event {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"skill": domain.Skill{ID: id, Scope: "global", Name: "demo", Description: "demo", Instructions: body, Version: version, Status: "active", Enabled: true},
		"files": []domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: \"demo\"\ndescription: \"demo\"\n---\n\n" + body + "\n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return centersync.Event{EventID: eventID, ObjectID: id, ObjectType: syncer.TypeSkill, Operation: "upsert", Payload: payload, Version: 1, SourceVersion: strconv.Itoa(version)}
}

// TestDeletedSkillIsNotResurrectedByPeerUpsert reproduces the field report:
// after a local skill delete, the center replays a peer's upsert whose source
// version is far above the tombstone. That upsert used to win, recreating the
// skill and restarting an unbounded version increase on every sync cycle.
func TestDeletedSkillIsNotResurrectedByPeerUpsert(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := httptest.NewServer(center.Handler())
	defer server.Close()
	device := newSkillSyncDevice(t, center, server.URL, "device-skill", "token-skill")

	now := time.Now().UTC()
	skill := domain.Skill{ID: "skill-delete-wins", Scope: "global", Name: "demo", Description: "demo", Instructions: "body", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := device.database.CreateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	// The center echoes the device's own upsert. Re-applying identical content
	// must not count as a change and must not raise the version.
	if err := device.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	applied, err := device.database.Skill(ctx, skill.ID)
	if err != nil || applied.Version != 1 {
		t.Fatalf("echo of own skill bumped the version: %#v err=%v", applied, err)
	}

	if err := device.database.DeleteSkill(ctx, skill.ID); err != nil {
		t.Fatal(err)
	}
	if err := device.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	// A peer that kept churning the (now deleted) skill pushes the same
	// content at a much higher version, then a differently shaped one.
	if _, err := center.Push(ctx, "peer-device", []centersync.Event{
		peerSkillEvent(t, "peer-skill-same", skill.ID, 9, "body"),
		peerSkillEvent(t, "peer-skill-other", skill.ID, 12, "peer body"),
	}); err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 3; cycle++ {
		if err := device.runner.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := device.database.Skill(ctx, skill.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("cycle %d resurrected the deleted skill: %v", cycle, err)
		}
		tombstone, err := device.database.SyncTombstone(ctx, syncer.TypeSkill, skill.ID)
		if err != nil || tombstone.Version != "1" {
			t.Fatalf("cycle %d tombstone=%#v err=%v", cycle, tombstone, err)
		}
	}
	pending, err := device.database.SyncOutboxCount(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("deleted skill keeps re-entering the outbox: %d pending", pending)
	}
}

// TestRebuiltSkillReplacesItsTombstoneAndSyncs pins the recovery path a review
// found missing: rebuilding a deleted skill ID locally must drop the tombstone
// in the same transaction, so the export carries only the upsert and a peer
// that has not seen the delete yet ends up with the rebuilt skill.
func TestRebuiltSkillReplacesItsTombstoneAndSyncs(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := httptest.NewServer(center.Handler())
	defer server.Close()
	deviceA := newSkillSyncDevice(t, center, server.URL, "device-rebuild-a", "token-rebuild-a")
	deviceB := newSkillSyncDevice(t, center, server.URL, "device-rebuild-b", "token-rebuild-b")

	now := time.Now().UTC()
	skill := domain.Skill{ID: "skill-rebuild", Scope: "global", Name: "demo", Description: "demo", Instructions: "old body", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := deviceA.database.CreateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	for _, runner := range []*syncer.Runner{&deviceA.runner, &deviceB.runner, &deviceA.runner} {
		if err := runner.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := deviceB.database.Skill(ctx, skill.ID); err != nil {
		t.Fatalf("skill did not synchronize: %v", err)
	}

	// Delete, then rebuild the same ID before the next sync, the way a legacy
	// re-import does after a cleanup.
	if err := deviceA.database.DeleteSkill(ctx, skill.ID); err != nil {
		t.Fatal(err)
	}
	rebuilt := domain.Skill{ID: skill.ID, Scope: "global", Name: "demo", Description: "demo", Instructions: "rebuilt body", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now.Add(time.Minute)}
	if err := deviceA.database.CreateSkill(ctx, rebuilt); err != nil {
		t.Fatal(err)
	}
	objects, err := syncer.ExportBusinessObjects(ctx, deviceA.database)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		if object.Type == syncer.TypeSkill && object.ID == skill.ID && object.Operation == "delete" {
			t.Fatal("rebuilt skill still exports its tombstone")
		}
	}

	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	applied, err := deviceB.database.Skill(ctx, skill.ID)
	if err != nil {
		t.Fatalf("peer did not receive the rebuilt skill: %v", err)
	}
	if applied.Version != 2 || applied.Instructions != "rebuilt body" {
		t.Fatalf("peer skill=%#v", applied)
	}
}

// TestResurrectReachesPeerThatAlreadyAppliedDelete covers the real recovery
// path: the delete has already propagated (so the peer holds a tombstone), then
// the skill is rebuilt locally under the same ID. Only an explicit resurrect
// event may clear the peer's tombstone; an ordinary upsert -- however high its
// version -- must stay blocked.
func TestResurrectReachesPeerThatAlreadyAppliedDelete(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := httptest.NewServer(center.Handler())
	defer server.Close()
	deviceA := newSkillSyncDevice(t, center, server.URL, "device-resurrect-a", "token-resurrect-a")
	deviceB := newSkillSyncDevice(t, center, server.URL, "device-resurrect-b", "token-resurrect-b")

	now := time.Now().UTC()
	skill := domain.Skill{ID: "skill-resurrect", Scope: "global", Name: "demo", Description: "demo", Instructions: "old body", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := deviceA.database.CreateSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	for _, runner := range []*syncer.Runner{&deviceA.runner, &deviceB.runner, &deviceA.runner} {
		if err := runner.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// Delete first and let it reach the peer, the way the field incident
	// looked: B really holds a tombstone before the rebuild happens.
	if err := deviceA.database.DeleteSkill(ctx, skill.ID); err != nil {
		t.Fatal(err)
	}
	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceB.database.Skill(ctx, skill.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("peer still has the deleted skill: %v", err)
	}
	if _, err := deviceB.database.SyncTombstone(ctx, syncer.TypeSkill, skill.ID); err != nil {
		t.Fatalf("peer did not record the tombstone: %v", err)
	}

	rebuilt := domain.Skill{ID: skill.ID, Scope: "global", Name: "demo", Description: "demo", Instructions: "rebuilt body", Version: 1, Status: "active", Enabled: true, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)}
	if err := deviceA.database.CreateSkill(ctx, rebuilt); err != nil {
		t.Fatal(err)
	}
	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	applied, err := deviceB.database.Skill(ctx, skill.ID)
	if err != nil {
		t.Fatalf("peer did not receive the resurrected skill: %v", err)
	}
	if applied.Version != 2 || applied.Instructions != "rebuilt body" {
		t.Fatalf("peer skill=%#v", applied)
	}
	if _, err := deviceB.database.SyncTombstone(ctx, syncer.TypeSkill, skill.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("peer tombstone survived the resurrect: %v", err)
	}

	// A replayed old delete must not swallow the rebuild again.
	if _, err := center.Push(ctx, "peer-replay", []centersync.Event{{
		EventID: "replay-old-delete", ObjectID: skill.ID, ObjectType: syncer.TypeSkill,
		Operation: "delete", Version: 1, SourceVersion: "1",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceB.database.Skill(ctx, skill.ID); err != nil {
		t.Fatalf("replayed old delete removed the rebuilt skill: %v", err)
	}

	// A later, higher-versioned delete still wins.
	if err := deviceA.database.DeleteSkill(ctx, skill.ID); err != nil {
		t.Fatal(err)
	}
	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceB.database.Skill(ctx, skill.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("later delete did not remove the rebuilt skill: %v", err)
	}

	// Re-delivering the resurrect at the same version must not undo the new
	// delete: only a version above the tombstone may resurrect.
	payload, err := json.Marshal(map[string]any{
		"skill":     domain.Skill{ID: skill.ID, Scope: "global", Name: "demo", Description: "demo", Instructions: "rebuilt body", Version: 2, Status: "active", Enabled: true},
		"files":     []domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: \"demo\"\ndescription: \"demo\"\n---\n\nrebuilt body\n"}},
		"resurrect": "resurrect:replay",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := center.Push(ctx, "peer-replay", []centersync.Event{{
		EventID: "replay-resurrect", ObjectID: skill.ID, ObjectType: syncer.TypeSkill,
		Operation: "upsert", Payload: payload, Version: 1, SourceVersion: "2",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceB.database.Skill(ctx, skill.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replayed resurrect undid the later delete: %v", err)
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
