package app

import (
	"errors"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestChannelMCPReceiptAcceptsGroupAndPrivateTaskRoutes(t *testing.T) {
	t.Parallel()
	for name, endpoint := range map[string]string{
		"group":   domain.ChannelEndpointGroupDigitalHuman,
		"private": domain.ChannelEndpointAssistantDM,
	} {
		receipt, err := channelMCPReceipt(map[string]any{
			"endpoint":           endpoint,
			"route":              map[string]any{"mode": "task_route"},
			"inbound_receipt_id": "channel_inbox_1",
		})
		if err != nil || receipt != "channel_inbox_1" {
			t.Fatalf("%s: receipt=%q err=%v", name, receipt, err)
		}
	}

	for name, context := range map[string]map[string]any{
		"host": {
			"endpoint":           domain.ChannelEndpointGroupDigitalHuman,
			"route":              map[string]any{"mode": "conversation_host"},
			"inbound_receipt_id": "channel_inbox_1",
		},
		"private host": {
			"endpoint":           domain.ChannelEndpointAssistantDM,
			"route":              map[string]any{"mode": "conversation_host"},
			"inbound_receipt_id": "channel_inbox_1",
		},
		"unknown endpoint": {
			"endpoint":           "unknown",
			"route":              map[string]any{"mode": "task_route"},
			"inbound_receipt_id": "channel_inbox_1",
		},
		"missing receipt": {
			"endpoint": domain.ChannelEndpointAssistantDM,
			"route":    map[string]any{"mode": "task_route"},
		},
	} {
		if _, err := channelMCPReceipt(context); !errors.Is(err, ErrAgentCallForbidden) {
			t.Fatalf("%s: err=%v, want ErrAgentCallForbidden", name, err)
		}
	}
}
