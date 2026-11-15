package channel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

const channelVaultTestProjectID = "project_channel_vault"

func buildChannelVaultManager(t *testing.T) *knowledgevault.Manager {
	t.Helper()
	ctx := context.Background()
	config := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "Channel"}); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"global/index.md":      "---\nid: knowledge_root_global\ntitle: Global\nscope: global\ntype: navigation\nstatus: verified\nis_index: true\n---\nGlobal.\n",
		"projects/pv/index.md": "---\nid: knowledge_root_project_pv\ntitle: Vault Project\nscope: project\nproject_id: " + channelVaultTestProjectID + "\ntype: navigation\nstatus: verified\nis_index: true\n---\nProject.\n",
		"projects/pv/doc.md":   "---\nid: knowledge_pv_doc\ntitle: Vault Doc\nscope: project\nproject_id: " + channelVaultTestProjectID + "\ntype: practice\nstatus: verified\n---\nVault body.\n",
	}
	for relative, content := range files {
		target := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index, err := knowledgevault.OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

type channelVaultFixture struct {
	service       *Service
	database      *store.Store
	owner         domain.Owner
	instance      domain.ChannelInstance
	hostProject   domain.Project
	vaultRootID   string
	legacyRoot    domain.KnowledgeEntry
	legacyProject domain.Project
}

// newChannelVaultFixture builds a vault-routed project and a legacy project
// behind one channel instance. seedPolicies mirrors the legacy CreateInstance
// behavior; without it the instance has no channel policy, like a vault-era
// instance created after task-488.
func newChannelVaultFixture(t *testing.T, seedPolicies bool) *channelVaultFixture {
	t.Helper()
	previous, existed := os.LookupEnv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS")
	if err := os.Unsetenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS", previous)
		} else {
			_ = os.Unsetenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS")
		}
	})
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	owner := domain.Owner{ID: "owner-vault-channel", Username: "owner", PasswordHash: "hash", CreatedAt: now}
	if err := database.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{
		ID: "plugin-vault-channel", ProviderKey: "feishu", DisplayName: "Feishu",
		ManifestVersion: 1, PackageVersion: "1.0.0", ProtocolMin: 1, ProtocolMax: 1,
		ExecutablePath: "/bin/true", ExecutableSHA256: "digest", InstallState: "installed",
		Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	vaultProject := domain.Project{ID: channelVaultTestProjectID, Name: "Vault", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	legacyProject := domain.Project{ID: "project_channel_legacy", Name: "Legacy", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	for _, project := range []domain.Project{vaultProject, legacyProject} {
		if err := database.CreateProject(ctx, project); err != nil {
			t.Fatal(err)
		}
	}
	legacyRoot, err := database.EnsureKnowledgeRoot(ctx, "project", legacyProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	hostProject := domain.Project{ID: "project_vault_channel_host", Name: "Channel", ProjectType: "channel", DefaultWorkspaceID: "workspace-vault-channel-host", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	hostWorkspace := domain.Workspace{ID: "workspace-vault-channel-host", ProjectID: hostProject.ID, Name: "host", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto", CreatedAt: now, UpdatedAt: now}
	instance := domain.ChannelInstance{
		ID: "instance-vault-channel", PluginID: plugin.ID, OwnerID: owner.ID, RuntimeDeviceID: "device",
		Name: "feishu", Status: "ready", Revision: 1, HostProjectID: hostProject.ID,
		HostWorkspaceID: hostWorkspace.ID, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	endpoints := []domain.ChannelEndpoint{
		{ID: "endpoint-vault-dm", InstanceID: instance.ID, Kind: domain.ChannelEndpointAssistantDM, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now},
		{ID: "endpoint-vault-group", InstanceID: instance.ID, Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now},
	}
	if err := database.CreateChannelInstanceWithHost(ctx, hostProject, hostWorkspace, instance, endpoints); err != nil {
		t.Fatal(err)
	}
	if seedPolicies {
		hostRoot, err := database.EnsureKnowledgeRoot(ctx, "project", hostProject.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := database.EnsureChannelKnowledgePolicies(ctx, instance.ID, hostRoot.ID, now); err != nil {
			t.Fatal(err)
		}
	}

	appService := app.NewService(database, nil, app.StubExecutor{})
	appService.SetKnowledgeVault(buildChannelVaultManager(t), true)
	service := New(Config{Store: database, App: appService, Secrets: &memorySecrets{}, DataDir: t.TempDir(), RuntimeDevice: "device"})
	return &channelVaultFixture{
		service: service, database: database, owner: owner, instance: instance,
		hostProject: hostProject, vaultRootID: "knowledge_root_project_pv",
		legacyRoot: legacyRoot, legacyProject: legacyProject,
	}
}

func TestChannelKnowledgeRootsIncludeVaultAndAcceptVaultGrant(t *testing.T) {
	fixture := newChannelVaultFixture(t, true)
	ctx := context.Background()
	roots, vaultRoots, err := fixture.service.channelKnowledgeSourceRoots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !roots[fixture.vaultRootID] || !vaultRoots[fixture.vaultRootID] {
		t.Fatalf("vault project root is not grantable: roots=%v vault=%v", roots, vaultRoots)
	}
	if !roots[fixture.legacyRoot.ID] || vaultRoots[fixture.legacyRoot.ID] {
		t.Fatalf("legacy project root is not grantable from the legacy source: roots=%v vault=%v", roots, vaultRoots)
	}

	policies, err := fixture.database.ChannelKnowledgePolicies(ctx, fixture.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision := 0
	for _, policy := range policies {
		if policy["endpoint"] == domain.ChannelEndpointGroupDigitalHuman {
			revision = policy["revision"].(int)
		}
	}
	if revision == 0 {
		t.Fatal("group policy missing")
	}
	if _, err := fixture.service.ReplaceOwnerKnowledgeGrants(ctx, fixture.owner.ID, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, "selected", revision, []KnowledgeGrantInput{
		{KnowledgeEntryID: fixture.vaultRootID, GrantScope: "subtree"},
	}); err != nil {
		t.Fatal(err)
	}
	policies, err = fixture.service.OwnerKnowledgePolicies(ctx, fixture.owner.ID, fixture.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	granted := false
	for _, policy := range policies {
		if policy["endpoint"] != domain.ChannelEndpointGroupDigitalHuman {
			continue
		}
		for _, grant := range policy["grants"].([]map[string]string) {
			if grant["knowledge_entry_id"] == fixture.vaultRootID {
				granted = true
			}
		}
	}
	if !granted {
		t.Fatalf("vault root grant was not stored: %#v", policies)
	}
}

// A vault-era instance has no policy and no legacy host root. The owner's first
// explicit selection creates the policy with an empty fixed index; it must not
// seed a legacy knowledge root just to make the policy row possible.
func TestChannelVaultGrantCreatesPolicyWithoutLegacyRoot(t *testing.T) {
	fixture := newChannelVaultFixture(t, false)
	ctx := context.Background()
	policies, err := fixture.database.ChannelKnowledgePolicies(ctx, fixture.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 0 {
		t.Fatalf("vault-era fixture unexpectedly has policies: %#v", policies)
	}
	if _, err := fixture.service.ReplaceOwnerKnowledgeGrants(ctx, fixture.owner.ID, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, "selected", 0, []KnowledgeGrantInput{
		{KnowledgeEntryID: fixture.vaultRootID, GrantScope: "subtree"},
	}); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.database.ChannelKnowledgeSelection(ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman)
	if err != nil {
		t.Fatal(err)
	}
	if !selection.Configured || selection.FixedIndexEntryID != "" || selection.ScopeMode != "selected" {
		t.Fatalf("explicit vault selection stored an unexpected policy: %#v", selection)
	}
	if len(selection.Grants) != 1 || selection.Grants[0].KnowledgeEntryID != fixture.vaultRootID {
		t.Fatalf("explicit vault selection grants=%#v", selection.Grants)
	}
	indexes, err := fixture.database.KnowledgeIndexes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if root, found := indexes[fixture.hostProject.ID]; found {
		t.Fatalf("explicit vault selection seeded a legacy host root: %#v", root)
	}
}
