package sync_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/centersync"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func TestExistingDevicesWithDivergentKnowledgeEventuallyConverge(t *testing.T) {
	ctx := context.Background()
	center, err := centersync.Open(ctx, filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := httptest.NewServer(center.Handler())
	defer server.Close()

	type device struct {
		store   *store.Store
		secrets *secrets.FileStore
		runner  syncer.Runner
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
		return device{store: database, secrets: secretStore, runner: syncer.Runner{Store: database, Secrets: secretStore, TokenRef: syncer.DefaultTokenRef}}
	}
	deviceA := makeDevice("device-a", "token-a")
	deviceB := makeDevice("device-b", "token-b")
	now := time.Now().UTC()
	create := func(database *store.Store, id, title, body string) {
		t.Helper()
		if err := database.CreateKnowledge(ctx, domain.KnowledgeEntry{
			ID: id, Scope: "global", Slug: id, Type: "practice", Title: title, Body: body,
			Status: domain.KnowledgeVerified, Confidence: 1, Revision: 1, ContentHash: body,
			CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	create(deviceA.store, "shared", "Shared", "content from device A")
	create(deviceA.store, "only-a", "Only A", "A")
	create(deviceB.store, "shared", "Shared", "content from device B")
	create(deviceB.store, "only-b", "Only B", "B")

	for round := 0; round < 4; round++ {
		if err := deviceA.runner.RunOnce(ctx); err != nil {
			t.Fatalf("device A round %d: %v", round, err)
		}
		if err := deviceB.runner.RunOnce(ctx); err != nil {
			t.Fatalf("device B round %d: %v", round, err)
		}
	}

	snapshot := func(database *store.Store) map[string]string {
		t.Helper()
		result := map[string]string{}
		for _, id := range []string{"shared", "only-a", "only-b"} {
			entry, err := database.Knowledge(ctx, id)
			if err != nil {
				t.Fatalf("knowledge %s: %v", id, err)
			}
			result[id] = entry.Body
		}
		return result
	}
	a, b := snapshot(deviceA.store), snapshot(deviceB.store)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("devices diverged after repeated sync: A=%#v B=%#v", a, b)
	}
	if a["shared"] != "content from device B" {
		t.Fatalf("center event order was not authoritative: %#v", a)
	}
	stateA, err := deviceA.store.SyncState(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	stateB, err := deviceB.store.SyncState(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if stateA.Cursor == "" || stateA.Cursor != stateB.Cursor {
		t.Fatalf("cursors did not converge: A=%q B=%q", stateA.Cursor, stateB.Cursor)
	}
	for _, current := range []device{deviceA, deviceB} {
		pending, err := current.store.SyncOutboxCount(ctx, "default")
		if err != nil || pending != 0 {
			t.Fatalf("pending=%d err=%v", pending, err)
		}
		conflicts, err := current.store.SyncConflicts(ctx, "default")
		if err != nil || len(conflicts) != 0 {
			t.Fatalf("conflicts=%#v err=%v", conflicts, err)
		}
	}
	events, cursor, err := center.Pull(ctx, "audit", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 6 || cursor != int64(len(events)) {
		t.Fatalf("center events=%d cursor=%d", len(events), cursor)
	}
	sharedEvents := 0
	lastShared := domain.KnowledgeEntry{}
	var lastSharedVersion int64
	for _, event := range events {
		if event.ObjectID != "shared" {
			continue
		}
		sharedEvents++
		if err := json.Unmarshal(event.Payload, &lastShared); err != nil {
			t.Fatal(err)
		}
		lastSharedVersion = event.Version
	}
	if sharedEvents != 2 || lastShared.Body != "content from device B" || lastSharedVersion != 2 {
		t.Fatalf("shared event history count=%d version=%d entry=%#v", sharedEvents, lastSharedVersion, lastShared)
	}
	auditClient := syncer.Client{BaseURL: server.URL, DeviceID: "device-a", Credential: func(context.Context) (string, error) { return "token-a", nil }}
	history, err := auditClient.Pull(ctx, "default", "0", "device-a", 100)
	if err != nil || len(history.Objects) != len(events) {
		t.Fatalf("center pull history=%d err=%v", len(history.Objects), err)
	}
	deliveryIDs := map[string]bool{}
	for _, object := range history.Objects {
		if !strings.HasPrefix(object.EventID, "center:") || object.EventID == "center:" || object.EventID == object.IdempotencyKey {
			t.Fatalf("invalid center delivery identity: %#v", object)
		}
		if deliveryIDs[object.EventID] {
			t.Fatalf("duplicate center delivery identity: %s", object.EventID)
		}
		deliveryIDs[object.EventID] = true
		for _, current := range []device{deviceA, deviceB} {
			applied, err := current.store.SyncWasApplied(ctx, object.EventID)
			if err != nil || !applied {
				t.Fatalf("device missing center event %s: applied=%t err=%v", object.EventID, applied, err)
			}
		}
	}
	if err := deviceA.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := deviceB.runner.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stableEvents, stableCursor, err := center.Pull(ctx, "audit", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(stableEvents) != len(events) || stableCursor != cursor {
		t.Fatalf("converged devices generated new events: before=%d/%d after=%d/%d", len(events), cursor, len(stableEvents), stableCursor)
	}
}
