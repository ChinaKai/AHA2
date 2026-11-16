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
func TestChannelKnowledgeBindingReplacesLegacyPolicyForVaultChannel(t *testing.T) {
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
	seedRouteProject(t, database, "project_other")
	// Model A: a binding only targets a ready space.
	if err := database.SetProjectKnowledgeState(ctx, "project_other", domain.KnowledgeStateReady); err != nil {
		t.Fatal(err)
	}
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
	// A vault-bound channel host refuses to write the legacy per-entry policy.
	response = channelJSON(t, client, http.MethodPut, policyURL, map[string]any{
		"endpoint": "group_digital_human", "scope_mode": "selected",
		"grants": []map[string]any{{"knowledge_entry_id": "knowledge_root_project_other", "grant_scope": "subtree"}},
	}, map[string]string{"X-CSRF-Token": csrf, "If-Match": `"0"`})
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("legacy policy write on a vault host status=%d", response.StatusCode)
	}

	bindingURL := server.URL + "/api/v1/channel-instances/" + instance.ID + "/knowledge-bindings"
	response = channelJSON(t, client, http.MethodPut, bindingURL, map[string]any{
		"endpoint":       "group_digital_human",
		"include_global": true,
		"bindings":       []map[string]any{{"space_project_id": "project_other", "access_mode": "read"}},
	}, map[string]string{"X-CSRF-Token": csrf})
	var bindingPayload map[string]any
	decodeResponse(t, response, &bindingPayload)
	if response.StatusCode != http.StatusOK || len(bindingPayload["bindings"].([]any)) != 1 || bindingPayload["include_global"] != true {
		t.Fatalf("binding write status=%d payload=%#v", response.StatusCode, bindingPayload)
	}
	resolved, err := database.ResolveChannelKnowledgeBindings(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman)
	if err != nil || len(resolved) != 1 || resolved[0].SpaceProject != "project_other" {
		t.Fatalf("resolved binding = %#v err=%v", resolved, err)
	}
	if enabled, err := database.ResolveChannelKnowledgeGlobalBinding(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman); err != nil || !enabled {
		t.Fatalf("resolved include_global = %v err=%v", enabled, err)
	}
	response = requestJSON(t, client, http.MethodGet, bindingURL+"?endpoint=group_digital_human", nil, "")
	var listed map[string]any
	decodeResponse(t, response, &listed)
	if response.StatusCode != http.StatusOK || listed["include_global"] != true || len(listed["bindings"].([]any)) != 1 {
		t.Fatalf("binding list status=%d payload=%#v", response.StatusCode, listed)
	}
	// Omitting include_global preserves the stored value instead of clearing it.
	response = channelJSON(t, client, http.MethodPut, bindingURL, map[string]any{
		"endpoint": "group_digital_human",
		"bindings": []map[string]any{{"space_project_id": "project_other", "access_mode": "read"}},
	}, map[string]string{"X-CSRF-Token": csrf})
	var preserved map[string]any
	decodeResponse(t, response, &preserved)
	if response.StatusCode != http.StatusOK || preserved["include_global"] != true {
		t.Fatalf("omitted include_global status=%d payload=%#v", response.StatusCode, preserved)
	}
	// The atomic group save writes the endpoint selection and clears both
	// instance defaults in one request; global-only must not fall back.
	response = channelJSON(t, client, http.MethodPut, bindingURL, map[string]any{
		"endpoint": "", "include_global": true,
		"bindings": []map[string]any{{"space_project_id": "project_other", "access_mode": "read"}},
	}, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("seed instance selection status=%d", response.StatusCode)
	}
	response = channelJSON(t, client, http.MethodPut, bindingURL+"/group", map[string]any{
		"include_global": true, "bindings": []any{},
	}, map[string]string{"X-CSRF-Token": csrf})
	var atomicPayload map[string]any
	decodeResponse(t, response, &atomicPayload)
	if response.StatusCode != http.StatusOK || atomicPayload["include_global"] != true || len(atomicPayload["bindings"].([]any)) != 0 {
		t.Fatalf("group atomic save status=%d payload=%#v", response.StatusCode, atomicPayload)
	}
	if instanceBindings, err := database.ListKnowledgeBindings(ctx, domain.KnowledgeBindingChannelInstance, instance.ID); err != nil || len(instanceBindings) != 0 {
		t.Fatalf("atomic save kept instance spaces = %#v err=%v", instanceBindings, err)
	}
	if _, found, err := database.KnowledgeGlobalBinding(ctx, domain.KnowledgeBindingChannelInstance, instance.ID); err != nil || found {
		t.Fatalf("atomic save kept instance global row: found=%v err=%v", found, err)
	}
	if selection, err := database.ResolveChannelKnowledgeBindingSelection(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman); err != nil || len(selection.Bindings) != 0 || !selection.IncludeGlobal {
		t.Fatalf("atomic global-only selection = %#v err=%v", selection, err)
	}
	// Replacing with an empty list clears the binding and global together; there
	// is no implicit host-space or global fallback.
	response = channelJSON(t, client, http.MethodPut, bindingURL, map[string]any{
		"endpoint": "group_digital_human", "include_global": false, "bindings": []any{},
	}, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("clear bindings status=%d", response.StatusCode)
	}
	resolved, err = database.ResolveChannelKnowledgeBindings(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman)
	if err != nil || len(resolved) != 0 {
		t.Fatalf("cleared binding = %#v err=%v", resolved, err)
	}
	if enabled, err := database.ResolveChannelKnowledgeGlobalBinding(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman); err != nil || enabled {
		t.Fatalf("cleared include_global = %v err=%v", enabled, err)
	}
}
