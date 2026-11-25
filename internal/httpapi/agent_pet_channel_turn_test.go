package httpapi

import (
	"context"
	"encoding/json"
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
// channel runtime hands in an @-mention, the channel service turns it into a
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
	executor := &blockingAgentAPIExecutor{started: make(chan app.ExecutionRequest, 4), release: make(chan struct{}), done: make(chan struct{})}
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
	instance := domain.ChannelInstance{ID: "pet-route-instance", PluginID: plugin.ID, OwnerID: owner.ID, RuntimeDeviceID: "local", Name: "团队飞书", Status: "ready", Revision: 1, HostProjectID: hostProject.ID, HostWorkspaceID: hostWorkspace.ID, Config: map[string]any{"operation_scope_mode": "all", "runtime_bot_open_id": "pet-route-bot"}, CreatedAt: now, UpdatedAt: now}
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
	conversation := domain.ChannelConversation{ID: "pet-route-conversation", InstanceID: instance.ID, EndpointID: endpoint.ID, ScopeKeyVersion: 2, ScopeKey: "pet-scope", ExternalChatID: "pet-provider-chat", DisplayName: "宠物联调群", HostTaskID: hostTask.ID, Status: "active", CreatedAt: now, UpdatedAt: now}
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
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+pet.ID+"/channel-contacts", map[string]any{"contacts": []map[string]any{{"identity_link_id": participant.ID, "collaboration_role": "测试"}}}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owner contacts for task_pet status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	// Channel runtime: one @-mention; the channel service routes it to task_pet.
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	channelService.Start(runCtx)
	receipt, duplicate, err := channelService.ReceiveInbound(ctx, channel.RuntimeClaims{InstanceID: instance.ID, Scopes: map[string]bool{"channel.inbound.write": true}}, domain.ChannelInboundEnvelope{
		SchemaVersion: 1, InstanceID: instance.ID, ExternalEventID: "pet-route-event-1", EventType: "message", OccurredAt: now,
		ChatType: "group", ExternalChatID: conversation.ExternalChatID, ExternalSenderID: "provider-user-li", ExternalMessageID: "pet-route-message-1",
		Content: "@AHA 帮忙看下日志", SenderDisplayName: "李四", MentionedBot: true,
	})
	if err != nil || duplicate {
		t.Fatalf("inbound receipt=%#v duplicate=%v err=%v", receipt, duplicate, err)
	}
	var turn domain.Turn
	select {
	case request := <-executor.started:
		if request.Task.ID != pet.ID {
			t.Fatalf("routed message started a Turn for %s, want task_pet", request.Task.ID)
		}
		turn = request.Turn
	case <-time.After(15 * time.Second):
		t.Fatal("routed channel message did not start a task_pet Turn")
	}
	defer func() {
		close(executor.release)
		select {
		case <-executor.done:
		case <-time.After(10 * time.Second):
		}
		stop()
	}()
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

	// channel_contacts: the owner-chosen contact, never the provider identity.
	contacts := coreMCPCallTool(t, url, token, "channel_contacts", map[string]any{})
	if contacts.IsError || !strings.Contains(contacts.Text, "李四") || !strings.Contains(contacts.Text, participant.ID) || strings.Contains(contacts.Text, "provider-user-li") {
		t.Fatalf("contacts=%#v", contacts)
	}
	t.Logf("evidence channel_contacts status=%d isError=%v text=%s", contacts.Status, contacts.IsError, contacts.Text)

	// channel_request_permission: a pending request plus an owner card; the
	// pet cannot approve it, the decision stays with the owner.
	permission := coreMCPCallTool(t, url, token, "channel_request_permission", map[string]any{"action": "读取固件日志", "scope": "本群当前问题", "reason": "PET-PERMISSION-REASON", "risk": "只读"})
	if permission.IsError || !strings.Contains(permission.Text, "Permission request") {
		t.Fatalf("permission=%#v", permission)
	}
	t.Logf("evidence channel_request_permission status=%d isError=%v text=%s", permission.Status, permission.IsError, permission.Text)
	requests, err := database.ListTaskPermissionRequests(ctx, pet.ID, 10)
	if err != nil || len(requests) != 1 || requests[0].Status != "pending" || requests[0].InboundReceiptID != receipt.ID || requests[0].ApplicantIdentityLinkID == "" {
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

	// channel_reply_decision records the end decision for the group topic.
	decision := coreMCPCallTool(t, url, token, "channel_reply_decision", map[string]any{"reason": "已在群里回复"})
	if decision.IsError {
		t.Fatalf("decision=%#v", decision)
	}
	stored, err := database.ChannelTopicReplyDecision(ctx, turn.ID, conversation.ID, "")
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
	for _, item := range deliveries() {
		if item.ProviderMessageID != "" || item.Attempts != 0 {
			t.Fatalf("a delivery left the process: %#v", item)
		}
		t.Logf("evidence delivery id=%s state=%s attempts=%d provider_message_id=%q", item.ID, item.State, item.Attempts, item.ProviderMessageID)
	}
}
