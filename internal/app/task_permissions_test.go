package app

import (
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestTaskPermissionRequestLifecycle(t *testing.T) {
	fixture := newPetTaskFixture(t)
	now := fixture.service.now().UTC()
	project := domain.Project{ID: "project-permission", Name: "Permission", ProjectType: "local", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-permission", ProjectID: project.ID, Name: "Permission",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Permission task", Request: "request",
		ModelID: fixture.model.ID, Isolation: "inplace", Filesystem: "workspace-write",
		Approval: "never", CollaborationMode: "single", MaxAgents: 1,
		KnowledgePolicy: "inherit", StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := fixture.service.CreateTaskPermissionRequest(fixture.ctx, TaskPermissionRequestInput{
		TaskID: task.ID, ChannelInstanceID: "instance-1", ChannelConversationID: "conversation-1",
		ApplicantIdentityLinkID: "member-1", ApplicantName: "张三", ApplicantIsBot: true,
		Action: "push main", Scope: "origin/main", Reason: "联调需要", Risk: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != "pending" || request.ApplicantName != "张三" {
		t.Fatalf("request=%#v", request)
	}
	if _, err := fixture.service.CreateTaskPermissionRequest(fixture.ctx, TaskPermissionRequestInput{
		TaskID: task.ID, Action: "second action", Reason: "should be blocked",
	}); err == nil {
		t.Fatal("second pending permission request was accepted")
	}
	resolved, changed, err := fixture.service.ResolveTaskPermissionRequest(
		fixture.ctx, request.ID, "approve", "owner-1", "Owner", "同意",
	)
	if err != nil || !changed || resolved.Status != "approved" {
		t.Fatalf("resolved=%#v changed=%v err=%v", resolved, changed, err)
	}
	if _, changed, err := fixture.service.ResolveTaskPermissionRequest(
		fixture.ctx, request.ID, "reject", "owner-1", "Owner", "重复",
	); err != nil || changed {
		t.Fatalf("second resolve changed=%v err=%v", changed, err)
	}
}

func TestTaskPermissionApprovalContinuationKeepsChannelRoute(t *testing.T) {
	fixture := newPetTaskFixture(t)
	now := fixture.service.now().UTC()
	project := domain.Project{
		ID: "project-permission-continuation", Name: "Permission continuation",
		ProjectType: "local", CreatedAt: now, UpdatedAt: now,
	}
	workspace := domain.Workspace{
		ID: "workspace-permission-continuation", ProjectID: project.ID, Name: "Permission continuation",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Permission continuation", Request: "request",
		ModelID: fixture.model.ID, Isolation: "inplace", Filesystem: "workspace-write",
		Approval: "never", CollaborationMode: "single", MaxAgents: 1,
		KnowledgePolicy: "inherit", StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{
		ID: "continuation-plugin", ProviderKey: "feishu", DisplayName: "Feishu",
		ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1,
		InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertChannelPlugin(fixture.ctx, plugin); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CreateOwner(fixture.ctx, domain.Owner{
		ID: "continuation-owner", Username: "continuation-owner", PasswordHash: "hash", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	instance := domain.ChannelInstance{
		ID: "continuation-instance", PluginID: plugin.ID, OwnerID: "continuation-owner",
		RuntimeDeviceID: "device", Name: "Continuation", Status: "ready", Revision: 1,
		HostProjectID: project.ID, HostWorkspaceID: workspace.ID,
		OwnerBound: true, CredentialConfigured: true,
		CreatedAt: now, UpdatedAt: now,
	}
	endpoint := domain.ChannelEndpoint{
		ID: "continuation-endpoint", InstanceID: instance.ID,
		Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateChannelInstance(fixture.ctx, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
		t.Fatal(err)
	}
	conversation := domain.ChannelConversation{
		ID: "continuation-conversation", InstanceID: instance.ID, EndpointID: endpoint.ID,
		ScopeKeyVersion: 1, ScopeKey: "scope", DisplayName: "Continuation Group",
		HostTaskID: task.ID, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateChannelConversation(fixture.ctx, conversation, domain.ChannelSession{
		ID: "continuation-session", ConversationID: conversation.ID, Generation: 1,
		Mode: "task_route", Status: "active", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.ActivateOwnerChannelTaskRoute(
		fixture.ctx, instance.ID, conversation.ID, task.ID, now,
	); err != nil {
		t.Fatal(err)
	}
	receipt, _, err := fixture.database.ReceiveChannelInbox(fixture.ctx, domain.ChannelInboxReceipt{
		ID: "continuation-receipt", InstanceID: instance.ID, ExternalEventID: "continuation-event",
		EventType: "message", PayloadDigest: "digest", NormalizedPayload: map[string]any{"mentioned_bot": false},
		State: "processed", ConversationID: conversation.ID, Outcome: "delivered_to_task",
		OccurredAt: now, ReceivedAt: now, ProcessedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := fixture.service.CreateTaskPermissionRequest(fixture.ctx, TaskPermissionRequestInput{
		TaskID: task.ID, InboundReceiptID: receipt.ID, ChannelInstanceID: instance.ID,
		ChannelConversationID: conversation.ID, ApplicantIdentityLinkID: "continuation-member",
		ApplicantName: "张三", Action: "push main", Reason: "联调需要", Risk: "medium",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, changed, err := fixture.service.ResolveTaskPermissionRequest(
		fixture.ctx, request.ID, "approve", "owner-1", "Owner", "同意",
	)
	if err != nil || !changed {
		t.Fatalf("resolved=%#v changed=%v err=%v", resolved, changed, err)
	}
	turn, err := fixture.service.SubmitTaskPermissionApprovalContinuation(
		fixture.ctx, resolved, "Owner 已批准权限申请，请继续执行。",
	)
	if err != nil {
		t.Fatal(err)
	}
	channelContext, err := fixture.database.ChannelContextForReceipt(fixture.ctx, task.ID, turn.ID, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	route, _ := channelContext["route"].(map[string]any)
	permission, _ := channelContext["permission_request"].(map[string]any)
	if route["mode"] != "task_route" || channelContext["endpoint"] != domain.ChannelEndpointGroupDigitalHuman ||
		channelContext["inbound_receipt_id"] != receipt.ID ||
		permission["decision"] != "approved" ||
		permission["applicant_identity_link_id"] != "continuation-member" ||
		permission["applicant_receipt_id"] != receipt.ID {
		t.Fatalf("continuation channel context=%#v", channelContext)
	}
	if conversationStatus, err := fixture.database.ChannelInboxReceipt(
		fixture.ctx, instance.ID, receipt.ID,
	); err != nil || conversationStatus.State != "processed" {
		t.Fatalf("continuation changed receipt state=%#v err=%v", conversationStatus, err)
	}
}
