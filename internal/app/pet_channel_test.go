package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type stubPetChannelSender struct {
	calls          int
	taskID         string
	turnID         string
	requestID      string
	message        string
	mentions       []string
	enabledCalls   int
	enabled        bool
	revision       int
	lifecycleCalls int
	lifecycle      string
}

func (s *stubPetChannelSender) SendPetTaskChannelMessage(_ context.Context, taskID, turnID, requestID, _ string, message string, mentionIDs, _ []string, _ time.Time) (domain.ChannelDelivery, error) {
	s.calls++
	s.taskID, s.turnID, s.requestID, s.message, s.mentions = taskID, turnID, requestID, message, mentionIDs
	return domain.ChannelDelivery{ID: "delivery-test"}, nil
}

func (s *stubPetChannelSender) SetInstanceEnabled(_ context.Context, _, id string, enabled bool, expectedRevision int) (domain.ChannelInstance, error) {
	s.enabledCalls++
	s.enabled, s.revision = enabled, expectedRevision
	return domain.ChannelInstance{ID: id, Status: "disabled"}, nil
}

func (s *stubPetChannelSender) ResetBinding(_ context.Context, _, id string, expectedRevision int) (domain.ChannelInstance, error) {
	s.lifecycleCalls++
	s.lifecycle, s.revision = "reset", expectedRevision
	return domain.ChannelInstance{ID: id}, nil
}

func (s *stubPetChannelSender) ArchiveInstance(_ context.Context, _, id string, expectedRevision int) (domain.ChannelInstance, error) {
	s.lifecycleCalls++
	s.lifecycle, s.revision = "archive", expectedRevision
	return domain.ChannelInstance{ID: id, Retired: true}, nil
}

func (s *stubPetChannelSender) PurgeInstance(_ context.Context, _, id string, expectedRevision int) (domain.ChannelInstance, error) {
	s.lifecycleCalls++
	s.lifecycle, s.revision = "purge", expectedRevision
	return domain.ChannelInstance{ID: id}, nil
}

type stubPetSecurityApplier struct {
	calls          int
	validateOrigin bool
	accessScope    string
}

func (s *stubPetSecurityApplier) ApplyPetSecuritySettings(_ context.Context, validateOrigin bool, accessScope string) (domain.SecuritySettings, error) {
	s.calls++
	s.validateOrigin, s.accessScope = validateOrigin, accessScope
	return domain.SecuritySettings{ValidateOrigin: validateOrigin, AccessScope: accessScope}, nil
}

func TestPetChannelSendRequiresApprovalAndExecutes(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
		AllowManageModels: boolPointer(true), AllowManageKnowledge: boolPointer(true),
		AllowManageChannels: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	owner := domain.Owner{ID: "owner-channel", Username: "owner", CreatedAt: now}
	if err := fixture.database.CreateOwner(fixture.ctx, owner); err != nil {
		t.Fatal(err)
	}
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "渠道项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "渠道 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "渠道任务",
		Request: "等待渠道消息", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{
		ID: "feishu-test", ProviderKey: "feishu", DisplayName: "Feishu Test",
		ManifestVersion: 1, PackageVersion: "1.0.0", ProtocolMin: 1, ProtocolMax: 1,
		InstallState: "installed", Enabled: true, Revision: 1,
		DiscoveredAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertChannelPlugin(fixture.ctx, plugin); err != nil {
		t.Fatal(err)
	}
	instance := domain.ChannelInstance{
		ID: "channel-test", PluginID: plugin.ID, OwnerID: owner.ID, RuntimeDeviceID: "device-test",
		Name: "团队渠道", Status: "ready", Revision: 1, HostProjectID: project.ID,
		HostWorkspaceID: workspace.ID, OwnerBound: true, CredentialConfigured: true,
		CreatedAt: now, UpdatedAt: now,
	}
	endpoint := domain.ChannelEndpoint{
		ID: "endpoint-test", InstanceID: instance.ID, Kind: domain.ChannelEndpointGroupDigitalHuman,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.CreateChannelInstance(fixture.ctx, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
		t.Fatal(err)
	}
	conversation := domain.ChannelConversation{
		ID: "conversation-test", InstanceID: instance.ID, EndpointID: endpoint.ID,
		ScopeKeyVersion: 1, ScopeKey: "scope-test", DisplayName: "测试群", Status: "active",
		HostTaskID: task.ID, CreatedAt: now, UpdatedAt: now,
	}
	session := domain.ChannelSession{
		ID: "session-test", ConversationID: conversation.ID, Generation: 1,
		Mode: "task_route", Status: "active", StartedAt: now,
	}
	if err := fixture.database.CreateChannelConversation(fixture.ctx, conversation, session); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.ActivateOwnerChannelTaskRoute(fixture.ctx, instance.ID, conversation.ID, task.ID, now); err != nil {
		t.Fatal(err)
	}

	channels, err := fixture.service.PetListChannels(fixture.ctx)
	if err != nil || len(channels) != 1 || channels[0].ID != instance.ID || channels[0].Status != "ready" {
		t.Fatalf("channels=%#v err=%v", channels, err)
	}
	detail, err := fixture.service.PetChannelDetail(fixture.ctx, instance.ID)
	if err != nil || len(detail.Endpoints) != 1 || detail.Endpoints[0].Kind != domain.ChannelEndpointGroupDigitalHuman {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	destinations, err := fixture.service.PetListChannelDestinations(fixture.ctx)
	if err != nil || len(destinations) != 1 || destinations[0].TargetTaskID != task.ID {
		t.Fatalf("destinations=%#v err=%v", destinations, err)
	}
	contacts, err := fixture.service.PetChannelContacts(fixture.ctx, conversation.ID)
	if err != nil || contacts.Channel.DisplayName != "测试群" {
		t.Fatalf("contacts=%#v err=%v", contacts, err)
	}

	sender := &stubPetChannelSender{}
	fixture.service.SetPetChannelSender(sender)
	if _, err := fixture.service.PetRequestChannelSend(fixture.ctx, PetChannelSendInput{TaskID: task.ID, Message: "缺少原因"}); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("channel send without reason was accepted: %v", err)
	}
	approval, err := fixture.service.PetRequestChannelSend(fixture.ctx, PetChannelSendInput{
		TaskID: task.ID, Message: "这是宠物申请发送的测试消息", Reason: "主人需要这条测试消息",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approval.Kind != "external_channel_send" || approval.Status != petApprovalPending {
		t.Fatalf("approval=%#v", approval)
	}
	if sender.calls != 0 {
		t.Fatal("channel send happened before approval")
	}
	resolved, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != petApprovalApproved || sender.calls != 1 {
		t.Fatalf("resolved=%#v calls=%d", resolved, sender.calls)
	}
	if sender.taskID != task.ID || sender.message != "这是宠物申请发送的测试消息" || sender.requestID == "" {
		t.Fatalf("sender args task=%q message=%q request=%q", sender.taskID, sender.message, sender.requestID)
	}

	enableApproval, err := fixture.service.PetRequestChannelEnabled(fixture.ctx, instance.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, enableApproval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if sender.enabledCalls != 1 || sender.enabled || sender.revision != instance.Revision {
		t.Fatalf("channel enable calls=%d enabled=%v revision=%d", sender.enabledCalls, sender.enabled, sender.revision)
	}

	if _, err := fixture.service.PetRequestChannelLifecycle(fixture.ctx, instance.ID, "reset"); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("channel reset without reason was accepted: %v", err)
	}
	resetApproval, err := fixture.service.PetRequestChannelLifecycle(fixture.ctx, instance.ID, "reset", "重置渠道绑定测试")
	if err != nil || resetApproval.Kind != "channel_reset_binding" {
		t.Fatalf("reset approval=%#v err=%v", resetApproval, err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, resetApproval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if sender.lifecycleCalls != 1 || sender.lifecycle != "reset" {
		t.Fatalf("lifecycle calls=%d action=%q", sender.lifecycleCalls, sender.lifecycle)
	}
	if _, err := fixture.service.PetRequestChannelLifecycle(fixture.ctx, instance.ID, "purge", "清理已归档渠道"); err == nil {
		t.Fatal("purging a non-archived channel was accepted")
	}
}
