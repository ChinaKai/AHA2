package channel

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestHandoffTaskChannelRouteCopiesContactsAndPreservesSingleActiveTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := app.NewService(database, nil, nil)
	service := New(Config{Store: database, App: application, DataDir: dataDir})
	now := time.Now().UTC()
	if err := database.CreateOwner(ctx, domain.Owner{
		ID: "owner-clone", Username: "clone", PasswordHash: "hash", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	project := domain.Project{ID: "clone-channel-project", Name: "Clone", DefaultWorkspaceID: "clone-channel-workspace", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: project.DefaultWorkspaceID, ProjectID: project.ID, Name: "Local", Locality: "local",
		Transport: "native", RootPath: dataDir, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	provider := domain.Provider{ID: "clone-channel-provider", Name: "Clone", CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{
		ID: "clone-channel-env", Name: "Clone", ProviderID: provider.ID, Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "clone-channel-model", DisplayName: "Clone", ProviderID: provider.ID, Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateRuntimeSnapshot(ctx, domain.RuntimeConfigSnapshot{
		ID: "clone-channel-runtime", WorkspaceID: workspace.ID, Backend: model.Backend, ModelID: model.ID,
		WireModel: model.WireModel, EnvGroupID: env.ID, EnvGroupRevision: env.Revision,
		PermissionsJSON: "{}", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	createTask := func(id string) {
		t.Helper()
		if err := database.CreateTask(ctx, domain.Task{
			ID: id, ProjectID: project.ID, WorkspaceID: workspace.ID, Title: id, OriginalRequest: "request",
			CurrentGoal: "request", Status: domain.TaskActive, RuntimeConfigSnapshotID: "clone-channel-runtime",
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	createTask("clone-channel-source")
	createTask("clone-channel-target")

	plugin := domain.ChannelPlugin{
		ID: "clone-channel-plugin", ProviderKey: "feishu", DisplayName: "Feishu", ManifestVersion: 1, PackageVersion: "1",
		ProtocolMin: 1, ProtocolMax: 1, InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	hostProject := domain.Project{ID: "clone-channel-host-project", Name: "Channel", ProjectType: "channel", DefaultWorkspaceID: "clone-channel-host-workspace", CreatedAt: now, UpdatedAt: now}
	hostWorkspace := domain.Workspace{
		ID: hostProject.DefaultWorkspaceID, ProjectID: hostProject.ID, Name: "Channel", Locality: "local",
		Transport: "native", RootPath: dataDir, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	instance := domain.ChannelInstance{
		ID: "clone-channel-instance", PluginID: plugin.ID, OwnerID: "owner-clone", RuntimeDeviceID: "local",
		Name: "Team", Status: "ready", Revision: 1, HostProjectID: hostProject.ID, HostWorkspaceID: hostWorkspace.ID,
		Config: map[string]any{"operation_scope_mode": "all"}, CreatedAt: now, UpdatedAt: now,
	}
	endpoint := domain.ChannelEndpoint{
		ID: "clone-channel-endpoint", InstanceID: instance.ID, Kind: domain.ChannelEndpointGroupDigitalHuman,
		Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelInstanceWithHost(ctx, hostProject, hostWorkspace, instance, []domain.ChannelEndpoint{endpoint}); err != nil {
		t.Fatal(err)
	}
	conversation := domain.ChannelConversation{
		ID: "clone-channel-conversation", InstanceID: instance.ID, EndpointID: endpoint.ID, ScopeKeyVersion: 2,
		ScopeKey: "scope", ExternalChatID: "chat", DisplayName: "Group", HostTaskID: "clone-channel-source",
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateChannelConversation(ctx, conversation, domain.ChannelSession{
		ID: "clone-channel-session", ConversationID: conversation.ID, Generation: 1, Mode: "group_qa", Status: "active", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureConversationHostSubscription(ctx, instance.ID, conversation.ID, conversation.HostTaskID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateOwnerChannelTaskRoute(ctx, instance.ID, conversation.ID, "clone-channel-source", now); err != nil {
		t.Fatal(err)
	}
	participant, err := database.UpsertChannelParticipant(ctx, domain.ChannelIdentityLink{
		ID: "clone-channel-participant", InstanceID: instance.ID, ExternalUserID: "user-1",
		Role: "participant", DisplayName: "Builder", Status: "active", LinkedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertChannelConversationMember(ctx, conversation.ID, participant.ID, false, now); err != nil {
		t.Fatal(err)
	}
	if err := database.ReplaceTaskChannelContacts(ctx, "clone-channel-source", instance.ID, conversation.ID, []store.TaskChannelContactInput{
		{IdentityLinkID: participant.ID, CollaborationRole: "firmware"},
	}, now); err != nil {
		t.Fatal(err)
	}

	route, transferred, err := service.HandoffTaskChannelRoute(ctx, instance.OwnerID, "clone-channel-source", "clone-channel-target")
	if err != nil {
		t.Fatal(err)
	}
	if !transferred || route.TargetTaskID != "clone-channel-target" || route.ConversationID != conversation.ID {
		t.Fatalf("transferred=%v route=%#v", transferred, route)
	}
	if _, err := database.ActiveChannelTaskRouteForTask(ctx, "clone-channel-source"); err == nil {
		t.Fatal("source channel route remained active")
	}
	contacts, err := database.TaskChannelContacts(ctx, "clone-channel-target", instance.ID, conversation.ID, 50)
	if err != nil || len(contacts) != 1 || contacts[0].IdentityLinkID != participant.ID || contacts[0].CollaborationRole != "firmware" {
		t.Fatalf("target contacts=%#v err=%v", contacts, err)
	}
}
