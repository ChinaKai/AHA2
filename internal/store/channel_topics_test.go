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

func TestChannelTopicTurnLimitsAreIsolatedAndPersistOutcomes(t *testing.T) {
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
	submit := func(threadID string, bot bool, expectedOutcome string) domain.Turn {
		t.Helper()
		locus++
		eventID := fmt.Sprintf("event-%d", locus)
		now = now.Add(ChannelTopicWindow + time.Second)
		envelope := domain.ChannelInboundEnvelope{ExternalChatID: conversation.ExternalChatID, MentionedBot: true, ThreadID: threadID, SenderIsBot: bot}
		receipt, _, err := database.ReceiveChannelGroupMessage(ctx, domain.ChannelInboxReceipt{
			ID: eventID, InstanceID: instance.ID, ExternalEventID: eventID, EventType: "message",
			State: "received", ReceivedAt: now, OccurredAt: now, PayloadDigest: eventID,
			NormalizedPayload: map[string]any{"external_chat_id": conversation.ExternalChatID, "mentioned_bot": true, "sender_is_bot": bot},
		}, route, envelope)
		if err != nil {
			t.Fatal(err)
		}
		leases, err := database.ClaimChannelInbox(ctx, 10, now.Add(ChannelTopicWindow), time.Minute)
		if err != nil || len(leases) != 1 || leases[0].ID != receipt.ID {
			t.Fatalf("receipt leases=%v err=%v", leases, err)
		}
		group, err := database.ChannelGroupReceipt(ctx, receipt.ID)
		if err != nil {
			t.Fatal(err)
		}
		outcome, err := database.ChannelGroupBatchLimit(ctx, group.BatchID, conversation.ID, threadID, now.Add(ChannelTopicWindow))
		if err != nil || outcome != expectedOutcome {
			t.Fatalf("preflight outcome=%q want=%q err=%v", outcome, expectedOutcome, err)
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
			Attempt: 1, Required: true, Status: domain.TurnQueued, QueuedAt: now.Add(ChannelTopicWindow),
			RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InboxBatchID: domain.NewID("inbox_batch"),
		}, []domain.AgentInboxItem{inbox}, nil)
		if err != nil {
			t.Fatal(err)
		}
		current, err := database.ChannelInboxReceipt(ctx, instance.ID, receipt.ID)
		if err != nil {
			t.Fatal(err)
		}
		if expectedOutcome != "" {
			if len(claimed) != 0 || turn.ID != "" || current.State != "rejected" || current.Outcome != expectedOutcome {
				t.Fatalf("topic=%q turn=%q claimed=%d receipt=%s/%s; want %s",
					threadID, turn.ID, len(claimed), current.State, current.Outcome, expectedOutcome)
			}
		} else {
			if len(claimed) != 1 || turn.ID == "" {
				t.Fatalf("topic %q did not create a Turn", threadID)
			}
			if _, err := database.db.ExecContext(ctx, `UPDATE turns SET status='succeeded' WHERE id=?`, turn.ID); err != nil {
				t.Fatal(err)
			}
		}
		return turn
	}
	var lastBotTurn domain.Turn
	for range channelTopicBotLimit {
		lastBotTurn = submit("omt_bot_limit", true, "")
	}
	retry := lastBotTurn
	retry.ID = domain.NewID("turn")
	retry.ParentTurnID = lastBotTurn.ID
	retry.Attempt = 2
	retry.QueuedAt = lastBotTurn.QueuedAt.Add(time.Second)
	if _, err := database.CreateAgentTurn(ctx, retry); !errors.Is(err, ErrChannelTopicTurnLimited) {
		t.Fatalf("retry bypassed topic turn cap: %v", err)
	}
	submit("", false, "") // A human top-level message does not reset the topic.
	submit("omt_bot_limit", true, "topic_bot_turn_limit")
	submit("omt_bot_limit", false, "")
	turn := submit("omt_bot_limit", true, "")
	if err := database.SetChannelTopicReplyDecision(ctx, turn.ID, conversation.ID, "omt_bot_limit", "end", "Already answered"); err != nil {
		t.Fatal(err)
	}
	submit("omt_bot_limit", true, "topic_dialogue_ended")
	submit("omt_unrelated", true, "")

	for range channelTopicHourlyLimit + 1 {
		submit("", false, "")
	}
	// Human main-chat traffic does not consume the bot-only hourly budget.
	submit("", true, "")
	for range channelTopicHourlyLimit - 1 {
		submit("", false, "")
		submit("", true, "")
	}
	submit("", true, "topic_hourly_turn_limit")
	submit("", false, "")

	for range channelTopicHourlyLimit {
		submit("omt_busy", false, "")
	}
	submit("omt_busy", false, "topic_hourly_turn_limit")
	now = now.Add(time.Hour)
	submit("omt_busy", false, "")
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
