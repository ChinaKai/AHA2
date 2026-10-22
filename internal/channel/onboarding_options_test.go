package channel

import (
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestResolveFeishuOnboardingSelectionAddsRequiredAndMenuCardScopes(t *testing.T) {
	t.Parallel()
	instance := domain.ChannelInstance{CredentialConfigured: true}
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
}

func TestResolveFeishuOnboardingSelectionRejectsUnknownOptions(t *testing.T) {
	t.Parallel()
	instance := domain.ChannelInstance{CredentialConfigured: true}
	plugin := domain.ChannelPlugin{ProviderKey: "feishu"}
	if _, err := resolveOnboardingSelection(instance, plugin, []string{"unknown"}); err == nil {
		t.Fatal("unknown permission was accepted")
	}
}

func TestStoredPermissionListMakesFutureCatalogEntriesOptIn(t *testing.T) {
	t.Parallel()
	config := map[string]any{"channel_permission_ids": []any{"messages_core", "media"}}
	enabled := configuredPermissionSet(config)
	if !enabled["messages_core"] || !enabled["media"] {
		t.Fatalf("stored permissions=%#v", enabled)
	}
	if enabled["group_messages"] || !enabled["cards"] || enabled["app_menu_maintenance"] {
		t.Fatalf("future/unselected permissions defaulted on: %#v", enabled)
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
