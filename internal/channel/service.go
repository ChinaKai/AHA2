package channel

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type SecretStore interface {
	Get(string) (string, bool)
	PutMany(map[string]string) error
	DeleteMany([]string) error
}

type Service struct {
	store              *store.Store
	application        *app.Service
	secrets            SecretStore
	pluginsRoot        string
	instancesRoot      string
	runtimeDeviceID    string
	logger             *slog.Logger
	now                func() time.Time
	createMu           sync.Mutex
	pluginRefreshMu    sync.Mutex
	pluginsRefreshedAt time.Time
	wakeInbound        chan struct{}
	processMu          sync.Mutex
	processes          map[string]*managedPluginProcess
	processRetry       map[string]time.Time
	processFailures    map[string]int
	runtimeBaseURL     string
	runCtx             context.Context
}

type RuntimeClaims struct {
	CapabilityID string
	PluginID     string
	InstanceID   string
	Scopes       map[string]bool
	ExpiresAt    time.Time
}

type ActionPreviewInput struct {
	Operation string         `json:"operation"`
	TargetID  string         `json:"target_id,omitempty"`
	Intent    map[string]any `json:"intent,omitempty"`
}

type AgentOutreachInput struct {
	RequestID              string   `json:"request_id"`
	Purpose                string   `json:"purpose"`
	Message                string   `json:"message"`
	MentionIdentityLinkIDs []string `json:"mention_identity_link_ids"`
	AttachmentIDs          []string `json:"attachment_ids,omitempty"`
}

type KnowledgeGrantInput struct {
	KnowledgeEntryID string `json:"knowledge_entry_id"`
	GrantScope       string `json:"grant_scope"`
}

type Config struct {
	Store         *store.Store
	App           *app.Service
	Secrets       SecretStore
	DataDir       string
	PluginsRoot   string
	Logger        *slog.Logger
	RuntimeDevice string
}

func New(config Config) *Service {
	pluginsRoot := config.PluginsRoot
	if pluginsRoot == "" {
		pluginsRoot = filepath.Join(config.DataDir, "plugins", "channels")
	}
	device := strings.TrimSpace(config.RuntimeDevice)
	if device == "" {
		digest := sha256.Sum256([]byte(filepath.Clean(config.DataDir)))
		device = "local-" + hex.EncodeToString(digest[:6])
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store: config.Store, application: config.App, secrets: config.Secrets, pluginsRoot: pluginsRoot,
		instancesRoot: filepath.Join(config.DataDir, "channels", "instances"), runtimeDeviceID: device,
		logger: logger, now: time.Now, wakeInbound: make(chan struct{}, 1), processes: map[string]*managedPluginProcess{}, processRetry: map[string]time.Time{}, processFailures: map[string]int{},
	}
}

func (s *Service) Start(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	s.processMu.Lock()
	s.runCtx = ctx
	s.processMu.Unlock()
	if err := s.store.SkipEphemeralMenuCards(ctx, s.now().UTC()); err != nil {
		s.logger.Warn("skip stale channel menu cards failed", "error", err)
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.wakeInbound:
			}
			if err := s.processInboundBatch(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Warn("channel inbound processing failed", "error", err)
			}
			s.reconcileProcesses(ctx)
		}
	}()
}

func (s *Service) SetRuntimeBaseURL(value string) {
	s.processMu.Lock()
	s.runtimeBaseURL = strings.TrimRight(strings.TrimSpace(value), "/")
	s.processMu.Unlock()
}

func (s *Service) RefreshPlugins(ctx context.Context) error {
	if s == nil || s.store == nil {
		return nil
	}
	s.pluginRefreshMu.Lock()
	defer s.pluginRefreshMu.Unlock()
	err := Discover(ctx, s.store, s.pluginsRoot, s.now().UTC())
	if err == nil {
		s.pluginsRefreshedAt = s.now().UTC()
	}
	return err
}

func (s *Service) Providers(ctx context.Context) ([]domain.ChannelPlugin, error) {
	s.pluginRefreshMu.Lock()
	stale := s.pluginsRefreshedAt.IsZero() || s.now().UTC().Sub(s.pluginsRefreshedAt) >= 5*time.Second
	s.pluginRefreshMu.Unlock()
	if stale {
		if err := s.RefreshPlugins(ctx); err != nil {
			s.logger.Warn("channel plugin discovery failed", "error", err)
		}
	}
	return s.store.ChannelPlugins(ctx)
}

func (s *Service) Instances(ctx context.Context, ownerID string) ([]domain.ChannelInstance, error) {
	return s.store.ChannelInstances(ctx, ownerID)
}

func (s *Service) Instance(ctx context.Context, ownerID, id string) (domain.ChannelInstance, []domain.ChannelEndpoint, error) {
	item, err := s.store.ChannelInstance(ctx, id)
	if err != nil || item.OwnerID != ownerID {
		if err == nil {
			err = sql.ErrNoRows
		}
		return domain.ChannelInstance{}, nil, err
	}
	endpoints, err := s.store.ChannelEndpoints(ctx, id)
	return item, endpoints, err
}

func (s *Service) TaskChannelRoutes(ctx context.Context, ownerID, taskID string) ([]domain.ChannelDestination, domain.ChannelTaskRoute, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil || task.ReadOnly || s.store.IsManagedChannelTask(ctx, taskID) {
		if err == nil {
			err = fmt.Errorf("task is not eligible for a primary channel")
		}
		return nil, domain.ChannelTaskRoute{}, err
	}
	destinations, err := s.store.ChannelDestinations(ctx, ownerID)
	if err != nil {
		return nil, domain.ChannelTaskRoute{}, err
	}
	filtered := destinations[:0]
	for _, destination := range destinations {
		instance, instanceErr := s.store.ChannelInstance(ctx, destination.InstanceID)
		if instanceErr == nil && channelOperationAllowed(instance.Config, task.ProjectID, task.WorkspaceID) {
			filtered = append(filtered, destination)
		}
	}
	route, routeErr := s.store.ActiveChannelTaskRouteForTask(ctx, taskID)
	if routeErr != nil && !errors.Is(routeErr, sql.ErrNoRows) {
		return nil, domain.ChannelTaskRoute{}, routeErr
	}
	return filtered, route, nil
}

func (s *Service) OwnerTaskChannelContacts(ctx context.Context, ownerID, taskID string) ([]domain.ChannelContact, error) {
	route, err := s.store.ActiveChannelTaskRouteForTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	destination, err := s.store.ChannelDestination(ctx, ownerID, route.ConversationID)
	if err != nil || destination.EndpointKind != domain.ChannelEndpointGroupDigitalHuman {
		if err == nil {
			return []domain.ChannelContact{}, nil
		}
		return nil, err
	}
	return s.store.TaskChannelContacts(ctx, taskID, route.InstanceID, route.ConversationID, 50)
}

func (s *Service) OwnerTaskChannelMembers(ctx context.Context, ownerID, taskID string) ([]domain.ChannelGroupMember, error) {
	route, err := s.store.ActiveChannelTaskRouteForTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	destination, err := s.store.ChannelDestination(ctx, ownerID, route.ConversationID)
	if err != nil || destination.EndpointKind != domain.ChannelEndpointGroupDigitalHuman {
		if err == nil {
			return []domain.ChannelGroupMember{}, nil
		}
		return nil, err
	}
	return s.store.ChannelMembersForConversation(ctx, route.InstanceID, route.ConversationID, 500)
}

func (s *Service) SetOwnerTaskChannelContacts(ctx context.Context, ownerID, taskID string, contacts []store.TaskChannelContactInput) ([]domain.ChannelContact, error) {
	if len(contacts) > 50 {
		return nil, fmt.Errorf("choose no more than 50 task channel contacts")
	}
	route, err := s.store.ActiveChannelTaskRouteForTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	destination, err := s.store.ChannelDestination(ctx, ownerID, route.ConversationID)
	if err != nil || destination.EndpointKind != domain.ChannelEndpointGroupDigitalHuman {
		return nil, fmt.Errorf("task primary channel is not a group chat")
	}
	if err := s.store.ReplaceTaskChannelContacts(ctx, taskID, route.InstanceID, route.ConversationID, contacts, s.now().UTC()); err != nil {
		return nil, err
	}
	return s.store.TaskChannelContacts(ctx, taskID, route.InstanceID, route.ConversationID, 50)
}

func (s *Service) RefreshOwnerTaskChannelMembers(ctx context.Context, ownerID, taskID string) (domain.ChannelPluginCommand, error) {
	route, err := s.store.ActiveChannelTaskRouteForTask(ctx, taskID)
	if err != nil {
		return domain.ChannelPluginCommand{}, err
	}
	destination, err := s.store.ChannelDestination(ctx, ownerID, route.ConversationID)
	if err != nil || destination.EndpointKind != domain.ChannelEndpointGroupDigitalHuman {
		return domain.ChannelPluginCommand{}, fmt.Errorf("task primary channel is not a group chat")
	}
	conversation, err := s.store.ChannelConversation(ctx, route.ConversationID)
	if err != nil || strings.TrimSpace(conversation.ExternalChatID) == "" {
		return domain.ChannelPluginCommand{}, fmt.Errorf("group chat destination is unavailable")
	}
	return s.enqueueChannelMemberSync(ctx, route, conversation)
}

func (s *Service) enqueueChannelMemberSync(ctx context.Context, route domain.ChannelTaskRoute, conversation domain.ChannelConversation) (domain.ChannelPluginCommand, error) {
	return s.EnqueueCommand(ctx, route.InstanceID, "sync_chat_members", domain.NewID("channel_member_sync"), map[string]any{
		"conversation_id":  conversation.ID,
		"external_chat_id": conversation.ExternalChatID,
	})
}

func (s *Service) BindTaskChannelRoute(ctx context.Context, ownerID, taskID, conversationID string) (domain.ChannelTaskRoute, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil || task.ReadOnly || s.store.IsManagedChannelTask(ctx, taskID) || !taskRouteEligible(task.Status) {
		return domain.ChannelTaskRoute{}, fmt.Errorf("task is not eligible for a primary channel")
	}
	destination, err := s.store.ChannelDestination(ctx, ownerID, conversationID)
	if err != nil {
		return domain.ChannelTaskRoute{}, fmt.Errorf("channel destination is not available")
	}
	if destination.TargetTaskID != "" && destination.TargetTaskID != taskID {
		return domain.ChannelTaskRoute{}, fmt.Errorf("channel destination is already connected to another task")
	}
	instance, err := s.store.ChannelInstance(ctx, destination.InstanceID)
	if err != nil || (instance.Status != "ready" && instance.Status != "degraded") || !channelOperationAllowed(instance.Config, task.ProjectID, task.WorkspaceID) {
		return domain.ChannelTaskRoute{}, fmt.Errorf("channel destination is not eligible for this task")
	}
	if current, currentErr := s.store.ActiveChannelTaskRouteForTask(ctx, taskID); currentErr == nil && current.ConversationID == conversationID {
		return current, nil
	}
	route, err := s.store.ActivateOwnerChannelTaskRoute(ctx, destination.InstanceID, conversationID, taskID, s.now().UTC())
	if err != nil {
		return domain.ChannelTaskRoute{}, err
	}
	if destination.EndpointKind == domain.ChannelEndpointGroupDigitalHuman {
		if conversation, conversationErr := s.store.ChannelConversation(ctx, conversationID); conversationErr == nil {
			_, _ = s.enqueueChannelMemberSync(ctx, route, conversation)
		}
	}
	return route, nil
}

func (s *Service) UnbindTaskChannelRoute(ctx context.Context, ownerID, taskID, routeID string) error {
	route, err := s.store.ChannelRouteOwnedBy(ctx, routeID, ownerID)
	if err != nil || route.TargetTaskID != taskID || route.State != "active" {
		return fmt.Errorf("active task channel route was not found")
	}
	return s.store.ExitOwnerChannelTaskRoute(ctx, route.ID, taskID, s.now().UTC())
}

func (s *Service) SetPluginEnabled(ctx context.Context, id string, enabled bool, revision int) (domain.ChannelPlugin, error) {
	item, err := s.store.UpdateChannelPluginEnabled(ctx, id, enabled, revision, s.now().UTC())
	if err != nil || enabled {
		return item, err
	}
	instances, _ := s.store.ChannelInstances(ctx, "")
	for _, instance := range instances {
		if instance.PluginID != id {
			continue
		}
		s.stopPluginProcess(instance.ID)
		_ = s.RevokeCapabilities(ctx, instance.ID)
	}
	return item, nil
}

func (s *Service) CreateInstance(ctx context.Context, ownerID, pluginID, name, idempotencyKey string) (domain.ChannelInstance, error) {
	name = strings.TrimSpace(name)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if ownerID == "" || name == "" || idempotencyKey == "" {
		return domain.ChannelInstance{}, fmt.Errorf("owner, channel instance name, and idempotency key are required")
	}
	plugin, err := s.store.ChannelPlugin(ctx, pluginID)
	if err != nil {
		return domain.ChannelInstance{}, fmt.Errorf("channel provider not found")
	}
	if !plugin.Available {
		return domain.ChannelInstance{}, fmt.Errorf("channel provider is unavailable")
	}
	s.createMu.Lock()
	defer s.createMu.Unlock()
	now := s.now().UTC()
	instanceID := stableID("channel_instance", ownerID, pluginID, idempotencyKey)
	projectID := stableID("project", instanceID)
	workspaceID := stableID("workspace", instanceID)
	if existing, existingErr := s.store.ChannelInstance(ctx, instanceID); existingErr == nil {
		if existing.OwnerID != ownerID || existing.PluginID != pluginID || existing.Name != name {
			return domain.ChannelInstance{}, fmt.Errorf("idempotency key was already used with a different request")
		}
		if root, rootErr := s.store.EnsureKnowledgeRoot(ctx, "project", existing.HostProjectID); rootErr == nil {
			_ = s.store.EnsureChannelKnowledgePolicies(ctx, existing.ID, root.ID, now)
		}
		return existing, nil
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return domain.ChannelInstance{}, existingErr
	}
	root := filepath.Join(s.instancesRoot, instanceID, "workspace")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return domain.ChannelInstance{}, fmt.Errorf("create channel workspace: %w", err)
	}
	project := domain.Project{
		ID: projectID, Name: "渠道 · " + plugin.DisplayName + " · " + name,
		Description: "AHA2 系统管理的渠道运行宿主；请在“渠道”页面管理。", ProjectType: "channel",
		DefaultWorkspaceID: workspaceID, KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	workspace := domain.Workspace{
		ID: workspaceID, ProjectID: projectID, Name: "Channel Runtime", Locality: "local", Transport: "native",
		RootPath: root, Platform: runtime.GOOS, Health: "ready", Capabilities: map[string]any{"channel_managed": true},
		CreatedAt: now, UpdatedAt: now, AgentAPIMode: "global", AgentAPIStatus: "unknown",
	}
	item := domain.ChannelInstance{
		ID: instanceID, PluginID: plugin.ID, ProviderKey: plugin.ProviderKey, OwnerID: ownerID,
		RuntimeDeviceID: s.runtimeDeviceID, Name: name, Status: "draft", EffectiveAvailability: "available", Revision: 1,
		HostProjectID: projectID, HostWorkspaceID: workspaceID, Config: map[string]any{"operation_scope_mode": "all"}, CreatedAt: now, UpdatedAt: now,
	}
	endpoints := []domain.ChannelEndpoint{
		{ID: domain.NewID("channel_endpoint"), InstanceID: instanceID, Kind: domain.ChannelEndpointAssistantDM, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now},
		{ID: domain.NewID("channel_endpoint"), InstanceID: instanceID, Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now},
	}
	if err := s.store.CreateChannelInstanceWithHost(ctx, project, workspace, item, endpoints); err != nil {
		return domain.ChannelInstance{}, err
	}
	knowledgeRoot, err := s.store.EnsureKnowledgeRoot(ctx, "project", projectID)
	if err != nil {
		return domain.ChannelInstance{}, fmt.Errorf("create channel knowledge root: %w", err)
	}
	if err := s.store.EnsureChannelKnowledgePolicies(ctx, item.ID, knowledgeRoot.ID, now); err != nil {
		return domain.ChannelInstance{}, fmt.Errorf("create channel knowledge policies: %w", err)
	}
	return s.store.ChannelInstance(ctx, instanceID)
}

func stableID(prefix string, values ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + "_" + hex.EncodeToString(digest[:12])
}

func safeChannelDisplayName(value, fallbackPrefix, instanceID, externalID string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if len([]rune(value)) > 60 {
		value = string([]rune(value)[:60])
	}
	if value != "" {
		return value
	}
	digest := sha256.Sum256([]byte(instanceID + "\x00" + externalID))
	return fallbackPrefix + " " + hex.EncodeToString(digest[:3])
}

func (s *Service) UpdateInstance(ctx context.Context, ownerID, id, name string, config map[string]any, expectedRevision int) (domain.ChannelInstance, error) {
	item, err := s.store.ChannelInstance(ctx, id)
	if err != nil || item.OwnerID != ownerID {
		if err == nil {
			err = sql.ErrNoRows
		}
		return domain.ChannelInstance{}, err
	}
	if item.Retired {
		return domain.ChannelInstance{}, fmt.Errorf("channel instance is archived")
	}
	if strings.TrimSpace(name) != "" {
		item.Name = strings.TrimSpace(name)
	}
	if config != nil {
		merged := map[string]any{}
		for key, value := range item.Config {
			merged[key] = value
		}
		for key, value := range config {
			merged[key] = value
		}
		if value, present := config["runtime_bot_display_name_override"]; present {
			merged["runtime_bot_display_name_override"] = strings.TrimSpace(fmt.Sprint(value))
		}
		if err := s.validateChannelInstanceConfig(ctx, item, merged); err != nil {
			return domain.ChannelInstance{}, err
		}
		item.Config = merged
	}
	item.UpdatedAt = s.now().UTC()
	return s.store.UpdateChannelInstance(ctx, item, expectedRevision)
}

func (s *Service) validateChannelInstanceConfig(ctx context.Context, instance domain.ChannelInstance, config map[string]any) error {
	if value, present := config["runtime_bot_display_name_override"]; present {
		displayName, ok := value.(string)
		if !ok || len([]rune(displayName)) > 60 || strings.IndexFunc(displayName, func(r rune) bool {
			return r == '\r' || r == '\n' || r < 0x20 || r == 0x7f
		}) >= 0 {
			return fmt.Errorf("invalid channel bot display name")
		}
	}
	if _, present := config["bot_dialogue_max_turns"]; present {
		maxTurns := intField(config, "bot_dialogue_max_turns")
		if maxTurns < 1 || maxTurns > 50 {
			return fmt.Errorf("invalid bot dialogue max turns")
		}
	}
	mode := stringField(config, "operation_scope_mode")
	if mode != "" && mode != "all" && mode != "selected" {
		return fmt.Errorf("invalid channel operation scope mode")
	}
	projects, _ := stringListField(config, "allowed_project_ids")
	workspaces, _ := stringListField(config, "allowed_workspace_ids")
	projectsRestricted, workspacesRestricted := channelOperationRestrictions(config)
	if projectsRestricted {
		for _, id := range projects {
			project, err := s.store.Project(ctx, id)
			if err != nil || project.ProjectType == "channel" || project.ProjectType == "knowledge" {
				return fmt.Errorf("invalid channel project allowlist")
			}
		}
	}
	if workspacesRestricted {
		allowedProjects := sliceSet(projects)
		for _, id := range workspaces {
			workspace, err := s.store.Workspace(ctx, id)
			if err != nil || workspace.ReadOnly || s.store.IsManagedChannelWorkspace(ctx, id) || (projectsRestricted && !allowedProjects[workspace.ProjectID]) {
				return fmt.Errorf("invalid channel workspace allowlist")
			}
		}
	}
	for _, key := range []string{"runtime_default", "runtime_assistant_dm", "runtime_group_digital_human"} {
		runtimeConfig, present := mapField(config, key)
		if !present || boolField(runtimeConfig, "inherit") || stringField(runtimeConfig, "model_id") == "" {
			continue
		}
		model, err := s.store.Model(ctx, stringField(runtimeConfig, "model_id"))
		if err != nil || !s.channelRuntimeModelAvailable(ctx, model, stringField(runtimeConfig, "codex_account_id"), model.WireModel) {
			return fmt.Errorf("invalid channel runtime configuration")
		}
	}
	_ = instance
	return nil
}

func (s *Service) SetInstanceEnabled(ctx context.Context, ownerID, id string, enabled bool, expectedRevision int) (domain.ChannelInstance, error) {
	item, err := s.store.ChannelInstance(ctx, id)
	if err != nil || item.OwnerID != ownerID {
		return domain.ChannelInstance{}, sql.ErrNoRows
	}
	if item.Retired {
		return domain.ChannelInstance{}, fmt.Errorf("channel instance is archived")
	}
	if !enabled {
		item.Status = "disabled"
	} else if item.CredentialConfigured && item.OwnerBound {
		item.Status = "degraded"
	} else {
		item.Status = "draft"
	}
	item.UpdatedAt = s.now().UTC()
	updated, err := s.store.UpdateChannelInstance(ctx, item, expectedRevision)
	if err != nil {
		return domain.ChannelInstance{}, err
	}
	if !enabled {
		s.stopPluginProcess(id)
		_ = s.RevokeCapabilities(ctx, id)
	} else {
		s.processMu.Lock()
		delete(s.processRetry, id)
		delete(s.processFailures, id)
		s.processMu.Unlock()
		s.reconcileProcesses(ctx)
	}
	return updated, nil
}

func (s *Service) clearLifecycleSecrets(ctx context.Context, instanceID string, refs []string) error {
	if s.secrets != nil && len(refs) > 0 {
		if err := s.secrets.DeleteMany(uniqueStrings(refs)); err != nil {
			return err
		}
	}
	return s.store.ClearChannelSecretRefs(ctx, instanceID)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func (s *Service) ResetBinding(ctx context.Context, ownerID, id string, expectedRevision int) (domain.ChannelInstance, error) {
	s.stopPluginProcess(id)
	item, refs, err := s.store.ResetChannelBinding(ctx, id, ownerID, expectedRevision, s.now().UTC())
	if err != nil {
		return domain.ChannelInstance{}, err
	}
	if err := s.clearLifecycleSecrets(ctx, id, refs); err != nil {
		return domain.ChannelInstance{}, err
	}
	return s.store.ChannelInstance(ctx, item.ID)
}

func (s *Service) ArchiveInstance(ctx context.Context, ownerID, id string, expectedRevision int) (domain.ChannelInstance, error) {
	if active, err := s.store.ChannelInstanceHasActiveTurn(ctx, id); err != nil || active {
		if err != nil {
			return domain.ChannelInstance{}, err
		}
		return domain.ChannelInstance{}, store.ErrActiveTurn
	}
	s.stopPluginProcess(id)
	item, refs, err := s.store.RetireChannelInstance(ctx, id, ownerID, expectedRevision, s.now().UTC())
	if err != nil {
		return domain.ChannelInstance{}, err
	}
	if err := s.clearLifecycleSecrets(ctx, id, refs); err != nil {
		return domain.ChannelInstance{}, err
	}
	return s.store.ChannelInstance(ctx, item.ID)
}

func (s *Service) PurgePreview(ctx context.Context, ownerID, id string) (domain.ChannelPurgePreview, error) {
	return s.store.ChannelPurgePreview(ctx, id, ownerID)
}

func (s *Service) PurgeInstance(ctx context.Context, ownerID, id string, expectedRevision int) (domain.ChannelInstance, error) {
	if active, err := s.store.ChannelInstanceHasActiveTurn(ctx, id); err != nil || active {
		if err != nil {
			return domain.ChannelInstance{}, err
		}
		return domain.ChannelInstance{}, store.ErrActiveTurn
	}
	s.stopPluginProcess(id)
	item, err := s.store.PurgeRetiredChannelInstance(ctx, id, ownerID, expectedRevision)
	if err != nil {
		return domain.ChannelInstance{}, err
	}
	root := filepath.Clean(filepath.Join(s.instancesRoot, item.ID))
	prefix := filepath.Clean(s.instancesRoot) + string(os.PathSeparator)
	if strings.HasPrefix(root+string(os.PathSeparator), prefix) {
		_ = os.RemoveAll(root)
	}
	return item, nil
}

func (s *Service) OwnerHandoffs(ctx context.Context, ownerID, instanceID string) ([]domain.ChannelHandoff, error) {
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil || instance.OwnerID != ownerID {
		return nil, sql.ErrNoRows
	}
	return s.store.ChannelHandoffs(ctx, instanceID)
}

func (s *Service) OwnerDeliveries(ctx context.Context, ownerID, instanceID string) ([]domain.ChannelDelivery, error) {
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil || instance.OwnerID != ownerID {
		return nil, sql.ErrNoRows
	}
	items, err := s.store.ChannelDeliveries(ctx, instanceID, 100)
	for index := range items {
		items[index].LeaseID = ""
		items[index].Target = nil
	}
	return items, err
}

func (s *Service) OwnerKnowledgePolicies(ctx context.Context, ownerID, instanceID string) ([]map[string]any, error) {
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil || instance.OwnerID != ownerID {
		return nil, sql.ErrNoRows
	}
	return s.store.ChannelKnowledgePolicies(ctx, instanceID)
}

func (s *Service) ReplaceOwnerKnowledgeGrants(ctx context.Context, ownerID, instanceID, endpoint, scopeMode string, revision int, inputs []KnowledgeGrantInput) ([]map[string]any, error) {
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil || instance.OwnerID != ownerID {
		return nil, sql.ErrNoRows
	}
	if instance.Retired {
		return nil, fmt.Errorf("channel instance is archived")
	}
	if scopeMode == "" {
		scopeMode = "selected"
	}
	grants := make([]domain.ChannelKnowledgeGrant, 0, len(inputs))
	allowedRoots, err := s.channelKnowledgeSourceRoots(ctx)
	if err != nil {
		return nil, err
	}
	for _, input := range inputs {
		entryID := strings.TrimSpace(input.KnowledgeEntryID)
		if entryID == "" || !allowedRoots[entryID] {
			return nil, fmt.Errorf("channel knowledge source is not selectable")
		}
		grants = append(grants, domain.ChannelKnowledgeGrant{KnowledgeEntryID: entryID, GrantScope: "subtree"})
	}
	if err := s.store.ReplaceChannelKnowledgeGrants(ctx, instanceID, endpoint, ownerID, scopeMode, revision, grants, s.now().UTC()); err != nil {
		return nil, err
	}
	return s.store.ChannelKnowledgePolicies(ctx, instanceID)
}

func (s *Service) channelKnowledgeSourceRoots(ctx context.Context) (map[string]bool, error) {
	entries, err := s.store.ListKnowledge(ctx, "", "", []domain.KnowledgeStatus{domain.KnowledgeVerified})
	if err != nil {
		return nil, err
	}
	libraries, err := s.store.ListKnowledgeLibraries(ctx)
	if err != nil {
		return nil, err
	}
	libraryProjects := map[string]bool{}
	for _, library := range libraries {
		libraryProjects[library.ContainerProjectID] = true
	}
	result := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsIndex || entry.ProjectID == "" {
			continue
		}
		if libraryProjects[entry.ProjectID] {
			result[entry.ID] = true
			continue
		}
		project, projectErr := s.store.Project(ctx, entry.ProjectID)
		if projectErr == nil && project.ProjectType != "channel" && project.ProjectType != "knowledge" {
			result[entry.ID] = true
		}
	}
	return result, nil
}

func (s *Service) ReplayOwnerDelivery(ctx context.Context, ownerID, id string) (domain.ChannelDelivery, error) {
	delivery, err := s.store.ChannelDelivery(ctx, id)
	if err != nil {
		return domain.ChannelDelivery{}, err
	}
	instance, err := s.store.ChannelInstance(ctx, delivery.InstanceID)
	if err != nil || instance.OwnerID != ownerID {
		return domain.ChannelDelivery{}, sql.ErrNoRows
	}
	if instance.Retired {
		return domain.ChannelDelivery{}, fmt.Errorf("channel instance is archived")
	}
	return s.store.ReplayChannelDelivery(ctx, id, instance.ID, s.now().UTC())
}

func (s *Service) SkipOwnerDelivery(ctx context.Context, ownerID, id string) error {
	delivery, err := s.store.ChannelDelivery(ctx, id)
	if err != nil {
		return err
	}
	instance, err := s.store.ChannelInstance(ctx, delivery.InstanceID)
	if err != nil || instance.OwnerID != ownerID {
		return sql.ErrNoRows
	}
	if instance.Retired {
		return fmt.Errorf("channel instance is archived")
	}
	return s.store.SkipChannelDelivery(ctx, id, instance.ID, s.now().UTC())
}

func (s *Service) StoreExistingCredential(ctx context.Context, ownerID, id, appID, secret string, expectedRevision int) (domain.ChannelInstance, error) {
	item, err := s.store.ChannelInstance(ctx, id)
	if err != nil || item.OwnerID != ownerID {
		if err == nil {
			err = sql.ErrNoRows
		}
		return domain.ChannelInstance{}, err
	}
	if item.Retired {
		return domain.ChannelInstance{}, fmt.Errorf("channel instance is archived")
	}
	appID, secret = strings.TrimSpace(appID), strings.TrimSpace(secret)
	if appID == "" || secret == "" {
		return domain.ChannelInstance{}, fmt.Errorf("app_id and app_secret are required")
	}
	if s.secrets == nil {
		return domain.ChannelInstance{}, fmt.Errorf("secret store is unavailable")
	}
	previousRef, previousAppID := item.CredentialRef, item.AppID
	ref := "channel/" + item.ID + "/" + item.ProviderKey + "/app_secret/" + domain.NewID("version")
	if err := s.secrets.PutMany(map[string]string{ref: secret}); err != nil {
		return domain.ChannelInstance{}, err
	}
	item.AppID, item.CredentialRef, item.CredentialConfigured = appID, ref, true
	item.Status, item.UpdatedAt = "onboarding", s.now().UTC()
	updated, err := s.store.UpdateChannelInstance(ctx, item, expectedRevision)
	if err != nil {
		_ = s.secrets.DeleteMany([]string{ref})
		return domain.ChannelInstance{}, err
	}
	if previousRef != "" && previousRef != ref {
		_ = s.secrets.DeleteMany([]string{previousRef})
	}
	s.stopPluginProcess(item.ID)
	_ = s.RevokeCapabilities(ctx, item.ID)
	if previousAppID != "" && previousAppID != appID {
		_ = s.store.ResetChannelExternalBinding(ctx, item.ID, s.now().UTC())
		return s.store.ChannelInstance(ctx, item.ID)
	}
	s.processMu.Lock()
	delete(s.processRetry, item.ID)
	delete(s.processFailures, item.ID)
	s.processMu.Unlock()
	s.reconcileProcesses(ctx)
	return updated, nil
}

func IsRevisionConflict(err error) bool {
	return errors.Is(err, store.ErrChannelRevision)
}

func (s *Service) IssueCapability(ctx context.Context, instanceID string, scopes []string, ttl time.Duration) (string, domain.ChannelServiceCapability, error) {
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil {
		return "", domain.ChannelServiceCapability{}, err
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	refs, err := s.store.RevokeChannelCapabilities(ctx, instanceID, s.now().UTC())
	if err != nil {
		return "", domain.ChannelServiceCapability{}, err
	}
	if s.secrets != nil && len(refs) > 0 {
		_ = s.secrets.DeleteMany(refs)
	}
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", domain.ChannelServiceCapability{}, err
	}
	raw := base64.RawURLEncoding.EncodeToString(rawBytes)
	digest := sha256.Sum256([]byte(raw))
	now := s.now().UTC()
	item := domain.ChannelServiceCapability{
		ID: domain.NewID("channel_capability"), PluginID: instance.PluginID, InstanceID: instance.ID,
		TokenHash: hex.EncodeToString(digest[:]), Scopes: uniqueScopes(scopes), Status: "active", IssuedAt: now, ExpiresAt: now.Add(ttl),
	}
	item.TokenSecretRef = "channel/" + instance.ID + "/runtime/capability/" + item.ID
	if s.secrets == nil {
		return "", domain.ChannelServiceCapability{}, fmt.Errorf("secret store is unavailable")
	}
	if err := s.secrets.PutMany(map[string]string{item.TokenSecretRef: raw}); err != nil {
		return "", domain.ChannelServiceCapability{}, err
	}
	if err := s.store.CreateChannelCapability(ctx, item); err != nil {
		_ = s.secrets.DeleteMany([]string{item.TokenSecretRef})
		return "", domain.ChannelServiceCapability{}, err
	}
	return raw, item, nil
}

func uniqueScopes(scopes []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		result = append(result, scope)
	}
	return result
}

func (s *Service) AuthenticateCapability(ctx context.Context, raw, requiredScope string) (RuntimeClaims, error) {
	if raw == "" {
		return RuntimeClaims{}, fmt.Errorf("capability_invalid")
	}
	digest := sha256.Sum256([]byte(raw))
	item, err := s.store.ChannelCapabilityByHash(ctx, hex.EncodeToString(digest[:]))
	if err != nil || item.Status != "active" || !item.ExpiresAt.After(s.now().UTC()) {
		return RuntimeClaims{}, fmt.Errorf("capability_invalid")
	}
	scopes := map[string]bool{}
	for _, scope := range item.Scopes {
		scopes[scope] = true
	}
	if requiredScope != "" && !scopes[requiredScope] {
		return RuntimeClaims{}, fmt.Errorf("capability_scope_denied")
	}
	_ = s.store.TouchChannelCapability(ctx, item.ID, s.now().UTC())
	return RuntimeClaims{CapabilityID: item.ID, PluginID: item.PluginID, InstanceID: item.InstanceID, Scopes: scopes, ExpiresAt: item.ExpiresAt}, nil
}

func (s *Service) RevokeCapabilities(ctx context.Context, instanceID string) error {
	refs, err := s.store.RevokeChannelCapabilities(ctx, instanceID, s.now().UTC())
	if err != nil {
		return err
	}
	if s.secrets != nil {
		return s.secrets.DeleteMany(refs)
	}
	return nil
}

func (s *Service) EnqueueCommand(ctx context.Context, instanceID, kind, key string, payload map[string]any) (domain.ChannelPluginCommand, error) {
	if containsSensitiveField(payload) {
		return domain.ChannelPluginCommand{}, fmt.Errorf("command payload contains forbidden sensitive fields")
	}
	now := s.now().UTC()
	return s.store.EnqueueChannelCommand(ctx, domain.ChannelPluginCommand{
		ID: domain.NewID("channel_command"), InstanceID: instanceID, Kind: strings.TrimSpace(kind), IdempotencyKey: strings.TrimSpace(key),
		Payload: payload, Progress: map[string]any{}, Result: map[string]any{}, State: "pending", AvailableAt: now, CreatedAt: now,
	})
}

func (s *Service) ClaimCommands(ctx context.Context, claims RuntimeClaims, limit int) ([]domain.ChannelPluginCommand, error) {
	if !claims.Scopes["channel.command.claim"] {
		return nil, fmt.Errorf("capability_scope_denied")
	}
	return s.store.ClaimChannelCommands(ctx, claims.InstanceID, limit, s.now().UTC(), 30*time.Second)
}

func (s *Service) CommandProgress(ctx context.Context, claims RuntimeClaims, id, leaseID string, progress map[string]any) error {
	if !claims.Scopes["channel.command.progress"] {
		return fmt.Errorf("capability_scope_denied")
	}
	if containsSensitiveField(progress) {
		return fmt.Errorf("invalid_envelope")
	}
	return s.store.UpdateChannelCommandProgress(ctx, claims.InstanceID, id, leaseID, progress, s.now().UTC().Add(30*time.Second))
}

func (s *Service) CompleteCommand(ctx context.Context, claims RuntimeClaims, id, leaseID string, success bool, result map[string]any, errorCode string) error {
	if !claims.Scopes["channel.command.complete"] {
		return fmt.Errorf("capability_scope_denied")
	}
	if containsSensitiveField(result) {
		return fmt.Errorf("invalid_envelope")
	}
	if command, err := s.store.ChannelCommand(ctx, claims.InstanceID, id); err == nil && command.Kind == "download_resource" {
		if success {
			return fmt.Errorf("invalid_envelope")
		}
		return s.store.RetryChannelMediaCommand(ctx, claims.InstanceID, id, leaseID, errorCode, s.now().UTC())
	}
	if command, err := s.store.ChannelCommand(ctx, claims.InstanceID, id); err == nil && command.Kind == "sync_chat_members" && success {
		if command.State != "leased" || command.LeaseID != leaseID || !command.LeaseUntil.After(s.now().UTC()) {
			return store.ErrChannelRevision
		}
		if err := s.applyChannelMemberSync(ctx, command, result); err != nil {
			return err
		}
	}
	return s.store.CompleteChannelCommand(ctx, claims.InstanceID, id, leaseID, success, result, errorCode, s.now().UTC())
}

func (s *Service) applyChannelMemberSync(ctx context.Context, command domain.ChannelPluginCommand, result map[string]any) error {
	conversationID := strings.TrimSpace(fmt.Sprint(command.Payload["conversation_id"]))
	conversation, err := s.store.ChannelConversation(ctx, conversationID)
	if err != nil || conversation.InstanceID != command.InstanceID {
		return fmt.Errorf("invalid_envelope")
	}
	rawMembers, ok := result["members"].([]any)
	if !ok {
		raw, marshalErr := json.Marshal(result["members"])
		if marshalErr != nil || json.Unmarshal(raw, &rawMembers) != nil {
			return fmt.Errorf("invalid_envelope")
		}
	}
	if len(rawMembers) > 1000 {
		return fmt.Errorf("invalid_envelope")
	}
	now := s.now().UTC()
	members := make([]store.ChannelMemberInput, 0, len(rawMembers))
	seen := map[string]bool{}
	for _, rawMember := range rawMembers {
		member, ok := rawMember.(map[string]any)
		if !ok {
			continue
		}
		externalID := strings.TrimSpace(fmt.Sprint(member["external_user_id"]))
		if externalID == "" || externalID == "<nil>" || seen[externalID] {
			continue
		}
		seen[externalID] = true
		isBot, _ := member["is_bot"].(bool)
		fallback := "群成员"
		if isBot {
			fallback = "群机器人"
		}
		displayName := strings.TrimSpace(fmt.Sprint(member["display_name"]))
		if displayName == "<nil>" {
			displayName = ""
		} else if displayName != "" {
			displayName = safeChannelDisplayName(displayName, fallback, command.InstanceID, externalID)
		}
		identity, linkErr := s.store.UpsertObservedChannelParticipant(ctx, domain.ChannelIdentityLink{
			ID: domain.NewID("channel_identity"), InstanceID: command.InstanceID, ExternalUserID: externalID,
			Role: "participant", DisplayName: displayName,
			Status: "active", LinkedAt: now,
		}, safeChannelDisplayName("", fallback, command.InstanceID, externalID))
		if linkErr != nil {
			return linkErr
		}
		members = append(members, store.ChannelMemberInput{IdentityLinkID: identity.ID, IsBot: isBot})
	}
	if err := s.store.ReplaceProviderChannelMembers(ctx, conversation.ID, members, now); err != nil {
		return err
	}
	return s.store.EnsureCurrentChannelBot(ctx, conversation.ID, now)
}

func (s *Service) ClaimDeliveries(ctx context.Context, claims RuntimeClaims, limit int) ([]domain.ChannelDelivery, error) {
	if !claims.Scopes["channel.delivery.claim"] {
		return nil, fmt.Errorf("capability_scope_denied")
	}
	items, err := s.store.ClaimChannelDeliveries(ctx, claims.InstanceID, limit, s.now().UTC(), 30*time.Second)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].Target, _ = s.store.ChannelDeliveryTarget(ctx, items[index].ConversationID, items[index].SourceEventSequence)
		if items[index].SemanticPayload["kind"] == "action_result" {
			actionID := strings.TrimSpace(fmt.Sprint(items[index].SemanticPayload["action_id"]))
			if messageID, messageErr := s.store.ChannelActionProviderMessage(ctx, actionID, claims.InstanceID); messageErr == nil && messageID != "" {
				items[index].Target["update_message_id"] = messageID
				delete(items[index].Target, "reply_message_id")
			}
		}
	}
	return items, nil
}

func (s *Service) AckDelivery(ctx context.Context, claims RuntimeClaims, id, leaseID, providerMessageID, providerRequestID string) error {
	if !claims.Scopes["channel.delivery.ack"] {
		return fmt.Errorf("capability_scope_denied")
	}
	return s.store.AckChannelDelivery(ctx, claims.InstanceID, id, leaseID, providerMessageID, providerRequestID, s.now().UTC())
}

func (s *Service) NackDelivery(ctx context.Context, claims RuntimeClaims, id, leaseID, errorCode, certainty string, retryAfter time.Duration, permanent bool) error {
	if !claims.Scopes["channel.delivery.ack"] {
		return fmt.Errorf("capability_scope_denied")
	}
	return s.store.NackChannelDelivery(ctx, claims.InstanceID, id, leaseID, errorCode, certainty, retryAfter, permanent, s.now().UTC())
}

func (s *Service) UpdateHealth(ctx context.Context, claims RuntimeClaims, status, errorCode string, metadata ...map[string]string) error {
	if !claims.Scopes["channel.health.write"] {
		return fmt.Errorf("capability_scope_denied")
	}
	switch status {
	case "ready", "degraded", "error":
	default:
		return fmt.Errorf("invalid_envelope")
	}
	if status == "ready" {
		s.processMu.Lock()
		delete(s.processRetry, claims.InstanceID)
		delete(s.processFailures, claims.InstanceID)
		s.processMu.Unlock()
	}
	return s.store.UpdateChannelInstanceHealth(ctx, claims.InstanceID, status, errorCode, s.now().UTC(), metadata...)
}

func containsSensitiveField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
			if strings.Contains(normalized, "secret") || strings.Contains(normalized, "token") || normalized == "authorization" || normalized == "device_code" || normalized == "app_secret" {
				return true
			}
			if containsSensitiveField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSensitiveField(child) {
				return true
			}
		}
	}
	return false
}

func (s *Service) ReceiveInbound(ctx context.Context, claims RuntimeClaims, envelope domain.ChannelInboundEnvelope) (domain.ChannelInboxReceipt, bool, error) {
	if !claims.Scopes["channel.inbound.write"] {
		return domain.ChannelInboxReceipt{}, false, fmt.Errorf("capability_scope_denied")
	}
	if envelope.SchemaVersion != 1 || envelope.InstanceID != claims.InstanceID || strings.TrimSpace(envelope.ExternalEventID) == "" || strings.TrimSpace(envelope.ExternalSenderID) == "" {
		return domain.ChannelInboxReceipt{}, false, fmt.Errorf("invalid_envelope")
	}
	if envelope.EventType != "message" && envelope.EventType != "card_action" && envelope.EventType != "menu_action" {
		return domain.ChannelInboxReceipt{}, false, fmt.Errorf("invalid_envelope")
	}
	if envelope.ChatType != "p2p" && envelope.ChatType != "group" {
		return domain.ChannelInboxReceipt{}, false, fmt.Errorf("invalid_envelope")
	}
	if (envelope.EventType != "menu_action" && strings.TrimSpace(envelope.ExternalChatID) == "") || len([]byte(envelope.Content)) > 256*1024 || containsSensitiveField(envelope.CardAction) || containsSensitiveField(envelope.MenuAction) {
		return domain.ChannelInboxReceipt{}, false, fmt.Errorf("invalid_envelope")
	}
	if envelope.EventType == "menu_action" && (envelope.ChatType != "p2p" || !validChannelMenuKey(stringField(envelope.MenuAction, "key"))) {
		return domain.ChannelInboxReceipt{}, false, fmt.Errorf("invalid_envelope")
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return domain.ChannelInboxReceipt{}, false, err
	}
	digest := sha256.Sum256(raw)
	normalized := map[string]any{}
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return domain.ChannelInboxReceipt{}, false, err
	}
	now := s.now().UTC()
	if envelope.OccurredAt.IsZero() {
		envelope.OccurredAt = now
		normalized["occurred_at"] = envelope.OccurredAt
	}
	receipt, duplicate, err := s.store.ReceiveChannelInbox(ctx, domain.ChannelInboxReceipt{
		ID: domain.NewID("channel_inbox"), InstanceID: claims.InstanceID, ExternalEventID: envelope.ExternalEventID,
		EventType: envelope.EventType, PayloadDigest: hex.EncodeToString(digest[:]), NormalizedPayload: normalized,
		State: "received", OccurredAt: envelope.OccurredAt, ReceivedAt: now,
	})
	if err == nil && !duplicate {
		select {
		case s.wakeInbound <- struct{}{}:
		default:
		}
	}
	return receipt, duplicate, err
}

func (s *Service) processInboundBatch(ctx context.Context) error {
	if s.application == nil {
		return nil
	}
	items, err := s.store.ClaimChannelInbox(ctx, 20, s.now().UTC(), time.Minute)
	if err != nil {
		return err
	}
	for _, receipt := range items {
		if err := s.processInbound(ctx, receipt); err != nil {
			s.logger.Warn("channel inbound receipt failed", "receipt_id", receipt.ID, "error", err)
			_ = s.store.FinishChannelInbox(ctx, receipt.ID, receipt.LeaseID, "failed", "", "", safeOutcome(err), s.now().UTC())
		}
	}
	return nil
}

func safeOutcome(err error) string {
	value := err.Error()
	if strings.Contains(value, "owner") {
		return "owner_required"
	}
	if strings.Contains(value, "mention") {
		return "mention_required"
	}
	if strings.Contains(value, "runtime") {
		return "runtime_unavailable"
	}
	return "processing_failed"
}

func (s *Service) processInbound(ctx context.Context, receipt domain.ChannelInboxReceipt) error {
	raw, err := json.Marshal(receipt.NormalizedPayload)
	if err != nil {
		return err
	}
	var envelope domain.ChannelInboundEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	instance, err := s.store.ChannelInstance(ctx, receipt.InstanceID)
	if err != nil || (instance.Status != "ready" && instance.Status != "degraded") {
		return fmt.Errorf("channel runtime is not ready")
	}
	if envelope.EventType == "card_action" {
		return s.processCardAction(ctx, receipt, instance, envelope)
	}
	if envelope.EventType == "menu_action" {
		return s.processMenuAction(ctx, receipt, instance, envelope)
	}
	endpointKind := domain.ChannelEndpointGroupDigitalHuman
	role := "participant"
	var identity domain.ChannelIdentityLink
	if envelope.ChatType == "p2p" {
		if strings.TrimSpace(envelope.SenderDisplayName) != "" {
			envelope.SenderDisplayName = safeChannelDisplayName(envelope.SenderDisplayName, "Owner", instance.ID, envelope.ExternalSenderID)
		}
		endpointKind = domain.ChannelEndpointAssistantDM
		role = "owner"
		identity, err = s.store.ChannelOwnerIdentity(ctx, instance.ID)
		if err != nil || identity.ExternalUserID != envelope.ExternalSenderID || identity.OwnerID != instance.OwnerID {
			return fmt.Errorf("channel owner identity is required")
		}
		if identity.DisplayName == "" {
			identity.DisplayName = envelope.SenderDisplayName
			if identity.DisplayName == "" {
				if owner, ownerErr := s.store.OwnerByID(ctx, instance.OwnerID); ownerErr == nil {
					identity.DisplayName = owner.Username
				}
			}
		}
	} else {
		if !envelope.MentionedBot {
			return fmt.Errorf("group mention is required")
		}
		envelope.ChatDisplayName = safeChannelDisplayName(envelope.ChatDisplayName, "群聊", instance.ID, envelope.ExternalChatID)
		senderDisplayName := strings.TrimSpace(envelope.SenderDisplayName)
		if senderDisplayName != "" {
			senderDisplayName = safeChannelDisplayName(senderDisplayName, "群成员", instance.ID, envelope.ExternalSenderID)
		}
		envelope.SenderDisplayName = safeChannelDisplayName(envelope.SenderDisplayName, "群成员", instance.ID, envelope.ExternalSenderID)
		identity, err = s.store.UpsertObservedChannelParticipant(ctx, domain.ChannelIdentityLink{
			ID: domain.NewID("channel_identity"), InstanceID: instance.ID, ExternalUserID: envelope.ExternalSenderID,
			Role: "participant", DisplayName: senderDisplayName, Status: "active", LinkedAt: s.now().UTC(),
		}, envelope.SenderDisplayName)
		if err != nil {
			return err
		}
		envelope.SenderDisplayName = identity.DisplayName
	}
	endpoint, err := s.store.ChannelEndpoint(ctx, instance.ID, endpointKind)
	if err != nil || !endpoint.Enabled {
		return fmt.Errorf("channel endpoint is unavailable")
	}
	scopeVersion := 1
	if endpointKind == domain.ChannelEndpointGroupDigitalHuman {
		scopeVersion = 2
	}
	scopeKey, err := s.scopeKey(instance.ID, endpointKind, identity.ID, envelope.ExternalChatID, envelope.ExternalSenderID)
	if err != nil {
		return err
	}
	conversation, err := s.ensureConversation(ctx, instance, endpoint, identity, envelope, scopeVersion, scopeKey)
	if err != nil {
		return err
	}
	if endpointKind == domain.ChannelEndpointGroupDigitalHuman {
		_ = s.store.UpsertChannelConversationMember(ctx, conversation.ID, identity.ID, envelope.SenderIsBot, envelope.OccurredAt)
	}
	targetTaskID, routeMode := conversation.HostTaskID, "assistant"
	if route, routeErr := s.store.ActiveChannelTaskRoute(ctx, conversation.ID); routeErr == nil {
		targetTaskID, routeMode = route.TargetTaskID, "task_route"
	} else if endpointKind == domain.ChannelEndpointGroupDigitalHuman {
		routeMode = "group_qa"
	}
	mentions := make([]map[string]any, 0, len(envelope.Mentions))
	for _, mention := range envelope.Mentions {
		if strings.TrimSpace(mention.ExternalUserID) == "" {
			continue
		}
		fallback := "群成员"
		if mention.IsBot {
			fallback = "群机器人"
		}
		mentionDisplayName := strings.TrimSpace(mention.DisplayName)
		if mention.ExternalUserID == stringField(instance.Config, "runtime_bot_open_id") {
			mentionDisplayName = domain.ChannelBotDisplayName(instance.Config, instance.Name)
		}
		if mentionDisplayName != "" {
			mentionDisplayName = safeChannelDisplayName(mentionDisplayName, fallback, instance.ID, mention.ExternalUserID)
		}
		linked, linkErr := s.store.UpsertObservedChannelParticipant(ctx, domain.ChannelIdentityLink{
			ID: domain.NewID("channel_identity"), InstanceID: instance.ID, ExternalUserID: mention.ExternalUserID,
			Role: "participant", DisplayName: mentionDisplayName, Status: "active", LinkedAt: s.now().UTC(),
		}, safeChannelDisplayName("", fallback, instance.ID, mention.ExternalUserID))
		if linkErr == nil {
			_ = s.store.UpsertChannelConversationMember(ctx, conversation.ID, linked.ID, mention.IsBot, envelope.OccurredAt)
			mentions = append(mentions, map[string]any{"identity_link_id": linked.ID, "display_name": linked.DisplayName, "is_bot": mention.IsBot})
		}
	}
	botDialogueTurns := 0
	if endpointKind == domain.ChannelEndpointGroupDigitalHuman && envelope.SenderIsBot {
		botDialogueTurns, _ = s.store.ChannelBotDialogueTurnCount(ctx, targetTaskID, conversation.ID)
		botDialogueTurns++
	}
	provenance := map[string]any{
		"schema": "aha.channel-context/v1", "instance_id": instance.ID, "provider": instance.ProviderKey,
		"endpoint": endpointKind, "conversation_id": conversation.ID, "chat_display_name": conversation.DisplayName,
		"actor":    map[string]any{"identity_link_id": identity.ID, "role": role, "display_name": identity.DisplayName, "is_bot": envelope.SenderIsBot},
		"mentions": mentions,
		"route":    map[string]any{"mode": routeMode, "target_task_id": targetTaskID}, "inbound_receipt_id": receipt.ID,
	}
	if endpointKind == domain.ChannelEndpointGroupDigitalHuman && envelope.SenderIsBot {
		provenance["bot_dialogue"] = map[string]any{
			"active": true, "turn": botDialogueTurns,
			"max_turns": domain.ChannelBotDialogueMaxTurns(instance.Config),
		}
	}
	attachmentIDs, warning, pending, err := s.inboundAttachments(ctx, receipt, conversation, targetTaskID, envelope)
	if errors.Is(err, errMediaRouteChanged) {
		if noticeErr := s.store.EnqueueChannelControlDelivery(ctx, instance.ID, conversation.ID, receipt.ID+":media-route-changed", "media_notice", map[string]any{"text": "接管目标已变化，本条消息及附件未转发，请在当前会话重新发送。"}, s.now().UTC()); noticeErr != nil {
			return noticeErr
		}
		return s.store.FinishChannelInbox(ctx, receipt.ID, receipt.LeaseID, "processed", conversation.ID, "", "media_route_changed", s.now().UTC())
	}
	if err != nil {
		return err
	}
	if pending {
		return s.store.DeferChannelInbox(ctx, receipt.ID, receipt.LeaseID, conversation.ID, s.now().UTC().Add(2*time.Second))
	}
	if warning != "" {
		envelope.Content = strings.TrimSpace(envelope.Content + "\n[渠道附件提示] " + warning)
	}
	_, err = s.application.SubmitChannelMessageWithAttachments(ctx, receipt.ID, receipt.LeaseID, conversation.ID, targetTaskID, envelope.Content, provenance, attachmentIDs)
	return err
}

func validChannelMenuKey(key string) bool {
	switch key {
	case "aha.project.query", "aha.workspace.query", "aha.task.query", "aha.task.create":
		return true
	default:
		return false
	}
}

func (s *Service) processMenuAction(ctx context.Context, receipt domain.ChannelInboxReceipt, instance domain.ChannelInstance, envelope domain.ChannelInboundEnvelope) error {
	identity, err := s.store.ChannelOwnerIdentity(ctx, instance.ID)
	if err != nil || identity.ExternalUserID != envelope.ExternalSenderID || identity.OwnerID != instance.OwnerID {
		return fmt.Errorf("channel owner identity is required")
	}
	conversation, err := s.ensureOwnerMenuConversation(ctx, instance, identity, envelope)
	if err != nil {
		return err
	}
	key := stringField(envelope.MenuAction, "key")
	catalog, err := s.allowedChannelCatalog(ctx, instance)
	if err != nil {
		return err
	}
	var payload map[string]any
	switch key {
	case "aha.project.query":
		payload = projectMenuPayload(catalog.projects)
	case "aha.workspace.query":
		payload = workspaceQueryFormPayload(catalog.projects)
	case "aha.task.query":
		payload = taskQueryFormPayload(catalog.projects)
		if actions := s.activeRouteMenuActions(ctx, conversation); len(actions) > 0 {
			payload["actions"] = actions
		}
	case "aha.task.create":
		payload = taskCreateProjectFormPayload(catalog.projects)
	default:
		return fmt.Errorf("unsupported menu action")
	}
	if err := s.store.EnqueueChannelControlDelivery(ctx, instance.ID, conversation.ID, receipt.ID+":menu", "menu_result", payload, s.now().UTC()); err != nil {
		return err
	}
	return s.store.FinishChannelInbox(ctx, receipt.ID, receipt.LeaseID, "processed", conversation.ID, "", "menu_control", s.now().UTC())
}

func (s *Service) ensureOwnerMenuConversation(ctx context.Context, instance domain.ChannelInstance, identity domain.ChannelIdentityLink, envelope domain.ChannelInboundEnvelope) (domain.ChannelConversation, error) {
	conversation, err := s.store.OwnerChannelConversation(ctx, instance.ID)
	if err == nil && conversation.Status == "active" {
		return conversation, nil
	}
	endpoint, endpointErr := s.store.ChannelEndpoint(ctx, instance.ID, domain.ChannelEndpointAssistantDM)
	if endpointErr != nil || !endpoint.Enabled {
		return domain.ChannelConversation{}, fmt.Errorf("channel owner conversation is required")
	}
	menuEnvelope := envelope
	menuEnvelope.ExternalChatID = "open_id:" + envelope.ExternalSenderID
	scopeKey, scopeErr := s.scopeKey(instance.ID, endpoint.Kind, identity.ID, menuEnvelope.ExternalChatID, envelope.ExternalSenderID)
	if scopeErr != nil {
		return domain.ChannelConversation{}, scopeErr
	}
	return s.ensureConversation(ctx, instance, endpoint, identity, menuEnvelope, 1, scopeKey)
}

type channelCatalog struct {
	projects   []domain.Project
	workspaces []domain.Workspace
	tasks      []domain.Task
}

func (s *Service) allowedChannelCatalog(ctx context.Context, instance domain.ChannelInstance) (channelCatalog, error) {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return channelCatalog{}, err
	}
	workspaces, err := s.store.ListWorkspaces(ctx, "")
	if err != nil {
		return channelCatalog{}, err
	}
	tasks, err := s.store.ListTasks(ctx, "")
	if err != nil {
		return channelCatalog{}, err
	}
	allowedProjects, _ := stringListField(instance.Config, "allowed_project_ids")
	allowedWorkspaces, _ := stringListField(instance.Config, "allowed_workspace_ids")
	projectsRestricted, workspacesRestricted := channelOperationRestrictions(instance.Config)
	projectSet, workspaceSet := sliceSet(allowedProjects), sliceSet(allowedWorkspaces)
	result := channelCatalog{}
	for _, project := range projects {
		if !s.store.IsManagedChannelProject(ctx, project.ID) && project.ProjectType != "knowledge" && (!projectsRestricted || projectSet[project.ID]) {
			result.projects = append(result.projects, project)
		}
	}
	for _, workspace := range workspaces {
		if !workspace.ReadOnly && !s.store.IsManagedChannelWorkspace(ctx, workspace.ID) && (!projectsRestricted || projectSet[workspace.ProjectID]) && (!workspacesRestricted || workspaceSet[workspace.ID]) {
			result.workspaces = append(result.workspaces, workspace)
		}
	}
	for _, task := range tasks {
		if !task.ReadOnly && !s.store.IsManagedChannelTask(ctx, task.ID) && (!projectsRestricted || projectSet[task.ProjectID]) && (!workspacesRestricted || workspaceSet[task.WorkspaceID]) {
			result.tasks = append(result.tasks, task)
		}
	}
	return result, nil
}

func projectOptions(projects []domain.Project) []map[string]any {
	options := make([]map[string]any, 0, len(projects))
	for index, project := range projects {
		if index >= 100 {
			break
		}
		options = append(options, map[string]any{"label": project.Name, "value": project.ID})
	}
	return options
}

func workspaceOptions(projects []domain.Project, workspaces []domain.Workspace) []map[string]any {
	projectNames := map[string]string{}
	for _, project := range projects {
		projectNames[project.ID] = project.Name
	}
	options := make([]map[string]any, 0, len(workspaces))
	for _, workspace := range workspaces {
		if len(options) >= 100 {
			break
		}
		options = append(options, map[string]any{"label": projectNames[workspace.ProjectID] + " / " + workspace.Name, "value": workspace.ID})
	}
	return options
}

func projectMenuPayload(projects []domain.Project) map[string]any {
	lines := []string{"AHA 直接查询，未调用 Agent。"}
	for index, project := range projects {
		if index >= 20 {
			lines = append(lines, "…仅展示前 20 个项目")
			break
		}
		lines = append(lines, fmt.Sprintf("**%d. %s**", index+1, menuSafeText(project.Name)))
	}
	if len(projects) == 0 {
		lines = append(lines, "当前 allowlist 内没有可用项目。")
	}
	return map[string]any{"kind": "menu_card", "title": "查询项目", "template": "blue", "markdown": strings.Join(lines, "\n")}
}

func workspaceQueryFormPayload(projects []domain.Project) map[string]any {
	return map[string]any{
		"kind": "menu_card", "title": "查询 Workspace", "template": "blue", "markdown": "请先选择项目。查询由 AHA 直接执行，不调用 Agent。",
		"fields": []map[string]any{{"type": "select", "name": "project_id", "label": "Project", "options": projectOptions(projects)}},
		"submit": map[string]any{"label": "查询", "value": map[string]any{"kind": "menu_control", "menu_action": "workspace.query"}},
	}
}

func taskQueryFormPayload(projects []domain.Project) map[string]any {
	statuses := []map[string]any{{"label": "全部", "value": "all"}, {"label": "进行中", "value": "active"}, {"label": "等待处理", "value": "waiting_user"}, {"label": "已完成", "value": "completed"}, {"label": "失败", "value": "failed"}, {"label": "阻塞", "value": "blocked"}}
	return map[string]any{
		"kind": "menu_card", "title": "查询 Task", "template": "blue", "markdown": "请选择筛选条件。查询由 AHA 直接执行，不调用 Agent。",
		"fields": []map[string]any{
			{"type": "select", "name": "project_id", "label": "Project", "options": projectOptions(projects)},
			{"type": "select", "name": "status", "label": "状态", "options": statuses},
			{"type": "text", "name": "keyword", "label": "关键词（可选）", "max_length": 100},
		},
		"submit": map[string]any{"label": "查询", "value": map[string]any{"kind": "menu_control", "menu_action": "task.query"}},
	}
}

func taskCreateProjectFormPayload(projects []domain.Project) map[string]any {
	return map[string]any{
		"kind": "menu_card", "title": "创建 Task · 选择 Project", "template": "blue", "markdown": "先选择 Project，下一步只会显示该 Project 下可用的 Workspace。",
		"fields": []map[string]any{{"type": "select", "name": "project_id", "label": "Project", "options": projectOptions(projects)}},
		"submit": map[string]any{"label": "下一步", "value": map[string]any{"kind": "menu_control", "menu_action": "task.create.workspaces"}},
	}
}

func taskCreateDetailsFormPayload(project domain.Project, workspaces []domain.Workspace) map[string]any {
	options := []map[string]any{}
	for _, workspace := range workspaces {
		if workspace.ProjectID == project.ID {
			options = append(options, map[string]any{"label": workspace.Name, "value": workspace.ID})
		}
	}
	if len(options) == 0 {
		return menuValidationErrorPayload("该 Project 当前没有可用 Workspace，请先在 AHA 中配置 Workspace。")
	}
	return map[string]any{
		"kind": "menu_card", "title": "创建 Task · " + project.Name, "template": "blue", "markdown": "请选择 Workspace 并填写任务内容。Runtime 默认继承渠道设置；提交后仍需一次确认。",
		"fields": []map[string]any{
			{"type": "select", "name": "workspace_id", "label": "Workspace", "options": options},
			{"type": "text", "name": "title", "label": "标题", "max_length": 200},
			{"type": "multiline", "name": "request", "label": "需求", "max_length": 1000},
		},
		"submit": map[string]any{"label": "生成预览", "value": map[string]any{"kind": "menu_control", "menu_action": "task.create.preview", "project_id": project.ID}},
	}
}

func menuSafeText(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > 120 {
		value = string([]rune(value)[:120]) + "…"
	}
	return strings.NewReplacer("\\", "\\\\", "*", "\\*", "`", "'", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;").Replace(value)
}

func (s *Service) channelAgentContext(ctx context.Context, claims agentapi.Claims, endpointRequired string) (app.AgentCallContext, map[string]any, domain.ChannelIdentityLink, error) {
	call, err := s.application.AgentCallContext(ctx, claims, true)
	if err != nil {
		return app.AgentCallContext{}, nil, domain.ChannelIdentityLink{}, err
	}
	channelContext, err := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID)
	if err != nil || strings.TrimSpace(fmt.Sprint(channelContext["endpoint"])) != endpointRequired {
		return app.AgentCallContext{}, nil, domain.ChannelIdentityLink{}, app.ErrAgentCallForbidden
	}
	instanceID := strings.TrimSpace(fmt.Sprint(channelContext["instance_id"]))
	identity, err := s.store.ChannelOwnerIdentity(ctx, instanceID)
	if err != nil && endpointRequired == domain.ChannelEndpointAssistantDM {
		return app.AgentCallContext{}, nil, domain.ChannelIdentityLink{}, app.ErrAgentCallForbidden
	}
	if endpointRequired == domain.ChannelEndpointAssistantDM {
		actor, _ := channelContext["actor"].(map[string]any)
		if strings.TrimSpace(fmt.Sprint(actor["identity_link_id"])) != identity.ID || strings.TrimSpace(fmt.Sprint(actor["role"])) != "owner" {
			return app.AgentCallContext{}, nil, domain.ChannelIdentityLink{}, app.ErrAgentCallForbidden
		}
	}
	return call, channelContext, identity, nil
}

func (s *Service) AgentCatalog(ctx context.Context, claims agentapi.Claims) (map[string]any, error) {
	_, channelContext, _, err := s.channelAgentContext(ctx, claims, domain.ChannelEndpointAssistantDM)
	if err != nil {
		return nil, err
	}
	instanceID := stringField(channelContext, "instance_id")
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	catalog, err := s.allowedChannelCatalog(ctx, instance)
	if err != nil {
		return nil, err
	}
	handoffs, _ := s.store.ChannelHandoffs(ctx, strings.TrimSpace(fmt.Sprint(channelContext["instance_id"])))
	return map[string]any{"projects": catalog.projects, "workspaces": catalog.workspaces, "tasks": catalog.tasks, "handoffs": handoffs}, nil
}

func (s *Service) CreateAgentHandoff(ctx context.Context, claims agentapi.Claims, summary, details string) (domain.ChannelHandoff, error) {
	_, channelContext, _, err := s.channelAgentContext(ctx, claims, domain.ChannelEndpointGroupDigitalHuman)
	if err != nil {
		return domain.ChannelHandoff{}, err
	}
	summary, details = strings.TrimSpace(summary), strings.TrimSpace(details)
	if summary == "" {
		return domain.ChannelHandoff{}, fmt.Errorf("handoff summary is required")
	}
	instanceID := strings.TrimSpace(fmt.Sprint(channelContext["instance_id"]))
	conversationID := strings.TrimSpace(fmt.Sprint(channelContext["conversation_id"]))
	receiptID := strings.TrimSpace(fmt.Sprint(channelContext["inbound_receipt_id"]))
	actor, _ := channelContext["actor"].(map[string]any)
	identityID := strings.TrimSpace(fmt.Sprint(actor["identity_link_id"]))
	if receiptID == "" || identityID == "" {
		return domain.ChannelHandoff{}, app.ErrAgentCallForbidden
	}
	ownerConversation, _ := s.store.OwnerChannelConversation(ctx, instanceID)
	now := s.now().UTC()
	return s.store.CreateChannelHandoff(ctx, domain.ChannelHandoff{
		ID: stableID("channel_handoff", instanceID, receiptID), InstanceID: instanceID, OriginConversationID: conversationID,
		OriginInboxID: receiptID, RequesterIdentityLinkID: identityID, OwnerConversationID: ownerConversation.ID,
		Summary: summary, Details: details, Source: map[string]any{"origin": "group_digital_human"}, State: "pending_owner", CreatedAt: now, UpdatedAt: now,
	})
}

func (s *Service) AgentChannelContext(ctx context.Context, claims agentapi.Claims) (map[string]any, error) {
	call, err := s.application.AgentCallContext(ctx, claims, true)
	if err != nil {
		return nil, err
	}
	value, err := s.store.ChannelContextForInboxBatch(ctx, call.Turn.InboxBatchID)
	if err != nil {
		return nil, app.ErrAgentCallForbidden
	}
	return value, nil
}

func (s *Service) AgentTaskChannelContacts(ctx context.Context, claims agentapi.Claims) (domain.ChannelDestination, []domain.ChannelContact, error) {
	call, err := s.application.AgentCallContext(ctx, claims, true)
	if err != nil {
		return domain.ChannelDestination{}, nil, err
	}
	route, err := s.store.ActiveChannelTaskRouteForTask(ctx, call.Task.ID)
	if err != nil {
		return domain.ChannelDestination{}, nil, app.ErrAgentCallForbidden
	}
	conversation, err := s.store.ChannelConversation(ctx, route.ConversationID)
	if err != nil || conversation.Status != "active" {
		return domain.ChannelDestination{}, nil, app.ErrAgentCallForbidden
	}
	endpoint, err := s.store.ChannelEndpoint(ctx, route.InstanceID, domain.ChannelEndpointGroupDigitalHuman)
	if err != nil || endpoint.ID != conversation.EndpointID || !endpoint.Enabled {
		return domain.ChannelDestination{}, nil, app.ErrAgentCallForbidden
	}
	instance, err := s.store.ChannelInstance(ctx, route.InstanceID)
	if err != nil || (instance.Status != "ready" && instance.Status != "degraded") {
		return domain.ChannelDestination{}, nil, app.ErrAgentCallForbidden
	}
	contacts, err := s.store.TaskChannelContacts(ctx, call.Task.ID, route.InstanceID, route.ConversationID, 50)
	if err != nil {
		return domain.ChannelDestination{}, nil, err
	}
	return domain.ChannelDestination{
		ConversationID: conversation.ID, InstanceID: instance.ID, InstanceName: instance.Name,
		EndpointKind: endpoint.Kind, DisplayName: conversation.DisplayName, Status: conversation.Status,
		RouteID: route.ID, RouteRevision: route.Revision, TargetTaskID: route.TargetTaskID, UpdatedAt: conversation.UpdatedAt,
	}, contacts, nil
}

func (s *Service) SendAgentTaskChannelMessage(ctx context.Context, claims agentapi.Claims, input AgentOutreachInput) (domain.ChannelDelivery, error) {
	_, contacts, err := s.AgentTaskChannelContacts(ctx, claims)
	if err != nil {
		return domain.ChannelDelivery{}, err
	}
	requestID := strings.TrimSpace(input.RequestID)
	purpose := strings.TrimSpace(input.Purpose)
	message := strings.TrimSpace(input.Message)
	if requestID == "" || len(requestID) > 120 || strings.ContainsAny(requestID, " \t\r\n/\\") {
		return domain.ChannelDelivery{}, fmt.Errorf("request_id must be a stable token up to 120 characters")
	}
	if purpose != "blocker" {
		return domain.ChannelDelivery{}, fmt.Errorf("purpose must be blocker")
	}
	if message == "" || len([]rune(message)) > 1000 {
		return domain.ChannelDelivery{}, fmt.Errorf("message must contain 1 to 1000 characters")
	}
	if len(input.MentionIdentityLinkIDs) < 1 || len(input.MentionIdentityLinkIDs) > 5 {
		return domain.ChannelDelivery{}, fmt.Errorf("choose between 1 and 5 channel contacts")
	}
	available := map[string]bool{}
	for _, contact := range contacts {
		available[contact.IdentityLinkID] = true
	}
	identityIDs := make([]string, 0, len(input.MentionIdentityLinkIDs))
	seen := map[string]bool{}
	for _, identityID := range input.MentionIdentityLinkIDs {
		identityID = strings.TrimSpace(identityID)
		if identityID == "" || seen[identityID] || !available[identityID] {
			return domain.ChannelDelivery{}, fmt.Errorf("channel contact is not available in the primary channel")
		}
		seen[identityID] = true
		identityIDs = append(identityIDs, identityID)
	}
	delivery, conversationItemID, created, err := s.store.EnqueueTaskChannelOutreach(
		ctx, claims.TaskID, claims.TurnID, requestID, purpose, message, identityIDs, input.AttachmentIDs, s.now().UTC(),
	)
	if err == nil && created && s.application != nil {
		s.application.PublishAgentChannelOutreach(ctx, claims.TaskID, claims.TurnID, conversationItemID, len(identityIDs))
	}
	return delivery, err
}

func (s *Service) PreviewAgentAction(ctx context.Context, claims agentapi.Claims, input ActionPreviewInput) (domain.ChannelPendingAction, error) {
	_, channelContext, identity, err := s.channelAgentContext(ctx, claims, domain.ChannelEndpointAssistantDM)
	if err != nil {
		return domain.ChannelPendingAction{}, err
	}
	instanceID := strings.TrimSpace(fmt.Sprint(channelContext["instance_id"]))
	conversationID := strings.TrimSpace(fmt.Sprint(channelContext["conversation_id"]))
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil {
		return domain.ChannelPendingAction{}, err
	}
	operation := strings.TrimSpace(input.Operation)
	precondition, preview := map[string]any{}, map[string]any{"operation": operation}
	targetType, targetID := "", strings.TrimSpace(input.TargetID)
	switch operation {
	case "takeover":
		task, err := s.store.Task(ctx, targetID)
		if err != nil || task.ReadOnly || s.store.IsManagedChannelTask(ctx, targetID) || !taskRouteEligible(task.Status) || !channelOperationAllowed(instance.Config, task.ProjectID, task.WorkspaceID) {
			return domain.ChannelPendingAction{}, fmt.Errorf("target task is not eligible for takeover")
		}
		targetType = "task"
		precondition = map[string]any{"task_id": task.ID, "status": task.Status, "updated_at": timeStringUTC(task.UpdatedAt)}
		preview = map[string]any{"operation": operation, "task_id": task.ID, "task_code": task.Code, "title": task.Title, "effect": "连接为 Task 主渠道；已有主渠道会被替换，不迁移或改变 Task"}
	case "exit":
		route, err := s.store.ActiveChannelTaskRoute(ctx, conversationID)
		if err != nil {
			return domain.ChannelPendingAction{}, fmt.Errorf("no active task route")
		}
		targetType, targetID = "task_route", route.TargetTaskID
		precondition = map[string]any{"route_id": route.ID, "route_revision": route.Revision, "task_id": route.TargetTaskID}
		preview = map[string]any{"operation": operation, "task_id": route.TargetTaskID, "effect": "仅解绑路由，不完成或中断 Task"}
	case "create_task":
		targetType = "task"
		projectID, workspaceID := stringField(input.Intent, "project_id"), stringField(input.Intent, "workspace_id")
		project, projectErr := s.store.Project(ctx, projectID)
		workspace, workspaceErr := s.store.Workspace(ctx, workspaceID)
		if projectErr != nil || workspaceErr != nil || workspace.ProjectID != project.ID || workspace.ReadOnly || s.store.IsManagedChannelProject(ctx, project.ID) || !channelOperationAllowed(instance.Config, project.ID, workspace.ID) {
			return domain.ChannelPendingAction{}, fmt.Errorf("task project or workspace is not eligible")
		}
		if stringField(input.Intent, "title") == "" || stringField(input.Intent, "request") == "" {
			return domain.ChannelPendingAction{}, fmt.Errorf("task title and request are required")
		}
		precondition = map[string]any{"project_id": project.ID, "project_updated_at": timeStringUTC(project.UpdatedAt), "workspace_id": workspace.ID, "workspace_updated_at": timeStringUTC(workspace.UpdatedAt)}
		preview = map[string]any{"operation": operation, "project": project.Name, "workspace": workspace.Name, "title": stringField(input.Intent, "title"), "request": stringField(input.Intent, "request")}
	case "status_change":
		task, err := s.store.Task(ctx, targetID)
		action := stringField(input.Intent, "action")
		if err != nil || task.ReadOnly || s.store.IsManagedChannelTask(ctx, task.ID) || !channelOperationAllowed(instance.Config, task.ProjectID, task.WorkspaceID) || (action != "complete" && action != "reopen" && action != "interrupt") {
			return domain.ChannelPendingAction{}, fmt.Errorf("task status change is not eligible")
		}
		targetType = "task"
		precondition = map[string]any{"task_id": task.ID, "status": task.Status, "updated_at": timeStringUTC(task.UpdatedAt), "action": action}
		preview = map[string]any{"operation": operation, "action": action, "task_id": task.ID, "task_code": task.Code, "title": task.Title, "current_status": task.Status}
	case "handoff_decision":
		handoff, err := s.store.ChannelHandoff(ctx, targetID)
		decision := stringField(input.Intent, "decision")
		if err != nil || handoff.InstanceID != instanceID || (handoff.State != "pending_owner" && handoff.State != "accepted_todo") || (decision != "accepted_todo" && decision != "accepted_task" && decision != "dismissed") {
			return domain.ChannelPendingAction{}, fmt.Errorf("handoff decision is not eligible")
		}
		targetType = "handoff"
		precondition = map[string]any{"handoff_id": handoff.ID, "state": handoff.State, "updated_at": timeStringUTC(handoff.UpdatedAt), "decision": decision}
		preview = map[string]any{"operation": operation, "decision": decision, "summary": handoff.Summary, "details": handoff.Details}
		if decision == "accepted_task" {
			if stringField(input.Intent, "title") == "" {
				input.Intent["title"] = handoff.Summary
			}
			if stringField(input.Intent, "request") == "" {
				input.Intent["request"] = handoff.Details
			}
			project, projectErr := s.store.Project(ctx, stringField(input.Intent, "project_id"))
			workspace, workspaceErr := s.store.Workspace(ctx, stringField(input.Intent, "workspace_id"))
			if projectErr != nil || workspaceErr != nil || workspace.ProjectID != project.ID || workspace.ReadOnly || s.store.IsManagedChannelProject(ctx, project.ID) || !channelOperationAllowed(instance.Config, project.ID, workspace.ID) || stringField(input.Intent, "request") == "" {
				return domain.ChannelPendingAction{}, fmt.Errorf("handoff task target is not eligible")
			}
			precondition["project_updated_at"], precondition["workspace_updated_at"] = timeStringUTC(project.UpdatedAt), timeStringUTC(workspace.UpdatedAt)
		}
	default:
		return domain.ChannelPendingAction{}, fmt.Errorf("unsupported channel action")
	}
	raw, _ := json.Marshal(precondition)
	digest := sha256.Sum256(raw)
	now := s.now().UTC()
	return s.store.CreateChannelPendingAction(ctx, domain.ChannelPendingAction{
		ID: domain.NewID("channel_action"), InstanceID: instanceID, ConversationID: conversationID, ActorIdentityLinkID: identity.ID,
		Operation: operation, TargetType: targetType, TargetID: targetID, Intent: input.Intent, Preview: preview,
		Precondition: precondition, PreconditionHash: hex.EncodeToString(digest[:]), Status: "pending", ExpiresAt: now.Add(15 * time.Minute), CreatedAt: now, UpdatedAt: now,
	})
}

func channelOperationAllowed(config map[string]any, projectID, workspaceID string) bool {
	projects, _ := stringListField(config, "allowed_project_ids")
	workspaces, _ := stringListField(config, "allowed_workspace_ids")
	projectsRestricted, workspacesRestricted := channelOperationRestrictions(config)
	return (!projectsRestricted || sliceSet(projects)[projectID]) && (!workspacesRestricted || sliceSet(workspaces)[workspaceID])
}

func channelOperationRestrictions(config map[string]any) (bool, bool) {
	switch stringField(config, "operation_scope_mode") {
	case "all":
		return false, false
	case "selected":
		return true, true
	default:
		_, projectsSet := stringListField(config, "allowed_project_ids")
		_, workspacesSet := stringListField(config, "allowed_workspace_ids")
		return projectsSet, workspacesSet
	}
}

func stringField(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func (s *Service) processCardAction(ctx context.Context, receipt domain.ChannelInboxReceipt, instance domain.ChannelInstance, envelope domain.ChannelInboundEnvelope) error {
	if stringField(envelope.CardAction, "kind") == "menu_control" {
		return s.processMenuCardAction(ctx, receipt, instance, envelope)
	}
	actionID := stringField(envelope.CardAction, "action_id")
	decision := stringField(envelope.CardAction, "decision")
	providerMessageID := stringField(envelope.CardAction, "provider_message_id")
	if actionID == "" || providerMessageID == "" || (decision != "confirm" && decision != "cancel") {
		return fmt.Errorf("invalid card action")
	}
	identity, err := s.store.ChannelOwnerIdentity(ctx, instance.ID)
	if err != nil || identity.ExternalUserID != envelope.ExternalSenderID || identity.OwnerID != instance.OwnerID {
		return fmt.Errorf("channel owner identity is required")
	}
	action, err := s.store.ChannelPendingAction(ctx, actionID)
	if err != nil || action.InstanceID != instance.ID || action.ActorIdentityLinkID != identity.ID {
		return fmt.Errorf("pending action is not available")
	}
	conversation, err := s.store.ChannelConversation(ctx, action.ConversationID)
	if err != nil || (conversation.ExternalChatID != envelope.ExternalChatID && conversation.ExternalChatID != "open_id:"+envelope.ExternalSenderID) {
		return fmt.Errorf("pending action conversation mismatch")
	}
	action, execute, err := s.store.BeginChannelPendingAction(ctx, action.ID, instance.ID, conversation.ID, identity.ID, providerMessageID, s.now().UTC())
	if err != nil {
		return err
	}
	if !execute {
		return s.store.FinishChannelInbox(ctx, receipt.ID, receipt.LeaseID, "processed", conversation.ID, "", "duplicate_action", s.now().UTC())
	}
	if decision == "cancel" {
		err = s.store.FinishChannelPendingAction(ctx, action.ID, action.TargetID, "cancelled", map[string]any{"cancelled": true}, s.now().UTC())
	} else {
		err = s.executePendingAction(ctx, action)
	}
	if err != nil {
		_ = s.store.FinishChannelPendingAction(ctx, action.ID, action.TargetID, "failed", map[string]any{"error_code": "precondition_or_execution_failed"}, s.now().UTC())
		return err
	}
	return s.store.FinishChannelInbox(ctx, receipt.ID, receipt.LeaseID, "processed", conversation.ID, "", "action_processed", s.now().UTC())
}

func (s *Service) processMenuCardAction(ctx context.Context, receipt domain.ChannelInboxReceipt, instance domain.ChannelInstance, envelope domain.ChannelInboundEnvelope) error {
	identity, err := s.store.ChannelOwnerIdentity(ctx, instance.ID)
	if err != nil || identity.ExternalUserID != envelope.ExternalSenderID || identity.OwnerID != instance.OwnerID {
		return fmt.Errorf("channel owner identity is required")
	}
	conversation, err := s.store.OwnerChannelConversation(ctx, instance.ID)
	if err != nil || conversation.Status != "active" || (conversation.ExternalChatID != envelope.ExternalChatID && conversation.ExternalChatID != "open_id:"+envelope.ExternalSenderID) {
		return fmt.Errorf("menu conversation mismatch")
	}
	catalog, err := s.allowedChannelCatalog(ctx, instance)
	if err != nil {
		return err
	}
	action := stringField(envelope.CardAction, "menu_action")
	values, _ := mapField(envelope.CardAction, "form_values")
	var payload map[string]any
	switch action {
	case "workspace.query":
		projectID := formString(values, "project_id")
		if !catalogHasProject(catalog, projectID) {
			return fmt.Errorf("menu project is not allowed")
		}
		lines := []string{"AHA 直接查询，未调用 Agent。"}
		count := 0
		for _, workspace := range catalog.workspaces {
			if workspace.ProjectID != projectID {
				continue
			}
			count++
			if count <= 20 {
				lines = append(lines, fmt.Sprintf("**%d. %s** · %s", count, menuSafeText(workspace.Name), menuSafeText(workspace.Health)))
			}
		}
		if count == 0 {
			lines = append(lines, "该项目下没有 allowlist 可用的 Workspace。")
		} else if count > 20 {
			lines = append(lines, "…仅展示前 20 个 Workspace")
		}
		payload = map[string]any{"kind": "menu_card", "title": "Workspace", "template": "blue", "markdown": strings.Join(lines, "\n")}
	case "task.query":
		projectID, status, keyword := formString(values, "project_id"), formString(values, "status"), strings.ToLower(formString(values, "keyword"))
		if !catalogHasProject(catalog, projectID) || !sliceSet([]string{"all", "active", "waiting_user", "completed", "failed", "blocked"})[status] {
			return fmt.Errorf("menu task filter is invalid")
		}
		lines := []string{"AHA 直接查询，未调用 Agent。"}
		count := 0
		actions := s.activeRouteMenuActions(ctx, conversation)
		for _, task := range catalog.tasks {
			if task.ProjectID != projectID || (status != "all" && string(task.Status) != status) {
				continue
			}
			haystack := strings.ToLower(task.Code + " " + task.Title + " " + task.CurrentGoal)
			if keyword != "" && !strings.Contains(haystack, keyword) {
				continue
			}
			count++
			if count <= 10 {
				lines = append(lines, fmt.Sprintf("**%s · %s**\n%s", menuSafeText(task.Code), menuSafeText(string(task.Status)), menuSafeText(task.Title)))
				if taskRouteEligible(task.Status) {
					actions = append(actions, map[string]any{"label": "接管 " + task.Code, "style": "default", "value": map[string]any{"kind": "menu_control", "menu_action": "task.takeover.preview", "task_id": task.ID}})
				}
			}
		}
		if count == 0 {
			lines = append(lines, "没有符合条件的 Task。")
		} else if count > 10 {
			lines = append(lines, "…仅展示前 10 个 Task")
		}
		payload = map[string]any{"kind": "menu_card", "title": "Task 查询结果", "template": "blue", "markdown": strings.Join(lines, "\n\n")}
		if len(actions) > 0 {
			payload["actions"] = actions
		}
	case "task.takeover.preview":
		taskID := stringField(envelope.CardAction, "task_id")
		var target domain.Task
		for _, task := range catalog.tasks {
			if task.ID == taskID {
				target = task
				break
			}
		}
		if target.ID == "" || !taskRouteEligible(target.Status) {
			return fmt.Errorf("menu takeover target is not eligible")
		}
		precondition := map[string]any{"task_id": target.ID, "status": target.Status, "updated_at": timeStringUTC(target.UpdatedAt)}
		preview := map[string]any{"operation": "takeover", "task_code": target.Code, "title": target.Title, "effect": "连接为 Task 主渠道；已有主渠道会被替换，不迁移或改变 Task"}
		if err := s.createMenuPendingAction(ctx, instance, conversation, identity, "takeover", "task", target.ID, map[string]any{}, preview, precondition); err != nil {
			return err
		}
		return s.store.FinishChannelInbox(ctx, receipt.ID, receipt.LeaseID, "processed", conversation.ID, "", "menu_takeover_preview", s.now().UTC())
	case "task.exit.preview":
		route, routeErr := s.store.ActiveChannelTaskRoute(ctx, conversation.ID)
		if routeErr != nil {
			return fmt.Errorf("no active task route")
		}
		precondition := map[string]any{"route_id": route.ID, "route_revision": route.Revision, "task_id": route.TargetTaskID}
		preview := map[string]any{"operation": "exit", "effect": "仅解绑当前 Task 路由，不完成或中断 Task"}
		if err := s.createMenuPendingAction(ctx, instance, conversation, identity, "exit", "task_route", route.TargetTaskID, map[string]any{}, preview, precondition); err != nil {
			return err
		}
		return s.store.FinishChannelInbox(ctx, receipt.ID, receipt.LeaseID, "processed", conversation.ID, "", "menu_exit_preview", s.now().UTC())
	case "task.create.workspaces":
		projectID := formString(values, "project_id")
		if !catalogHasProject(catalog, projectID) {
			payload = menuValidationErrorPayload("Project 不在当前渠道可操作范围内，请重新选择。")
			break
		}
		var project domain.Project
		for _, candidate := range catalog.projects {
			if candidate.ID == projectID {
				project = candidate
				break
			}
		}
		payload = taskCreateDetailsFormPayload(project, catalog.workspaces)
	case "task.create.preview":
		projectID := formString(values, "project_id")
		if projectID == "" {
			projectID = stringField(envelope.CardAction, "project_id")
		}
		workspaceID := formString(values, "workspace_id")
		title, request := formString(values, "title"), formString(values, "request")
		project, workspace, ok := catalogTaskTarget(catalog, projectID, workspaceID)
		if !ok || title == "" || request == "" || len([]rune(title)) > 200 || len([]rune(request)) > 1000 {
			payload = menuValidationErrorPayload("Project、Workspace 或任务内容无效，请从“创建任务”菜单重新开始。")
			break
		}
		precondition := map[string]any{"project_id": project.ID, "project_updated_at": timeStringUTC(project.UpdatedAt), "workspace_id": workspace.ID, "workspace_updated_at": timeStringUTC(workspace.UpdatedAt)}
		intent := map[string]any{"project_id": project.ID, "workspace_id": workspace.ID, "title": title, "request": request}
		preview := map[string]any{"operation": "create_task", "project": project.Name, "workspace": workspace.Name, "title": title, "request": request, "runtime": "继承渠道配置"}
		if err := s.createMenuPendingAction(ctx, instance, conversation, identity, "create_task", "task", "", intent, preview, precondition); err != nil {
			return err
		}
		return s.store.FinishChannelInbox(ctx, receipt.ID, receipt.LeaseID, "processed", conversation.ID, "", "menu_create_preview", s.now().UTC())
	default:
		return fmt.Errorf("unsupported menu card action")
	}
	if err := s.store.EnqueueChannelControlDelivery(ctx, instance.ID, conversation.ID, receipt.ID+":menu-card", "menu_result", payload, s.now().UTC()); err != nil {
		return err
	}
	return s.store.FinishChannelInbox(ctx, receipt.ID, receipt.LeaseID, "processed", conversation.ID, "", "menu_control", s.now().UTC())
}

func menuValidationErrorPayload(message string) map[string]any {
	return map[string]any{"kind": "menu_card", "title": "操作未提交", "template": "red", "markdown": menuSafeText(message)}
}

func (s *Service) activeRouteMenuActions(ctx context.Context, conversation domain.ChannelConversation) []map[string]any {
	route, err := s.store.ActiveChannelTaskRoute(ctx, conversation.ID)
	if err != nil {
		return nil
	}
	label := "退出当前 Task"
	if task, taskErr := s.store.Task(ctx, route.TargetTaskID); taskErr == nil && task.Code != "" {
		label = "退出 " + task.Code
	}
	return []map[string]any{{"label": label, "style": "danger", "value": map[string]any{"kind": "menu_control", "menu_action": "task.exit.preview"}}}
}

func (s *Service) createMenuPendingAction(ctx context.Context, instance domain.ChannelInstance, conversation domain.ChannelConversation, identity domain.ChannelIdentityLink, operation, targetType, targetID string, intent, preview, precondition map[string]any) error {
	raw, _ := json.Marshal(precondition)
	digest := sha256.Sum256(raw)
	now := s.now().UTC()
	_, err := s.store.CreateChannelPendingAction(ctx, domain.ChannelPendingAction{
		ID: domain.NewID("channel_action"), InstanceID: instance.ID, ConversationID: conversation.ID, ActorIdentityLinkID: identity.ID,
		Operation: operation, TargetType: targetType, TargetID: targetID, Intent: intent, Preview: preview, Precondition: precondition,
		PreconditionHash: hex.EncodeToString(digest[:]), Status: "pending", ExpiresAt: now.Add(15 * time.Minute), CreatedAt: now, UpdatedAt: now,
	})
	return err
}

func formString(values map[string]any, key string) string {
	value := values[key]
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range []string{"value", "text", "content"} {
			if result := stringField(typed, child); result != "" {
				return result
			}
		}
	case []any:
		if len(typed) > 0 {
			return strings.TrimSpace(fmt.Sprint(typed[0]))
		}
	}
	result := strings.TrimSpace(fmt.Sprint(value))
	if result == "<nil>" {
		return ""
	}
	return result
}

func catalogHasProject(catalog channelCatalog, projectID string) bool {
	for _, project := range catalog.projects {
		if project.ID == projectID {
			return true
		}
	}
	return false
}

func catalogTaskTarget(catalog channelCatalog, projectID, workspaceID string) (domain.Project, domain.Workspace, bool) {
	var project domain.Project
	for _, item := range catalog.projects {
		if item.ID == projectID {
			project = item
			break
		}
	}
	if project.ID == "" {
		return domain.Project{}, domain.Workspace{}, false
	}
	for _, workspace := range catalog.workspaces {
		if workspace.ID == workspaceID && workspace.ProjectID == project.ID {
			return project, workspace, true
		}
	}
	return domain.Project{}, domain.Workspace{}, false
}

func (s *Service) executePendingAction(ctx context.Context, action domain.ChannelPendingAction) error {
	switch action.Operation {
	case "takeover":
		task, err := s.store.Task(ctx, action.TargetID)
		instance, instanceErr := s.store.ChannelInstance(ctx, action.InstanceID)
		if err != nil || instanceErr != nil || task.ReadOnly || s.store.IsManagedChannelTask(ctx, task.ID) || !channelOperationAllowed(instance.Config, task.ProjectID, task.WorkspaceID) || !taskRouteEligible(task.Status) || string(task.Status) != stringField(action.Precondition, "status") || timeStringUTC(task.UpdatedAt) != stringField(action.Precondition, "updated_at") {
			return fmt.Errorf("takeover precondition changed")
		}
		return s.store.ActivateChannelTaskRoute(ctx, action, s.now().UTC())
	case "exit":
		route, err := s.store.ActiveChannelTaskRoute(ctx, action.ConversationID)
		if err != nil || route.ID != stringField(action.Precondition, "route_id") || route.Revision != intField(action.Precondition, "route_revision") {
			return fmt.Errorf("exit precondition changed")
		}
		return s.store.ExitChannelTaskRoute(ctx, action, s.now().UTC())
	case "create_task":
		return s.executeCreateTaskAction(ctx, action)
	case "status_change":
		task, err := s.store.Task(ctx, action.TargetID)
		if err != nil || timeStringUTC(task.UpdatedAt) != stringField(action.Precondition, "updated_at") || string(task.Status) != stringField(action.Precondition, "status") {
			return fmt.Errorf("task status precondition changed")
		}
		switch stringField(action.Intent, "action") {
		case "complete":
			err = s.application.CompleteTask(ctx, task.ID)
		case "reopen":
			err = s.application.ReopenTask(ctx, task.ID)
		case "interrupt":
			turn, turnErr := s.store.ActiveTurn(ctx, task.ID)
			if turnErr != nil {
				return turnErr
			}
			err = s.application.InterruptTurn(ctx, turn.ID)
		}
		if err != nil {
			return err
		}
		return s.store.FinishChannelPendingAction(ctx, action.ID, task.ID, "succeeded", map[string]any{"task_id": task.ID}, s.now().UTC())
	case "handoff_decision":
		handoff, err := s.store.ChannelHandoff(ctx, action.TargetID)
		if err != nil || handoff.State != stringField(action.Precondition, "state") || timeStringUTC(handoff.UpdatedAt) != stringField(action.Precondition, "updated_at") {
			return fmt.Errorf("handoff precondition changed")
		}
		decision := stringField(action.Intent, "decision")
		if decision == "accepted_task" {
			if err := s.executeCreateTaskAction(ctx, action); err != nil {
				return err
			}
			completed, err := s.store.ChannelPendingAction(ctx, action.ID)
			if err != nil {
				return err
			}
			return s.store.ResolveChannelHandoff(ctx, handoff.ID, action.ID, "task_created", decision, completed.TargetID, s.now().UTC())
		}
		if err := s.store.ResolveChannelHandoff(ctx, handoff.ID, action.ID, decision, decision, "", s.now().UTC()); err != nil {
			return err
		}
		return s.store.FinishChannelPendingAction(ctx, action.ID, action.TargetID, "succeeded", map[string]any{"handoff_id": handoff.ID, "decision": decision}, s.now().UTC())
	default:
		return fmt.Errorf("unsupported channel action")
	}
}

func taskRouteEligible(status domain.TaskStatus) bool {
	return status == domain.TaskPreparing || status == domain.TaskActive || status == domain.TaskWaitingUser || status == domain.TaskFailed
}

func intField(values map[string]any, key string) int {
	value, _ := strconv.Atoi(strings.TrimSuffix(fmt.Sprint(values[key]), ".0"))
	return value
}

func (s *Service) executeCreateTaskAction(ctx context.Context, action domain.ChannelPendingAction) error {
	intent := action.Intent
	workspace, err := s.store.Workspace(ctx, stringField(intent, "workspace_id"))
	if err != nil || workspace.ReadOnly || workspace.ProjectID != stringField(intent, "project_id") {
		return fmt.Errorf("task workspace precondition changed")
	}
	instance, err := s.store.ChannelInstance(ctx, action.InstanceID)
	if err != nil || !channelOperationAllowed(instance.Config, workspace.ProjectID, workspace.ID) {
		return fmt.Errorf("task workspace is outside channel allowlist")
	}
	if modelID := stringField(intent, "model_id"); modelID != "" {
		config := map[string]any{}
		for key, value := range instance.Config {
			config[key] = value
		}
		config["runtime_default"] = map[string]any{"model_id": modelID, "codex_account_id": stringField(intent, "codex_account_id")}
		instance.Config = config
	}
	runtimeChoice, err := s.resolveChannelRuntime(ctx, instance)
	if err != nil {
		return fmt.Errorf("task runtime is unavailable")
	}
	taskID := stableID("task", action.ID)
	task, err := s.store.Task(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		task, err = s.application.CreateTask(ctx, app.CreateTaskInput{
			ID: taskID, ProjectID: workspace.ProjectID, WorkspaceID: workspace.ID, Title: stringField(intent, "title"), Request: stringField(intent, "request"),
			Isolation: "inplace", Backend: runtimeChoice.Model.Backend, ModelSource: runtimeChoice.Model.Source, ModelID: runtimeChoice.Model.ID, WireModel: runtimeChoice.WireModel,
			CodexAccountID: runtimeChoice.CodexAccountID, ProxyEnabled: runtimeChoice.ProxyEnabled, ReasoningEffort: runtimeChoice.ReasoningEffort, Filesystem: "workspace-write", Approval: "never",
			CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "inherit",
		})
	}
	if err != nil {
		return err
	}
	if _, roundErr := s.store.LatestRound(ctx, task.ID); roundErr == nil {
		return s.store.FinishChannelPendingAction(ctx, action.ID, task.ID, "succeeded", map[string]any{"task_id": task.ID, "task_code": task.Code}, s.now().UTC())
	}
	if _, err := s.application.SubmitMessage(ctx, task.ID, task.OriginalRequest); err != nil {
		_ = s.store.FinishChannelPendingAction(ctx, action.ID, task.ID, "failed", map[string]any{"task_id": task.ID, "error_code": "task_start_failed"}, s.now().UTC())
		return err
	}
	return s.store.FinishChannelPendingAction(ctx, action.ID, task.ID, "succeeded", map[string]any{"task_id": task.ID, "task_code": task.Code}, s.now().UTC())
}

func (s *Service) scopeKey(instanceID, endpointKind, identityID, chatID, senderID string) (string, error) {
	if s.secrets == nil {
		return "", fmt.Errorf("secret store is unavailable")
	}
	const ref = "channel/runtime/scope_pepper"
	pepper, ok := s.secrets.Get(ref)
	if !ok || pepper == "" {
		value := make([]byte, 32)
		if _, err := rand.Read(value); err != nil {
			return "", err
		}
		pepper = base64.RawURLEncoding.EncodeToString(value)
		if err := s.secrets.PutMany(map[string]string{ref: pepper}); err != nil {
			return "", err
		}
	}
	values := []string{instanceID, endpointKind}
	if endpointKind == domain.ChannelEndpointAssistantDM {
		values = append(values, identityID)
	} else {
		values = append(values, chatID)
	}
	hash := hmac.New(sha256.New, []byte(pepper))
	for _, value := range values {
		_, _ = fmt.Fprintf(hash, "%d:", len(value))
		_, _ = hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *Service) ensureConversation(ctx context.Context, instance domain.ChannelInstance, endpoint domain.ChannelEndpoint, identity domain.ChannelIdentityLink, envelope domain.ChannelInboundEnvelope, scopeVersion int, scopeKey string) (domain.ChannelConversation, error) {
	if endpoint.Kind == domain.ChannelEndpointGroupDigitalHuman {
		preferred, preferredErr := s.store.PreferredChannelGroupConversation(ctx, endpoint.ID, envelope.ExternalChatID)
		if preferredErr == nil {
			if _, routeErr := s.store.ActiveChannelTaskRoute(ctx, preferred.ID); routeErr == nil || preferred.ScopeKeyVersion >= scopeVersion {
				return s.updateChannelConversationIdentity(ctx, preferred, instance, endpoint.Kind, envelope)
			}
			if err := s.store.PromoteChannelConversationScope(ctx, preferred.ID, scopeVersion, scopeKey, envelope.ChatDisplayName, s.now().UTC()); err == nil {
				preferred.ScopeKeyVersion = scopeVersion
				preferred.ScopeKey = scopeKey
				if strings.TrimSpace(envelope.ChatDisplayName) != "" {
					preferred.DisplayName = envelope.ChatDisplayName
				}
				return s.updateChannelConversationIdentity(ctx, preferred, instance, endpoint.Kind, envelope)
			} else if current, lookupErr := s.store.ChannelConversationByScope(ctx, endpoint.ID, scopeVersion, scopeKey); lookupErr == nil {
				return s.updateChannelConversationIdentity(ctx, current, instance, endpoint.Kind, envelope)
			} else {
				return domain.ChannelConversation{}, err
			}
		} else if !errors.Is(preferredErr, sql.ErrNoRows) {
			return domain.ChannelConversation{}, preferredErr
		}
	}
	conversation, err := s.store.ChannelConversationByScope(ctx, endpoint.ID, scopeVersion, scopeKey)
	if err == nil {
		return s.updateChannelConversationIdentity(ctx, conversation, instance, endpoint.Kind, envelope)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelConversation{}, err
	}
	runtimeChoice, err := s.resolveChannelRuntime(ctx, instance, endpoint.Kind)
	if err != nil {
		return domain.ChannelConversation{}, err
	}
	conversationID := domain.NewID("channel_conversation")
	task, err := s.application.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: instance.HostProjectID, WorkspaceID: instance.HostWorkspaceID,
		Title:   s.channelConversationTitle(ctx, instance, endpoint.Kind, envelope),
		Request: "System-managed external channel conversation host.", Isolation: "inplace",
		Backend: runtimeChoice.Model.Backend, ModelSource: runtimeChoice.Model.Source, ModelID: runtimeChoice.Model.ID, WireModel: runtimeChoice.WireModel,
		CodexAccountID: runtimeChoice.CodexAccountID, ProxyEnabled: runtimeChoice.ProxyEnabled,
		ReasoningEffort: runtimeChoice.ReasoningEffort, Filesystem: "read-only", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "enabled",
	})
	if err != nil {
		return domain.ChannelConversation{}, fmt.Errorf("channel runtime task: %w", err)
	}
	now := s.now().UTC()
	_ = s.store.UpdateTaskStatus(ctx, task.ID, task.Status, domain.TaskWaitingUser, timeStringUTC(now), "")
	conversation = domain.ChannelConversation{
		ID: conversationID, InstanceID: instance.ID, EndpointID: endpoint.ID, ScopeKeyVersion: scopeVersion, ScopeKey: scopeKey,
		ExternalChatID: envelope.ExternalChatID, ExternalSenderID: envelope.ExternalSenderID, HostTaskID: task.ID,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	conversation.DisplayName = envelope.SenderDisplayName
	if endpoint.Kind == domain.ChannelEndpointGroupDigitalHuman {
		conversation.DisplayName = envelope.ChatDisplayName
	}
	if endpoint.Kind == domain.ChannelEndpointAssistantDM {
		conversation.OwnerIdentityLinkID = identity.ID
	}
	mode := "assistant"
	if endpoint.Kind == domain.ChannelEndpointGroupDigitalHuman {
		mode = "group_qa"
	}
	session := domain.ChannelSession{ID: domain.NewID("channel_session"), ConversationID: conversation.ID, Generation: 1, Mode: mode, Status: "active", StartedAt: now}
	if err := s.store.CreateChannelConversation(ctx, conversation, session); err != nil {
		_ = s.store.DeleteTask(ctx, task.ID)
		_ = s.store.DeleteRuntimeSnapshot(ctx, task.RuntimeConfigSnapshotID)
		if existing, lookupErr := s.store.ChannelConversationByScope(ctx, endpoint.ID, scopeVersion, scopeKey); lookupErr == nil {
			return existing, nil
		}
		return domain.ChannelConversation{}, err
	}
	if err := s.store.EnsureConversationHostSubscription(ctx, instance.ID, conversation.ID, task.ID, now); err != nil {
		return domain.ChannelConversation{}, err
	}
	if endpoint.Kind == domain.ChannelEndpointAssistantDM {
		if err := s.store.EnsureOwnerGlobalSubscription(ctx, instance.ID, conversation.ID, now); err != nil {
			return domain.ChannelConversation{}, err
		}
		if err := s.store.DeliverPendingChannelHandoffs(ctx, instance.ID, conversation.ID, now); err != nil {
			return domain.ChannelConversation{}, err
		}
	}
	return conversation, nil
}

func (s *Service) updateChannelConversationIdentity(ctx context.Context, conversation domain.ChannelConversation, instance domain.ChannelInstance, endpointKind string, envelope domain.ChannelInboundEnvelope) (domain.ChannelConversation, error) {
	desiredTitle := s.channelConversationTitle(ctx, instance, endpointKind, envelope)
	nameResolved := endpointKind == domain.ChannelEndpointAssistantDM || envelope.ChatDisplayName != ""
	if task, taskErr := s.store.Task(ctx, conversation.HostTaskID); taskErr == nil && nameResolved && desiredTitle != "" && task.Title != desiredTitle {
		_ = s.store.UpdateTaskTitle(ctx, task.ID, desiredTitle, timeStringUTC(s.now().UTC()))
	}
	displayName := envelope.SenderDisplayName
	if endpointKind == domain.ChannelEndpointGroupDigitalHuman {
		displayName = envelope.ChatDisplayName
	}
	if strings.TrimSpace(displayName) != "" && conversation.DisplayName != displayName {
		_ = s.store.UpdateChannelConversationDisplayName(ctx, conversation.ID, displayName, s.now().UTC())
		conversation.DisplayName = displayName
	}
	return conversation, nil
}

type channelRuntimeChoice struct {
	Model           domain.Model
	WireModel       string
	CodexAccountID  string
	ReasoningEffort string
	ProxyEnabled    bool
}

func (s *Service) resolveChannelRuntime(ctx context.Context, instance domain.ChannelInstance, endpointKind ...string) (channelRuntimeChoice, error) {
	runtimeConfig, _ := mapField(instance.Config, "runtime_default")
	if len(endpointKind) > 0 {
		if override, present := mapField(instance.Config, "runtime_"+endpointKind[0]); present && !boolField(override, "inherit") {
			runtimeConfig = override
		}
	}
	configuredModelID := stringField(runtimeConfig, "model_id")
	configuredAccountID := stringField(runtimeConfig, "codex_account_id")
	tasks, _ := s.store.ListTasks(ctx, "")
	for _, task := range tasks {
		if task.ReadOnly || task.ProjectID == instance.HostProjectID || s.store.IsManagedChannelTask(ctx, task.ID) {
			continue
		}
		snapshot, snapshotErr := s.store.RuntimeSnapshot(ctx, task.RuntimeConfigSnapshotID)
		if snapshotErr != nil || (configuredModelID != "" && snapshot.ModelID != configuredModelID) {
			continue
		}
		model, modelErr := s.store.Model(ctx, snapshot.ModelID)
		accountID := snapshot.CodexAccountID
		if configuredAccountID != "" {
			accountID = configuredAccountID
		}
		if modelErr != nil || !s.channelRuntimeModelAvailable(ctx, model, accountID, snapshot.WireModel) {
			continue
		}
		return channelRuntimeChoice{
			Model: model, WireModel: snapshot.WireModel, CodexAccountID: accountID,
			ReasoningEffort: firstNonEmpty(stringField(runtimeConfig, "reasoning_effort"), snapshot.ReasoningEffort), ProxyEnabled: boolFieldDefault(runtimeConfig, "proxy_enabled", snapshot.ProxyEnabled),
		}, nil
	}

	models, err := s.store.ListModels(ctx)
	if err != nil {
		return channelRuntimeChoice{}, fmt.Errorf("channel runtime model is unavailable")
	}
	accounts, _ := s.store.ListCodexAccounts(ctx)
	for _, model := range models {
		if configuredModelID != "" && model.ID != configuredModelID {
			continue
		}
		if model.Source == domain.ModelSourceOfficial {
			for _, account := range accounts {
				if configuredAccountID != "" && account.ID != configuredAccountID {
					continue
				}
				if !account.CredentialConfigured || account.Status != "ready" {
					continue
				}
				if s.channelRuntimeModelAvailable(ctx, model, account.ID, model.WireModel) {
					return channelRuntimeChoice{Model: model, WireModel: model.WireModel, CodexAccountID: account.ID, ReasoningEffort: firstNonEmpty(stringField(runtimeConfig, "reasoning_effort"), model.DefaultEffort), ProxyEnabled: boolFieldDefault(runtimeConfig, "proxy_enabled", account.ProxyEnabled)}, nil
				}
			}
			continue
		}
		if s.channelRuntimeModelAvailable(ctx, model, "", model.WireModel) {
			return channelRuntimeChoice{Model: model, WireModel: model.WireModel, ReasoningEffort: firstNonEmpty(stringField(runtimeConfig, "reasoning_effort"), model.DefaultEffort), ProxyEnabled: boolField(runtimeConfig, "proxy_enabled")}, nil
		}
	}
	return channelRuntimeChoice{}, fmt.Errorf("channel runtime model is unavailable")
}

func (s *Service) channelConversationTitle(ctx context.Context, instance domain.ChannelInstance, endpointKind string, envelope domain.ChannelInboundEnvelope) string {
	clean := func(value, fallback string) string {
		value = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, strings.TrimSpace(value))
		if value == "" {
			value = fallback
		}
		if len([]rune(value)) > 60 {
			value = string([]rune(value)[:60])
		}
		return value
	}
	if endpointKind == domain.ChannelEndpointAssistantDM {
		ownerName := envelope.SenderDisplayName
		if ownerName == "" {
			if owner, err := s.store.OwnerByID(ctx, instance.OwnerID); err == nil {
				ownerName = owner.Username
			}
		}
		return "飞书私聊 · " + clean(ownerName, "Owner")
	}
	return "飞书群聊 · " + clean(envelope.ChatDisplayName, "未命名群聊")
}

func mapField(values map[string]any, key string) (map[string]any, bool) {
	if values == nil {
		return nil, false
	}
	value, ok := values[key]
	if !ok || value == nil {
		return nil, false
	}
	typed, ok := value.(map[string]any)
	return typed, ok
}

func stringListField(values map[string]any, key string) ([]string, bool) {
	if values == nil {
		return nil, false
	}
	raw, present := values[key]
	if !present {
		return nil, false
	}
	result, seen := []string{}, map[string]bool{}
	switch typed := raw.(type) {
	case []any:
		for _, item := range typed {
			value := strings.TrimSpace(fmt.Sprint(item))
			if value != "" && value != "<nil>" && !seen[value] {
				seen[value] = true
				result = append(result, value)
			}
		}
	case []string:
		for _, item := range typed {
			value := strings.TrimSpace(item)
			if value != "" && !seen[value] {
				seen[value] = true
				result = append(result, value)
			}
		}
	}
	return result, true
}

func sliceSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}

func boolFieldDefault(values map[string]any, key string, fallback bool) bool {
	if values == nil {
		return fallback
	}
	value, ok := values[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func boolField(values map[string]any, key string) bool {
	return boolFieldDefault(values, key, false)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Service) channelRuntimeModelAvailable(ctx context.Context, model domain.Model, accountID, wireModel string) bool {
	if model.Source == domain.ModelSourceOfficial {
		if accountID == "" {
			return false
		}
		account, err := s.store.CodexAccount(ctx, accountID)
		if err != nil || !account.CredentialConfigured || account.Status != "ready" {
			return false
		}
		for _, option := range account.AvailableModels {
			if option.WireModel == wireModel {
				return true
			}
		}
		return false
	}
	if model.Source != domain.ModelSourceProvider || model.DefaultEnvGroupID == "" {
		return false
	}
	env, err := s.store.EnvGroup(ctx, model.DefaultEnvGroupID)
	return err == nil && (model.ProviderID == "" || env.ProviderID == "" || model.ProviderID == env.ProviderID)
}

func timeStringUTC(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
