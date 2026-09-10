package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

func TestExistingAppReauthorizationKeepsChannelRuntimeOnline(t *testing.T) {
	t.Parallel()
	if registrationOnly(bootstrap{Registration: true, AppID: "app", AppSecret: "secret"}) {
		t.Fatal("existing-app reauthorization would replace the live channel with a registration-only process")
	}
	if !registrationOnly(bootstrap{Registration: true}) {
		t.Fatal("new app registration requires the registration-only process")
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

func TestRegistrationRequestsInboundAndOutboundMediaScopes(t *testing.T) {
	t.Parallel()
	scopes := registrationTenantScopes()
	for _, required := range []string{"im:message:readonly", "im:resource"} {
		found := false
		for _, scope := range scopes {
			if scope == required {
				found = true
			}
		}
		if !found {
			t.Errorf("registration missing media scope %s", required)
		}
	}
}

func TestNewAppMenuInitializationRequestsMediaScopes(t *testing.T) {
	t.Parallel()
	configured := false
	client := lark.NewClient("media-scope-config", "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/auth/") {
			return mediaTestResponse(200, `{"code":0,"tenant_access_token":"sdk-test","expire":7200}`), nil
		}
		if strings.HasSuffix(request.URL.Path, "/config") {
			configured = true
			var payload struct {
				Scope struct {
					AddScopes []struct {
						ScopeName string `json:"scope_name"`
						TokenType string `json:"token_type"`
					} `json:"add_scopes"`
				} `json:"scope"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{"im:message:readonly", "im:resource"} {
				found := false
				for _, scope := range payload.Scope.AddScopes {
					if scope.ScopeName == required && scope.TokenType == "tenant" {
						found = true
					}
				}
				if !found {
					t.Errorf("existing app configuration missing tenant media scope %s", required)
				}
			}
		}
		return mediaTestResponse(200, `{"code":0}`), nil
	}}))
	if err := configureFeishuMenu(context.Background(), client, "media-scope-config"); err != nil {
		t.Fatal(err)
	}
	if !configured {
		t.Fatal("existing app configuration was not requested")
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

func TestMenuAbilityHasLocalizedNodesAndDeterministicOrdering(t *testing.T) {
	t.Parallel()
	ability := buildFeishuMenuAbility()
	if ability.Enable == nil || !*ability.Enable || ability.BotMenuEnable == nil || !*ability.BotMenuEnable || len(ability.BotMenus) != 6 || len(ability.I18ns) != 1 {
		t.Fatalf("ability=%#v", ability)
	}
	for _, index := range []int{0, 3} {
		if ability.BotMenus[index].ParentMenuId != nil {
			t.Fatalf("root menu %d must omit parent", index)
		}
	}
	for index, item := range ability.BotMenus {
		if item.Sort == nil || *item.Sort != index+1 || item.I18nName["zh_cn"] == "" {
			t.Fatalf("menu %d=%#v", index, item)
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

func TestRenderMenuFormCard(t *testing.T) {
	t.Parallel()
	msgType, content := renderDelivery(map[string]any{
		"kind": "menu_card", "title": "查询 Task", "markdown": "直接查询",
		"fields": []any{
			map[string]any{"type": "select", "name": "project_id", "label": "Project", "options": []any{map[string]any{"label": "P", "value": "p1"}}},
			map[string]any{"type": "text", "name": "keyword", "label": "关键词", "max_length": float64(100)},
			map[string]any{"type": "multiline", "name": "request", "label": "需求", "max_length": float64(4000)},
		},
		"submit":  map[string]any{"label": "查询", "value": map[string]any{"kind": "menu_control", "menu_action": "task.query"}},
		"actions": []any{map[string]any{"label": "接管 task-001", "style": "default", "value": map[string]any{"kind": "menu_control", "menu_action": "task.takeover.preview", "task_id": "task-1"}}},
	})
	if msgType != "interactive" || !strings.Contains(content, "form_submit") || !strings.Contains(content, "select_static") || !strings.Contains(content, "menu_control") || !strings.Contains(content, `"max_length":1000`) || !strings.Contains(content, `"task_id":"task-1"`) {
		t.Fatalf("menu card type=%s content=%s", msgType, content)
	}
	if got := cardFormScalar(map[string]any{"value": " project "}, 20); got != "project" {
		t.Fatalf("form scalar=%q", got)
	}
	form := normalizedCardFormValues(map[string]any{"aha_menu_control": map[string]any{"aha_menu_control.project_id": "p1", "title": "Task"}})
	if form["project_id"] != "p1" || form["title"] != "Task" {
		t.Fatalf("normalized form=%#v", form)
	}
	action := normalizedCardActionValue(map[string]any{"kind": "menu_control", "menu_action": "task.create.preview", "project_id": "p1", "untrusted": "drop"})
	if action["project_id"] != "p1" || action["untrusted"] != nil {
		t.Fatalf("normalized action=%#v", action)
	}
}

func TestFeishuMenuTimestampAcceptsSecondsAndMilliseconds(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, 9, 9, 4, 33, 32, 0, time.UTC)
	if got := feishuEventTime(want.Unix()); !got.Equal(want) {
		t.Fatalf("seconds timestamp=%s", got)
	}
	if got := feishuEventTime(want.UnixMilli()); !got.Equal(want) {
		t.Fatalf("milliseconds timestamp=%s", got)
	}
}

func TestRenderTaskStatusNotificationIsUserFacing(t *testing.T) {
	t.Parallel()
	msgType, content := renderDelivery(map[string]any{"status": "waiting_user", "task_id": "internal-id", "task_code": "task-005", "task_title": "Review"})
	if msgType != "text" || !strings.Contains(content, "task-005") || !strings.Contains(content, "等待处理") || !strings.Contains(content, "Review") || strings.Contains(content, "internal-id") {
		t.Fatalf("status notification type=%s content=%s", msgType, content)
	}
}

func TestRenderCoalescedFinalReplyPrefersReplyText(t *testing.T) {
	t.Parallel()
	msgType, content := renderDelivery(map[string]any{"text": "final answer", "status": "waiting_user", "task_code": "task-005", "task_title": "Review"})
	if msgType != "text" || !strings.Contains(content, "final answer") || strings.Contains(content, "等待处理") {
		t.Fatalf("coalesced final reply type=%s content=%s", msgType, content)
	}
}
