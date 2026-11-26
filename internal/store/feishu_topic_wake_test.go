package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestFeishuTopicWakeUpgradeDoesNotBackfillParticipation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	instance, conversation := createDeliveryTestInstance(t, database)
	now := time.Now().UTC()
	chat := conversation.ExternalChatID
	if _, _, err := database.SetFeishuGroupReception(ctx, instance.ID, instance.OwnerID, chat, true, now); err != nil {
		t.Fatal(err)
	}
	messages := []struct {
		id, state, thread string
	}{
		{"old-delivered", "processed", "omt_old"},
		{"old-pending", "received", "omt_old"},
	}
	for _, message := range messages {
		if _, _, err := database.ReceiveChannelInbox(ctx, domain.ChannelInboxReceipt{
			ID: message.id, InstanceID: instance.ID, ConversationID: conversation.ID,
			ExternalEventID: message.id, EventType: "message", State: message.state,
			ReceivedAt: now, OccurredAt: now, ProcessedAt: now,
			NormalizedPayload: map[string]any{"external_chat_id": chat, "external_message_id": message.id, "thread_id": message.thread},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO channel_group_receipts
		(receipt_id,conversation_id,target_task_id,thread_id,batch_id,ready_ms)
		VALUES('old-pending',?,?,'omt_old','old-pending',?)`, conversation.ID, conversation.HostTaskID, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	delivery := enqueueTestDelivery(t, database, instance, conversation, 1, now)
	leased, err := database.ClaimChannelDeliveries(ctx, instance.ID, 1, now, time.Minute)
	if err != nil || len(leased) != 1 || leased[0].Target["thread_id"] != "omt_old" {
		t.Fatalf("claim historical topic delivery: %+v, %v", leased, err)
	}
	if err := database.AckChannelDelivery(ctx, instance.ID, delivery.ID, leased[0].LeaseID, "om_delivered", "", nil, now); err != nil {
		t.Fatal(err)
	}
	// Keep all v165 data and remove just the v166 schema.
	if _, err := database.db.ExecContext(ctx, `
		DROP TABLE channel_feishu_topics;
		DROP TABLE channel_feishu_topic_revocations;
		ALTER TABLE channel_delivery_attempts DROP COLUMN feishu_thread_id;
		ALTER TABLE channel_feishu_groups DROP COLUMN topic_wake_mode;
		DELETE FROM schema_migrations WHERE version=166;
	`); err != nil {
		t.Fatal(err)
	}
	database = reopenAfterMigration(t, database, path)
	defer database.Close()
	groups, _, err := database.feishuConversationGroups(ctx, instance.ID)
	if err != nil || groups[chat].TopicWakeMode != domain.FeishuTopicWakeFull || !groups[chat].ReceiveAllMessages {
		t.Fatalf("v165 group changed during upgrade: %+v, %v", groups[chat], err)
	}
	inbound, err := database.ClaimChannelInbox(ctx, 1, now, time.Minute)
	if err != nil || len(inbound) != 1 {
		t.Fatalf("claim historical pending message: %+v, %v", inbound, err)
	}
	group, err := database.ChannelGroupReceipt(ctx, inbound[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	envelope := domain.ChannelInboundEnvelope{ExternalChatID: chat, ExternalMessageID: "old-pending", ThreadID: "omt_old", Content: "pending at upgrade"}
	if listened, err := database.ListenPendingFeishuTopic(ctx, inbound[0], group, envelope, now); err != nil || !listened {
		t.Fatalf("historical delivery woke a pending topic: listened=%v, %v", listened, err)
	}
	if pending, err := database.PendingAgentInbox(ctx, conversation.HostTaskID, "main"); err != nil || len(pending) != 0 {
		t.Fatalf("historical pending message reached agent inbox: %+v, %v", pending, err)
	}

	delivery = enqueueTestDelivery(t, database, instance, conversation, 2, now)
	leased, err = database.ClaimChannelDeliveries(ctx, instance.ID, 1, now, time.Minute)
	if err != nil || len(leased) != 1 || leased[0].Target["thread_id"] != "omt_old" {
		t.Fatalf("claim new topic delivery: %+v, %v", leased, err)
	}
	actualThread := "omt_provider"
	if err := database.AckChannelDelivery(ctx, instance.ID, delivery.ID, leased[0].LeaseID, "om_new", "", &actualThread, now); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := database.db.QueryRowContext(ctx, `SELECT thread_id FROM channel_feishu_topics WHERE instance_id=? AND chat_id=?`,
		instance.ID, chat).Scan(&got); err != nil || got != actualThread {
		t.Fatalf("participation must follow the actual provider thread: %q, %v", got, err)
	}
}
