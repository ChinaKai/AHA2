package domain

import "slices"

const FeishuFullGroupPermission = "group_message_read"

func FeishuFullGroupMessagesEnabled(config map[string]any) bool {
	return channelConfigHasValue(config["channel_permission_ids"], FeishuFullGroupPermission)
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
