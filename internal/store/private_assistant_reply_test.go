package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type privateAssistantReplyFixture struct {
	database       *Store
	task           domain.Task
	round          domain.TaskRound
	turn           domain.Turn
	instance       domain.ChannelInstance
	conversation   domain.ChannelConversation
	inboundReceipt string
	now            time.Time
}

func newPrivateAssistantReplyFixture(t *testing.T) privateAssistantReplyFixture {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	task := createStoreTestTask(t, database)
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "hello", CreatedAt: now,
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
	owner := domain.Owner{
		ID: domain.NewID("owner"), Username: "private-reply-owner", PasswordHash: "hash", CreatedAt: now,
	}
	if err := database.CreateOwner(ctx, owner); err != nil {
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
		ID: domain.NewID("instance"), PluginID: plugin.ID, OwnerID: owner.ID,
		RuntimeDeviceID: "local", Name: "Private", Status: "ready", Revision: 1,
		HostProjectID: hostProject.ID, HostWorkspaceID: hostWorkspace.ID,
		Config: map[string]any{"operation_scope_mode": "all"}, CreatedAt: now, UpdatedAt: now,
	}
	endpoint := domain.ChannelEndpoint{
		ID: domain.NewID("endpoint"), InstanceID: instance.ID,
		Kind: domain.ChannelEndpointAssistantDM, Enabled: true, Config: map[string]any{},
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
		ScopeKeyVersion: 2, ScopeKey: "private-reply-scope", ExternalChatID: "provider-chat",
		DisplayName: "Owner", HostTaskID: hostTask.ID, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelConversation(ctx, conversation, domain.ChannelSession{
		ID: domain.NewID("session"), ConversationID: conversation.ID, Generation: 1,
		Mode: "assistant", Status: "active", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, task.ID, now); err != nil {
		t.Fatal(err)
	}
	actor, err := database.UpsertChannelParticipant(ctx, domain.ChannelIdentityLink{
		ID: domain.NewID("identity"), InstanceID: instance.ID, ExternalUserID: "private-user",
		Role: "participant", DisplayName: "Owner", Status: "active", LinkedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	receiptID := domain.NewID("channel_inbox")
	normalized, _ := json.Marshal(map[string]any{
		"external_message_id": "private-provider-message",
		"chat_type":           "p2p",
	})
	channelContext := map[string]any{
		"schema": "aha.channel-context/v1", "provider": "feishu", "instance_id": instance.ID,
		"conversation_id": conversation.ID, "endpoint": endpoint.Kind,
		"chat_display_name": conversation.DisplayName, "inbound_receipt_id": receiptID,
		"route": map[string]any{"mode": "task_route", "target_task_id": task.ID},
		"actor": map[string]any{
			"identity_link_id": actor.ID, "display_name": actor.DisplayName,
			"role": "owner", "is_bot": false,
		},
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO channel_inbox_dedup(
			id,instance_id,external_event_id,event_type,payload_digest,normalized_payload_json,
			state,lease_id,lease_until,conversation_id,aha_message_id,outcome,
			occurred_at,received_at,processed_at
		) VALUES(?,?,?,?,?,?, 'processed','','',?,'','delivered_to_task',?,?,?)`,
		receiptID, instance.ID, "private-event", "message", "digest", string(normalized),
		conversation.ID, timeString(now), timeString(now), timeString(now),
	); err != nil {
		t.Fatal(err)
	}
	payloadJSON := encodeJSON(map[string]any{
		"channel_receipt_id": receiptID,
		"channel_context":    channelContext,
	})
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,
			route_kind,category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?, 'owner','main','owner','main','owner_message','chat','user_message',?,?,?)`,
		domain.NewID("conversation"), task.ID, round.ID, turn.ID,
		"hello", payloadJSON, timeString(now),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO agent_inbox(
			id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,
			message_id,content,payload_json,status,batch_id,created_at,claimed_at,processed_at,
			delivered_turn_id,delivered_at
		) VALUES(?,?,?, 'main','owner','owner_message','','',?,?, 'claimed',?,?,?,?, '', '')`,
		domain.NewID("inbox"), task.ID, round.ID, "hello", payloadJSON,
		turn.InboxBatchID, timeString(now), timeString(now), timeString(now),
	); err != nil {
		t.Fatal(err)
	}
	return privateAssistantReplyFixture{
		database: database, task: task, round: round, turn: turn,
		instance: instance, conversation: conversation, inboundReceipt: receiptID, now: now,
	}
}

func TestPrivateAssistantFinalReplyFallsBackWhenModelSkipsReply(t *testing.T) {
	fixture := newPrivateAssistantReplyFixture(t)
	delivery, conversationItemID, created, err := fixture.database.EnqueuePrivateAssistantFinalReply(
		context.Background(), fixture.task.ID, fixture.turn.ID, "Hello from the final answer", nil, fixture.now,
	)
	if err != nil || !created || delivery.ID == "" || conversationItemID == "" {
		t.Fatalf("fallback delivery=%#v item=%q created=%v err=%v", delivery, conversationItemID, created, err)
	}
	if delivery.ConversationID != fixture.conversation.ID {
		t.Fatalf("delivery conversation=%q want=%q", delivery.ConversationID, fixture.conversation.ID)
	}
	var status, receiptID, text string
	if err := fixture.database.db.QueryRowContext(context.Background(), `
		SELECT json_extract(payload_json,'$.message_status'),
		       json_extract(payload_json,'$.inbound_receipt_id'),
		       json_extract(payload_json,'$.reply_message')
		FROM conversation_items WHERE id=?`, conversationItemID,
	).Scan(&status, &receiptID, &text); err != nil {
		t.Fatal(err)
	}
	if status != "replied" || receiptID != fixture.inboundReceipt || text != "Hello from the final answer" {
		t.Fatalf("fallback status=%q receipt=%q text=%q", status, receiptID, text)
	}
	if _, _, created, err := fixture.database.EnqueuePrivateAssistantFinalReply(
		context.Background(), fixture.task.ID, fixture.turn.ID, "Hello from the final answer", nil, fixture.now,
	); err != nil || created {
		t.Fatalf("duplicate fallback created=%v err=%v", created, err)
	}
}

func TestPrivateAssistantFinalReplyDoesNotDuplicateExplicitReply(t *testing.T) {
	fixture := newPrivateAssistantReplyFixture(t)
	explicit, _, created, err := fixture.database.EnqueueAgentChannelReply(
		context.Background(), fixture.task.ID, fixture.turn.ID, "explicit-reply",
		fixture.inboundReceipt, "Explicit channel answer", nil, fixture.now,
	)
	if err != nil || !created || explicit.ID == "" {
		t.Fatalf("explicit delivery=%#v created=%v err=%v", explicit, created, err)
	}
	if _, _, created, err := fixture.database.EnqueuePrivateAssistantFinalReply(
		context.Background(), fixture.task.ID, fixture.turn.ID, "Duplicate final answer", nil, fixture.now,
	); err != nil || created {
		t.Fatalf("fallback after explicit reply created=%v err=%v", created, err)
	}
	var count int
	if err := fixture.database.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM channel_delivery_outbox
		WHERE conversation_id=? AND json_extract(semantic_payload_json,'$.reply_to_receipt_id')=?`,
		fixture.conversation.ID, fixture.inboundReceipt,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("reply delivery count=%d want=1", count)
	}
}

func TestPrivateAssistantFinalReplyFindsLiveInboxDelivery(t *testing.T) {
	fixture := newPrivateAssistantReplyFixture(t)
	if _, err := fixture.database.db.ExecContext(context.Background(), `
		UPDATE agent_inbox
		SET status='delivered_in_turn',batch_id='',delivered_turn_id=?,delivered_at=?
		WHERE task_id=? AND target_agent_id='main'`,
		fixture.turn.ID, timeString(fixture.now), fixture.task.ID,
	); err != nil {
		t.Fatal(err)
	}
	_, _, created, err := fixture.database.EnqueuePrivateAssistantFinalReply(
		context.Background(), fixture.task.ID, fixture.turn.ID, "Live inbox answer", nil, fixture.now,
	)
	if err != nil || !created {
		t.Fatalf("live inbox fallback created=%v err=%v", created, err)
	}
}
