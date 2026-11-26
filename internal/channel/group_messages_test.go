package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestFeishuGroupListeningAndImmediateTurns(t *testing.T) {
	service, instance, claims := mediaTestService(t)
	ctx := context.Background()
	database := service.store
	clock := time.Now().UTC()
	service.now = func() time.Time { return clock }

	groupMessage := func(id, chatID, threadID, rootID, senderType, content string, mentioned bool) domain.ChannelInboundEnvelope {
		return domain.ChannelInboundEnvelope{
			SchemaVersion: 1, InstanceID: instance.ID, ExternalEventID: id, ExternalMessageID: id,
			EventType: "message", OccurredAt: clock, ChatType: "group", ExternalChatID: chatID,
			ExternalSenderID: "sender-" + id, SenderDisplayName: "Sender " + id,
			ThreadID: threadID, RootID: rootID, SenderType: senderType, Content: content, MentionedBot: mentioned,
		}
	}
	send := func(id, chatID, threadID, rootID, senderType, content string, mentioned bool) (domain.ChannelInboxReceipt, error) {
		receipt, _, err := service.ReceiveInbound(ctx, claims, groupMessage(id, chatID, threadID, rootID, senderType, content, mentioned))
		return receipt, err
	}
	const chat = "oc_allowed"
	conversation := startFeishuMentionHost(t, service, instance, claims, chat, clock)

	task, err := service.application.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: instance.HostProjectID, WorkspaceID: instance.HostWorkspaceID, Title: "Topic Task",
		Request: "Listen and answer", Isolation: "inplace", Backend: "stub", ModelID: "model-stub",
		CollaborationMode: "single", AllowSystemProject: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskStatus(ctx, task.ID, task.Status, domain.TaskWaitingUser, clock.Format(time.RFC3339Nano), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, task.ID, clock); err != nil {
		t.Fatal(err)
	}
	instance, err = database.ChannelInstance(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	instance.Config["channel_permission_ids"] = []string{domain.FeishuFullGroupPermission}
	instance, err = database.UpdateChannelInstance(ctx, instance, instance.Revision)
	if err != nil {
		t.Fatal(err)
	}
	setTestGroupReception(t, service, instance, chat, true)
	instance.Config["runtime_bot_open_id"] = "sender-self"
	instance, err = database.UpdateChannelInstance(ctx, instance, instance.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := send("self", chat, "omt_a", "", "app", "self-authored message", false); !errors.Is(err, store.ErrChannelGroupSelf) || receipt.ID != "" {
		t.Fatalf("self-authored message was not rejected before receipt creation: receipt=%v err=%v", receipt, err)
	}
	listener := domain.ChannelInboundEnvelope{
		SchemaVersion: 1, InstanceID: instance.ID, ExternalEventID: "om_root", ExternalMessageID: "om_root",
		EventType: "message", OccurredAt: clock, ChatType: "group", ExternalChatID: chat,
		ExternalSenderID: "root-sender", SenderDisplayName: "Root sender", SenderType: "user",
		Content: "ROOT_BACKGROUND_FOREACH", Resources: []map[string]any{{"type": "file", "file_key": "private-attachment"}},
	}
	listened, duplicate, err := service.ReceiveInbound(ctx, claims, listener)
	if err != nil || duplicate || listened.State != "listening" {
		t.Fatalf("listen receipt=%v duplicate=%v err=%v", listened, duplicate, err)
	}
	if _, duplicate, err := service.ReceiveInbound(ctx, claims, listener); err != nil || !duplicate {
		t.Fatalf("listener repeated duplicate=%v err=%v", duplicate, err)
	}
	if _, err := database.ChannelInboxReceipt(ctx, instance.ID, listened.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("listener entered normal channel dedup: %v", err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := database.Task(ctx, task.ID)
	if err != nil || current.Status != domain.TaskWaitingUser || !current.UpdatedAt.Equal(clock) {
		t.Fatalf("listener mutated Task: task=%v err=%v", current, err)
	}
	if _, err := database.LatestRound(ctx, task.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("listener created round: %v", err)
	}
	if pending, err := database.PendingAgentInbox(ctx, task.ID, "main"); err != nil || len(pending) != 0 {
		t.Fatalf("listener queued Agent messages: %v err=%v", pending, err)
	}
	if commands, err := service.ClaimCommands(ctx, claims, 10); err != nil || len(commands) != 0 {
		t.Fatalf("listener created attachment commands: %v err=%v", commands, err)
	}

	acceptTestFeishuTopicDelivery(t, service, instance, conversation.ID, "omt_a")
	firstMessage := groupMessage("om_first", chat, "omt_a", "om_root", "user", "WAKE_FIRST", false)
	first, _, err := service.ReceiveInbound(ctx, claims, firstMessage)
	if err != nil {
		t.Fatal(err)
	}
	redelivery := firstMessage
	redelivery.ExternalEventID = "fresh_event_for_om_first"
	duplicateReceipt, duplicate, err := service.ReceiveInbound(ctx, claims, redelivery)
	if err != nil || !duplicate || duplicateReceipt.ID != first.ID {
		t.Fatalf("same message under a different event ID was not deduplicated: duplicate=%v err=%v", duplicate, err)
	}
	clock = clock.Add(100 * time.Millisecond)
	_, err = send("om_second", chat, "omt_b", "om_other_root", "user", "WAKE_SECOND", true)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(100 * time.Millisecond)
	_, err = send("om_third", chat, "omt_a", "om_root", "app", "WAKE_OTHER_BOT", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if turns, err := database.ListTurns(ctx, task.ID); err != nil || len(turns) == 0 {
		t.Fatalf("no Turn immediately after processing incoming messages: %v err=%v", turns, err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		turns, err := database.ListTurns(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(turns) == 3 && turns[0].Status.Terminal() && turns[1].Status.Terminal() && turns[2].Status.Terminal() {
			wantBackground := map[string]bool{"WAKE_FIRST": true, "WAKE_SECOND": false, "WAKE_OTHER_BOT": true}
			for _, turn := range turns {
				if turn.Status != domain.TurnSucceeded {
					t.Fatalf("turn failed: %s %s", turn.Status, turn.Error)
				}
				matches := 0
				for message, want := range wantBackground {
					if strings.Contains(turn.Instruction, message) {
						matches++
						if strings.Contains(turn.PromptSnapshot, "ROOT_BACKGROUND_FOREACH") != want {
							t.Fatalf("wrong background for %s, expected present=%v", message, want)
						}
					}
				}
				if matches != 1 {
					t.Fatalf("expected one starting message per Turn: %q", turn.Instruction)
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	turns, _ := database.ListTurns(ctx, task.ID)
	t.Fatalf("expected three finished topic Turns, got %d", len(turns))
}

func startFeishuMentionHost(t *testing.T, service *Service, instance domain.ChannelInstance, claims RuntimeClaims, chat string, now time.Time) domain.ChannelConversation {
	t.Helper()
	ctx := context.Background()
	envelope := domain.ChannelInboundEnvelope{
		SchemaVersion: 1, InstanceID: instance.ID, EventType: "message", ChatType: "group",
		ExternalChatID: chat, ChatDisplayName: "回归测试群", ExternalSenderID: "ou_human",
		ExternalEventID: "seed-" + chat, ExternalMessageID: "om_seed-" + chat, Content: "wake group host",
		MentionedBot: true, SenderType: "user", OccurredAt: now,
	}
	receipt, duplicate, err := service.ReceiveInbound(ctx, claims, envelope)
	if err != nil || duplicate {
		t.Fatalf("unbound @ was not accepted: duplicate=%v err=%v", duplicate, err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := service.store.ChannelInboxReceipt(ctx, instance.ID, receipt.ID)
	if err != nil || stored.State != "processed" || stored.AHAMessageID == "" {
		t.Fatalf("unbound @ did not reach a host Task: state=%s outcome=%s err=%v", stored.State, stored.Outcome, err)
	}
	conversation, err := service.store.ChannelConversation(ctx, stored.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.store.Task(ctx, conversation.HostTaskID)
	if err != nil || task.Title != "飞书群聊 · 回归测试群" {
		t.Fatalf("unbound @ did not create the baseline host Task: title=%q err=%v", task.Title, err)
	}
	return conversation
}

func TestFeishuGroupIngressKeepsMentionsWithoutFullReception(t *testing.T) {
	service, instance, claims := mediaTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	const chat = "oc_bound"

	host := startFeishuMentionHost(t, service, instance, claims, chat, now)
	// Verify the host's final answer is queued against the original mention.
	deadline := time.Now().Add(4 * time.Second)
	replied := false
	for !replied && time.Now().Before(deadline) {
		deliveries, err := service.store.ChannelDeliveries(ctx, instance.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, delivery := range deliveries {
			if reply, _ := delivery.SemanticPayload["text"].(string); delivery.ConversationID == host.ID && reply != "" {
				target, err := service.store.ChannelDeliveryTarget(ctx, host.ID, delivery.SourceEventSequence)
				if err != nil || target["reply_message_id"] != "om_seed-"+chat {
					t.Fatalf("automatic reply did not target the mention: %v, err=%v", target, err)
				}
				replied = true
				break
			}
		}
		if !replied {
			time.Sleep(15 * time.Millisecond)
		}
	}
	if !replied {
		t.Fatal("unbound @ produced no automatic group reply")
	}

	route, err := service.store.ActivateOwnerChannelTaskRoute(ctx, instance.ID, host.ID, host.HostTaskID, now)
	if err != nil {
		t.Fatal(err)
	}
	instance, err = service.store.ChannelInstance(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	setTestGroupReception(t, service, instance, chat, true)
	envelope := domain.ChannelInboundEnvelope{
		SchemaVersion: 1, InstanceID: instance.ID, EventType: "message", ChatType: "group",
		ExternalChatID: chat, ExternalSenderID: "ou_human", ExternalEventID: "nonmention",
		ExternalMessageID: "om_nonmention", Content: "must not be stored", SenderType: "user", OccurredAt: now,
	}
	for _, threadID := range []string{"", "omt_private"} {
		envelope.ThreadID = threadID
		// Exercise the service check independently from the store transaction.
		if _, err := service.feishuGroupIngressRoute(ctx, instance, envelope); !errors.Is(err, store.ErrChannelGroupNotAllowed) {
			t.Fatalf("service allowed %q with permission off: %v", threadID, err)
		}
		receipt, _, err := service.ReceiveInbound(ctx, claims, envelope)
		if receipt.ID != "" || !errors.Is(err, store.ErrChannelGroupNotAllowed) {
			t.Fatalf("non-mention created receipt %q with permission off: %v", receipt.ID, err)
		}
	}
	instance.Config["channel_permission_ids"] = []string{domain.FeishuFullGroupPermission}
	instance, err = service.store.UpdateChannelInstance(ctx, instance, instance.Revision)
	if err != nil {
		t.Fatal(err)
	}
	setTestGroupReception(t, service, instance, chat, false)
	if receipt, _, err := service.ReceiveInbound(ctx, claims, envelope); receipt.ID != "" || !errors.Is(err, store.ErrChannelGroupNotAllowed) {
		t.Fatalf("outside non-mention reached storage: receipt=%q err=%v", receipt.ID, err)
	}
	envelope.MentionedBot, envelope.Content = true, "explicit mention outside allowlist"
	receipt, _, err := service.ReceiveInbound(ctx, claims, envelope)
	if err != nil || receipt.State != "received" {
		t.Fatalf("bound @ outside the allowlist was rejected: state=%s err=%v", receipt.State, err)
	}
	groupReceipt, err := service.store.ChannelGroupReceipt(ctx, receipt.ID)
	if err != nil || groupReceipt.RouteID != route.ID {
		t.Fatalf("bound @ outside the allowlist did not retain its Task route: route=%q err=%v", groupReceipt.RouteID, err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := service.store.ChannelInboxReceipt(ctx, instance.ID, receipt.ID)
	if err != nil || stored.Outcome != "delivered_to_task" {
		t.Fatalf("bound @ outside allowlist was not processed: outcome=%q err=%v", stored.Outcome, err)
	}
}

func TestFeishuGroupMentionContinuesFinishedBinding(t *testing.T) {
	for _, cleanup := range []string{"route still active", "released after receipt", "released before receipt", "manually unbound"} {
		t.Run(cleanup, func(t *testing.T) {
			service, instance, claims := mediaTestService(t)
			ctx := context.Background()
			now := time.Now().UTC()
			service.now = func() time.Time { return now }
			const chat = "oc_continue"
			conversation := startFeishuMentionHost(t, service, instance, claims, chat, now)
			previous, err := service.application.CreateTask(ctx, app.CreateTaskInput{
				ProjectID: instance.HostProjectID, WorkspaceID: instance.HostWorkspaceID, Title: "Bound work",
				Request: "Previous Task", Isolation: "inplace", Backend: "stub", ModelID: "model-stub",
				CollaborationMode: "single", AllowSystemProject: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			previousRoute, err := service.store.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, previous.ID, now)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.application.CompleteTask(ctx, previous.ID); err != nil {
				t.Fatal(err)
			}
			if cleanup == "manually unbound" {
				if err := service.UnbindTaskChannelRoute(ctx, instance.OwnerID, previous.ID, previousRoute.ID); err != nil {
					t.Fatal(err)
				}
			}
			if cleanup == "released before receipt" {
				if err := service.reconcileTerminalTaskRoutes(ctx); err != nil {
					t.Fatal(err)
				}
			}
			first := domain.ChannelInboundEnvelope{
				SchemaVersion: 1, InstanceID: instance.ID, EventType: "message", ChatType: "group",
				ExternalChatID: chat, ExternalSenderID: "ou_human", ExternalEventID: "wake-first",
				ExternalMessageID: "om_wake_first", Content: "continue first", MentionedBot: true,
				SenderType: "user", OccurredAt: now, ThreadID: "omt_continue",
			}
			second := first
			second.ExternalEventID, second.ExternalMessageID, second.Content = "wake-second", "om_wake_second", "continue second"
			receipts := make([]domain.ChannelInboxReceipt, 0, 2)
			for _, envelope := range []domain.ChannelInboundEnvelope{first, second} {
				receipt, _, err := service.ReceiveInbound(ctx, claims, envelope)
				if err != nil {
					t.Fatal(err)
				}
				receipts = append(receipts, receipt)
			}
			var nonMention domain.ChannelInboxReceipt
			if cleanup == "released after receipt" {
				instance, err = service.store.ChannelInstance(ctx, instance.ID)
				if err != nil {
					t.Fatal(err)
				}
				instance.Config["channel_permission_ids"] = []string{domain.FeishuFullGroupPermission}
				if _, err := service.store.UpdateChannelInstance(ctx, instance, instance.Revision); err != nil {
					t.Fatal(err)
				}
				setTestGroupReception(t, service, instance, chat, true)
				acceptTestFeishuTopicDelivery(t, service, instance, conversation.ID, "omt_continue")
				quiet := first
				quiet.ExternalEventID, quiet.ExternalMessageID, quiet.Content = "quiet-event", "om_quiet", "non-mention must not migrate"
				quiet.MentionedBot = false
				nonMention, _, err = service.ReceiveInbound(ctx, claims, quiet)
				if err != nil {
					t.Fatal(err)
				}
				if err := service.reconcileTerminalTaskRoutes(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.processInboundBatch(ctx); err != nil {
				t.Fatal(err)
			}
			route, err := service.store.ActiveChannelTaskRoute(ctx, conversation.ID)
			targetTaskID := route.TargetTaskID
			if cleanup == "manually unbound" {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("explicit @ resurrected an owner-unbound route: route=%v err=%v", route, err)
				}
				targetTaskID = conversation.HostTaskID
			} else if err != nil || targetTaskID == previous.ID || targetTaskID == conversation.HostTaskID {
				t.Fatalf("no continuation of finished binding: route=%v err=%v", route, err)
			}
			if nonMention.ID != "" {
				quietGroup, err := service.store.ChannelGroupReceipt(ctx, nonMention.ID)
				if err != nil || quietGroup.RouteID != previousRoute.ID || quietGroup.TargetTaskID != previous.ID {
					t.Fatalf("non-mention followed the continuation: group=%v err=%v", quietGroup, err)
				}
				stored, err := service.store.ChannelInboxReceipt(ctx, instance.ID, nonMention.ID)
				if err != nil || stored.AHAMessageID != "" || stored.Outcome != "group_task_route_required" {
					t.Fatalf("non-mention was not rejected at the old binding: state=%q outcome=%q err=%v", stored.State, stored.Outcome, err)
				}
			}
			for _, receipt := range receipts {
				stored, err := service.store.ChannelInboxReceipt(ctx, instance.ID, receipt.ID)
				if err != nil || stored.Outcome != "delivered_to_task" || stored.AHAMessageID == "" {
					t.Fatalf("receipt did not reach the continuation: outcome=%q err=%v", stored.Outcome, err)
				}
			}
			page, err := service.store.ConversationPageForAgent(ctx, targetTaskID, "main", 0, 0, 100, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range page.Items {
				if item.Kind == "user_message" && item.Summary == "non-mention must not migrate" {
					t.Fatal("non-mention body reached the continuation")
				}
			}
			for _, input := range []string{"continue first", "continue second"} {
				found := false
				for _, item := range page.Items {
					found = found || (item.Kind == "user_message" && item.Summary == input)
				}
				if !found {
					t.Fatalf("selected Task did not receive %q", input)
				}
			}
		})
	}
}

func TestFeishuMentionRepairsMetadataOnlyGroup(t *testing.T) {
	service, instance, claims := mediaTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	const chat = "oc_existing_metadata"
	endpoint, err := service.store.ChannelEndpoint(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := service.scopeKey(instance.ID, endpoint.Kind, "", chat, "")
	if err != nil {
		t.Fatal(err)
	}
	existing := domain.ChannelConversation{
		ID: domain.NewID("conversation"), InstanceID: instance.ID, EndpointID: endpoint.ID,
		ExternalChatID: chat, ScopeKey: scope, ScopeKeyVersion: 2, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := service.store.CreateChannelConversation(ctx, existing, domain.ChannelSession{
		ID: domain.NewID("session"), ConversationID: existing.ID, Generation: 1, Mode: "group_qa", Status: "active", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	conversation := startFeishuMentionHost(t, service, instance, claims, chat, now)
	if conversation.ID != existing.ID {
		t.Fatalf("legacy group metadata was replaced: got %q want %q", conversation.ID, existing.ID)
	}
}

func TestFeishuGroupHostBotDialogueCountsAndKeepsParticipantRole(t *testing.T) {
	service, instance, claims := mediaTestService(t)
	ctx := context.Background()

	for wantTurn := 1; wantTurn <= 2; wantTurn++ {
		eventID := fmt.Sprintf("bot-event-%d", wantTurn)
		receipt, _, err := service.ReceiveInbound(ctx, claims, domain.ChannelInboundEnvelope{
			SchemaVersion: 1, InstanceID: instance.ID, EventType: "message", ChatType: "group",
			ExternalChatID: "oc_bot_dialogue", ExternalEventID: eventID, ExternalMessageID: "om_" + eventID,
			ExternalSenderID: "owner-open", // Exercise the fixture's existing owner link.
			SenderType:       "app", MentionedBot: true, Content: "robot dialogue",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.processInboundBatch(ctx); err != nil {
			t.Fatal(err)
		}
		stored, err := service.store.ChannelInboxReceipt(ctx, instance.ID, receipt.ID)
		if err != nil || stored.Outcome != "delivered_to_task" {
			t.Fatalf("bot mention did not reach the host: outcome=%q err=%v", stored.Outcome, err)
		}
		conversation, err := service.store.ChannelConversation(ctx, stored.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		turnContext := waitForFeishuHostTurnContext(t, service.store, conversation.HostTaskID, wantTurn)
		actor, _ := turnContext["actor"].(map[string]any)
		if actor["role"] != "participant" || actor["is_owner"] != false || actor["is_bot"] != true {
			t.Fatalf("bot with an owner identity link gained owner authority: actor=%v", actor)
		}
		dialogue, _ := turnContext["bot_dialogue"].(map[string]any)
		if dialogue["active"] != true || dialogue["turn"] != float64(wantTurn) {
			t.Fatalf("host bot dialogue turn = %v, want %d", dialogue, wantTurn)
		}
	}
}

func waitForFeishuHostTurnContext(t *testing.T, database *store.Store, taskID string, count int) map[string]any {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		turns, err := database.ListTurns(ctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if len(turns) == count && turns[count-1].Status.Terminal() {
			turn := turns[count-1]
			if turn.Status != domain.TurnSucceeded {
				t.Fatalf("host turn did not succeed: status=%s err=%s", turn.Status, turn.Error)
			}
			task, err := database.Task(ctx, taskID)
			if err != nil {
				t.Fatal(err)
			}
			// The Task returns to waiting_user after the Round settles, so the
			// next inbound message starts a separate dialogue round.
			if task.Status == domain.TaskWaitingUser {
				channelContext, err := database.ChannelContextForInboxBatch(ctx, turn.InboxBatchID)
				if err != nil {
					t.Fatal(err)
				}
				return channelContext
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("expected %d host Turns to finish", count)
	return nil
}
