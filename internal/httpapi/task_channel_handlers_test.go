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

func TestTaskChannelRouteAPI(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	executor := &blockingAgentAPIExecutor{started: make(chan app.ExecutionRequest, 1), release: make(chan struct{})}
	appService := app.NewService(database, nil, executor)
	channelService := channel.New(channel.Config{Store: database, App: appService, DataDir: dataDir})
	capabilities := agentapi.NewCapabilities()
	server := httptest.NewServer(New(Config{
		Store: database, Auth: auth.NewService(database, "setup-test", time.Hour),
		App: appService, Channels: channelService, AgentCapabilities: capabilities,
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
	env := domain.EnvGroup{ID: "channel-route-env", Name: "Channel route", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "channel-route-model", DisplayName: "Channel route", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "channel-route-project", Name: "Firmware", DefaultWorkspaceID: "channel-route-workspace", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: project.DefaultWorkspaceID, ProjectID: project.ID, Name: "Firmware", Locality: "local", Transport: "native", RootPath: dataDir, Health: "ready", AgentAPIMode: "global", AgentAPIStatus: "unknown", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	task, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "联调", Request: "联调",
		Isolation: "inplace", Backend: model.Backend, ModelSource: model.Source, ModelID: model.ID, WireModel: model.WireModel,
		Filesystem: "workspace-write", Approval: "never", CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "inherit",
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{
		ID: "channel-route-plugin", ProviderKey: "feishu", DisplayName: "飞书", ManifestVersion: 1, PackageVersion: "1",
		ProtocolMin: 1, ProtocolMax: 1, InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	hostProject := domain.Project{ID: "channel-host-project", Name: "Channel", ProjectType: "channel", DefaultWorkspaceID: "channel-host-workspace", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	hostWorkspace := domain.Workspace{ID: hostProject.DefaultWorkspaceID, ProjectID: hostProject.ID, Name: "Channel", Locality: "local", Transport: "native", RootPath: dataDir, Health: "ready", AgentAPIMode: "global", AgentAPIStatus: "unknown", CreatedAt: now, UpdatedAt: now}
	instance := domain.ChannelInstance{
		ID: "channel-route-instance", PluginID: plugin.ID, OwnerID: owner.ID, RuntimeDeviceID: "local",
		Name: "团队飞书", Status: "ready", Revision: 1, HostProjectID: hostProject.ID, HostWorkspaceID: hostWorkspace.ID,
		Config: map[string]any{
			"operation_scope_mode":              "all",
			"runtime_bot_open_id":               "current-bot-open",
			"runtime_bot_display_name_override": "AHA-WORK",
			"runtime_bot_provider_display_name": "飞书应用原名",
		},
		CreatedAt: now, UpdatedAt: now,
	}
	endpoint := domain.ChannelEndpoint{ID: "channel-route-endpoint", InstanceID: instance.ID, Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateChannelInstanceWithHost(ctx, hostProject, hostWorkspace, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
		t.Fatal(err)
	}
	hostTask, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: hostProject.ID, WorkspaceID: hostWorkspace.ID, Title: "飞书群聊 · APP 与固件联调群", Request: "host",
		Isolation: "inplace", Backend: model.Backend, ModelSource: model.Source, ModelID: model.ID, WireModel: model.WireModel,
		Filesystem: "read-only", Approval: "never", CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "enabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation := domain.ChannelConversation{
		ID: "channel-route-conversation", InstanceID: instance.ID, EndpointID: endpoint.ID, ScopeKeyVersion: 2, ScopeKey: "scope",
		ExternalChatID: "provider-chat", DisplayName: "APP 与固件联调群", HostTaskID: hostTask.ID, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelConversation(ctx, conversation, domain.ChannelSession{ID: "channel-route-session", ConversationID: conversation.ID, Generation: 1, Mode: "group_qa", Status: "active", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureConversationHostSubscription(ctx, instance.ID, conversation.ID, hostTask.ID, now); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/channel-routes", nil, "")
	var listed struct {
		Destinations []domain.ChannelDestination `json:"destinations"`
		Route        *domain.ChannelTaskRoute    `json:"route"`
	}
	decodeResponse(t, response, &listed)
	if response.StatusCode != http.StatusOK || len(listed.Destinations) != 1 || listed.Route != nil {
		t.Fatalf("list status=%d payload=%#v", response.StatusCode, listed)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+task.ID+"/channel-routes", map[string]any{"conversation_id": conversation.ID}, csrf)
	var bound struct {
		Route domain.ChannelTaskRoute `json:"route"`
	}
	decodeResponse(t, response, &bound)
	if response.StatusCode != http.StatusCreated || bound.Route.TargetTaskID != task.ID || bound.Route.ConversationID != conversation.ID {
		t.Fatalf("bind status=%d payload=%#v", response.StatusCode, bound)
	}
	runtimeClaims := channel.RuntimeClaims{
		InstanceID: instance.ID,
		Scopes:     map[string]bool{"channel.command.claim": true, "channel.command.complete": true},
	}
	commands, err := channelService.ClaimCommands(ctx, runtimeClaims, 5)
	if err != nil || len(commands) != 1 || commands[0].Kind != "sync_chat_members" {
		t.Fatalf("member sync command=%#v err=%v", commands, err)
	}
	if err := channelService.CompleteCommand(ctx, runtimeClaims, commands[0].ID, commands[0].LeaseID, true, map[string]any{
		"members": []any{map[string]any{"external_user_id": "provider-user-zhang", "display_name": "张三", "is_bot": false}},
	}, ""); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/channel-routes", nil, "")
	var routeState struct {
		Destinations []domain.ChannelDestination `json:"destinations"`
		Route        *domain.ChannelTaskRoute    `json:"route"`
		Members      []domain.ChannelGroupMember `json:"members"`
	}
	decodeResponse(t, response, &routeState)
	if response.StatusCode != http.StatusOK || len(routeState.Members) != 2 {
		t.Fatalf("synced members status=%d payload=%#v", response.StatusCode, routeState)
	}
	var participant, currentBot domain.ChannelGroupMember
	for _, member := range routeState.Members {
		if member.IsSelf {
			currentBot = member
		} else {
			participant = member
		}
	}
	if participant.DisplayName != "张三" ||
		currentBot.DisplayName != "AHA-WORK" || !currentBot.IsSelf {
		t.Fatalf("channel members=%#v", routeState.Members)
	}
	if err := database.UpdateChannelInstanceHealth(ctx, instance.ID, "ready", "", now.Add(time.Second), map[string]string{
		"runtime_bot_provider_display_name": "飞书应用新名称",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureCurrentChannelBot(ctx, conversation.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/channel-routes", nil, "")
	decodeResponse(t, response, &routeState)
	for _, member := range routeState.Members {
		if member.IsSelf && member.DisplayName != "AHA-WORK" {
			t.Fatalf("runtime health overwrote bot display name: %#v", routeState.Members)
		}
	}
	storedInstance, err := database.ChannelInstance(ctx, instance.ID)
	if err != nil ||
		storedInstance.Config["runtime_bot_display_name_override"] != "AHA-WORK" ||
		storedInstance.Config["runtime_bot_provider_display_name"] != "飞书应用新名称" {
		t.Fatalf("bot display name config=%#v err=%v", storedInstance.Config, err)
	}
	bot, err := database.UpsertChannelParticipant(ctx, domain.ChannelIdentityLink{
		ID: "channel-route-bot", InstanceID: instance.ID, ExternalUserID: "provider-bot-ci",
		Role: "participant", DisplayName: "CI 机器人", Status: "active", LinkedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertChannelConversationMember(ctx, conversation.ID, bot.ID, true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/channel-routes", nil, "")
	decodeResponse(t, response, &listed)
	if response.StatusCode != http.StatusOK || listed.Route == nil {
		t.Fatalf("bound route list status=%d payload=%#v", response.StatusCode, listed)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+task.ID+"/channel-contacts", map[string]any{
		"contacts": []map[string]any{
			{"identity_link_id": currentBot.IdentityLinkID, "collaboration_role": "当前机器人"},
		},
	}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("current bot was selectable status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+task.ID+"/channel-contacts", map[string]any{
		"contacts": []map[string]any{
			{"identity_link_id": participant.IdentityLinkID, "collaboration_role": "APP"},
			{"identity_link_id": bot.ID, "collaboration_role": "自动化"},
		},
	}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update task contacts status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	var ownerContacts struct {
		Contacts []domain.ChannelContact `json:"contacts"`
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/channel-routes", nil, "")
	decodeResponse(t, response, &ownerContacts)
	if response.StatusCode != http.StatusOK || len(ownerContacts.Contacts) != 2 ||
		(ownerContacts.Contacts[0].DisplayName != "CI 机器人" && ownerContacts.Contacts[1].DisplayName != "CI 机器人") {
		t.Fatalf("owner contacts status=%d payload=%#v", response.StatusCode, ownerContacts)
	}
	if err := database.UpsertChannelConversationMember(ctx, conversation.ID, bot.ID, false, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+task.ID+"/channel-contacts", map[string]any{
		"contacts": []map[string]any{
			{"identity_link_id": bot.ID, "collaboration_role": "自动化"},
			{"identity_link_id": "channel-route-unknown", "collaboration_role": "无效"},
		},
	}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid contact save status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/channel-routes", nil, "")
	var failedSaveState struct {
		Contacts []domain.ChannelContact     `json:"contacts"`
		Members  []domain.ChannelGroupMember `json:"members"`
	}
	decodeResponse(t, response, &failedSaveState)
	if response.StatusCode != http.StatusOK || len(failedSaveState.Contacts) != 2 {
		t.Fatalf("failed save changed contacts status=%d payload=%#v", response.StatusCode, failedSaveState)
	}
	persistedBot := false
	for _, member := range failedSaveState.Members {
		if member.IdentityLinkID == bot.ID {
			persistedBot = member.IsBot
		}
	}
	if !persistedBot {
		t.Fatalf("observed bot was downgraded after failed save payload=%#v", failedSaveState)
	}

	turn, err := appService.SubmitMessage(ctx, task.ID, "继续联调并在阻塞时联系对方")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("agent outreach turn did not start")
	}
	token, err := capabilities.Issue(task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/capabilities", http.MethodGet, token, nil)
	var capabilityPayload map[string]any
	decodeResponse(t, response, &capabilityPayload)
	if response.StatusCode != http.StatusOK || capabilityPayload["capabilities"].(map[string]any)["channel_outreach"] != true {
		t.Fatalf("channel outreach capability status=%d payload=%#v", response.StatusCode, capabilityPayload)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/channel/contacts", http.MethodGet, token, nil)
	rawContacts := readBody(t, response)
	if response.StatusCode != http.StatusOK || strings.Contains(rawContacts, "provider-user-zhang") {
		t.Fatalf("agent contacts status=%d leaked=%q", response.StatusCode, rawContacts)
	}
	var agentContacts struct {
		Contacts []domain.ChannelContact `json:"contacts"`
	}
	if err := json.Unmarshal([]byte(rawContacts), &agentContacts); err != nil || len(agentContacts.Contacts) != 2 {
		t.Fatalf("agent contacts=%#v err=%v", agentContacts, err)
	}
	foreignHostTask, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: hostProject.ID, WorkspaceID: hostWorkspace.ID, Title: "飞书群聊 · 其他群聊", Request: "host",
		Isolation: "inplace", Backend: model.Backend, ModelSource: model.Source, ModelID: model.ID, WireModel: model.WireModel,
		Filesystem: "read-only", Approval: "never", CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "enabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	foreignConversation := domain.ChannelConversation{
		ID: "channel-route-foreign-conversation", InstanceID: instance.ID, EndpointID: endpoint.ID,
		ScopeKeyVersion: 2, ScopeKey: "foreign-scope", ExternalChatID: "provider-foreign-chat",
		DisplayName: "其他群聊", HostTaskID: foreignHostTask.ID, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelConversation(ctx, foreignConversation, domain.ChannelSession{
		ID: "channel-route-foreign-session", ConversationID: foreignConversation.ID,
		Generation: 1, Mode: "group_qa", Status: "active", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	foreignParticipant, err := database.UpsertChannelParticipant(ctx, domain.ChannelIdentityLink{
		ID: "channel-route-foreign-participant", InstanceID: instance.ID, ExternalUserID: "provider-user-foreign",
		Role: "participant", DisplayName: "其他群成员", Status: "active", LinkedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertChannelConversationMember(ctx, foreignConversation.ID, foreignParticipant.ID, false, now); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := database.EnqueueTaskChannelOutreach(
		ctx, task.ID, turn.ID, "cross-conversation-contact", "blocker",
		"不应发送", []string{foreignParticipant.ID}, nil, now,
	); err == nil {
		t.Fatal("outreach accepted a participant from another conversation")
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/channel/messages", http.MethodPost, token, map[string]any{
		"request_id": "missing-purpose", "message": "不应发送",
		"mention_identity_link_ids": []string{participant.IdentityLinkID},
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("outreach without blocker purpose status=%d", response.StatusCode)
	}
	response.Body.Close()
	attachment, err := database.CreateAttachment(ctx, task.ID, "interop.png", "image/png", []byte("\x89PNG\r\n\x1a\ninterop"), now)
	if err != nil {
		t.Fatal(err)
	}
	outreach := map[string]any{
		"request_id":                "api-contract-blocker-1",
		"purpose":                   "blocker",
		"message":                   "接口字段与约定不一致，请确认最终契约。",
		"mention_identity_link_ids": []string{participant.IdentityLinkID, bot.ID},
		"attachment_ids":            []string{attachment.ID},
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/channel/messages", http.MethodPost, token, outreach)
	var sent struct {
		Delivery domain.ChannelDelivery `json:"delivery"`
	}
	decodeResponse(t, response, &sent)
	if response.StatusCode != http.StatusCreated || sent.Delivery.ID == "" {
		t.Fatalf("outreach status=%d payload=%#v", response.StatusCode, sent)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/channel/messages", http.MethodPost, token, outreach)
	var retried struct {
		Delivery domain.ChannelDelivery `json:"delivery"`
	}
	decodeResponse(t, response, &retried)
	if response.StatusCode != http.StatusCreated || retried.Delivery.ID != sent.Delivery.ID {
		t.Fatalf("outreach retry status=%d first=%s retry=%#v", response.StatusCode, sent.Delivery.ID, retried)
	}
	page, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	outreachCards := []domain.ConversationItem{}
	for _, item := range page.Items {
		if item.Kind == "agent_channel_outreach" {
			outreachCards = append(outreachCards, item)
		}
	}
	if len(outreachCards) != 1 {
		t.Fatalf("outreach cards=%#v", outreachCards)
	}
	card := outreachCards[0]
	recipients, _ := card.Payload["recipients"].([]any)
	channelInfo, _ := card.Payload["channel"].(map[string]any)
	if card.RouteKind != "channel_outreach" || card.StreamAgentID != "main" ||
		card.Payload["purpose"] != "blocker" || card.Payload["message"] != outreach["message"] ||
		card.Payload["delivery_id"] != sent.Delivery.ID || len(recipients) != 2 ||
		channelInfo["display_name"] != conversation.DisplayName {
		t.Fatalf("outreach card=%#v", card)
	}
	boundAttachment, err := database.Attachment(ctx, task.ID, attachment.ID)
	if err != nil || boundAttachment.MessageID != card.ID {
		t.Fatalf("outreach attachment=%#v err=%v", boundAttachment, err)
	}
	target, err := database.ChannelDeliveryTarget(ctx, sent.Delivery.ConversationID, sent.Delivery.SourceEventSequence)
	if err != nil || target["mention_as_post"] != "" ||
		!strings.Contains(target["mention_user_ids"], "provider-user-zhang") ||
		!strings.Contains(target["mention_user_ids"], "provider-bot-ci") || target["reply_message_id"] != "" {
		t.Fatalf("outreach target=%#v err=%v", target, err)
	}
	deliveries, err := database.ChannelDeliveries(ctx, instance.ID, 100)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("outreach deliveries=%#v err=%v", deliveries, err)
	}
	rawImages, _ := json.Marshal(deliveries[0].SemanticPayload["image_attachments"])
	var bundledImages []map[string]any
	if json.Unmarshal(rawImages, &bundledImages) != nil || len(bundledImages) != 1 || bundledImages[0]["attachment_id"] != attachment.ID {
		t.Fatalf("outreach bundled images=%#v delivery=%#v", bundledImages, deliveries[0])
	}
	deliveryClaims := channel.RuntimeClaims{
		InstanceID: instance.ID,
		Scopes: map[string]bool{
			"channel.delivery.claim": true,
			"channel.delivery.ack":   true,
			"channel.media.read":     true,
		},
	}
	claimed, err := channelService.ClaimDeliveries(ctx, deliveryClaims, 10)
	if err != nil || len(claimed) != 1 || claimed[0].ID != sent.Delivery.ID {
		t.Fatalf("outreach claim=%#v err=%v", claimed, err)
	}
	leased := claimed[0]
	imageItem, imageContent, err := channelService.DeliveryAttachment(ctx, deliveryClaims, leased.ID, leased.LeaseID, attachment.ID)
	if err != nil || imageItem.ID != attachment.ID || len(imageContent) == 0 {
		t.Fatalf("bundled image read item=%#v bytes=%d err=%v", imageItem, len(imageContent), err)
	}
	if _, _, err := channelService.DeliveryAttachment(ctx, deliveryClaims, leased.ID, leased.LeaseID, "unknown-image"); err == nil {
		t.Fatal("outreach delivery exposed an unbound attachment")
	}
	if err := channelService.RecordMediaUpload(ctx, deliveryClaims, leased.ID, leased.LeaseID, attachment.ID, "image", "provider-image-key"); err != nil {
		t.Fatal(err)
	}
	if err := channelService.NackDelivery(ctx, deliveryClaims, leased.ID, leased.LeaseID, "transport_failed", "confirmed_failure", 0, false); err != nil {
		t.Fatal(err)
	}
	claimed, err = channelService.ClaimDeliveries(ctx, deliveryClaims, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("outreach retry=%#v err=%v", claimed, err)
	}
	imageKeys, _ := claimed[0].SemanticPayload["provider_image_keys"].(map[string]any)
	if imageKeys[attachment.ID] != "provider-image-key" {
		t.Fatalf("outreach retry=%#v keys=%#v err=%v", claimed, imageKeys, err)
	}
	if err := channelService.AckDelivery(ctx, deliveryClaims, claimed[0].ID, claimed[0].LeaseID, "provider-post-message", "provider-request"); err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/channel/messages", http.MethodPost, token, map[string]any{
		"request_id": "unknown-contact", "message": "不应发送",
		"mention_identity_link_ids": []string{"channel-route-unknown"},
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown contact status=%d", response.StatusCode)
	}
	response.Body.Close()

	refreshCommand, err := channelService.RefreshOwnerTaskChannelMembers(ctx, owner.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	commands, err = channelService.ClaimCommands(ctx, runtimeClaims, 5)
	if err != nil || len(commands) != 1 || commands[0].ID != refreshCommand.ID {
		t.Fatalf("refresh member command=%#v err=%v", commands, err)
	}
	if err := channelService.CompleteCommand(ctx, runtimeClaims, commands[0].ID, commands[0].LeaseID, true, map[string]any{
		"members": []any{},
	}, ""); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/channel-routes", nil, "")
	var refreshed struct {
		Contacts []domain.ChannelContact     `json:"contacts"`
		Members  []domain.ChannelGroupMember `json:"members"`
	}
	decodeResponse(t, response, &refreshed)
	if response.StatusCode != http.StatusOK || len(refreshed.Members) != 2 ||
		len(refreshed.Contacts) != 1 {
		t.Fatalf("departed member remained selectable status=%d payload=%#v", response.StatusCode, refreshed)
	}
	for _, member := range refreshed.Members {
		if !member.IsBot || (member.IsSelf == (member.DisplayName == "CI 机器人")) {
			t.Fatalf("unexpected refreshed member=%#v", member)
		}
	}

	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/tasks/"+task.ID+"/channel-routes/"+bound.Route.ID, nil, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unbind status=%d", response.StatusCode)
	}
	response.Body.Close()
	if _, err := database.ActiveChannelTaskRouteForTask(ctx, task.ID); err == nil {
		t.Fatal("route remained active after API unbind")
	}
	close(executor.release)
}
