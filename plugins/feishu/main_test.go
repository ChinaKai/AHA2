package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkapplicationv7 "github.com/larksuite/oapi-sdk-go/v3/service/application/v7"
)

type mockHTTPClient struct {
	do func(*http.Request) (*http.Response, error)
}

func (client mockHTTPClient) Do(request *http.Request) (*http.Response, error) {
	return client.do(request)
}

func TestNewFeishuClientsInstallEventDispatcher(t *testing.T) {
	t.Parallel()
	_, wsClient := newFeishuClients(bootstrap{AppID: "cli_test", AppSecret: "secret"})
	if wsClient.EventHandler() == nil {
		t.Fatal("WebSocket client has no event dispatcher; inbound events cannot reach Channel handlers")
	}
}

func TestMenuEventConfigurationUsesWebsocketSubscription(t *testing.T) {
	t.Parallel()
	event := larkapplicationv7.NewAppConfigEventBuilder().SubscriptionType("websocket").AddEvents([]string{"application.bot.menu_v6"}).Build()
	if event.SubscriptionType == nil || *event.SubscriptionType != "websocket" {
		t.Fatalf("subscription=%v", event.SubscriptionType)
	}
}

func TestRegistrationRequestsMenuManagementScope(t *testing.T) {
	t.Parallel()
	found := false
	for _, scope := range registrationTenantScopes() {
		if scope == "application:application:patch" {
			found = true
		}
	}
	if !found {
		t.Fatal("registration must request the permission required to configure and publish the bot menu")
	}
}

func TestMenuConfigurationErrorCodeIsSafeAndActionable(t *testing.T) {
	t.Parallel()
	if got := menuConfigurationErrorCode(menuConfigFailure{stage: "ability", code: 230001}); got != "menu_ability_rejected_230001" {
		t.Fatalf("code=%q", got)
	}
	if got := menuConfigurationErrorCode(menuConfigFailure{stage: "config"}); got != "menu_config_transport_failed" {
		t.Fatalf("transport code=%q", got)
	}
	if got := menuConfigurationErrorCode(errors.New("opaque provider failure")); got != "menu_configuration_failed" {
		t.Fatalf("fallback code=%q", got)
	}
	result := menuConfigurationFailureResult(menuConfigFailure{stage: "ability", code: 210011, message: "invalid", field: "bot.bot_menus"})
	if result["stage"] != "ability" || result["field"] != "bot.bot_menus" {
		t.Fatalf("failure result=%#v", result)
	}
}

func TestMenuAbilityHasExplicitRootParentsAndI18n(t *testing.T) {
	t.Parallel()
	ability := buildFeishuMenuAbility()
	if ability.Enable == nil || !*ability.Enable || ability.BotMenuEnable == nil || !*ability.BotMenuEnable || len(ability.BotMenus) != 6 || len(ability.I18ns) != 1 {
		t.Fatalf("ability=%#v", ability)
	}
	for _, index := range []int{0, 3} {
		if ability.BotMenus[index].ParentMenuId == nil || *ability.BotMenus[index].ParentMenuId != "" {
			t.Fatalf("root menu %d must carry an explicit empty parent", index)
		}
	}
}

func TestGroupMemberDisplayNameUsesChatMembership(t *testing.T) {
	t.Parallel()
	client := lark.NewClient("app", "secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		body := `{"code":0,"msg":"success"}`
		switch request.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			body = `{"code":0,"msg":"success","tenant_access_token":"tenant-token","expire":7200}`
		case "/open-apis/im/v1/chats/chat-test/members":
			body = `{"code":0,"msg":"success","data":{"items":[{"member_id_type":"open_id","member_id":"sender-open","name":"群成员张三"}],"has_more":false}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: header}, nil
	}}))
	if name := groupMemberDisplayName(context.Background(), client, "chat-test", "sender-open"); name != "群成员张三" {
		t.Fatalf("name=%q", name)
	}
}

func TestDeterministicUUIDAndConfirmationCard(t *testing.T) {
	first := deterministicUUID("delivery-1")
	if first != deterministicUUID("delivery-1") || first == deterministicUUID("delivery-2") || len(first) != 36 {
		t.Fatalf("uuid=%q", first)
	}
	msgType, content := renderDelivery(map[string]any{"kind": "confirmation", "action_id": "action-1", "preview": map[string]any{"effect": "route only"}})
	if msgType != "interactive" {
		t.Fatalf("msgType=%s", msgType)
	}
	var card map[string]any
	if json.Unmarshal([]byte(content), &card) != nil || card["schema"] != "2.0" {
		t.Fatalf("card=%s", content)
	}
	if delay := deliveryBackoffMS("delivery-1", 4); delay < 1 || delay > 120000 {
		t.Fatalf("backoff=%d", delay)
	}
}
