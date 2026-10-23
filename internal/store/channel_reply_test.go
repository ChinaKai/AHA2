package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestAgentChannelReplyIsReceiptBoundAndIdempotent(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "work", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnRunning,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		InboxBatchID:            domain.NewID("inbox_batch"), Attempt: 1, Required: true, QueuedAt: now,
	}
	if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn); err != nil {
		t.Fatal(err)
	}

	plugin := domain.ChannelPlugin{
		ID: domain.NewID("plugin"), ProviderKey: "feishu", DisplayName: "Feishu",
		ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1,
		InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateOwner(ctx, domain.Owner{
		ID: "reply-owner", Username: "reply-owner", PasswordHash: "hash", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	hostProject := domain.Project{
		ID: domain.NewID("project"), Name: "Channel", ProjectType: "channel",
		DefaultWorkspaceID: domain.NewID("workspace"), KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	hostWorkspace := domain.Workspace{
		ID: hostProject.DefaultWorkspaceID, ProjectID: hostProject.ID, Name: "Channel",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	instance := domain.ChannelInstance{
		ID: domain.NewID("instance"), PluginID: plugin.ID, OwnerID: "reply-owner",
		RuntimeDeviceID: "local", Name: "Channel", Status: "ready", Revision: 1,
		HostProjectID: hostProject.ID, HostWorkspaceID: hostWorkspace.ID,
		Config: map[string]any{"operation_scope_mode": "all"}, CreatedAt: now, UpdatedAt: now,
	}
	endpoint := domain.ChannelEndpoint{
		ID: domain.NewID("endpoint"), InstanceID: instance.ID,
		Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, Config: map[string]any{},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelInstanceWithHost(
		ctx, hostProject, hostWorkspace, instance, []domain.ChannelEndpoint{endpoint},
	); err != nil {
		t.Fatal(err)
	}
	hostTask := createStoreTestTask(t, database)
	conversation := domain.ChannelConversation{
		ID: domain.NewID("conversation"), InstanceID: instance.ID, EndpointID: endpoint.ID,
		ScopeKeyVersion: 2, ScopeKey: "reply-scope", ExternalChatID: "provider-chat",
		DisplayName: "Reply Group", HostTaskID: hostTask.ID, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelConversation(ctx, conversation, domain.ChannelSession{
		ID: domain.NewID("session"), ConversationID: conversation.ID, Generation: 1,
		Mode: "group_qa", Status: "active", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateOwnerChannelTaskRoute(
		ctx, instance.ID, conversation.ID, task.ID, now,
	); err != nil {
		t.Fatal(err)
	}
	actor, err := database.UpsertChannelParticipant(ctx, domain.ChannelIdentityLink{
		ID: "reply-actor", InstanceID: instance.ID, ExternalUserID: "provider-user-reply",
		Role: "participant", DisplayName: "小红", Status: "active", LinkedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertChannelConversationMember(ctx, conversation.ID, actor.ID, false, now); err != nil {
		t.Fatal(err)
	}
	receiptID := domain.NewID("channel_inbox")
	normalized, _ := json.Marshal(map[string]any{
		"external_message_id": "provider-message-reply",
		"mentioned_bot":       true,
	})
	channelContext := map[string]any{
		"schema": "aha.channel-context/v1", "provider": "feishu", "instance_id": instance.ID,
		"conversation_id": conversation.ID, "endpoint": endpoint.Kind,
		"chat_display_name": conversation.DisplayName, "inbound_receipt_id": receiptID,
		"route": map[string]any{"mode": "task_route", "target_task_id": task.ID},
		"actor": map[string]any{
			"identity_link_id": actor.ID, "display_name": actor.DisplayName,
			"role": actor.Role, "is_bot": false,
		},
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO channel_inbox_dedup(
			id,instance_id,external_event_id,event_type,payload_digest,normalized_payload_json,
			state,lease_id,lease_until,conversation_id,aha_message_id,outcome,
			occurred_at,received_at,processed_at
		) VALUES(?,?,?,?,?,?, 'processed','','',?,'','delivered_to_task',?,?,?)`,
		receiptID, instance.ID, "reply-event", "message", "digest", string(normalized),
		conversation.ID, timeString(now), timeString(now), timeString(now),
	); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"channel_receipt_id": receiptID, "channel_context": channelContext}
	payloadJSON := encodeJSON(payload)
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,
			route_kind,category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?, 'owner','main','owner','main','owner_message','chat','user_message',?,?,?)`,
		domain.NewID("conversation"), task.ID, round.ID, turn.ID,
		"你好", payloadJSON, timeString(now),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO agent_inbox(
			id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,
			message_id,content,payload_json,status,batch_id,created_at,claimed_at,processed_at,
			delivered_turn_id,delivered_at
		) VALUES(?,?,?, 'main','owner','owner_message','','',?,?, 'delivered_in_turn','',?,?,?,?,?)`,
		domain.NewID("inbox"), task.ID, round.ID, "你好", payloadJSON,
		timeString(now), timeString(now), "", turn.ID, timeString(now),
	); err != nil {
		t.Fatal(err)
	}

	delivery, itemID, created, err := database.EnqueueAgentChannelReply(
		ctx, task.ID, turn.ID, "reply-request-1", receiptID, "早上好", nil, now,
	)
	if err != nil || !created || delivery.ID == "" || itemID == "" {
		t.Fatalf("reply delivery=%#v item=%q created=%v err=%v", delivery, itemID, created, err)
	}
	retry, retryItemID, retryCreated, err := database.EnqueueAgentChannelReply(
		ctx, task.ID, turn.ID, "reply-request-1", receiptID, "早上好", nil, now,
	)
	if err != nil || retryCreated || retry.ID != delivery.ID || retryItemID != itemID {
		t.Fatalf("reply retry=%#v item=%q created=%v err=%v", retry, retryItemID, retryCreated, err)
	}
	var cardSummary, cardPayloadJSON string
	if err := database.db.QueryRowContext(ctx,
		`SELECT summary,payload_json FROM conversation_items WHERE id=?`, itemID,
	).Scan(&cardSummary, &cardPayloadJSON); err != nil {
		t.Fatal(err)
	}
	cardPayload := decodeJSON(cardPayloadJSON, map[string]any{})
	if cardSummary != "AHA 已回复渠道消息：早上好" || cardPayload["message"] != "早上好" {
		t.Fatalf("reply card summary=%q payload=%#v", cardSummary, cardPayload)
	}
	target, err := database.ChannelDeliveryTarget(ctx, conversation.ID, delivery.SourceEventSequence)
	if err != nil || target["reply_message_id"] != "provider-message-reply" ||
		target["mention_user_ids"] != `["provider-user-reply"]` {
		t.Fatalf("reply target=%#v err=%v", target, err)
	}

	botReceiptID := domain.NewID("channel_inbox")
	botNormalized, _ := json.Marshal(map[string]any{
		"external_message_id": "provider-message-bot",
		"mentioned_bot":       true,
	})
	botContext := make(map[string]any, len(channelContext))
	for key, value := range channelContext {
		botContext[key] = value
	}
	botContext["inbound_receipt_id"] = botReceiptID
	botContext["actor"] = map[string]any{
		"identity_link_id": actor.ID, "display_name": "联调机器人",
		"role": "participant", "is_bot": true,
	}
	botPayloadJSON := encodeJSON(map[string]any{"channel_receipt_id": botReceiptID, "channel_context": botContext})
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO channel_inbox_dedup(
			id,instance_id,external_event_id,event_type,payload_digest,normalized_payload_json,
			state,lease_id,lease_until,conversation_id,aha_message_id,outcome,
			occurred_at,received_at,processed_at
		) VALUES(?,?,?,?,?,?, 'processed','','',?,'','delivered_to_task',?,?,?)`,
		botReceiptID, instance.ID, "reply-bot-event", "message", "digest-bot", string(botNormalized),
		conversation.ID, timeString(now.Add(time.Second)), timeString(now.Add(time.Second)), timeString(now.Add(time.Second)),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,
			route_kind,category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?, 'owner','main','owner','main','owner_message','chat','user_message',?,?,?)`,
		domain.NewID("conversation"), task.ID, round.ID, turn.ID,
		"机器人问题", botPayloadJSON, timeString(now.Add(time.Second)),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO agent_inbox(
			id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,
			message_id,content,payload_json,status,batch_id,created_at,claimed_at,processed_at,
			delivered_turn_id,delivered_at
		) VALUES(?,?,?, 'main','owner','owner_message','','',?,?, 'delivered_in_turn','',?,?,?,?,?)`,
		domain.NewID("inbox"), task.ID, round.ID, "机器人问题", botPayloadJSON,
		timeString(now.Add(time.Second)), timeString(now.Add(time.Second)), "", turn.ID, timeString(now.Add(time.Second)),
	); err != nil {
		t.Fatal(err)
	}

	botDelivery, _, _, err := database.EnqueueAgentChannelReply(
		ctx, task.ID, turn.ID, "bot-reply-request", botReceiptID, "机器人回复", nil, now.Add(time.Second),
	)
	if err != nil || botDelivery.ID == "" {
		t.Fatalf("explicit bot reply delivery=%#v err=%v", botDelivery, err)
	}
	var replyDecision string
	if err := database.db.QueryRowContext(ctx,
		`SELECT channel_reply_decision FROM turns WHERE id=?`, turn.ID,
	).Scan(&replyDecision); err != nil || replyDecision != "continue" {
		t.Fatalf("bot reply decision=%q err=%v", replyDecision, err)
	}
	statuses, err := database.ReconcileChannelMessageStatuses(
		ctx, task.ID, turn.ID, botReceiptID, "replied", "", now.Add(2*time.Second),
	)
	if err != nil || len(statuses) != 1 || statuses[0].Payload["message_status"] != "replied" ||
		statuses[0].Payload["delivery_state"] != "pending" {
		t.Fatalf("replied status cards=%#v err=%v", statuses, err)
	}
	if _, err := database.db.ExecContext(ctx,
		`UPDATE turns SET channel_reply_decision='end' WHERE id=?`, turn.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := database.EnqueueAgentChannelReply(
		ctx, task.ID, turn.ID, "bot-reply-after-end", botReceiptID, "不应发送", nil, now.Add(2*time.Second),
	); err == nil || !strings.Contains(err.Error(), "end decision") {
		t.Fatalf("reply after bot dialogue end err=%v", err)
	}

	if err := database.AppendChannelSourceAndProject(ctx, domain.ChannelSourceEvent{
		ID: domain.NewID("source"), SourceKey: "ordinary-final-reply", TaskID: task.ID,
		RoundID: round.ID, TurnID: turn.ID, EventClass: "message", EventType: "agent_reply",
		SemanticPayload: map[string]any{"task_id": task.ID, "text": "Web-only final"}, OccurredAt: now,
	}, ""); err != nil {
		t.Fatal(err)
	}
	deliveries, err := database.ChannelDeliveries(ctx, instance.ID, 20)
	if err != nil || len(deliveries) != 2 ||
		!slices.ContainsFunc(deliveries, func(item domain.ChannelDelivery) bool { return item.ID == delivery.ID }) ||
		!slices.ContainsFunc(deliveries, func(item domain.ChannelDelivery) bool { return item.ID == botDelivery.ID }) {
		t.Fatalf("ordinary final reply leaked into task route: %#v err=%v", deliveries, err)
	}
}

func TestChannelStatusOutcome(t *testing.T) {
	for _, scenario := range []struct {
		name, terminal, reason, decision string
		replied                          bool
		wantStatus, wantReason           string
	}{
		{"replied", "succeeded", "", "", true, "replied", ""},
		{"decision end", "decided_end", "reply_decision_end", "end", false, "not_replied", "reply_decision_end"},
		{"continue pending", "decided_continue", "reply_decision_continue", "continue", false, "pending_reply", "reply_decision_continue"},
		{"turn success silent", "succeeded", "", "", false, "not_replied", "no_channel_reply"},
		{"turn failed", "failed", "", "", false, "failed", "turn_failed"},
		{"turn interrupted", "interrupted", "", "", false, "interrupted", "turn_interrupted"},
		{"max turns", "reply_rejected", "bot_dialogue_max_turns", "", false, "not_replied", "bot_dialogue_max_turns"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			status, reason := channelStatusOutcome(scenario.terminal, scenario.reason, scenario.decision, scenario.replied)
			if status != scenario.wantStatus || reason != scenario.wantReason {
				t.Fatalf("status=%q reason=%q", status, reason)
			}
		})
	}
}

func TestCompactChannelReply(t *testing.T) {
	if got := compactChannelReply("  第一行\n\n第二行\t内容  "); got != "第一行 第二行 内容" {
		t.Fatalf("compact reply=%q", got)
	}
	long := strings.Repeat("字", 130)
	got := compactChannelReply(long)
	if len([]rune(got)) != 120 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long reply length=%d value=%q", len([]rune(got)), got)
	}
}
