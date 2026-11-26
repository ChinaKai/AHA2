package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/channel/normalize"
	"github.com/larksuite/oapi-sdk-go/v3/channel/safety"
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

func TestInboundChannelOptionsDisableSDKBatchingAndSetGroupPolicy(t *testing.T) {
	for _, tt := range []struct {
		name      string
		allowlist []string
	}{
		{name: "mention only"},
		{name: "full reception", allowlist: []string{"oc_allowed"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := channeltypes.DefaultChannelConfig()
			for _, option := range inboundChannelOptions(tt.allowlist) {
				option(&cfg)
			}
			if cfg.Safety.Batch.DelayMs != 0 {
				t.Errorf("SDK batch delay = %s, want disabled", cfg.Safety.Batch.DelayMs)
			}
			full := len(tt.allowlist) > 0
			if cfg.Policy.RequireMention == nil || *cfg.Policy.RequireMention != !full {
				t.Error("RequireMention must be true only when the effective allowlist is empty")
			}

			gate := safety.NewPolicyGate(&cfg.Policy, nil)
			for _, message := range []struct {
				name       string
				chat       string
				mentionAll bool
				mentionBot bool
				want       bool
			}{
				{name: "ordinary", chat: "oc_allowed", want: full},
				{name: "mention all", chat: "oc_allowed", mentionAll: true, want: full},
				{name: "mention all and bot", chat: "oc_allowed", mentionAll: true, mentionBot: true, want: full},
				{name: "mention all outside", chat: "oc_other", mentionAll: true},
				{name: "explicit mention outside", chat: "oc_other", mentionBot: true, want: true},
			} {
				event := &channeltypes.NormalizedMessage{
					ChatType: "group", ChatID: message.chat, MentionAll: message.mentionAll, MentionedBot: message.mentionBot,
				}
				decision := gate.Evaluate(event)
				if got := decision.Allowed && allowInboundMessage(event, tt.allowlist); got != message.want {
					t.Errorf("%s forwarded = %v, want %v (SDK reason %q)", message.name, got, message.want, decision.Reason)
				}
			}
		})
	}
}

func TestInboundMessageRejectsUnknownChatType(t *testing.T) {
	if allowInboundMessage(&channeltypes.NormalizedMessage{ChatType: "unsupported", MentionedBot: true}, []string{"oc_allowed"}) {
		t.Fatal("an unsupported chat type passed the plugin gate")
	}
	if !allowInboundMessage(&channeltypes.NormalizedMessage{ChatType: "p2p"}, nil) {
		t.Fatal("private chat must pass the plugin gate")
	}
}

func TestInboundSenderMissingTypeAndNonMentionedBot(t *testing.T) {
	raw := `{"header":{"event_id":"evt"},"event":{"sender":{"sender_id":{"open_id":"bot_sender"}},"message":{"chat_id":"oc_allowed","chat_type":"group","message_id":"om_bot","thread_id":"omt_bot","message_type":"text","content":"{\"text\":\"状态\"}"}}}`
	var event larkim.P2MessageReceiveV1
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}
	message := normalize.ParseMessage(&event)
	if message == nil || inboundSenderType(message) != "unknown" || !inboundSenderIsBot(message) {
		t.Fatal("an event without sender_type must retain the conservative bot classification")
	}
	if !inboundSenderIsBot(&channeltypes.NormalizedMessage{}) {
		t.Fatal("an event without raw sender metadata must also be treated as a bot")
	}

	kind := "bot"
	event.Event.Sender.SenderType = &kind
	allowlist := []string{"oc_allowed"}
	cfg := channeltypes.DefaultChannelConfig()
	for _, option := range inboundChannelOptions(allowlist) {
		option(&cfg)
	}
	message = normalize.ParseMessage(&event)
	if message == nil || message.MentionedBot || !inboundSenderIsBot(message) ||
		!safety.NewPolicyGate(&cfg.Policy, nil).Evaluate(message).Allowed || !allowInboundMessage(message, allowlist) {
		t.Fatal("a non-mention bot topic event in an allowlisted group must reach core")
	}
	message.ChatID = "oc_other"
	if allowInboundMessage(message, allowlist) {
		t.Fatal("a non-mention bot topic event outside the allowlist reached core")
	}
}

func TestBotDisplayNameLookupFailureIsBrieflyCached(t *testing.T) {
	const senderID = "bot_name_lookup_failure"
	const key = "user:" + senderID
	t.Cleanup(func() { displayNameCache.Delete(key) })
	requests := 0
	client := lark.NewClient("sender-name-cache", "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "/auth/"):
			return mediaTestResponse(200, `{"code":0,"tenant_access_token":"token","expire":3600}`), nil
		case strings.HasSuffix(request.URL.Path, "/om_missing_name"):
			requests++
			if requests == 1 {
				return mediaTestResponse(200, `{"code":230008}`), nil
			}
			return mediaTestResponse(200, `{"code":0,"data":{"items":[{"message_id":"om_missing_name","sender":{"sender_name":"联调机器人"}}]}}`), nil
		default:
			return mediaTestResponse(200, `{"code":0,"data":{"name":"测试群"}}`), nil
		}
	}}))
	message := &channeltypes.NormalizedMessage{ChatType: "group", ChatID: "oc_bot_name_lookup_failure", MessageID: "om_missing_name", UserID: senderID}
	t.Cleanup(func() { displayNameCache.Delete("chat:" + message.ChatID) })
	for range 2 {
		if _, name := feishuDisplayNames(context.Background(), client, message); name != "" {
			t.Fatalf("failed lookup returned name %q", name)
		}
	}
	if requests != 1 {
		t.Fatalf("sender lookup made %d requests within the short failure cache window", requests)
	}
	displayNameCache.Store(key, cachedDisplayName{expiresAt: time.Now().Add(-time.Second)})
	if _, name := feishuDisplayNames(context.Background(), client, message); name != "联调机器人" || requests != 2 {
		t.Fatalf("expired lookup did not recover: name=%q requests=%d", name, requests)
	}
}

func TestGroupMessageAllowlistDoesNotRequireTaskBinding(t *testing.T) {
	// The bootstrap knows which groups have full reception enabled and carries
	// no Task routing information, including for this unbound group.
	message := &channeltypes.NormalizedMessage{ChatType: "group", ChatID: "oc_unbound"}
	allowlist := []string{"oc_unbound"}
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

func TestListBotGroupsUsesTenantAndPaginates(t *testing.T) {
	var pages int
	client := lark.NewClient("bot-group-list", "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/auth/") {
			return mediaTestResponse(200, `{"code":0,"tenant_access_token":"tenant-token","expire":3600}`), nil
		}
		if request.Method != http.MethodGet || request.URL.Path != "/open-apis/im/v1/chats" ||
			request.Header.Get("Authorization") != "Bearer tenant-token" || request.URL.Query().Get("types") != "group" {
			t.Fatalf("expected tenant group-list request, got %s %s", request.Method, request.URL)
		}
		pages++
		switch request.URL.Query().Get("page_token") {
		case "":
			return mediaTestResponse(200, `{"code":0,"data":{"items":[{"chat_id":"oc_unbound","name":"群一"},{"chat_id":"oc_private","chat_mode":"p2p"}],"has_more":true,"page_token":"next"}}`), nil
		case "next":
			return mediaTestResponse(200, `{"code":0,"data":{"items":[{"chat_id":"oc_topic","name":"话题群","chat_mode":"topic"}],"has_more":false}}`), nil
		default:
			t.Fatal("unexpected page token")
			return nil, nil
		}
	}}))
	groups, err := listBotGroups(context.Background(), client)
	if err != nil || pages != 2 || len(groups) != 2 || groups[0] != (feishuBotGroup{"oc_unbound", "群一"}) || groups[1] != (feishuBotGroup{"oc_topic", "话题群"}) {
		t.Fatalf("groups=%+v, pages=%d, err=%v", groups, pages, err)
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

func TestInteractiveCardEventDisplayText(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "received card with rows of elements",
			raw:  `{"title":"工具告警","elements":[[{"tag":"img","image_key":"img_hidden"},{"tag":"text","text":"请升级至最新版本客户端，以查看内容"}]]}`,
			want: "工具告警\n请升级至最新版本客户端，以查看内容",
		},
		{
			name: "form and table",
			raw: `{"body":{"elements":[
				{"tag":"div","text":{"content":"处理结果"},"fields":[{"text":{"content":"已完成"}}]},
				{"tag":"form","name":"form_1","elements":[
					{"tag":"input","name":"reason_input","label":{"content":"原因"},"value":"private input"},
					{"tag":"button","name":"submit_btn","text":{"content":"提交"},"value":{"text":"hidden action"}}]},
				{"tag":"table",
				 "columns":[{"name":"customer_id","display_name":"客户"},{"name":"status_id","display_name":"状态"}],
				 "rows":[{"status_id":{"content":"通过"},"customer_id":"张三","private_key":"hidden column"}]}
			]}}`,
			want: "处理结果\n已完成\n原因\n提交\n客户 | 状态\n张三 | 通过",
		},
		{name: "malformed content falls back to the SDK placeholder", raw: `{"title":`, want: "[unsupported message]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			payload := `{"header":{"event_id":"evt"},"event":{"sender":{"sender_id":{"open_id":"ou_sender"},"sender_type":"user"},"message":{"chat_id":"oc_allowed","chat_type":"group","message_id":"om_card","message_type":"interactive","content":` + string(raw) + `}}}`
			var event larkim.P2MessageReceiveV1
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				t.Fatal(err)
			}
			message := normalize.ParseMessage(&event)
			if message == nil {
				t.Fatal("SDK did not normalize the event")
			}
			if got, _ := normalizedInboundMedia(message); got != tt.want {
				t.Fatalf("card text = %q, want %q", got, tt.want)
			}
		})
	}
}
