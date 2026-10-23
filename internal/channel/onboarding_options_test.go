package channel

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestResolveFeishuOnboardingSelectionAddsRequiredAndMenuCardScopes(t *testing.T) {
	t.Parallel()
	instance := domain.ChannelInstance{CredentialConfigured: true, Config: map[string]any{
		"channel_permission_ids": []any{"messages_core", "cards"},
	}}
	plugin := domain.ChannelPlugin{ProviderKey: "feishu"}
	selection, err := resolveOnboardingSelection(
		instance, plugin,
		[]string{"messages_core", "media"},
	)
	if err != nil {
		t.Fatal(err)
	}
	permissions := sliceSet(selection.PermissionIDs)
	if !permissions["messages_core"] || !permissions["media"] || !permissions["cards"] {
		t.Fatalf("permissions=%#v", selection.PermissionIDs)
	}
	if permissions["group_messages"] || permissions["chat_members"] || permissions["app_menu_maintenance"] {
		t.Fatalf("unselected permissions leaked: %#v", selection.PermissionIDs)
	}
	scopes := sliceSet(selection.TenantScopes)
	for _, required := range []string{
		"im:message.p2p_msg:readonly", "im:message:readonly", "im:message:send_as_bot",
		"application:bot.basic_info:read", "im:resource", "cardkit:card:write",
	} {
		if !scopes[required] {
			t.Fatalf("required scope %s missing from %#v", required, selection.TenantScopes)
		}
	}
	if scopes["im:message.group_at_msg:readonly"] {
		t.Fatalf("group scope was not selected: %#v", selection.TenantScopes)
	}
	if got := strings.Join(selection.RequestScopes, ","); got != "im:resource" {
		t.Fatalf("only the new media scope should be requested: %#v", selection.RequestScopes)
	}
}

func TestResolveFeishuOnboardingSelectionRejectsUnknownOptions(t *testing.T) {
	t.Parallel()
	instance := domain.ChannelInstance{CredentialConfigured: true}
	plugin := domain.ChannelPlugin{ProviderKey: "feishu"}
	if _, err := resolveOnboardingSelection(instance, plugin, []string{"unknown"}); err == nil {
		t.Fatal("unknown permission was accepted")
	}
}

func TestFeishuPermissionCatalogExcludesSensitiveGroupHistory(t *testing.T) {
	t.Parallel()
	for _, definition := range feishuPermissionCatalog {
		if definition.ID == "group_message_read" || sliceSet(definition.Scopes)["im:message.group_msg"] {
			t.Fatalf("sensitive group history scope must remain disabled: %#v", definition)
		}
	}
}

func TestResolveFeishuCloudDocumentReadScopes(t *testing.T) {
	t.Parallel()
	selection, err := resolveOnboardingSelection(
		domain.ChannelInstance{CredentialConfigured: true, Config: map[string]any{
			"channel_permission_ids": []any{"messages_core", "cards"},
		}},
		domain.ChannelPlugin{ProviderKey: "feishu"},
		[]string{"messages_core", "cloud_docs_read"},
	)
	if err != nil {
		t.Fatal(err)
	}
	scopes := sliceSet(selection.TenantScopes)
	for _, scope := range []string{"wiki:node:read", "docx:document:readonly"} {
		if !scopes[scope] {
			t.Fatalf("cloud document scope %s missing from %#v", scope, selection.TenantScopes)
		}
	}
	if got := strings.Join(selection.RequestScopes, ","); got != "wiki:node:read,docx:document:readonly" {
		t.Fatalf("cloud document request scopes=%#v", selection.RequestScopes)
	}
}

func TestResolveFeishuOnboardingSelectionAllowsNoPermissionChanges(t *testing.T) {
	t.Parallel()
	permissionIDs := []string{"messages_core", "group_messages", "cards"}
	selection, err := resolveOnboardingSelection(
		domain.ChannelInstance{CredentialConfigured: true, Config: map[string]any{
			"channel_permission_ids": []any{"messages_core", "group_messages", "cards"},
		}},
		domain.ChannelPlugin{ProviderKey: "feishu"},
		permissionIDs,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.RequestScopes) != 0 {
		t.Fatalf("no-op selection unexpectedly requests scopes: %#v", selection.RequestScopes)
	}
	if got := strings.Join(selection.PermissionIDs, ","); got != strings.Join(permissionIDs, ",") {
		t.Fatalf("no-op selection lost permission IDs: %#v", selection.PermissionIDs)
	}
}

func TestStoredPermissionListMakesFutureCatalogEntriesOptIn(t *testing.T) {
	t.Parallel()
	config := map[string]any{"channel_permission_ids": []any{"messages_core", "media"}}
	enabled := configuredPermissionSet(config)
	if !enabled["messages_core"] || !enabled["media"] {
		t.Fatalf("stored permissions=%#v", enabled)
	}
	if enabled["group_messages"] || enabled["cloud_docs_read"] || !enabled["cards"] || enabled["app_menu_maintenance"] {
		t.Fatalf("future/unselected permissions defaulted on: %#v", enabled)
	}
}

func TestMissingPermissionListTreatsOnlyLegacyCatalogAsEnabled(t *testing.T) {
	t.Parallel()
	enabled := configuredPermissionSet(nil)
	for _, id := range legacyFeishuPermissionIDs() {
		if !enabled[id] {
			t.Fatalf("legacy permission %s was disabled: %#v", id, enabled)
		}
	}
	if enabled["cloud_docs_read"] {
		t.Fatalf("new permission cloud_docs_read was implicitly enabled: %#v", enabled)
	}
}

func TestNewAppRequiresMenuMaintenancePermission(t *testing.T) {
	t.Parallel()
	instance := domain.ChannelInstance{}
	plugin := domain.ChannelPlugin{ProviderKey: "feishu"}
	selection, err := resolveOnboardingSelection(instance, plugin, []string{"messages_core"})
	if err != nil {
		t.Fatal(err)
	}
	if !sliceSet(selection.PermissionIDs)["app_menu_maintenance"] {
		t.Fatalf("new app permission ids=%#v", selection.PermissionIDs)
	}
}
