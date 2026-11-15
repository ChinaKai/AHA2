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

func TestChannelKnowledgeRootsIncludeVaultAndAcceptVaultGrant(t *testing.T) {
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
	hostRoot, err := database.EnsureKnowledgeRoot(ctx, "project", hostProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureChannelKnowledgePolicies(ctx, instance.ID, hostRoot.ID, now); err != nil {
		t.Fatal(err)
	}

	appService := app.NewService(database, nil, app.StubExecutor{})
	appService.SetKnowledgeVault(buildChannelVaultManager(t), true)
	service := New(Config{Store: database, App: appService, Secrets: &memorySecrets{}, DataDir: t.TempDir(), RuntimeDevice: "device"})
	roots, vaultRoots, err := service.channelKnowledgeSourceRoots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !roots["knowledge_root_project_pv"] || !vaultRoots["knowledge_root_project_pv"] {
		t.Fatalf("vault project root is not grantable: roots=%v vault=%v", roots, vaultRoots)
	}
	if !roots[legacyRoot.ID] || vaultRoots[legacyRoot.ID] {
		t.Fatalf("legacy project root is not grantable from the legacy source: roots=%v vault=%v", roots, vaultRoots)
	}

	policies, err := database.ChannelKnowledgePolicies(ctx, instance.ID)
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
	if _, err := service.ReplaceOwnerKnowledgeGrants(ctx, owner.ID, instance.ID, domain.ChannelEndpointGroupDigitalHuman, "selected", revision, []KnowledgeGrantInput{
		{KnowledgeEntryID: "knowledge_root_project_pv", GrantScope: "subtree"},
	}); err != nil {
		t.Fatal(err)
	}
	policies, err = service.OwnerKnowledgePolicies(ctx, owner.ID, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	granted := false
	for _, policy := range policies {
		if policy["endpoint"] != domain.ChannelEndpointGroupDigitalHuman {
			continue
		}
		for _, grant := range policy["grants"].([]map[string]string) {
			if grant["knowledge_entry_id"] == "knowledge_root_project_pv" {
				granted = true
			}
		}
	}
	if !granted {
		t.Fatalf("vault root grant was not stored: %#v", policies)
	}
}
