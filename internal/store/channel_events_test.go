package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestChannelKnowledgeScopeMigrationIsSafeForExistingPolicies(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var defaultValue string
	if err := database.db.QueryRowContext(ctx, `SELECT dflt_value FROM pragma_table_info('channel_knowledge_policies') WHERE name='scope_mode'`).Scan(&defaultValue); err != nil {
		t.Fatal(err)
	}
	if defaultValue != "'selected'" {
		t.Fatalf("legacy policy default=%q", defaultValue)
	}
	var migrated bool
	if err := database.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=48)`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("schema v48 migrated=%v err=%v", migrated, err)
	}
}

func TestChannelSubscriptionAllowsUpdatesOnlyForPrivateConversations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, endpoint, subscription, eventClass, eventType string
		want                                                bool
	}{
		{"assistant update", domain.ChannelEndpointAssistantDM, "conversation_host", "update", "agent_message_update", true},
		{"routed update", domain.ChannelEndpointAssistantDM, "task_route", "update", "agent_progress", true},
		{"group final", domain.ChannelEndpointGroupDigitalHuman, "conversation_host", "message", "agent_reply", true},
		{"group message update", domain.ChannelEndpointGroupDigitalHuman, "conversation_host", "update", "agent_message_update", false},
		{"group progress", domain.ChannelEndpointGroupDigitalHuman, "conversation_host", "update", "agent_progress", false},
		{"global status", domain.ChannelEndpointAssistantDM, "owner_global", "status", "waiting_user", true},
		{"global update", domain.ChannelEndpointAssistantDM, "owner_global", "update", "agent_progress", false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := channelSubscriptionAllows(test.endpoint, test.subscription, test.eventClass, test.eventType); got != test.want {
				t.Fatalf("allowed=%v want=%v", got, test.want)
			}
		})
	}
}
