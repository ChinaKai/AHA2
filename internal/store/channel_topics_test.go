package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestChannelTopicTurnsHaveNoCapsAndEndStaysWithItsInput(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	route, err := database.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, task.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	locus := 0
	submit := func(threadID string, bot bool) domain.Turn {
		t.Helper()
		locus++
		eventID := fmt.Sprintf("event-%d", locus)
		now = now.Add(time.Second)
		envelope := domain.ChannelInboundEnvelope{ExternalChatID: conversation.ExternalChatID, MentionedBot: true, ThreadID: threadID, SenderIsBot: bot}
		receipt, _, err := database.ReceiveChannelGroupMessage(ctx, domain.ChannelInboxReceipt{
			ID: eventID, InstanceID: instance.ID, ExternalEventID: eventID, EventType: "message",
			State: "received", ReceivedAt: now, OccurredAt: now, PayloadDigest: eventID,
			NormalizedPayload: map[string]any{"external_chat_id": conversation.ExternalChatID, "mentioned_bot": true, "sender_is_bot": bot},
		}, route, envelope)
		if err != nil {
			t.Fatal(err)
		}
		leases, err := database.ClaimChannelInbox(ctx, 10, now, time.Minute)
		if err != nil || len(leases) != 1 || leases[0].ID != receipt.ID {
			t.Fatalf("receipt leases=%v err=%v", leases, err)
		}
		group, err := database.ChannelGroupReceipt(ctx, receipt.ID)
		if err != nil {
			t.Fatal(err)
		}
		message := domain.Message{ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "sender", Content: "wake", CreatedAt: now}
		round, inbox, _, _, err := database.EnqueueChannelOwnerMessage(ctx, receipt.ID, leases[0].LeaseID, conversation.ID, message, "main", map[string]any{
			"group_batch_id": group.BatchID, "conversation_id": conversation.ID, "thread_id": threadID,
			"actor": map[string]any{"is_bot": bot},
		})
		if err != nil {
			t.Fatal(err)
		}
		turn, claimed, err := database.ClaimInboxAndCreateTurn(ctx, domain.Turn{
			ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main", InputMessageID: message.ID,
			Attempt: 1, Required: true, Status: domain.TurnQueued, QueuedAt: now,
			RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InboxBatchID: domain.NewID("inbox_batch"),
		}, []domain.AgentInboxItem{inbox}, nil)
		if err != nil {
			t.Fatal(err)
		}
		current, err := database.ChannelInboxReceipt(ctx, instance.ID, receipt.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(claimed) != 1 || turn.ID == "" {
			t.Fatalf("topic %q did not create a Turn: claimed=%d outcome=%s", threadID, len(claimed), current.Outcome)
		}
		if _, err := database.db.ExecContext(ctx, `UPDATE turns SET status='succeeded' WHERE id=?`, turn.ID); err != nil {
			t.Fatal(err)
		}
		return turn
	}
	// These Turns fit within a single hour and cross both former caps.
	var lastBotTurn domain.Turn
	for range 13 {
		lastBotTurn = submit("omt_robot", true)
	}
	for range 13 {
		submit("omt_busy", false)
	}
	for range 13 {
		submit("", true)
	}

	retry := lastBotTurn
	retry.ID = domain.NewID("turn")
	retry.ParentTurnID = lastBotTurn.ID
	retry.Attempt = 2
	retry.QueuedAt = now
	retry.Status = domain.TurnQueued
	retried, err := database.CreateAgentTurn(ctx, retry)
	if err != nil {
		t.Fatalf("bot topic retry was rejected before end: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE turns SET status='succeeded' WHERE id=?`, retried.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.SetChannelTopicReplyDecision(ctx, retried.ID, conversation.ID, "omt_robot", "end", "Already answered"); err != nil {
		t.Fatal(err)
	}
	retry.ID, retry.ParentTurnID, retry.Attempt = domain.NewID("turn"), retried.ID, 3
	retry.QueuedAt = retry.QueuedAt.Add(time.Second)
	endedRetry, err := database.CreateAgentTurn(ctx, retry)
	if err != nil {
		t.Fatal(err)
	}
	if decision, err := database.ChannelTopicReplyDecision(ctx, endedRetry.ID, conversation.ID, "omt_robot"); err != nil || decision != "end" {
		t.Fatalf("retry of the same input lost its end decision: %q, %v", decision, err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE turns SET status='succeeded' WHERE id=?`, endedRetry.ID); err != nil {
		t.Fatal(err)
	}
	newBotTurn := submit("omt_robot", true)
	if decision, err := database.ChannelTopicReplyDecision(ctx, newBotTurn.ID, conversation.ID, "omt_robot"); err != nil || decision != "" {
		t.Fatalf("new bot message inherited the preceding Turn's decision: %q, %v", decision, err)
	}

}

func TestReceiveChannelGroupMessageRechecksBindingAndPermissionBeforeStorage(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	route, err := database.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, task.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	instance, err = database.ChannelInstance(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	instance.Config[domain.FeishuGroupAllowlistKey] = []string{conversation.ExternalChatID}
	instance, err = database.UpdateChannelInstance(ctx, instance, instance.Revision)
	if err != nil {
		t.Fatal(err)
	}

	assertDenied := func(t *testing.T, want error) {
		t.Helper()
		for _, thread := range []string{"", "omt_topic"} {
			envelope := domain.ChannelInboundEnvelope{
				InstanceID: instance.ID, ExternalChatID: conversation.ExternalChatID, ThreadID: thread,
				ExternalMessageID: "om_private", Content: "private test content", SenderType: "user",
			}
			receipt := domain.ChannelInboxReceipt{
				ID: domain.NewID("receipt"), InstanceID: instance.ID, ExternalEventID: "denied-" + thread, EventType: "message",
				State: "received", ReceivedAt: now, OccurredAt: now, PayloadDigest: "test-digest",
				NormalizedPayload: map[string]any{"external_chat_id": conversation.ExternalChatID, "content": envelope.Content},
			}
			stored, _, err := database.ReceiveChannelGroupMessage(ctx, receipt, route, envelope)
			if stored.ID != "" || !errors.Is(err, want) {
				t.Fatalf("thread=%q receipt=%q err=%v, want %v", thread, stored.ID, err, want)
			}
		}
		var wakes, listening int
		if err := database.db.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM channel_inbox_dedup WHERE instance_id=?),
			(SELECT COUNT(*) FROM channel_group_listening WHERE conversation_id=?)`,
			instance.ID, conversation.ID).Scan(&wakes, &listening); err != nil {
			t.Fatal(err)
		}
		if wakes != 0 || listening != 0 {
			t.Fatalf("denied body reached storage: inbox=%d listening=%d", wakes, listening)
		}
	}
	t.Run("permission off", func(t *testing.T) {
		assertDenied(t, ErrChannelGroupNotAllowed)
	})
	instance.Config["channel_permission_ids"] = []string{domain.FeishuFullGroupPermission}
	if _, err := database.UpdateChannelInstance(ctx, instance, instance.Revision); err != nil {
		t.Fatal(err)
	}
	if err := database.ExitOwnerChannelTaskRoute(ctx, route.ID, task.ID, now); err != nil {
		t.Fatal(err)
	}
	// The service saw a live binding earlier. A direct store call simulates
	// the owner unbinding it immediately before the receipt transaction.
	t.Run("binding released", func(t *testing.T) {
		assertDenied(t, ErrChannelGroupUnbound)
	})
}

func TestFeishuTopicLiveInboxMatchesTurnAndRechecksPermission(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	instance, conversation := createDeliveryTestInstance(t, database)
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	route, err := database.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, task.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	instance, err = database.ChannelInstance(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	instance.Config[domain.FeishuGroupAllowlistKey] = []string{conversation.ExternalChatID}
	instance.Config["channel_permission_ids"] = []string{domain.FeishuFullGroupPermission}
	instance, err = database.UpdateChannelInstance(ctx, instance, instance.Revision)
	if err != nil {
		t.Fatal(err)
	}

	enqueue := func(id, thread string, mentioned, bot bool) (domain.TaskRound, domain.AgentInboxItem) {
		t.Helper()
		envelope := domain.ChannelInboundEnvelope{
			ExternalChatID: conversation.ExternalChatID, ThreadID: thread, MentionedBot: mentioned, SenderIsBot: bot,
		}
		receipt, _, err := database.ReceiveChannelGroupMessage(ctx, domain.ChannelInboxReceipt{
			ID: id, InstanceID: instance.ID, ExternalEventID: id, EventType: "message",
			State: "received", ReceivedAt: now, OccurredAt: now, PayloadDigest: id,
			NormalizedPayload: map[string]any{"external_chat_id": conversation.ExternalChatID, "thread_id": thread, "mentioned_bot": mentioned},
		}, route, envelope)
		if err != nil {
			t.Fatal(err)
		}
		leases, err := database.ClaimChannelInbox(ctx, 10, now, time.Minute)
		if err != nil || len(leases) != 1 || leases[0].ID != receipt.ID {
			t.Fatalf("receipt did not become eligible immediately: %v, %v", leases, err)
		}
		group, err := database.ChannelGroupReceipt(ctx, receipt.ID)
		if err != nil {
			t.Fatal(err)
		}
		message := domain.Message{ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Content: id, CreatedAt: now}
		round, item, _, _, err := database.EnqueueChannelOwnerMessage(ctx, receipt.ID, leases[0].LeaseID, conversation.ID, message, "main", map[string]any{
			"group_batch_id": group.BatchID, "conversation_id": conversation.ID, "thread_id": thread, "inbound_receipt_id": receipt.ID,
			"actor": map[string]any{"is_bot": bot},
		})
		if err != nil {
			t.Fatal(err)
		}
		return round, item
	}

	round, first := enqueue("receipt-start", "omt_release", true, true)
	turn, _, err := database.ClaimInboxAndCreateTurn(ctx, domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main", InputMessageID: first.MessageID,
		Attempt: 1, Required: true, Status: domain.TurnRunning, QueuedAt: now,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InboxBatchID: domain.NewID("inbox_batch"),
	}, []domain.AgentInboxItem{first}, nil)
	if err != nil || turn.ID == "" {
		t.Fatalf("creating topic Turn: %v, %v", turn, err)
	}

	_, other := enqueue("receipt-other", "omt_unrelated", true, false)
	_, liveBot := enqueue("receipt-live-bot", "omt_release", false, true)
	items, err := database.ClaimOwnerInboxForTurn(ctx, task.ID, "main", round.ID, turn.ID, first.Sequence, 10)
	if err != nil || len(items) != 1 || items[0].ID != liveBot.ID {
		t.Fatalf("inbox/wait did not return exactly the matching bot message: %v, %v", items, err)
	}
	contextForReply, err := database.ChannelContextForReceipt(ctx, task.ID, turn.ID, "receipt-live-bot")
	if err != nil || contextForReply["thread_id"] != "omt_release" || contextForReply["inbound_receipt_id"] != "receipt-live-bot" {
		t.Fatalf("live message lost its reply context: %v, %v", contextForReply, err)
	}
	if allowed, err := database.ChannelReceiptDeliveredToTurn(ctx, task.ID, turn.InboxBatchID, turn.ID, "receipt-other"); err != nil || allowed {
		t.Fatalf("running Turn can access another topic's reply receipt: %v, %v", allowed, err)
	}

	_, revoked := enqueue("receipt-revoked", "omt_release", false, true)
	_, liveHuman := enqueue("receipt-live-human", "omt_release", true, false)
	instance.Config[domain.FeishuGroupAllowlistKey] = []string{}
	if _, err := database.UpdateChannelInstance(ctx, instance, instance.Revision); err != nil {
		t.Fatal(err)
	}
	items, err = database.ClaimOwnerInboxForLiveInjection(ctx, task.ID, "main", round.ID, turn.ID, 10)
	if err != nil || len(items) != 1 || items[0].ID != liveHuman.ID {
		t.Fatalf("automatic claim did not enforce permission and topic boundaries: %v, %v", items, err)
	}
	if count, err := database.ChannelTopicBotTurns(ctx, conversation.ID, "omt_release"); err != nil || count != 1 {
		t.Fatalf("unacknowledged human delivery changed bot Turn count: %d, %v", count, err)
	}
	if _, err := database.AckLiveInboxForTurn(ctx, task.ID, "main", turn.ID, []string{liveHuman.ID}, true, now); err != nil {
		t.Fatal(err)
	}
	if count, err := database.ChannelTopicBotTurns(ctx, conversation.ID, "omt_release"); err != nil || count != 0 {
		t.Fatalf("human delivery did not reset bot Turn count: %d, %v", count, err)
	}

	if err := database.SetChannelTopicReplyDecision(ctx, turn.ID, conversation.ID, "omt_release", "end", "No reply needed"); err != nil {
		t.Fatal(err)
	}
	_, afterEnd := enqueue("receipt-after-end", "omt_release", true, true)
	items, err = database.ClaimOwnerInboxForTurn(ctx, task.ID, "main", round.ID, turn.ID, 0, 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("ended or unrelated topic entered the old Turn: %v, %v", items, err)
	}
	items, err = database.PendingAgentInbox(ctx, task.ID, "main")
	if err != nil || len(items) != 2 || items[0].ID != other.ID || items[1].ID != afterEnd.ID {
		t.Fatalf("messages for another Turn did not stay pending, or revoked item %s survived: %v, %v", revoked.ID, items, err)
	}
}
