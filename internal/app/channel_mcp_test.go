package app

import (
	"errors"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestChannelMCPGroupReceiptOnlyAcceptsGroupTaskRoutes(t *testing.T) {
	t.Parallel()
	group := map[string]any{
		"endpoint":           domain.ChannelEndpointGroupDigitalHuman,
		"route":              map[string]any{"mode": "task_route"},
		"inbound_receipt_id": "channel_inbox_1",
	}
	receipt, err := channelMCPGroupReceipt(group)
	if err != nil || receipt != "channel_inbox_1" {
		t.Fatalf("receipt=%q err=%v", receipt, err)
	}

	for name, context := range map[string]map[string]any{
		"private": {
			"endpoint":           domain.ChannelEndpointAssistantDM,
			"route":              map[string]any{"mode": "task_route"},
			"inbound_receipt_id": "channel_inbox_1",
		},
		"host": {
			"endpoint":           domain.ChannelEndpointGroupDigitalHuman,
			"route":              map[string]any{"mode": "conversation_host"},
			"inbound_receipt_id": "channel_inbox_1",
		},
		"missing receipt": {
			"endpoint": domain.ChannelEndpointGroupDigitalHuman,
			"route":    map[string]any{"mode": "task_route"},
		},
	} {
		if _, err := channelMCPGroupReceipt(context); !errors.Is(err, ErrAgentCallForbidden) {
			t.Fatalf("%s: err=%v, want ErrAgentCallForbidden", name, err)
		}
	}
}
