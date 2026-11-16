package channel

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

// A channel host project created while the vault is the read source must not
// seed the legacy knowledge root or its channel policies. The same helper also
// proves the fallback: disabling the vault restores the historical behavior.
func TestCreateInstanceSkipsLegacyKnowledgeRootsWhenVaultReads(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	if err := database.CreateOwner(ctx, domain.Owner{ID: "owner-channel-vault", Username: "owner", PasswordHash: "hash", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertChannelPlugin(ctx, domain.ChannelPlugin{
		ID: "plugin-feishu-vault", ProviderKey: "feishu", DisplayName: "飞书", ManifestVersion: 1,
		PackageVersion: "1.0.0", ProtocolMin: 1, ProtocolMax: 1, InstallState: "installed",
		Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	vaultConfig := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(vaultConfig, knowledgevault.InitOptions{VaultName: "Channel Vault", Commit: false}); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(vaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	application := app.NewService(database, nil, app.StubExecutor{})
	application.SetKnowledgeVault(manager, true)
	service := New(Config{Store: database, App: application, DataDir: dataDir, RuntimeDevice: "device-local"})

	instance, err := service.CreateInstance(ctx, "owner-channel-vault", "plugin-feishu-vault", "vault channel", "vault-request")
	if err != nil {
		t.Fatal(err)
	}
	assertNoLegacyChannelKnowledge(t, ctx, database, instance)
	assertNoVaultChannelSpace(t, vaultConfig, instance.HostProjectID)

	// Replaying the same idempotency key runs the re-ensure branch too; it must
	// not create the root or policies either.
	repeated, err := service.CreateInstance(ctx, "owner-channel-vault", "plugin-feishu-vault", "vault channel", "vault-request")
	if err != nil || repeated.ID != instance.ID {
		t.Fatalf("idempotent instance=%#v err=%v", repeated, err)
	}
	assertNoLegacyChannelKnowledge(t, ctx, database, repeated)
	assertNoVaultChannelSpace(t, vaultConfig, repeated.HostProjectID)

	// Fallback: with the switch off the historical root and per-endpoint
	// policies come back, so an unbound/read-fallback deployment is unchanged.
	if err := manager.SetEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	fallback, err := service.CreateInstance(ctx, "owner-channel-vault", "plugin-feishu-vault", "legacy channel", "legacy-request")
	if err != nil {
		t.Fatal(err)
	}
	indexes, err := database.KnowledgeIndexes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := indexes[fallback.HostProjectID]; !found {
		t.Fatal("legacy fallback did not create the channel knowledge root")
	}
	policies, err := database.ChannelKnowledgePolicies(ctx, fallback.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 2 {
		t.Fatalf("fallback channel policies=%#v, want two endpoints", policies)
	}
}

func assertNoVaultChannelSpace(t *testing.T, config knowledgevault.Config, projectID string) {
	t.Helper()
	manifest, err := knowledgevault.ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	slug := manifest.Projects[projectID]
	if slug != "" {
		t.Fatalf("channel host %s was provisioned a vault space: %#v", projectID, manifest.Projects)
	}
}

func assertNoLegacyChannelKnowledge(t *testing.T, ctx context.Context, database *store.Store, instance domain.ChannelInstance) {
	t.Helper()
	indexes, err := database.KnowledgeIndexes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if root, found := indexes[instance.HostProjectID]; found {
		t.Fatalf("vault-backed channel project created legacy root %#v", root)
	}
	policies, err := database.ChannelKnowledgePolicies(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 0 {
		t.Fatalf("vault-backed channel instance created legacy policies: %#v", policies)
	}
}
