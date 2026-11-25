package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/channel"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// A pet channel Turn reaches the four aha2_channel tools the same way an
// ordinary routed Task does. The chain is real end to end inside one process:
// the owner routes a group conversation to task_pet over the Web API, the
// channel runtime hands in a Feishu topic message, the channel service turns it into a
// task_pet Turn, and that Turn's token drives the tools. No plugin runtime is
// running and none is started, so the queued reply and permission card stay
// in the isolated database and nothing reaches a real channel.
func TestPetChannelTurnUsesEveryChannelToolThroughARealTaskRoute(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	executor := &blockingAgentAPIExecutor{started: make(chan app.ExecutionRequest, 4), release: make(chan struct{}, 1), done: make(chan struct{})}
	appService := app.NewService(database, nil, executor)
	secrets := &fakeSecretStore{values: map[string]string{}}
	channelService := channel.New(channel.Config{Store: database, App: appService, Secrets: secrets, DataDir: dataDir})
	capabilities := agentapi.NewCapabilities()
	server := httptest.NewServer(New(Config{
		Store: database, Auth: auth.NewService(database, "setup-test", time.Hour),
		App: appService, Channels: channelService, AgentCapabilities: capabilities, Secrets: secrets,
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	owner, err := database.OwnerByUsername(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	activate := func(task domain.Task) {
		t.Helper()
		if task.Status != domain.TaskDraft {
			return
		}
		stamp := now.Format(time.RFC3339Nano)
		if err := database.UpdateTaskStatus(ctx, task.ID, domain.TaskDraft, domain.TaskPreparing, stamp, ""); err != nil {
			t.Fatal(err)
		}
		if err := database.UpdateTaskStatus(ctx, task.ID, domain.TaskPreparing, domain.TaskActive, stamp, ""); err != nil {
			t.Fatal(err)
		}
	}
	base := createHardwareAPITask(t, database)
	activate(base)
	pet := createPetTaskInStore(t, database, base)
	activate(pet)
	snapshot, err := database.RuntimeSnapshot(ctx, base.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}

	plugin := domain.ChannelPlugin{ID: "pet-route-plugin", ProviderKey: "feishu", DisplayName: "飞书", ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1, InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	hostProject := domain.Project{ID: "pet-route-host-project", Name: "Channel", ProjectType: "channel", DefaultWorkspaceID: "pet-route-host-workspace", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	hostWorkspace := domain.Workspace{ID: hostProject.DefaultWorkspaceID, ProjectID: hostProject.ID, Name: "Channel", Locality: "local", Transport: "native", RootPath: dataDir, Health: "ready", AgentAPIMode: "global", AgentAPIStatus: "unknown", CreatedAt: now, UpdatedAt: now}
	instance := domain.ChannelInstance{ID: "pet-route-instance", PluginID: plugin.ID, OwnerID: owner.ID, RuntimeDeviceID: "local", Name: "团队飞书", Status: "ready", Revision: 1, HostProjectID: hostProject.ID, HostWorkspaceID: hostWorkspace.ID, Config: map[string]any{"operation_scope_mode": "all", "runtime_bot_open_id": "pet-route-bot",
		"channel_permission_ids": []string{domain.FeishuFullGroupPermission}, domain.FeishuGroupAllowlistKey: []string{"oc_pet_provider_chat"}}, CreatedAt: now, UpdatedAt: now}
	endpoint := domain.ChannelEndpoint{ID: "pet-route-endpoint", InstanceID: instance.ID, Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateChannelInstanceWithHost(ctx, hostProject, hostWorkspace, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
		t.Fatal(err)
	}
	// The owner's own channel identity, which receives permission cards.
	if _, err := database.BindChannelOwnerIdentity(ctx, domain.ChannelIdentityLink{ID: "pet-route-owner", InstanceID: instance.ID, OwnerID: owner.ID, ExternalUserID: "pet-route-owner-open", Role: "owner", Status: "active", LinkedAt: now}); err != nil {
		t.Fatal(err)
	}
	hostTask, err := appService.CreateTask(ctx, app.CreateTaskInput{ProjectID: hostProject.ID, WorkspaceID: hostWorkspace.ID, Title: "飞书群聊 · 宠物联调群", Request: "host", Isolation: "inplace", ModelID: snapshot.ModelID, Filesystem: "read-only", Approval: "never", CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "enabled", AllowSystemProject: true})
	if err != nil {
		t.Fatal(err)
	}
	conversation := domain.ChannelConversation{ID: "pet-route-conversation", InstanceID: instance.ID, EndpointID: endpoint.ID, ScopeKeyVersion: 2, ScopeKey: "pet-scope", ExternalChatID: "oc_pet_provider_chat", DisplayName: "宠物联调群", HostTaskID: hostTask.ID, Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateChannelConversation(ctx, conversation, domain.ChannelSession{ID: "pet-route-session", ConversationID: conversation.ID, Generation: 1, Mode: "group_qa", Status: "active", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureConversationHostSubscription(ctx, instance.ID, conversation.ID, hostTask.ID, now); err != nil {
		t.Fatal(err)
	}
	// Owner Web: route the group to task_pet and choose its contact.
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+pet.ID+"/channel-routes", map[string]any{"conversation_id": conversation.ID}, csrf)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("owner route to task_pet status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	participant, err := database.UpsertChannelParticipant(ctx, domain.ChannelIdentityLink{ID: "pet-route-participant", InstanceID: instance.ID, ExternalUserID: "provider-user-li", Role: "participant", DisplayName: "李四", Status: "active", LinkedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertChannelConversationMember(ctx, conversation.ID, participant.ID, false, now); err != nil {
		t.Fatal(err)
	}
	contactSelection := []map[string]any{{"identity_link_id": participant.ID, "collaboration_role": "测试"}}
	extraMentionIDs := []string{}
	expectedMentions := []string{participant.ExternalUserID}
	for index := range 6 {
		member, err := database.UpsertChannelParticipant(ctx, domain.ChannelIdentityLink{
			ID: fmt.Sprintf("extra-member-%d", index), InstanceID: instance.ID,
			ExternalUserID: fmt.Sprintf("extra-provider-member-%d", index), Role: "participant",
			DisplayName: fmt.Sprintf("Member %d", index), Status: "active", LinkedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := database.UpsertChannelConversationMember(ctx, conversation.ID, member.ID, false, now); err != nil {
			t.Fatal(err)
		}
		contactSelection = append(contactSelection, map[string]any{"identity_link_id": member.ID, "collaboration_role": "联调"})
		extraMentionIDs = append(extraMentionIDs, member.ID)
		expectedMentions = append(expectedMentions, member.ExternalUserID)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+pet.ID+"/channel-contacts", map[string]any{"contacts": contactSelection}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owner contacts for task_pet status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	// Channel runtime: a human message follows an existing topic without @.
	runCtx, stop := context.WithCancel(ctx)
	channelService.Start(runCtx)
	defer func() {
		stop()
		close(executor.release)
		shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := appService.Shutdown(shutdownCtx); err != nil {
			t.Errorf("waiting for channel Turns to stop: %v", err)
		}
	}()
	receive := func(eventID, messageID, content string) domain.ChannelInboxReceipt {
		t.Helper()
		receipt, duplicate, err := channelService.ReceiveInbound(ctx, channel.RuntimeClaims{
			InstanceID: instance.ID, Scopes: map[string]bool{"channel.inbound.write": true},
		}, domain.ChannelInboundEnvelope{
			SchemaVersion: 1, InstanceID: instance.ID, ExternalEventID: eventID, EventType: "message", OccurredAt: now,
			ChatType: "group", ExternalChatID: conversation.ExternalChatID, ExternalSenderID: "provider-user-li",
			ExternalMessageID: messageID, Content: content, SenderDisplayName: "李四", SenderType: "user",
			ThreadID: "omt_pet", RootID: "om_pet_root",
		})
		if err != nil || duplicate {
			t.Fatalf("inbound receipt=%#v duplicate=%v err=%v", receipt, duplicate, err)
		}
		return receipt
	}
	waitForTurn := func(receiptID string) domain.Turn {
		t.Helper()
		select {
		case request := <-executor.started:
			if request.Task.ID != pet.ID {
				t.Fatalf("routed message started a Turn for %s, want task_pet", request.Task.ID)
			}
			return request.Turn
		case <-time.After(15 * time.Second):
			stored, _ := database.ChannelInboxReceipt(ctx, instance.ID, receiptID)
			turns, _ := database.ListTurns(ctx, pet.ID)
			for _, turn := range turns {
				t.Logf("Turn %d: %s (%s)", turn.Sequence, turn.Status, turn.Error)
			}
			t.Fatalf("routed message did not start a task_pet Turn: receipt state=%s outcome=%s", stored.State, stored.Outcome)
			return domain.Turn{}
		}
	}
	priorReceipt := receive("pet-route-prior-event", "pet-route-prior-message", "Earlier group request")
	priorTurn := waitForTurn(priorReceipt.ID)
	executor.release <- struct{}{}
	waitForTaskStatus(t, database, pet.ID, domain.TaskWaitingUser)
	receipt := receive("pet-route-event-1", "pet-route-message-1", "帮忙看下日志")
	turn := waitForTurn(receipt.ID)
	token, err := capabilities.Issue(pet.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	url := server.URL + "/api/v1/agent/channel/mcp"
	status, names, _ := coreMCPToolsList(t, url, token)
	if status != http.StatusOK || strings.Join(names, ",") != "channel_reply,channel_request_permission,channel_reply_decision,channel_contacts" {
		t.Fatalf("pet channel tools status=%d names=%v", status, names)
	}
	t.Logf("evidence tools/list status=%d names=%v (task=%s turn=%s receipt=%s)", status, names, pet.ID, turn.ID, receipt.ID)
	deliveries := func() []domain.ChannelDelivery {
		t.Helper()
		items, err := database.ChannelDeliveries(ctx, instance.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		return items
	}
	carrying := func(marker string) []domain.ChannelDelivery {
		var found []domain.ChannelDelivery
		for _, item := range deliveries() {
			payload, _ := json.Marshal(item.SemanticPayload)
			if strings.Contains(string(payload), marker) {
				found = append(found, item)
			}
		}
		return found
	}

	if !t.Run("rejects_receipt_from_another_Turn_in_the_same_Task", func(t *testing.T) {
		if priorTurn.ID == turn.ID || priorTurn.TaskID != turn.TaskID {
			t.Fatalf("expected two distinct Turns in the same Task, got %s and %s", priorTurn.ID, turn.ID)
		}
		priorContext, err := database.ChannelContextForReceipt(ctx, pet.ID, priorTurn.ID, priorReceipt.ID)
		if err != nil || priorContext["inbound_receipt_id"] != priorReceipt.ID {
			t.Fatalf("the previous Turn never owned the receipt: context=%v, err=%v", priorContext, err)
		}
		if borrowedContext, err := database.ChannelContextForReceipt(ctx, pet.ID, turn.ID, priorReceipt.ID); borrowedContext != nil || !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("current Turn can read the previous Turn's receipt: context=%v, err=%v", borrowedContext, err)
		}

		const replyText = "REPLY-USING-PREVIOUS-TURN-RECEIPT"
		reply := coreMCPCallTool(t, url, token, "channel_reply", map[string]any{
			"message": replyText, "inbound_receipt_id": priorReceipt.ID,
		})
		if !refused(reply) || len(carrying(replyText)) != 0 {
			t.Errorf("reply using the previous Turn's receipt was not refused: %#v", reply)
		}
		permission := coreMCPCallTool(t, url, token, "channel_request_permission", map[string]any{
			"action": "读取固件日志", "reason": "Earlier group request", "inbound_receipt_id": priorReceipt.ID,
		})
		if !refused(permission) || !strings.Contains(permission.Text, "no channel context") {
			t.Errorf("permission request using the previous Turn's receipt was not refused: %#v", permission)
		}
		requests, err := database.ListTaskPermissionRequests(ctx, pet.ID, 10)
		if err != nil || len(requests) != 0 {
			t.Errorf("attempt using the previous Turn's receipt left permission requests: %v, err=%v", requests, err)
		}
	}) {
		return
	}

	// channel_contacts: the owner-chosen contact, never the provider identity.
	contacts := coreMCPCallTool(t, url, token, "channel_contacts", map[string]any{})
	if contacts.IsError || !strings.Contains(contacts.Text, "李四") || !strings.Contains(contacts.Text, participant.ID) || strings.Contains(contacts.Text, "provider-user-li") {
		t.Fatalf("contacts=%#v", contacts)
	}
	t.Logf("evidence channel_contacts status=%d isError=%v text=%s", contacts.Status, contacts.IsError, contacts.Text)

	// A second sender can join this running Turn through the live inbox.
	liveReceipt, duplicate, err := channelService.ReceiveInbound(ctx, channel.RuntimeClaims{
		InstanceID: instance.ID, Scopes: map[string]bool{"channel.inbound.write": true},
	}, domain.ChannelInboundEnvelope{
		SchemaVersion: 1, InstanceID: instance.ID, ExternalEventID: "pet-route-live-event", EventType: "message",
		ChatType: "group", ExternalChatID: conversation.ExternalChatID, ExternalSenderID: "extra-provider-member-0",
		ExternalMessageID: "pet-route-live-message", Content: "请读取固件日志", SenderType: "user",
		ThreadID: "omt_pet", RootID: "om_pet_root",
	})
	if err != nil || duplicate {
		t.Fatalf("live receipt: %v, duplicate=%v", err, duplicate)
	}
	wait := agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/wait", http.MethodPost, token, map[string]any{
		"after_cursor": 0, "timeout_seconds": 3,
	})
	var batch struct {
		Messages []domain.LiveInboxMessage `json:"messages"`
	}
	decodeErr := json.NewDecoder(wait.Body).Decode(&batch)
	wait.Body.Close()
	if wait.StatusCode != http.StatusOK || decodeErr != nil || len(batch.Messages) != 1 {
		t.Fatalf("live inbox status=%d messages=%v err=%v", wait.StatusCode, batch.Messages, decodeErr)
	}
	live := batch.Messages[0]
	if live.Source.Authority != "task_channel_participant" || live.Payload["channel_receipt_id"] != liveReceipt.ID {
		t.Fatalf("live inbox lost sender authority or its receipt: %v", live)
	}

	// channel_request_permission: a pending request plus an owner card; the
	// pet cannot approve it, the decision stays with the owner.
	permission := coreMCPCallTool(t, url, token, "channel_request_permission", map[string]any{"action": "读取固件日志", "scope": "本群当前问题", "reason": "PET-PERMISSION-REASON", "risk": "只读", "inbound_receipt_id": liveReceipt.ID})
	if permission.IsError || !strings.Contains(permission.Text, "Permission request") {
		t.Fatalf("permission=%#v", permission)
	}
	t.Logf("evidence channel_request_permission status=%d isError=%v text=%s", permission.Status, permission.IsError, permission.Text)
	requests, err := database.ListTaskPermissionRequests(ctx, pet.ID, 10)
	if err != nil || len(requests) != 1 || requests[0].Status != "pending" || requests[0].InboundReceiptID != liveReceipt.ID || requests[0].ApplicantIdentityLinkID != extraMentionIDs[0] {
		t.Fatalf("permission requests=%#v err=%v", requests, err)
	}
	t.Logf("evidence permission request id=%s status=%s receipt=%s", requests[0].ID, requests[0].Status, requests[0].InboundReceiptID)

	// channel_reply: queued once for this receipt; a retry is idempotent.
	for attempt := 0; attempt < 2; attempt++ {
		reply := coreMCPCallTool(t, url, token, "channel_reply", map[string]any{"message": "PET-CHANNEL-REPLY"})
		if reply.IsError || !strings.Contains(reply.Text, "Reply queued") {
			t.Fatalf("reply attempt %d=%#v", attempt, reply)
		}
		t.Logf("evidence channel_reply attempt=%d status=%d isError=%v text=%s", attempt, reply.Status, reply.IsError, reply.Text)
	}
	replies := carrying("PET-CHANNEL-REPLY")
	if len(replies) != 1 || replies[0].ConversationID != conversation.ID || replies[0].ProviderMessageID != "" || replies[0].Attempts != 0 {
		t.Fatalf("queued replies=%#v", replies)
	}
	t.Logf("evidence reply delivery id=%s state=%s attempts=%d provider_message_id=%q (one row after two calls)", replies[0].ID, replies[0].State, replies[0].Attempts, replies[0].ProviderMessageID)
	target, err := database.ChannelDeliveryTarget(ctx, conversation.ID, replies[0].SourceEventSequence)
	if err != nil || target["thread_id"] != "omt_pet" || target["root_id"] != "om_pet_root" ||
		target["reply_message_id"] != "pet-route-message-1" || target["mention_user_ids"] != `["provider-user-li"]` {
		t.Fatalf("automatic topic reply target=%v err=%v", target, err)
	}
	wide := coreMCPCallTool(t, url, token, "channel_reply", map[string]any{
		"message": "MANY-MENTIONS", "mention_identity_link_ids": extraMentionIDs,
	})
	if wide.IsError {
		t.Fatalf("six valid additional mentions: %v", wide)
	}
	mentionReplies := carrying("MANY-MENTIONS")
	if len(mentionReplies) != 1 {
		t.Fatalf("explicit mention reply count=%d", len(mentionReplies))
	}
	fullTarget, err := database.ChannelDeliveryTarget(ctx, conversation.ID, mentionReplies[0].SourceEventSequence)
	want, _ := json.Marshal(expectedMentions)
	if err != nil || fullTarget["mention_user_ids"] != string(want) {
		t.Fatalf("explicit mention target=%v want=%s err=%v", fullTarget, want, err)
	}

	liveReply := coreMCPCallTool(t, url, token, "channel_reply", map[string]any{
		"message": "PET-LIVE-REPLY", "inbound_receipt_id": liveReceipt.ID,
	})
	liveReplies := carrying("PET-LIVE-REPLY")
	if liveReply.IsError || len(liveReplies) != 1 {
		t.Fatalf("live receipt reply=%#v, deliveries=%v", liveReply, liveReplies)
	}
	liveTarget, err := database.ChannelDeliveryTarget(ctx, conversation.ID, liveReplies[0].SourceEventSequence)
	if err != nil || liveTarget["thread_id"] != "omt_pet" || liveTarget["reply_message_id"] != "pet-route-live-message" ||
		liveTarget["mention_user_ids"] != `["extra-provider-member-0"]` {
		t.Fatalf("reply used the wrong live message target: %v err=%v", liveTarget, err)
	}

	// channel_reply_decision records the end decision for the group topic.
	decision := coreMCPCallTool(t, url, token, "channel_reply_decision", map[string]any{"reason": "已在群里回复"})
	if decision.IsError {
		t.Fatalf("decision=%#v", decision)
	}
	stored, err := database.ChannelTopicReplyDecision(ctx, turn.ID, conversation.ID, "omt_pet")
	if err != nil || stored != "end" {
		t.Fatalf("topic decision=%q err=%v", stored, err)
	}
	t.Logf("evidence channel_reply_decision status=%d isError=%v text=%s topic.decision=%s", decision.Status, decision.IsError, decision.Text, stored)

	// Boundary: another Task's Turn cannot borrow task_pet's receipt.
	otherTurn := createCoreTurn(t, database, base, "main", "batch-pet-route-other")
	otherToken, err := capabilities.Issue(base.ID, "main", otherTurn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	borrowed := coreMCPCallTool(t, url, otherToken, "channel_reply", map[string]any{"message": "BORROWED-REPLY", "inbound_receipt_id": receipt.ID})
	if !refused(borrowed) || len(carrying("BORROWED-REPLY")) != 0 {
		t.Fatalf("borrowed receipt reply=%#v", borrowed)
	}
	t.Logf("evidence borrowed-receipt channel_reply from %s status=%d isError=%v text=%s", base.ID, borrowed.Status, borrowed.IsError, borrowed.Text)
	refusedDecision := coreMCPCallTool(t, url, otherToken, "channel_reply_decision", map[string]any{"reason": "x", "inbound_receipt_id": receipt.ID})
	if !refused(refusedDecision) {
		t.Fatalf("borrowed receipt decision=%#v", refusedDecision)
	}
	t.Logf("evidence borrowed-receipt channel_reply_decision status=%d isError=%v text=%s", refusedDecision.Status, refusedDecision.IsError, refusedDecision.Text)
	refusedPermission := coreMCPCallTool(t, url, token, "channel_request_permission", map[string]any{
		"action": "读取固件日志", "reason": "foreign receipt", "inbound_receipt_id": "unrelated-receipt",
	})
	if !refused(refusedPermission) || !strings.Contains(refusedPermission.Text, "no channel context") {
		t.Fatalf("permission request accepted an unrelated receipt: %#v", refusedPermission)
	}

	for _, item := range deliveries() {
		if item.ProviderMessageID != "" || item.Attempts != 0 {
			t.Fatalf("a delivery left the process: %#v", item)
		}
		t.Logf("evidence delivery id=%s state=%s attempts=%d provider_message_id=%q", item.ID, item.State, item.Attempts, item.ProviderMessageID)
	}
}
