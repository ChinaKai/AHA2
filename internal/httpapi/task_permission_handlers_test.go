package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// A permission approval in AHA Web must keep the applicant's channel route so
// the follow-up Turn can answer the group instead of stopping at AHA Web.
func TestResolveTaskPermissionFromWebKeepsChannelReplyRoute(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	appService := app.NewService(database, nil, app.StubExecutor{})
	server := httptest.NewServer(New(Config{
		Store: database, Auth: auth.NewService(database, "setup-test", time.Hour), App: appService,
	}).Handler())
	defer server.Close()
	client := newCookieClient(t)
	csrf := registerOwner(t, client, server.URL)

	now := time.Now().UTC()
	for _, operation := range []func() error{
		func() error {
			return database.UpsertProvider(ctx, domain.Provider{
				ID: "permission-provider", Name: "Permission", BaseURL: "https://example.test/v1",
				AuthStyle: "bearer", CredentialRef: "provider/permission/credential",
				CredentialConfigured: true, CreatedAt: now, UpdatedAt: now,
			})
		},
		func() error {
			return database.UpsertEnvGroup(ctx, domain.EnvGroup{
				ID: "permission-env", Name: "Permission", ProviderID: "permission-provider",
				Backend: "codex", Revision: 1, Environment: map[string]string{},
				SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
			})
		},
		func() error {
			return database.UpsertModel(ctx, domain.Model{
				ID: "permission-model", DisplayName: "Permission", ProviderID: "permission-provider",
				Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "permission",
				WireAPI: "chat", DefaultEnvGroupID: "permission-env", CreatedAt: now, UpdatedAt: now,
			})
		},
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	project := domain.Project{
		ID: "permission-project", Name: "Permission", ProjectType: "local", CreatedAt: now, UpdatedAt: now,
	}
	workspace := domain.Workspace{
		ID: "permission-workspace", ProjectID: project.ID, Name: "Permission",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	task, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Permission", Request: "request",
		ModelID: "permission-model", Isolation: "inplace", Filesystem: "workspace-write",
		Approval: "never", CollaborationMode: "single", MaxAgents: 1,
		KnowledgePolicy: "inherit", StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appService.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{
		ID: "permission-plugin", ProviderKey: "feishu", DisplayName: "Feishu",
		ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1,
		InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateOwner(ctx, domain.Owner{
		ID: "permission-channel-owner", Username: "permission-channel-owner",
		PasswordHash: "hash", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	instance := domain.ChannelInstance{
		ID: "permission-instance", PluginID: plugin.ID, OwnerID: "permission-channel-owner",
		RuntimeDeviceID: "device", Name: "Permission", Status: "ready", Revision: 1,
		HostProjectID: project.ID, HostWorkspaceID: workspace.ID,
		OwnerBound: true, CredentialConfigured: true, CreatedAt: now, UpdatedAt: now,
	}
	endpoint := domain.ChannelEndpoint{
		ID: "permission-endpoint", InstanceID: instance.ID,
		Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelInstance(ctx, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
		t.Fatal(err)
	}
	conversation := domain.ChannelConversation{
		ID: "permission-conversation", InstanceID: instance.ID, EndpointID: endpoint.ID,
		ScopeKeyVersion: 1, ScopeKey: "scope", DisplayName: "Permission Group",
		HostTaskID: task.ID, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelConversation(ctx, conversation, domain.ChannelSession{
		ID: "permission-session", ConversationID: conversation.ID, Generation: 1,
		Mode: "task_route", Status: "active", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, task.ID, now); err != nil {
		t.Fatal(err)
	}
	receipt, _, err := database.ReceiveChannelInbox(ctx, domain.ChannelInboxReceipt{
		ID: "permission-receipt", InstanceID: instance.ID, ExternalEventID: "permission-event",
		EventType: "message", PayloadDigest: "digest",
		NormalizedPayload: map[string]any{"mentioned_bot": false},
		State:             "processed", ConversationID: conversation.ID, Outcome: "delivered_to_task",
		OccurredAt: now, ReceivedAt: now, ProcessedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := appService.CreateTaskPermissionRequest(ctx, app.TaskPermissionRequestInput{
		TaskID: task.ID, InboundReceiptID: receipt.ID, ChannelInstanceID: instance.ID,
		ChannelConversationID: conversation.ID, ApplicantIdentityLinkID: "permission-member",
		ApplicantName: "张三", Action: "push main", Reason: "联调需要", Risk: "medium",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := requestJSON(t, client, http.MethodPost,
		server.URL+"/api/v1/tasks/"+task.ID+"/permission-requests/"+request.ID+"/resolve",
		map[string]any{"decision": "approve"}, csrf)
	var result map[string]any
	decodeResponse(t, response, &result)
	if response.StatusCode != http.StatusOK || result["continuation_error"] != nil {
		t.Fatalf("resolve status=%d result=%#v", response.StatusCode, result)
	}
	turn, err := database.LatestTurnForAgent(ctx, task.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	channelContext, err := database.ChannelContextForReceipt(ctx, task.ID, turn.ID, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	route, _ := channelContext["route"].(map[string]any)
	permission, _ := channelContext["permission_request"].(map[string]any)
	if route["mode"] != "task_route" ||
		channelContext["endpoint"] != domain.ChannelEndpointGroupDigitalHuman ||
		channelContext["inbound_receipt_id"] != receipt.ID ||
		permission["decision"] != "approved" ||
		permission["applicant_identity_link_id"] != "permission-member" {
		t.Fatalf("web approval continuation channel context=%#v", channelContext)
	}
}
