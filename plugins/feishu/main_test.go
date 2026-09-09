package main

import (
	"encoding/json"
	"errors"
	"testing"

	larkapplicationv7 "github.com/larksuite/oapi-sdk-go/v3/service/application/v7"
)

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
