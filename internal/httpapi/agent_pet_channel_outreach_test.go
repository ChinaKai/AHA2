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

// An ordinary Task posts a blocker message to its group directly through
// POST /api/v1/agent/channel/messages. The pet reaches the same outreach only
// through aha_send_channel_message, which files an owner approval; the pet
// cannot resolve that approval itself, a rejection sends nothing, and only an
// owner-authenticated approval runs the real channel service. No runtime
// claims the queued delivery, so nothing leaves the process.
func TestPetChannelOutreachNeedsOwnerApprovalAndQueuesThroughTheRealChannelService(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	appService := app.NewService(database, nil, &blockingAgentAPIExecutor{started: make(chan app.ExecutionRequest, 4), release: make(chan struct{}), done: make(chan struct{})})
	secrets := &fakeSecretStore{values: map[string]string{}}
	channelService := channel.New(channel.Config{Store: database, App: appService, Secrets: secrets, DataDir: dataDir})
	appService.SetPetChannelSender(channelService)
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
	base := createHardwareAPITask(t, database)
	if base.Status == domain.TaskDraft {
		for _, step := range [][2]domain.TaskStatus{{domain.TaskDraft, domain.TaskPreparing}, {domain.TaskPreparing, domain.TaskActive}} {
			if err := database.UpdateTaskStatus(ctx, base.ID, step[0], step[1], now.Format(time.RFC3339Nano), ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	pet := createPetTaskInStore(t, database, base)
	assistant, err := database.EnsureOwnerAssistant(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	// view, create tasks, create workspaces, message tasks, models, knowledge, channels, system
	if err := database.UpdateOwnerPetPermissions(ctx, assistant.ID, true, false, false, false, false, false, true, false, now); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.RuntimeSnapshot(ctx, base.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}

	plugin := domain.ChannelPlugin{ID: "outreach-plugin", ProviderKey: "feishu", DisplayName: "飞书", ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1, InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	hostProject := domain.Project{ID: "outreach-host-project", Name: "Channel", ProjectType: "channel", DefaultWorkspaceID: "outreach-host-workspace", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	hostWorkspace := domain.Workspace{ID: hostProject.DefaultWorkspaceID, ProjectID: hostProject.ID, Name: "Channel", Locality: "local", Transport: "native", RootPath: dataDir, Health: "ready", AgentAPIMode: "global", AgentAPIStatus: "unknown", CreatedAt: now, UpdatedAt: now}
	instance := domain.ChannelInstance{ID: "outreach-instance", PluginID: plugin.ID, OwnerID: owner.ID, RuntimeDeviceID: "local", Name: "团队飞书", Status: "ready", Revision: 1, HostProjectID: hostProject.ID, HostWorkspaceID: hostWorkspace.ID, Config: map[string]any{"operation_scope_mode": "all", "runtime_bot_open_id": "outreach-bot"}, CreatedAt: now, UpdatedAt: now}
	endpoint := domain.ChannelEndpoint{ID: "outreach-endpoint", InstanceID: instance.ID, Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateChannelInstanceWithHost(ctx, hostProject, hostWorkspace, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
		t.Fatal(err)
	}
	hostTask, err := appService.CreateTask(ctx, app.CreateTaskInput{ProjectID: hostProject.ID, WorkspaceID: hostWorkspace.ID, Title: "飞书群聊 · 外发联调群", Request: "host", Isolation: "inplace", ModelID: snapshot.ModelID, Filesystem: "read-only", Approval: "never", CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "enabled", AllowSystemProject: true})
	if err != nil {
		t.Fatal(err)
	}
	conversation := domain.ChannelConversation{ID: "outreach-conversation", InstanceID: instance.ID, EndpointID: endpoint.ID, ScopeKeyVersion: 2, ScopeKey: "outreach-scope", ExternalChatID: "outreach-provider-chat", DisplayName: "外发联调群", HostTaskID: hostTask.ID, Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateChannelConversation(ctx, conversation, domain.ChannelSession{ID: "outreach-session", ConversationID: conversation.ID, Generation: 1, Mode: "group_qa", Status: "active", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureConversationHostSubscription(ctx, instance.ID, conversation.ID, hostTask.ID, now); err != nil {
		t.Fatal(err)
	}
	// Owner Web: route the group to the ordinary Task and choose its contact.
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+base.ID+"/channel-routes", map[string]any{"conversation_id": conversation.ID}, csrf)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("owner route status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	participant, err := database.UpsertChannelParticipant(ctx, domain.ChannelIdentityLink{ID: "outreach-participant", InstanceID: instance.ID, ExternalUserID: "provider-user-wang", Role: "participant", DisplayName: "王五", Status: "active", LinkedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertChannelConversationMember(ctx, conversation.ID, participant.ID, false, now); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+base.ID+"/channel-contacts", map[string]any{"contacts": []map[string]any{{"identity_link_id": participant.ID, "collaboration_role": "测试"}}}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owner contacts status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	carrying := func(marker string) []domain.ChannelDelivery {
		t.Helper()
		items, err := database.ChannelDeliveries(ctx, instance.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		var found []domain.ChannelDelivery
		for _, item := range items {
			payload, _ := json.Marshal(item.SemanticPayload)
			if strings.Contains(string(payload), marker) {
				found = append(found, item)
			}
		}
		return found
	}

	// The ordinary Task: direct outreach from its own Turn.
	baseTurn := createCoreTurn(t, database, base, "main", "batch-outreach-base")
	baseToken, err := capabilities.Issue(base.ID, "main", baseTurn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/channel/messages", http.MethodPost, baseToken, map[string]any{
		"request_id": "outreach-base-1", "purpose": "blocker", "message": "TASK-OUTREACH", "mention_identity_link_ids": []string{participant.ID},
	})
	if response.StatusCode != http.StatusCreated || len(carrying("TASK-OUTREACH")) != 1 {
		t.Fatalf("ordinary outreach status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	t.Logf("evidence ordinary Task POST /api/v1/agent/channel/messages status=%d deliveries=1", http.StatusCreated)

	// The pet: the same outreach is only an approval request.
	petTurn := createCoreTurn(t, database, pet, "main", "batch-outreach-pet")
	petToken, err := capabilities.Issue(pet.ID, "main", petTurn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	petURL := server.URL + "/api/v1/pet/mcp"
	request := func(marker string) domain.PetApproval {
		t.Helper()
		call := coreMCPCallTool(t, petURL, petToken, "aha_send_channel_message", map[string]any{
			"task_id": base.ID, "message": marker, "reason": "主人要求在群里同步阻塞情况", "mention_identity_link_ids": []string{participant.ID},
		})
		var approval domain.PetApproval
		decodeMCPText(t, call, &approval)
		if approval.Kind != "external_channel_send" || approval.Status != "pending" || len(carrying(marker)) != 0 {
			t.Fatalf("pet send request=%#v deliveries=%d", approval, len(carrying(marker)))
		}
		t.Logf("evidence pet aha_send_channel_message status=%d isError=%v approval=%s kind=%s status=%s deliveries=0", call.Status, call.IsError, approval.ID, approval.Kind, approval.Status)
		return approval
	}
	approval := request("PET-OUTREACH-APPROVED")

	// The pet cannot resolve its own approval: the route needs the owner session.
	response = agentRequest(t, server.URL+"/api/v1/pet/approvals/"+approval.ID+"/resolve", http.MethodPost, petToken, map[string]any{"decision": "approve"})
	selfStatus := response.StatusCode
	response.Body.Close()
	stored, err := database.PetApproval(ctx, approval.ID)
	if selfStatus != http.StatusUnauthorized && selfStatus != http.StatusForbidden || err != nil || stored.Status != "pending" || len(carrying("PET-OUTREACH-APPROVED")) != 0 {
		t.Fatalf("pet self-approval status=%d stored=%#v err=%v", selfStatus, stored, err)
	}
	t.Logf("evidence pet token POST /api/v1/pet/approvals/{id}/resolve status=%d approval still %s, deliveries=0", selfStatus, stored.Status)

	// Owner rejection: nothing is queued.
	rejected := request("PET-OUTREACH-REJECTED")
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/approvals/"+rejected.ID+"/resolve", map[string]any{"decision": "reject", "decision_reason": "不需要外发"}, csrf)
	if response.StatusCode != http.StatusOK || len(carrying("PET-OUTREACH-REJECTED")) != 0 {
		t.Fatalf("owner reject status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	t.Logf("evidence owner reject status=%d deliveries=0", http.StatusOK)

	// Owner approval: the real channel service queues one outreach delivery.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/approvals/"+approval.ID+"/resolve", map[string]any{"decision": "approve"}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owner approve status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	queued := carrying("PET-OUTREACH-APPROVED")
	if len(queued) != 1 || queued[0].ConversationID != conversation.ID || queued[0].ProviderMessageID != "" || queued[0].Attempts != 0 {
		t.Fatalf("approved outreach deliveries=%#v", queued)
	}
	t.Logf("evidence owner approve status=%d delivery=%s state=%s attempts=%d provider_message_id=%q", http.StatusOK, queued[0].ID, queued[0].State, queued[0].Attempts, queued[0].ProviderMessageID)

	// Replaying the decision is refused and queues nothing more.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/approvals/"+approval.ID+"/resolve", map[string]any{"decision": "approve"}, csrf)
	replayStatus := response.StatusCode
	response.Body.Close()
	if replayStatus != http.StatusConflict || len(carrying("PET-OUTREACH-APPROVED")) != 1 {
		t.Fatalf("replayed approval status=%d deliveries=%d", replayStatus, len(carrying("PET-OUTREACH-APPROVED")))
	}
	t.Logf("evidence replayed approve status=%d deliveries still 1", replayStatus)
}
