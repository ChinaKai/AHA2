package channel

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

type permissionCardFixture struct {
	ctx          context.Context
	database     *store.Store
	appService   *app.Service
	service      *Service
	instance     domain.ChannelInstance
	conversation domain.ChannelConversation
	task         domain.Task
	identity     domain.ChannelIdentityLink
	now          time.Time
}

func newPermissionCardFixture(t *testing.T) *permissionCardFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := database.CreateOwner(ctx, domain.Owner{ID: "owner-card", Username: "owner", PasswordHash: "hash", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-card", Name: "Card", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-card", ProjectID: project.ID, Name: "Card", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{
		ID: "feishu", ProviderKey: "feishu", DisplayName: "Feishu", ManifestVersion: 1,
		PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1, ExecutablePath: "test",
		ExecutableSHA256: "test", Manifest: map[string]any{}, InstallState: "installed",
		Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	instance := domain.ChannelInstance{
		ID: "instance-card", PluginID: plugin.ID, OwnerID: "owner-card", Name: "Card",
		Status: "ready", Revision: 1, HostProjectID: project.ID, HostWorkspaceID: workspace.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	endpoint := domain.ChannelEndpoint{
		ID: "endpoint-card", InstanceID: instance.ID, Kind: domain.ChannelEndpointGroupDigitalHuman,
		Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelInstance(ctx, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
		t.Fatal(err)
	}
	identity, err := database.BindChannelOwnerIdentity(ctx, domain.ChannelIdentityLink{
		ID: "owner-identity", InstanceID: instance.ID, OwnerID: "owner-card",
		ExternalUserID: "owner-open", Role: "owner", Status: "active", LinkedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	envGroup := domain.EnvGroup{ID: "env-card", Name: "Card", ProviderID: "provider-card", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, envGroup); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{ID: "model-card", DisplayName: "Stub", ProviderID: "provider-card", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeConfigSnapshot{ID: "runtime-card", WorkspaceID: workspace.ID, Backend: "stub", ModelID: model.ID, WireModel: "stub", EnvGroupID: envGroup.ID, EnvGroupRevision: 1, PermissionsJSON: "{}", CreatedAt: now}
	if err := database.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "task-card", ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Card", OriginalRequest: "request", CurrentGoal: "request", Status: domain.TaskActive, RuntimeConfigSnapshotID: snapshot.ID, CollaborationMode: "single", MaxAgents: 1, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	conversation := domain.ChannelConversation{
		ID: "conversation-card", InstanceID: instance.ID, EndpointID: endpoint.ID,
		ScopeKeyVersion: 2, ScopeKey: "scope-card", ExternalChatID: "chat-card",
		HostTaskID: task.ID, DisplayName: "Group", Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	session := domain.ChannelSession{
		ID: "session-card", ConversationID: conversation.ID, Generation: 1,
		Mode: "task_route", Status: "active", StartedAt: now,
	}
	if err := database.CreateChannelConversation(ctx, conversation, session); err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	return &permissionCardFixture{
		ctx: ctx, database: database, appService: appService,
		service:  New(Config{Store: database, App: appService, Secrets: secretStore, DataDir: t.TempDir()}),
		instance: instance, conversation: conversation, task: task, identity: identity, now: now,
	}
}

func (f *permissionCardFixture) processingReceipt(t *testing.T, id string) domain.ChannelInboxReceipt {
	t.Helper()
	receipt, _, err := f.database.ReceiveChannelInbox(f.ctx, domain.ChannelInboxReceipt{
		ID: id, InstanceID: f.instance.ID, ExternalEventID: "event-" + id,
		EventType: "message", PayloadDigest: "digest-" + id,
		NormalizedPayload: map[string]any{"mentioned_bot": false},
		State:             "processing", LeaseID: "lease-" + id,
		ConversationID: f.conversation.ID, OccurredAt: f.now, ReceivedAt: f.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func (f *permissionCardFixture) resultCount(t *testing.T, text string) int {
	t.Helper()
	page, err := f.database.ConversationPage(f.ctx, f.task.ID, 0, 0, 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range page.Items {
		if item.Kind == "agent_message" && strings.Contains(item.Summary, text) {
			count += 1
		}
	}
	return count
}

func TestSendTaskPermissionRequestCardCreatesOwnerAction(t *testing.T) {
	f := newPermissionCardFixture(t)
	request := domain.TaskPermissionRequest{
		ID: "task_permission_card", TaskID: f.task.ID, ApplicantName: "张三",
		Action: "push main", Scope: "origin/main", Reason: "联调", Risk: "high",
		Status: "pending", CreatedAt: f.now, UpdatedAt: f.now,
	}
	actionID, err := f.service.SendTaskPermissionRequestCard(f.ctx, f.instance.ID, f.conversation.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	action, err := f.database.ChannelPendingAction(f.ctx, actionID)
	if err != nil || action.Operation != "task_permission" || action.ActorIdentityLinkID != "owner-identity" ||
		action.TargetID != request.ID || !action.ExpiresAt.IsZero() {
		t.Fatalf("action=%#v err=%v", action, err)
	}
}

// Channel card decisions write the same result message the Web path writes.
// The fixture Task is terminal so the test can assert the history-only record
// without depending on a running Agent.
func TestTaskPermissionCardDecisionsNotifyTerminalTask(t *testing.T) {
	f := newPermissionCardFixture(t)
	clock := f.now
	f.service.now = func() time.Time { return clock }
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := f.database.UpdateTaskStatus(f.ctx, f.task.ID, domain.TaskActive, domain.TaskCompleted, now, now); err != nil {
		t.Fatal(err)
	}
	for index, item := range []struct {
		decision string
		input    string
		want     string
	}{
		{decision: "approved", input: "confirm", want: "owner 已批准"},
		{decision: "rejected", input: "cancel", want: "owner 已拒绝"},
	} {
		clock = f.now
		expectedResults := index + 1
		request, err := f.appService.CreateTaskPermissionRequest(f.ctx, app.TaskPermissionRequestInput{
			TaskID: f.task.ID, ChannelInstanceID: f.instance.ID, ChannelConversationID: f.conversation.ID,
			ApplicantIdentityLinkID: "member-1", ApplicantName: "张三",
			Action: "push main", Reason: "联调", Risk: "medium",
		})
		if err != nil {
			t.Fatal(err)
		}
		actionID, err := f.service.SendTaskPermissionRequestCard(f.ctx, f.instance.ID, f.conversation.ID, request)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.appService.SetTaskPermissionRequestChannelAction(f.ctx, request.ID, actionID); err != nil {
			t.Fatal(err)
		}
		messageID := "message-" + item.decision
		if err := f.database.BindChannelActionProviderMessage(f.ctx, actionID, f.instance.ID, f.conversation.ID, messageID); err != nil {
			t.Fatal(err)
		}
		// New confirmations in the same group must leave this request active.
		other, err := f.database.CreateChannelPendingAction(f.ctx, domain.ChannelPendingAction{
			ID: "exit-" + item.decision, InstanceID: f.instance.ID, ConversationID: f.conversation.ID,
			ActorIdentityLinkID: f.identity.ID, Operation: "exit", TargetType: "task_route",
			Status: "pending", ExpiresAt: f.now.Add(time.Minute), CreatedAt: f.now, UpdatedAt: f.now,
		})
		if err != nil {
			t.Fatal(err)
		}
		clock = f.now.Add(72 * time.Hour)
		if _, claimed, err := f.database.BeginChannelPendingAction(
			f.ctx, other.ID, f.instance.ID, f.conversation.ID, f.identity.ID, "", f.service.now(),
		); claimed || !errors.Is(err, store.ErrChannelRevision) {
			t.Fatalf("claim expired exit confirmation: claimed = %v, err = %v", claimed, err)
		}
		envelope := domain.ChannelInboundEnvelope{
			ExternalChatID: f.conversation.ExternalChatID, ExternalSenderID: f.identity.ExternalUserID,
			CardAction: map[string]any{"action_id": actionID, "provider_message_id": messageID, "decision": item.input},
		}
		receipt := f.processingReceipt(t, "receipt-"+item.decision)
		if err := f.service.processCardAction(f.ctx, receipt, f.instance, envelope); err != nil {
			t.Fatal(err)
		}
		resolved, err := f.database.TaskPermissionRequest(f.ctx, request.ID)
		if err != nil || resolved.Status != item.decision {
			t.Fatalf("resolved request = %#v, err = %v", resolved, err)
		}
		repeat := f.processingReceipt(t, "repeat-"+item.decision)
		if err := f.service.processCardAction(f.ctx, repeat, f.instance, envelope); err != nil {
			t.Fatal(err)
		}
		finished, err := f.database.ChannelInboxReceipt(f.ctx, f.instance.ID, repeat.ID)
		if err != nil || finished.State != "processed" || finished.Outcome != "duplicate_action" {
			t.Fatalf("%s repeat receipt = %#v, err = %v", item.decision, finished, err)
		}
		if count := f.resultCount(t, "渠道权限申请「push main」"); count != expectedResults {
			t.Fatalf("%s result messages = %d, want %d", item.decision, count, expectedResults)
		}
		if count := f.resultCount(t, item.want); count != 1 {
			t.Fatalf("%s decision messages = %d, want 1", item.decision, count)
		}
	}
}
