package main

import (
	"encoding/json"
	"testing"

	"github.com/larksuite/oapi-sdk-go/v3/channel/normalize"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestGroupMessageMetadataAndCardText(t *testing.T) {
	raw := `{"header":{"event_id":"evt"},"event":{"sender":{"sender_id":{"open_id":"ou_app"},"sender_type":"app"},"message":{"message_id":"om_root","chat_id":"oc_test","chat_type":"group","thread_id":"omt_first","message_type":"interactive","content":"{\"header\":{\"title\":{\"content\":\"发布结果\"}},\"body\":{\"elements\":[{\"columns\":[{\"elements\":[{\"tag\":\"markdown\",\"content\":\"已构建\"},{\"tag\":\"button\",\"text\":{\"content\":\"查看详情\"},\"value\":{\"text\":\"hidden\"}}]}]}]}}"}}}`
	var event larkim.P2MessageReceiveV1
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}
	message := normalize.ParseMessage(&event)
	if message == nil {
		t.Fatal("SDK did not normalize the event")
	}
	if thread, root := inboundTopic(message); thread != "omt_first" || root != "" {
		t.Fatalf("root event thread=%q root=%q", thread, root)
	}
	if !inboundSenderIsBot(message) || inboundSenderType(message) != "app" {
		t.Fatal("app sender must be treated as a bot")
	}
	if text, _ := normalizedInboundMedia(message); text != "发布结果\n已构建\n查看详情" {
		t.Fatalf("card body=%q", text)
	}
	root := "om_root"
	event.Event.Message.RootId = &root
	if thread, root := inboundTopic(message); thread != "omt_first" || root != "om_root" {
		t.Fatalf("reply event thread=%q root=%q", thread, root)
	}
	event.Event.Message.ThreadId, event.Event.Message.RootId = nil, nil
	if thread, root := inboundTopic(message); thread != "" || root != "" {
		t.Fatalf("top-level message thread=%q root=%q", thread, root)
	}
}

func TestGroupMessageAllowlistKeepsMentions(t *testing.T) {
	message := &channeltypes.NormalizedMessage{ChatType: "group", ChatID: "oc_allowed"}
	allowlist := []string{"oc_allowed"}
	if allowInboundMessage(message, nil) || !allowInboundMessage(message, allowlist) {
		t.Fatal("ordinary messages require an enabled group")
	}
	message.ChatID = "oc_other"
	if allowInboundMessage(message, allowlist) {
		t.Fatal("ordinary message escaped the group allowlist")
	}
	message.MentionedBot = true
	if !allowInboundMessage(message, allowlist) {
		t.Fatal("core must receive explicit mentions to enforce group routing")
	}
}

func TestGroupMessageScopesAreAbsentFromRegistrationFallback(t *testing.T) {
	for _, scope := range registrationTenantScopes() {
		if scope == "im:message.group_msg" || scope == "im:message.group_msg.include_bot:read" {
			t.Fatalf("sensitive scope %q in fallback", scope)
		}
	}
}
