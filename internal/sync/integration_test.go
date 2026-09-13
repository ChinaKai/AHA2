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

func TestChannelBotIdentityDoesNotEnterProfileSync(t *testing.T) {
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
		if err := db.PutSyncSettings(ctx, domain.SyncSettings{
			Scope: "default", Enabled: true, Endpoint: server.URL,
			DeviceID: id, DeviceName: id, IntervalSeconds: 60,
		}); err != nil {
			t.Fatal(err)
		}
		if err := center.PutDeviceToken(ctx, id, token); err != nil {
			t.Fatal(err)
		}
		return db, secretStore
	}
	addChannelBot := func(db *store.Store, suffix, name, openID string) {
		now := time.Now().UTC()
		owner := domain.Owner{ID: "owner-" + suffix, Username: "owner-" + suffix, PasswordHash: "test", CreatedAt: now}
		if err := db.CreateOwner(ctx, owner); err != nil {
			t.Fatal(err)
		}
		project := domain.Project{
			ID: "channel-project-" + suffix, Name: "Channel " + suffix, ProjectType: "channel",
			DefaultWorkspaceID: "channel-workspace-" + suffix, KnowledgePolicy: "enabled",
			CreatedAt: now, UpdatedAt: now,
		}
		workspace := domain.Workspace{
			ID: project.DefaultWorkspaceID, ProjectID: project.ID, Name: project.Name,
			Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
			CreatedAt: now, UpdatedAt: now,
		}
		if err := db.CreateProject(ctx, project); err != nil {
			t.Fatal(err)
		}
		if err := db.CreateWorkspace(ctx, workspace); err != nil {
			t.Fatal(err)
		}
		plugin := domain.ChannelPlugin{
			ID: "feishu-" + suffix, ProviderKey: "feishu", DisplayName: "Feishu",
			ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1,
			InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
		}
		if err := db.UpsertChannelPlugin(ctx, plugin); err != nil {
			t.Fatal(err)
		}
		if err := db.CreateChannelInstance(ctx, domain.ChannelInstance{
			ID: "channel-instance-" + suffix, PluginID: plugin.ID, OwnerID: owner.ID,
			RuntimeDeviceID: suffix, Name: name, Status: "ready", Revision: 1,
			HostProjectID: project.ID, HostWorkspaceID: workspace.ID,
			Config: map[string]any{
				"runtime_bot_open_id":               openID,
				"runtime_bot_display_name_override": name,
				"runtime_bot_provider_display_name": name,
			},
			CreatedAt: now, UpdatedAt: now,
		}, nil); err != nil {
			t.Fatal(err)
		}
	}

	source, sourceSecrets := makeDevice("device-a", "token-a")
	destination, destinationSecrets := makeDevice("device-b", "token-b")
	addChannelBot(source, "a", "AHA A", "bot-open-a")

	objects, err := syncer.ExportBusinessObjectsForDevice(ctx, source, "device-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		if object.Type == "channel_bot_identity" {
			t.Fatalf("channel bot identity leaked into Profile Sync: %#v", object)
		}
	}
	if err := (syncer.Runner{Store: source, Secrets: sourceSecrets, TokenRef: syncer.DefaultTokenRef}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := (syncer.Runner{Store: destination, Secrets: destinationSecrets, TokenRef: syncer.DefaultTokenRef}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if instances, err := destination.ChannelInstances(ctx, ""); err != nil || len(instances) != 0 {
		t.Fatalf("channel runtime state crossed devices: %#v err=%v", instances, err)
	}
}
