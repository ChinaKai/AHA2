package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

type memorySecrets struct{ values map[string]string }

func (m *memorySecrets) Get(key string) (string, bool) { value, ok := m.values[key]; return value, ok }
func (m *memorySecrets) PutMany(values map[string]string) error {
	if m.values == nil {
		m.values = map[string]string{}
	}
	for key, value := range values {
		m.values[key] = value
	}
	return nil
}
func (m *memorySecrets) DeleteMany(keys []string) error {
	for _, key := range keys {
		delete(m.values, key)
	}
	return nil
}

func TestDiscoverAndCreateInstanceIsOptionalAndIdempotent(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	owner := domain.Owner{ID: "owner-channel", Username: "owner", PasswordHash: "hash", CreatedAt: now}
	if err := database.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	pluginDir := filepath.Join(dataDir, "plugins", "channels", "feishu")
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(pluginDir, "feishu-plugin.exe")
	content := []byte("test plugin")
	if err := os.WriteFile(executable, content, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	manifest := fmt.Sprintf(`{"manifest_version":1,"plugin_id":"feishu","provider_key":"feishu","display_name":"飞书","package_version":"1.0.0","protocol_versions":["channel-runtime/v1"],"endpoints":["assistant_dm","group_digital_human"],"executable":"feishu-plugin.exe","sha256":"%s","requested_capabilities":[]}`, hex.EncodeToString(digest[:]))
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := &memorySecrets{}
	service := New(Config{Store: database, Secrets: secrets, DataDir: dataDir, RuntimeDevice: "device-local"})
	if err := service.RefreshPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	providers, err := service.Providers(ctx)
	if err != nil || len(providers) != 1 || !providers[0].Available {
		t.Fatalf("providers=%#v err=%v", providers, err)
	}
	first, err := service.CreateInstance(ctx, owner.ID, "feishu", "团队飞书", "request-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateInstance(ctx, owner.ID, "feishu", "团队飞书", "request-1")
	if err != nil || second.ID != first.ID {
		t.Fatalf("idempotent result=%#v err=%v", second, err)
	}
	if _, err := service.CreateInstance(ctx, owner.ID, "feishu", "另一个名字", "request-1"); err == nil {
		t.Fatal("reused idempotency key accepted different payload")
	}
	item, endpoints, err := service.Instance(ctx, owner.ID, first.ID)
	if err != nil || item.RuntimeDeviceID != "device-local" || len(endpoints) != 2 {
		t.Fatalf("instance=%#v endpoints=%#v err=%v", item, endpoints, err)
	}
	project, err := database.Project(ctx, item.HostProjectID)
	if err != nil || project.ProjectType != "channel" {
		t.Fatalf("project=%#v err=%v", project, err)
	}
	if !database.IsManagedChannelProject(ctx, item.HostProjectID) || !database.IsManagedChannelWorkspace(ctx, item.HostWorkspaceID) {
		t.Fatal("host resources are not protected")
	}
	objects, err := syncer.ExportBusinessObjectsForDevice(ctx, database, "device-local")
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		if object.ID == item.HostProjectID || strings.Contains(object.ID, item.HostWorkspaceID) {
			t.Fatalf("channel host leaked into Profile Sync: %#v", object)
		}
	}
	policies, err := database.ChannelKnowledgePolicies(ctx, item.ID)
	if err != nil || len(policies) != 2 {
		t.Fatalf("policies=%#v err=%v", policies, err)
	}
	raw, capability, err := service.IssueCapability(ctx, item.ID, []string{"channel.command.claim", "channel.command.progress", "channel.command.complete"}, time.Hour)
	if err != nil || raw == "" || capability.TokenHash == raw {
		t.Fatalf("capability=%#v raw=%t err=%v", capability, raw != "", err)
	}
	claims, err := service.AuthenticateCapability(ctx, raw, "channel.command.claim")
	if err != nil || claims.InstanceID != item.ID {
		t.Fatalf("claims=%#v err=%v", claims, err)
	}
	if _, err := service.AuthenticateCapability(ctx, raw, "channel.inbound.write"); err == nil {
		t.Fatal("capability scope escalation was accepted")
	}
	command, err := service.EnqueueCommand(ctx, item.ID, "verify_installation", "command-1", map[string]any{"check": "connection"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.ClaimCommands(ctx, claims, 10)
	if err != nil || len(claimed) != 1 || claimed[0].ID != command.ID || claimed[0].LeaseID == "" {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err := service.CommandProgress(ctx, claims, command.ID, claimed[0].LeaseID, map[string]any{"app_secret": "must-not-pass"}); err == nil {
		t.Fatal("sensitive command progress was accepted")
	}
	if err := service.CommandProgress(ctx, claims, command.ID, claimed[0].LeaseID, map[string]any{"phase": "connected"}); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteCommand(ctx, claims, command.ID, claimed[0].LeaseID, true, map[string]any{"status": "ok"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeCapabilities(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateCapability(ctx, raw, "channel.command.claim"); err == nil {
		t.Fatal("revoked capability remained valid")
	}
	registerRaw, _, err := service.IssueCapability(ctx, item.ID, onboardingScopes(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	registerClaims, err := service.AuthenticateCapability(ctx, registerRaw, "channel.command.claim")
	if err != nil {
		t.Fatal(err)
	}
	registerCommand, err := service.EnqueueCommand(ctx, item.ID, "register_app", "register-app-test", map[string]any{"onboarding_id": "onboarding-test"})
	if err != nil {
		t.Fatal(err)
	}
	registerClaim, err := service.ClaimCommands(ctx, registerClaims, 1)
	if err != nil || len(registerClaim) != 1 {
		t.Fatalf("claim=%#v err=%v", registerClaim, err)
	}
	onboarding := domain.ChannelOnboardingSession{ID: "onboarding-test", InstanceID: item.ID, OwnerSessionID: "", Mode: "register_app", RegistrationCommandID: registerCommand.ID, Status: "pending", Step: "starting", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
	if err := database.CreateChannelOnboarding(ctx, onboarding); err != nil {
		t.Fatal(err)
	}
	service.handleVerificationURL(ctx, secretIPCMessage{Schema: "channel-secret/v1", Type: "verification_url", InstanceID: item.ID, OnboardingID: onboarding.ID, CommandID: registerCommand.ID, LeaseID: registerClaim[0].LeaseID, URL: "https://accounts.feishu.cn/example", ExpireIn: 600})
	visible, err := service.OwnerOnboarding(ctx, owner.ID, "", onboarding.ID)
	if err != nil || visible.Status != "qr_ready" || visible.VerificationURL == "" {
		t.Fatalf("onboarding=%#v err=%v", visible, err)
	}
	if err := service.completeRegistrationFromIPC(ctx, secretIPCMessage{Schema: "channel-secret/v1", Type: "registration_result", InstanceID: item.ID, OnboardingID: onboarding.ID, CommandID: registerCommand.ID, LeaseID: registerClaim[0].LeaseID, AppID: "cli_test", AppSecret: "top-secret-value", ScannerOpenID: "scanner-owner", TenantBrand: "feishu"}); err != nil {
		t.Fatal(err)
	}
	registered, _, err := service.Instance(ctx, owner.ID, item.ID)
	if err != nil || !registered.CredentialConfigured || registered.AppID != "cli_test" {
		t.Fatalf("registered=%#v err=%v", registered, err)
	}
	encoded, _ := json.Marshal(registered)
	if strings.Contains(string(encoded), "top-secret-value") || strings.Contains(string(encoded), "credential_ref") {
		t.Fatalf("secret leaked: %s", encoded)
	}
	if _, err := database.BindChannelOwnerIdentity(ctx, domain.ChannelIdentityLink{ID: "other-owner", InstanceID: item.ID, OwnerID: owner.ID, ExternalUserID: "different-scanner", Role: "owner", Status: "active", LinkedAt: now}); err == nil {
		t.Fatal("second active channel owner was accepted")
	}
	reauthorization := domain.ChannelOnboardingSession{
		ID: "onboarding-reauthorize", InstanceID: item.ID, OwnerSessionID: "", Mode: "existing_app",
		RegistrationCommandID: "register-command-reauthorize", Status: "pending", Step: "starting_registration",
		ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute),
	}
	if err := database.CreateChannelOnboarding(ctx, reauthorization); err != nil {
		t.Fatal(err)
	}
	registered.Status = "onboarding"
	required, err := service.registrationProcessRequired(ctx, registered)
	if err != nil || !required {
		t.Fatalf("existing-owner reauthorization must use registration process: required=%v err=%v", required, err)
	}
}

func TestMissingPluginDoesNotPreventEmptyProviderList(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := New(Config{Store: database, DataDir: dataDir})
	providers, err := service.Providers(ctx)
	if err != nil || len(providers) != 0 {
		t.Fatalf("providers=%#v err=%v", providers, err)
	}
}

func TestChannelRuntimeFallsBackToReadyOfficialCodexAccount(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	account := domain.CodexAccount{
		ID: "account-ready", Label: "Ready", Status: "ready", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-channel", DisplayName: "Channel"}},
		CreatedAt:       now, UpdatedAt: now, LastUsedAt: now,
	}
	if err := database.UpsertCodexAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model-official-channel", DisplayName: "Channel", ProviderID: domain.OfficialCodexProviderID,
		Source: domain.ModelSourceOfficial, Backend: "codex", WireModel: "gpt-channel", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	service := New(Config{Store: database, DataDir: t.TempDir()})
	choice, err := service.resolveChannelRuntime(ctx, domain.ChannelInstance{Config: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Model.ID != model.ID || choice.CodexAccountID != account.ID || choice.WireModel != model.WireModel {
		t.Fatalf("unexpected runtime choice: %#v", choice)
	}
}

func TestManifestRejectsExecutableTraversal(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{ManifestVersion: 1, PluginID: "bad", ProviderKey: "bad", DisplayName: "bad", PackageVersion: "1", ProtocolVersions: []string{"channel-runtime/v1"}, Endpoints: []string{domain.ChannelEndpointAssistantDM, domain.ChannelEndpointGroupDigitalHuman}, Executable: "../escape.exe", SHA256: "00"}
	item := validateManifest(manifest, filepath.Join(root, "plugin.json"), []byte(`{}`), time.Now().UTC())
	if item.InstallState != "invalid" || item.LastError == "" {
		t.Fatalf("item=%#v", item)
	}
}

func TestInboundOwnerAndGroupScopesAreServerEnforcedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	owner := domain.Owner{ID: "owner-inbound", Username: "owner", PasswordHash: "hash", CreatedAt: now}
	if err := database.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	provider := domain.Provider{ID: "provider-stub", Name: "Stub", AuthStyle: "none", CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{ID: "env-stub", Name: "Stub", ProviderID: provider.ID, Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{ID: "model-stub", DisplayName: "Stub", ProviderID: provider.ID, Source: "provider", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{ID: "feishu", ProviderKey: "feishu", DisplayName: "飞书", ManifestVersion: 1, PackageVersion: "1", ProtocolMin: 1, ProtocolMax: 1, ExecutablePath: "test", ExecutableSHA256: "test", Manifest: map[string]any{}, InstallState: "installed", Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	service := New(Config{Store: database, App: appService, Secrets: secretStore, DataDir: dataDir, RuntimeDevice: "device-local"})
	instance, err := service.CreateInstance(ctx, owner.ID, plugin.ID, "Inbox", "create-inbox")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.BindChannelOwnerIdentity(ctx, domain.ChannelIdentityLink{ID: "channel-owner", InstanceID: instance.ID, OwnerID: owner.ID, ExternalUserID: "owner-open", Role: "owner", Status: "active", LinkedAt: now}); err != nil {
		t.Fatal(err)
	}
	instance.Status, instance.UpdatedAt = "ready", now
	instance, err = database.UpdateChannelInstance(ctx, instance, instance.Revision)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := service.IssueCapability(ctx, instance.ID, []string{"channel.inbound.write"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := service.AuthenticateCapability(ctx, raw, "channel.inbound.write")
	if err != nil {
		t.Fatal(err)
	}
	dm := domain.ChannelInboundEnvelope{SchemaVersion: 1, RequestID: "req-1", InstanceID: instance.ID, ExternalEventID: "event-dm", EventType: "message", OccurredAt: now, ChatType: "p2p", ExternalChatID: "chat-owner", ExternalSenderID: "owner-open", ExternalMessageID: "message-dm", Content: "hello"}
	receipt, duplicate, err := service.ReceiveInbound(ctx, claims, dm)
	if err != nil || duplicate {
		t.Fatalf("receipt=%#v duplicate=%v err=%v", receipt, duplicate, err)
	}
	if _, duplicate, err = service.ReceiveInbound(ctx, claims, dm); err != nil || !duplicate {
		t.Fatalf("duplicate=%v err=%v", duplicate, err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	tasks, err := database.ListTasks(ctx, instance.HostProjectID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%#v err=%v", tasks, err)
	}
	if tasks[0].Title != "飞书私聊 · owner" {
		t.Fatalf("private channel task title=%q", tasks[0].Title)
	}
	endpoint, err := database.ChannelEndpoint(ctx, instance.ID, domain.ChannelEndpointAssistantDM)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := service.scopeKey(instance.ID, endpoint.Kind, "channel-owner", "chat-owner", "owner-open")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := database.ChannelConversationByScope(ctx, endpoint.ID, 1, scope)
	if err != nil {
		t.Fatal(err)
	}
	menu := domain.ChannelInboundEnvelope{
		SchemaVersion: 1, RequestID: "menu-request", InstanceID: instance.ID, ExternalEventID: "menu-event",
		EventType: "menu_action", OccurredAt: time.Now().UTC(), ChatType: "p2p", ExternalSenderID: "owner-open",
		SenderDisplayName: "Owner", MenuAction: map[string]any{"key": "aha.project.query"},
	}
	if _, duplicate, err := service.ReceiveInbound(ctx, claims, menu); err != nil || duplicate {
		t.Fatalf("menu receive duplicate=%v err=%v", duplicate, err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	storedMenu, duplicate, err := service.ReceiveInbound(ctx, claims, menu)
	if err != nil || !duplicate || storedMenu.State != "processed" || storedMenu.Outcome != "delivered_to_task" {
		t.Fatalf("menu receipt=%#v duplicate=%v err=%v", storedMenu, duplicate, err)
	}
	regularProject := domain.Project{ID: "regular-project", Name: "Regular", ProjectType: "folder", DefaultWorkspaceID: "regular-workspace", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, regularProject); err != nil {
		t.Fatal(err)
	}
	regularWorkspace := domain.Workspace{ID: "regular-workspace", ProjectID: regularProject.ID, Name: "Regular", Locality: "local", Transport: "native", RootPath: dataDir, Health: "ready", CreatedAt: now, UpdatedAt: now, AgentAPIMode: "global", AgentAPIStatus: "unknown"}
	if err := database.CreateWorkspace(ctx, regularWorkspace); err != nil {
		t.Fatal(err)
	}
	target, err := appService.CreateTask(ctx, app.CreateTaskInput{ProjectID: regularProject.ID, WorkspaceID: regularWorkspace.ID, Title: "Target", Request: "Target", Isolation: "inplace", Backend: model.Backend, ModelSource: model.Source, ModelID: model.ID, WireModel: model.WireModel, Filesystem: "workspace-write", Approval: "never", CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "inherit"})
	if err != nil {
		t.Fatal(err)
	}
	beforeUnrouted, err := database.ChannelDeliveries(ctx, instance.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AppendChannelSourceAndProject(ctx, domain.ChannelSourceEvent{
		ID: "source-unrouted", SourceKey: "source-unrouted", TaskID: target.ID, EventClass: "message", EventType: "agent_reply",
		SemanticPayload: map[string]any{"text": "must stay isolated"}, OccurredAt: time.Now().UTC(),
	}, ""); err != nil {
		t.Fatal(err)
	}
	afterUnrouted, err := database.ChannelDeliveries(ctx, instance.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterUnrouted) != len(beforeUnrouted) {
		t.Fatalf("unrouted task leaked through owner-global subscription: before=%d after=%d", len(beforeUnrouted), len(afterUnrouted))
	}
	if err := database.AppendChannelSourceAndProject(ctx, domain.ChannelSourceEvent{
		ID: "source-unrouted-status", SourceKey: "source-unrouted-status", TaskID: target.ID, EventClass: "status", EventType: "waiting_user",
		SemanticPayload: map[string]any{"status": "waiting_user"}, OccurredAt: time.Now().UTC(),
	}, ""); err != nil {
		t.Fatal(err)
	}
	afterGlobalStatus, err := database.ChannelDeliveries(ctx, instance.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterGlobalStatus) != len(beforeUnrouted)+1 {
		t.Fatalf("whitelisted global task status was not delivered: before=%d after=%d", len(beforeUnrouted), len(afterGlobalStatus))
	}
	actionNow := time.Now().UTC()
	action := domain.ChannelPendingAction{ID: "action-takeover", InstanceID: instance.ID, ConversationID: conversation.ID, ActorIdentityLinkID: "channel-owner", Operation: "takeover", TargetType: "task", TargetID: target.ID, Intent: map[string]any{}, Preview: map[string]any{"task": target.ID}, Precondition: map[string]any{}, PreconditionHash: "hash", Status: "pending", ExpiresAt: actionNow.Add(time.Hour), CreatedAt: actionNow, UpdatedAt: actionNow}
	if _, err := database.CreateChannelPendingAction(ctx, action); err != nil {
		t.Fatal(err)
	}
	deliveryRaw, _, err := service.IssueCapability(ctx, instance.ID, []string{"channel.delivery.claim", "channel.delivery.ack"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	deliveryClaims, err := service.AuthenticateCapability(ctx, deliveryRaw, "channel.delivery.claim")
	if err != nil {
		t.Fatal(err)
	}
	var confirmation domain.ChannelDelivery
	for attempt := 0; attempt < 20 && confirmation.ID == ""; attempt++ {
		deliveries, claimErr := service.ClaimDeliveries(ctx, deliveryClaims, 10)
		if claimErr != nil || len(deliveries) == 0 {
			t.Fatalf("deliveries=%#v err=%v", deliveries, claimErr)
		}
		for _, delivery := range deliveries {
			messageID := fmt.Sprintf("provider-message-%d-%d", attempt, delivery.StreamSequence)
			if delivery.SemanticPayload["kind"] == "confirmation" {
				messageID = "provider-card-1"
				confirmation = delivery
			}
			if err := service.AckDelivery(ctx, deliveryClaims, delivery.ID, delivery.LeaseID, messageID, "request-1"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if confirmation.ID == "" {
		t.Fatal("confirmation delivery was not claimed")
	}
	action, execute, err := database.BeginChannelPendingAction(ctx, action.ID, instance.ID, conversation.ID, "channel-owner", "provider-card-1", time.Now().UTC())
	if err != nil || !execute {
		t.Fatalf("action=%#v execute=%v err=%v", action, execute, err)
	}
	if err := database.ActivateChannelTaskRoute(ctx, action, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	route, err := database.ActiveChannelTaskRoute(ctx, conversation.ID)
	if err != nil || route.TargetTaskID != target.ID {
		t.Fatalf("route=%#v err=%v", route, err)
	}
	resultDeliveries, err := service.ClaimDeliveries(ctx, deliveryClaims, 10)
	if err != nil || len(resultDeliveries) == 0 {
		t.Fatalf("result deliveries=%#v err=%v", resultDeliveries, err)
	}
	dead := resultDeliveries[0]
	if err := service.NackDelivery(ctx, deliveryClaims, dead.ID, dead.LeaseID, "rate_limited", "confirmed_failure", 0, true); err != nil {
		t.Fatal(err)
	}
	blocked, err := service.ClaimDeliveries(ctx, deliveryClaims, 10)
	if err != nil || len(blocked) != 0 {
		t.Fatalf("dead letter did not block stream: %#v err=%v", blocked, err)
	}
	replay, err := database.ReplayChannelDelivery(ctx, dead.ID, instance.ID, time.Now().UTC())
	if err != nil || replay.ReplayGeneration != 1 {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	replayed, err := service.ClaimDeliveries(ctx, deliveryClaims, 10)
	if err != nil || len(replayed) != 1 || replayed[0].ReplayOfID != dead.ID {
		t.Fatalf("replayed=%#v err=%v", replayed, err)
	}
	if err := service.AckDelivery(ctx, deliveryClaims, replayed[0].ID, replayed[0].LeaseID, "provider-replay", "request-replay"); err != nil {
		t.Fatal(err)
	}
	exitAction := domain.ChannelPendingAction{ID: "action-exit", InstanceID: instance.ID, ConversationID: conversation.ID, ActorIdentityLinkID: "channel-owner", Operation: "exit", TargetType: "task_route", TargetID: target.ID, Intent: map[string]any{}, Preview: map[string]any{"effect": "unbind only"}, Precondition: map[string]any{}, PreconditionHash: "hash", Status: "pending", ProviderMessageID: "provider-card-exit", ExpiresAt: actionNow.Add(time.Hour), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if _, err := database.CreateChannelPendingAction(ctx, exitAction); err != nil {
		t.Fatal(err)
	}
	exitAction, execute, err = database.BeginChannelPendingAction(ctx, exitAction.ID, instance.ID, conversation.ID, "channel-owner", "provider-card-exit", time.Now().UTC())
	if err != nil || !execute {
		t.Fatalf("exit action=%#v execute=%v err=%v", exitAction, execute, err)
	}
	if err := database.ExitChannelTaskRoute(ctx, exitAction, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActiveChannelTaskRoute(ctx, conversation.ID); err == nil {
		t.Fatal("route remained active after exit")
	}
	targetAfterExit, err := database.Task(ctx, target.ID)
	if err != nil || targetAfterExit.Status != target.Status {
		t.Fatalf("exit changed target task: before=%s after=%s err=%v", target.Status, targetAfterExit.Status, err)
	}
	wrong := dm
	wrong.ExternalEventID, wrong.ExternalSenderID = "event-wrong", "not-owner"
	if _, _, err := service.ReceiveInbound(ctx, claims, wrong); err != nil {
		t.Fatal(err)
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	tasks, _ = database.ListTasks(ctx, instance.HostProjectID)
	if len(tasks) != 1 {
		t.Fatalf("non-owner DM created task: %#v", tasks)
	}
	for index, chatID := range []string{"group-a", "group-b"} {
		group := dm
		group.RequestID = fmt.Sprintf("group-request-%d", index)
		group.ExternalEventID = fmt.Sprintf("group-event-%d", index)
		group.ChatType, group.ExternalChatID, group.ExternalSenderID, group.MentionedBot = "group", chatID, "same-user", true
		group.ChatDisplayName, group.SenderDisplayName = "研发群", "张三"
		if _, _, err := service.ReceiveInbound(ctx, claims, group); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	tasks, _ = database.ListTasks(ctx, instance.HostProjectID)
	if len(tasks) != 3 {
		t.Fatalf("group scopes did not isolate by chat: %#v", tasks)
	}
	groupTitles := 0
	for _, task := range tasks {
		if task.Title == "飞书群聊 · 研发群 · 张三" {
			groupTitles++
		}
	}
	if groupTitles != 2 {
		t.Fatalf("group channel task titles=%#v", tasks)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		active := false
		for _, task := range tasks {
			if turn, turnErr := database.ActiveTurn(ctx, task.ID); turnErr == nil && turn.ID != "" {
				active = true
				break
			}
		}
		if !active {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	dmTurns, err := database.ListTurns(ctx, conversation.HostTaskID)
	if err != nil || len(dmTurns) == 0 {
		t.Fatalf("dm turns=%#v err=%v", dmTurns, err)
	}
	prompt := dmTurns[len(dmTurns)-1].PromptSnapshot
	if !strings.Contains(prompt, "Channel Assistant Identity") || !strings.Contains(prompt, "channel-context.json") {
		t.Fatalf("channel prompt route missing: %s", prompt)
	}
	if strings.Contains(prompt, "owner-open") || strings.Contains(prompt, "chat-owner") {
		t.Fatal("raw provider identity leaked into prompt")
	}
	var records []domain.ChannelKnowledgeRecord
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		records, err = database.ChannelKnowledgeRecords(ctx, instance.ID)
		if err == nil && len(records) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || len(records) != 2 {
		t.Fatalf("knowledge records=%#v err=%v", records, err)
	}
	if records[0].AuthorityStatus != "observed" || records[0].Visibility != "conversation_only" {
		t.Fatalf("record auto-promoted: %#v", records[0])
	}
	groupEndpoint, err := database.ChannelEndpoint(ctx, instance.ID, domain.ChannelEndpointGroupDigitalHuman)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := database.ChannelAllowedKnowledge(ctx, instance.ID, groupEndpoint.Kind, records[0].ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range allowed {
		if entry.ID == records[1].KnowledgeEntryID {
			t.Fatal("conversation-only knowledge crossed group conversation")
		}
	}
	if _, err := database.PromoteChannelKnowledgeRecord(ctx, records[0].ID, instance.ID, "Curated answer", "Human reviewed content", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	allowed, err = database.ChannelAllowedKnowledge(ctx, instance.ID, groupEndpoint.Kind, records[1].ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	shared := false
	for _, entry := range allowed {
		shared = shared || entry.ID == records[0].KnowledgeEntryID
	}
	if !shared {
		t.Fatal("human-promoted knowledge was not shared within instance")
	}
}
