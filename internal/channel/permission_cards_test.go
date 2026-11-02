package channel

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestSendTaskPermissionRequestCardCreatesOwnerAction(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
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
	if _, err := database.BindChannelOwnerIdentity(ctx, domain.ChannelIdentityLink{
		ID: "owner-identity", InstanceID: instance.ID, OwnerID: "owner-card",
		ExternalUserID: "owner-open", Role: "owner", Status: "active", LinkedAt: now,
	}); err != nil {
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
	service := New(Config{Store: database, App: app.NewService(database, secretStore, app.StubExecutor{}), Secrets: secretStore, DataDir: t.TempDir()})
	request := domain.TaskPermissionRequest{
		ID: "task_permission_card", TaskID: "task-card", ApplicantName: "张三",
		Action: "push main", Scope: "origin/main", Reason: "联调", Risk: "high",
		Status: "pending", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}
	actionID, err := service.SendTaskPermissionRequestCard(ctx, instance.ID, "conversation-card", request)
	if err != nil {
		t.Fatal(err)
	}
	action, err := database.ChannelPendingAction(ctx, actionID)
	if err != nil || action.Operation != "task_permission" || action.ActorIdentityLinkID != "owner-identity" ||
		action.TargetID != request.ID {
		t.Fatalf("action=%#v err=%v", action, err)
	}
}
