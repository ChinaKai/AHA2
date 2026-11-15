package httpapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/channel"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

// A channel created while the vault is the read source has no policy. The API
// must report that explicitly and accept the owner's first explicit selection
// with If-Match "0" (create), then require the concrete revision afterwards.
func TestChannelKnowledgePolicyReportsUnconfiguredAndCreatesVaultSelection(t *testing.T) {
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
	manager := knowledgeRouteManager(t)
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secretStore, err := secrets.Open(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	appService.SetKnowledgeVault(manager, true)
	channels := channel.New(channel.Config{Store: database, App: appService, Secrets: secretStore, DataDir: dataDir, RuntimeDevice: "device"})
	server := httptest.NewServer(New(Config{
		Store: database, Auth: auth.NewService(database, "setup-test", time.Hour),
		App: appService, Secrets: secretStore, Channels: channels, KnowledgeVault: manager,
	}).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/auth/register", map[string]any{
		"setup_token": "setup-test", "username": "owner", "password": "correct-horse-battery",
	}, "")
	var registration map[string]any
	decodeResponse(t, response, &registration)
	csrf, _ := registration["csrf_token"].(string)
	owner, _ := registration["owner"].(map[string]any)
	ownerID, _ := owner["id"].(string)
	if csrf == "" || ownerID == "" {
		t.Fatalf("registration=%#v", registration)
	}
	// The vault manager already has project_other; the database needs the same
	// project for KnowledgeIndexSources to offer its vault root.
	seedRouteProject(t, database, "project_other")
	now := time.Now().UTC()
	plugin := domain.ChannelPlugin{
		ID: "plugin-http-vault", ProviderKey: "feishu", DisplayName: "Feishu",
		ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1,
		ExecutablePath: "/bin/true", ExecutableSHA256: "digest", InstallState: "installed", Enabled: true, Revision: 1,
		DiscoveredAt: now, UpdatedAt: now,
	}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	instance, err := channels.CreateInstance(ctx, ownerID, plugin.ID, "Vault channel", "http-vault-create")
	if err != nil {
		t.Fatal(err)
	}
	policies, err := database.ChannelKnowledgePolicies(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 0 {
		t.Fatalf("vault-era instance seeded policies: %#v", policies)
	}

	policyURL := server.URL + "/api/v1/channel-instances/" + instance.ID + "/knowledge-policy"
	response = requestJSON(t, client, http.MethodGet, policyURL, nil, "")
	var initial map[string]any
	decodeResponse(t, response, &initial)
	if response.StatusCode != http.StatusOK || len(initial["policies"].([]any)) != 0 || initial["configured"] != false {
		t.Fatalf("unconfigured policy response=%#v", initial)
	}

	response = channelJSON(t, client, http.MethodPut, policyURL, map[string]any{
		"endpoint": "group_digital_human", "scope_mode": "selected",
		"grants": []map[string]any{{"knowledge_entry_id": "knowledge_root_project_other", "grant_scope": "subtree"}},
	}, map[string]string{"X-CSRF-Token": csrf, "If-Match": `"0"`})
	var created map[string]any
	decodeResponse(t, response, &created)
	if response.StatusCode != http.StatusOK || created["configured"] != true {
		t.Fatalf("create policy response=%#v", created)
	}
	selection, err := database.ChannelKnowledgeSelection(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman)
	if err != nil {
		t.Fatal(err)
	}
	if !selection.Configured || selection.FixedIndexEntryID != "" || selection.ScopeMode != "selected" || len(selection.Grants) != 1 {
		t.Fatalf("stored selection=%#v", selection)
	}

	// Revision 0 is only for the first selection: a second create must conflict.
	response = channelJSON(t, client, http.MethodPut, policyURL, map[string]any{
		"endpoint": "group_digital_human", "scope_mode": "selected", "grants": []any{},
	}, map[string]string{"X-CSRF-Token": csrf, "If-Match": `"0"`})
	response.Body.Close()
	if response.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("second create status=%d, want %d", response.StatusCode, http.StatusPreconditionFailed)
	}
}
