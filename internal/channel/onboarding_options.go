package channel

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const feishuPermissionCatalogVersion = 4

type onboardingPermissionDefinition struct {
	ID          string
	Name        string
	Description string
	Required    bool
	Scopes      []string
}

var feishuPermissionCatalog = []onboardingPermissionDefinition{
	{
		ID: "messages_core", Name: "基础消息与机器人身份", Required: true,
		Description: "接收私聊消息、读取消息内容并以机器人身份回复。",
		Scopes: []string{
			"im:message.p2p_msg:readonly", "im:message:readonly", "im:message:send_as_bot",
			"application:bot.basic_info:read",
		},
	},
	{
		ID: "group_messages", Name: "群聊消息",
		Description: "接收群聊中 @机器人 的消息以及机器人之间的消息。",
		Scopes:      []string{"im:message.group_at_msg:readonly", "im:message.group_at_msg.include_bot:readonly"},
	},
	{
		ID: "chat_members", Name: "群成员与联系人",
		Description: "读取群成员列表和基础联系人信息，用于联调人选择。",
		Scopes:      []string{"im:chat:readonly", "contact:user.base:readonly"},
	},
	{
		ID: "media", Name: "图片与文件",
		Description: "下载入站图片/文件并上传出站附件。",
		Scopes:      []string{"im:resource"},
	},
	{
		ID: "cloud_docs_read", Name: "云文档只读",
		Description: "读取消息中的飞书知识库节点和新版云文档正文。",
		Scopes:      []string{"wiki:node:read", "docx:document:readonly"},
	},
	{
		ID: "cards", Name: "交互卡片与菜单", Required: true,
		Description: "发送菜单卡片并接收卡片操作；AHA 菜单始终启用，因此这是核心权限。",
		Scopes:      []string{"cardkit:card:write"},
	},
	{
		ID: "app_menu_maintenance", Name: "应用配置与菜单维护",
		Description: "允许新应用初始化飞书机器人菜单；存量应用的菜单不会被重新授权改写。",
		Scopes:      []string{"application:application:patch"},
	},
}

type onboardingSelection struct {
	PermissionIDs []string
	TenantScopes  []string
	RequestScopes []string
}

func (s *Service) OnboardingOptions(ctx context.Context, ownerID, instanceID string) (domain.ChannelOnboardingOptions, error) {
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil || instance.OwnerID != ownerID {
		if err == nil {
			err = sql.ErrNoRows
		}
		return domain.ChannelOnboardingOptions{}, err
	}
	plugin, err := s.store.ChannelPlugin(ctx, instance.PluginID)
	if err != nil {
		return domain.ChannelOnboardingOptions{}, err
	}
	if plugin.ProviderKey != "feishu" {
		return domain.ChannelOnboardingOptions{Mode: channelOnboardingMode(instance)}, nil
	}
	permissionSet := configuredPermissionSet(instance.Config)
	options := domain.ChannelOnboardingOptions{
		Mode: channelOnboardingMode(instance), PermissionCatalogVersion: feishuPermissionCatalogVersion,
	}
	for _, definition := range feishuPermissionCatalog {
		required := definition.Required || (!instance.CredentialConfigured && definition.ID == "app_menu_maintenance")
		options.Permissions = append(options.Permissions, domain.ChannelOnboardingPermission{
			ID: definition.ID, Name: definition.Name, Description: definition.Description,
			Required: required, Enabled: required || permissionSet[definition.ID],
		})
	}
	return options, nil
}

func channelOnboardingMode(instance domain.ChannelInstance) string {
	if instance.CredentialConfigured {
		return "existing_app"
	}
	return "register_app"
}

func resolveOnboardingSelection(instance domain.ChannelInstance, plugin domain.ChannelPlugin, permissionIDs []string) (onboardingSelection, error) {
	if plugin.ProviderKey != "feishu" {
		return onboardingSelection{PermissionIDs: permissionIDs}, nil
	}
	permissions := permissionIDs
	if permissions == nil {
		if instance.CredentialConfigured {
			permissions = configuredPermissionIDs(instance.Config)
		} else {
			permissions = defaultFeishuPermissionIDs()
		}
	}
	permissionOptions := map[string]onboardingPermissionDefinition{}
	for _, definition := range feishuPermissionCatalog {
		permissionOptions[definition.ID] = definition
	}
	selected := map[string]bool{}
	for _, id := range permissions {
		if _, ok := permissionOptions[id]; !ok {
			return onboardingSelection{}, fmt.Errorf("unknown channel permission %q", id)
		}
		selected[id] = true
	}
	for _, definition := range feishuPermissionCatalog {
		if definition.Required || (!instance.CredentialConfigured && definition.ID == "app_menu_maintenance") {
			selected[definition.ID] = true
		}
	}
	resolved := onboardingSelection{}
	configured := configuredPermissionSet(instance.Config)
	for _, definition := range feishuPermissionCatalog {
		if !selected[definition.ID] {
			continue
		}
		resolved.PermissionIDs = append(resolved.PermissionIDs, definition.ID)
		resolved.TenantScopes = append(resolved.TenantScopes, definition.Scopes...)
		if !instance.CredentialConfigured || !configured[definition.ID] {
			resolved.RequestScopes = append(resolved.RequestScopes, definition.Scopes...)
		}
	}
	resolved.TenantScopes = uniqueStrings(resolved.TenantScopes)
	resolved.RequestScopes = uniqueStrings(resolved.RequestScopes)
	return resolved, nil
}

func configuredPermissionSet(config map[string]any) map[string]bool {
	return sliceSet(configuredPermissionIDs(config))
}

func configuredPermissionIDs(config map[string]any) []string {
	values, present := stringListField(config, "channel_permission_ids")
	if !present {
		// Permission IDs were introduced after the original catalog. Without a
		// stored list, assume only those legacy features, never later additions
		// such as cloud-document scopes.
		values = legacyFeishuPermissionIDs()
	}
	result := sliceSet(values)
	for _, definition := range feishuPermissionCatalog {
		if definition.Required {
			result[definition.ID] = true
		}
	}
	ordered := make([]string, 0, len(result))
	for _, definition := range feishuPermissionCatalog {
		if result[definition.ID] {
			ordered = append(ordered, definition.ID)
		}
	}
	return ordered
}

func legacyFeishuPermissionIDs() []string {
	return []string{"messages_core", "group_messages", "chat_members", "media", "cards", "app_menu_maintenance"}
}

func defaultFeishuPermissionIDs() []string {
	result := make([]string, 0, len(feishuPermissionCatalog))
	for _, definition := range feishuPermissionCatalog {
		result = append(result, definition.ID)
	}
	return result
}
