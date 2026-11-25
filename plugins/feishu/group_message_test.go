package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestTopicHandshakeKeepsStrictLegacyRuntimeCompatible(t *testing.T) {
	for _, supportsTopics := range []bool{false, true} {
		t.Run(fmt.Sprintf("topics-%t", supportsTopics), func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if strings.HasSuffix(req.URL.Path, "/handshake") {
					result := map[string]any{"ok": true, "selected_protocol": "channel-runtime/v1"}
					if supportsTopics {
						result["group_topics"] = true
					}
					_ = json.NewEncoder(w).Encode(result)
					return
				}
				var payload map[string]any
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusUnprocessableEntity)
					return
				}
				for _, field := range []string{"thread_id", "root_id", "sender_type"} {
					if _, present := payload[field]; present != supportsTopics {
						t.Errorf("field %s present=%v, want %v", field, present, supportsTopics)
						w.WriteHeader(http.StatusUnprocessableEntity)
						return
					}
				}
				requests <- payload
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			ctx := context.Background()
			client := &runtimeClient{baseURL: server.URL, instanceID: "feishu", http: server.Client()}
			if err := client.handshake(ctx, bootstrap{PluginID: "feishu"}); err != nil {
				t.Fatal(err)
			}
			if err := client.inbound(ctx, map[string]any{
				"schema_version": 1, "mentioned_bot": true, "sender_is_bot": true,
				"thread_id": "omt_topic", "root_id": "om_root", "sender_type": "app",
			}); err != nil {
				t.Fatal(err)
			}
			if payload := <-requests; payload["mentioned_bot"] != true || payload["sender_is_bot"] != true {
				t.Fatalf("older runtime metadata missing: %v", payload)
			}
		})
	}
}
