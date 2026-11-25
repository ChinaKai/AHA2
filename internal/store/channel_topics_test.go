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

func TestChannelTopicTurnsHaveNoNumericCapsAndRespectEnd(t *testing.T) {
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
	submit := func(threadID string, bot, wantEnded bool) domain.Turn {
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
		ended, err := database.ChannelGroupBatchDialogueEnded(ctx, group.BatchID, conversation.ID, threadID)
		if err != nil || ended != wantEnded {
			t.Fatalf("preflight ended=%v want=%v err=%v", ended, wantEnded, err)
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
		if wantEnded {
			if len(claimed) != 0 || turn.ID != "" || current.State != "rejected" || current.Outcome != ChannelTopicDialogueEndedOutcome {
				t.Fatalf("closed topic %q: turn=%q claimed=%d receipt=%s/%s",
					threadID, turn.ID, len(claimed), current.State, current.Outcome)
			}
			return turn
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
		lastBotTurn = submit("omt_robot", true, false)
	}
	for range 13 {
		submit("omt_busy", false, false)
	}
	for range 13 {
		submit("", true, false)
	}

	retry := lastBotTurn
	retry.ID = domain.NewID("turn")
	retry.ParentTurnID = lastBotTurn.ID
	retry.Attempt = 2
	retry.QueuedAt = now.Add(ChannelTopicWindow)
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
	if _, err := database.CreateAgentTurn(ctx, retry); !errors.Is(err, ErrChannelBotDialogueEnded) {
		t.Fatalf("bot topic retry bypassed end: %v", err)
	}
	submit("omt_robot", true, true)
	submit("omt_unrelated", true, false)
	submit("omt_robot", false, false)
	submit("omt_robot", true, false)
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
