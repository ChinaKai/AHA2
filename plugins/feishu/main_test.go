package main

import (
	"encoding/json"
	"testing"
)

func TestNewFeishuClientsInstallEventDispatcher(t *testing.T) {
	t.Parallel()
	_, wsClient := newFeishuClients(bootstrap{AppID: "cli_test", AppSecret: "secret"})
	if wsClient.EventHandler() == nil {
		t.Fatal("WebSocket client has no event dispatcher; inbound events cannot reach Channel handlers")
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
