package channel

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestMediaDeliveriesAreOrderedScopedAndDurable(t *testing.T) {
	service, instance, claims := mediaTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	endpoint, err := service.store.ChannelEndpoint(ctx, instance.ID, domain.ChannelEndpointAssistantDM)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := service.store.ChannelOwnerIdentity(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	envelope := domain.ChannelInboundEnvelope{ChatType: "p2p", ExternalChatID: "chat", ExternalSenderID: identity.ExternalUserID}
	scope, err := service.scopeKey(instance.ID, endpoint.Kind, identity.ID, "chat", identity.ExternalUserID)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := service.ensureConversation(ctx, instance, endpoint, identity, envelope, scope)
	if err != nil {
		t.Fatal(err)
	}
	var attachments []domain.Attachment
	for _, name := range []string{"first.txt", "second.txt"} {
		attachment, err := service.store.CreateAttachment(ctx, conversation.HostTaskID, name, "text/plain", []byte(name), now)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.store.AddConversationItemWithAttachments(ctx, domain.ConversationItem{ID: "output-" + name, TaskID: conversation.HostTaskID, AgentID: "main", Category: "update", Kind: "agent_progress", CreatedAt: now}, []string{attachment.ID})
		if err != nil {
			t.Fatal(err)
		}
		attachments = append(attachments, attachment)
	}
	source := domain.ChannelSourceEvent{ID: "source-media", SourceKey: "source-media", TaskID: conversation.HostTaskID, RoundID: "round-media", EventClass: "message", EventType: "agent_reply", SemanticPayload: map[string]any{"task_id": conversation.HostTaskID, "text": "final result", "attachments": attachments}, OccurredAt: now}
	coalesce := store.ChannelCoalesceKey(source.TaskID, source.RoundID, "agent_reply")
	if err := service.store.AppendChannelSourceAndProject(ctx, source, coalesce); err != nil {
		t.Fatal(err)
	}
	status := domain.ChannelSourceEvent{ID: "status-media", SourceKey: "status-media", TaskID: source.TaskID, RoundID: source.RoundID, EventClass: "status", EventType: "waiting_user", SemanticPayload: map[string]any{"task_id": source.TaskID, "status": "waiting_user"}, OccurredAt: now}
	if err := service.store.AppendChannelSourceAndProject(ctx, status, coalesce); err != nil {
		t.Fatal(err)
	}
	deliveries, err := service.ClaimDeliveries(ctx, claims, 20)
	if err != nil || len(deliveries) != 1 || deliveries[0].SemanticPayload["text"] != "final result" {
		t.Fatalf("text delivery=%#v err=%v", deliveries, err)
	}
	first := deliveries[0]
	if _, _, err := service.DeliveryAttachment(ctx, claims, first.ID, first.LeaseID); err == nil {
		t.Fatal("text delivery granted attachment access")
	}
	if err := service.AckDelivery(ctx, claims, first.ID, first.LeaseID, "text-message", ""); err != nil {
		t.Fatal(err)
	}
	for index, attachment := range attachments {
		deliveries, err = service.ClaimDeliveries(ctx, claims, 20)
		if err != nil || len(deliveries) != 1 || deliveries[0].SemanticPayload["attachment_id"] != attachment.ID {
			t.Fatalf("media delivery=%#v err=%v", deliveries, err)
		}
		item := deliveries[0]
		foreign := claims
		foreign.InstanceID = "foreign"
		if _, _, err := service.DeliveryAttachment(ctx, foreign, item.ID, item.LeaseID); err == nil {
			t.Fatal("cross-instance attachment read accepted")
		}
		if _, _, err := service.DeliveryAttachment(ctx, claims, item.ID, "wrong"); err == nil {
			t.Fatal("wrong lease attachment read accepted")
		}
		actual, content, err := service.DeliveryAttachment(ctx, claims, item.ID, item.LeaseID)
		if err != nil || actual.ID != attachment.ID || !bytes.Equal(content, []byte(attachment.Name)) {
			t.Fatalf("attachment content=%#v err=%v", actual, err)
		}
		if index == 0 {
			if err := service.RecordMediaUpload(ctx, claims, item.ID, item.LeaseID, "file", "uploaded-key"); err != nil {
				t.Fatal(err)
			}
			if err := service.NackDelivery(ctx, claims, item.ID, item.LeaseID, "transport_failed", "confirmed_failure", 0, false); err != nil {
				t.Fatal(err)
			}
			deliveries, err = service.ClaimDeliveries(ctx, claims, 20)
			if err != nil || len(deliveries) != 1 || deliveries[0].ID != item.ID || deliveries[0].SemanticPayload["provider_resource_key"] != "uploaded-key" {
				t.Fatalf("media retry lost resource=%#v err=%v", deliveries, err)
			}
			item = deliveries[0]
		}
		if err := service.AckDelivery(ctx, claims, item.ID, item.LeaseID, "file-message", ""); err != nil {
			t.Fatal(err)
		}
		if _, _, err := service.DeliveryAttachment(ctx, claims, item.ID, item.LeaseID); err == nil {
			t.Fatal("acknowledged delivery still grants file access")
		}
	}
	source.ID, source.SourceKey = "source-repeat", "source-repeat"
	if err := service.store.AppendChannelSourceAndProject(ctx, source, coalesce); err != nil {
		t.Fatal(err)
	}
	deliveries, err = service.store.ChannelDeliveries(ctx, instance.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	mediaCount := 0
	for _, delivery := range deliveries {
		if delivery.SemanticPayload["kind"] == "attachment" {
			mediaCount++
		}
	}
	if mediaCount != 2 {
		t.Fatalf("duplicate final replay created %d media deliveries", mediaCount)
	}
}
