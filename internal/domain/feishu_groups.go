package domain

import "slices"

const (
	FeishuFullGroupPermission = "group_message_read"
	FeishuGroupAllowlistKey   = "feishu_group_message_allowlist"
)

func FeishuFullGroupMessagesEnabled(config map[string]any) bool {
	return channelConfigHasValue(config["channel_permission_ids"], FeishuFullGroupPermission)
}

func FeishuGroupMessageAllowed(config map[string]any, chatID string, mentioned bool) bool {
	return mentioned || (FeishuFullGroupMessagesEnabled(config) &&
		channelConfigHasValue(config[FeishuGroupAllowlistKey], chatID))
}

func channelConfigHasValue(value any, expected string) bool {
	switch list := value.(type) {
	case []string:
		return slices.Contains(list, expected)
	case []any:
		return slices.ContainsFunc(list, func(item any) bool {
			text, ok := item.(string)
			return ok && text == expected
		})
	}
	return false
}
