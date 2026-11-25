package channel

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestFeishuGroupListeningAndTopicBatches(t *testing.T) {
	service, instance, claims := mediaTestService(t)
	ctx := context.Background()
	database := service.store
	clock := time.Now().UTC()
	service.now = func() time.Time { return clock }

	send := func(id, chatID, threadID, rootID, senderType, content string, mentioned bool) (domain.ChannelInboxReceipt, error) {
		receipt, _, err := service.ReceiveInbound(ctx, claims, domain.ChannelInboundEnvelope{
			SchemaVersion: 1, InstanceID: instance.ID, ExternalEventID: id, ExternalMessageID: id,
			EventType: "message", OccurredAt: clock, ChatType: "group", ExternalChatID: chatID,
			ExternalSenderID: "sender-" + id, SenderDisplayName: "Sender " + id,
			ThreadID: threadID, RootID: rootID, SenderType: senderType, Content: content, MentionedBot: mentioned,
			Resources: nil,
		})
		return receipt, err
	}
	const chat = "oc_allowed"
	if _, err := send("unbound", chat, "", "", "user", "unbound private text", true); !errors.Is(err, store.ErrChannelGroupUnbound) {
		t.Fatalf("unbound @ returned %v", err)
	}
	destinations, err := database.ChannelDestinations(ctx, instance.OwnerID)
	if err != nil || len(destinations) != 1 {
		t.Fatalf("bindable destinations=%v err=%v", destinations, err)
	}
	conversation, err := database.ChannelConversation(ctx, destinations[0].ConversationID)
	if err != nil || conversation.HostTaskID != "" {
		t.Fatalf("discovery created host Task: conversation=%v err=%v", conversation, err)
	}
	summary, err := database.ChannelConversationSummaries(ctx, instance.OwnerID, instance.ID)
	if err != nil || len(summary) != 1 || summary[0].InboundCount != 0 || summary[0].OutboundCount != 0 {
		t.Fatalf("unbound message storage or delivery: %v err=%v", summary, err)
	}

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
	instance.Config[domain.FeishuGroupAllowlistKey] = []string{"oc_somewhere_else"}
	instance, err = database.UpdateChannelInstance(ctx, instance, instance.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := send("outside", chat, "omt_a", "", "user", "outside private text", true); !errors.Is(err, store.ErrChannelGroupNotAllowed) {
		t.Fatalf("non-allowlisted @ returned %v", err)
	}
	instance.Config[domain.FeishuGroupAllowlistKey] = []string{chat}
	instance, err = database.UpdateChannelInstance(ctx, instance, instance.Revision)
	if err != nil {
		t.Fatal(err)
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

	first, err := send("om_first", chat, "omt_a", "om_root", "user", "WAKE_FIRST", false)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(100 * time.Millisecond)
	second, err := send("om_second", chat, "omt_b", "om_other_root", "user", "WAKE_SECOND", true)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(100 * time.Millisecond)
	third, err := send("om_third", chat, "omt_a", "om_root", "app", "WAKE_OTHER_BOT", false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := database.ChannelGroupReceipt(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := database.ChannelGroupReceipt(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := database.ChannelGroupReceipt(ctx, third.ID)
	if err != nil || a.BatchID == b.BatchID || a.BatchID != c.BatchID {
		t.Fatalf("topic batch IDs %v %v %v err=%v", a, b, c, err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if turns, err := database.ListTurns(ctx, task.ID); err != nil || len(turns) != 0 {
		t.Fatalf("Turn before topic window closed: %v err=%v", turns, err)
	}
	clock = clock.Add(store.ChannelTopicWindow)
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		turns, err := database.ListTurns(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(turns) == 2 && turns[0].Status.Terminal() && turns[1].Status.Terminal() {
			for _, turn := range turns {
				switch {
				case strings.Contains(turn.Instruction, "WAKE_FIRST"):
					if !strings.Contains(turn.Instruction, "WAKE_OTHER_BOT") || strings.Contains(turn.Instruction, "WAKE_SECOND") ||
						!strings.Contains(turn.PromptSnapshot, "ROOT_BACKGROUND_FOREACH") {
						t.Fatalf("incorrect first-topic input, instruction=%q status=%s error=%s", turn.Instruction, turn.Status, turn.Error)
					}
				case strings.Contains(turn.Instruction, "WAKE_SECOND"):
					if strings.Contains(turn.Instruction, "WAKE_OTHER_BOT") || strings.Contains(turn.PromptSnapshot, "ROOT_BACKGROUND_FOREACH") {
						t.Fatalf("background or messages leaked across topics: %q", turn.Instruction)
					}
				default:
					t.Fatalf("unexpected Turn %q", turn.Instruction)
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	turns, _ := database.ListTurns(ctx, task.ID)
	t.Fatalf("expected two finished topic Turns, got %d", len(turns))
}
