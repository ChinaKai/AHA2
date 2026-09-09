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
	store           *store.Store
	application     *app.Service
	secrets         SecretStore
	pluginsRoot     string
	instancesRoot   string
	runtimeDeviceID string
	logger          *slog.Logger
	now             func() time.Time
	createMu        sync.Mutex
	wakeInbound     chan struct{}
	processMu       sync.Mutex
	processes       map[string]*managedPluginProcess
	processRetry    map[string]time.Time
	processFailures map[string]int
	runtimeBaseURL  string
	runCtx          context.Context
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
	return Discover(ctx, s.store, s.pluginsRoot, s.now().UTC())
}

func (s *Service) Providers(ctx context.Context) ([]domain.ChannelPlugin, error) {
	if err := s.RefreshPlugins(ctx); err != nil {
		s.logger.Warn("channel plugin discovery failed", "error", err)
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
		HostProjectID: projectID, HostWorkspaceID: workspaceID, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now,
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

func (s *Service) UpdateInstance(ctx context.Context, ownerID, id, name string, config map[string]any, expectedRevision int) (domain.ChannelInstance, error) {
	item, err := s.store.ChannelInstance(ctx, id)
	if err != nil || item.OwnerID != ownerID {
		if err == nil {
			err = sql.ErrNoRows
		}
		return domain.ChannelInstance{}, err
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
		if err := s.validateChannelInstanceConfig(ctx, item, merged); err != nil {
			return domain.ChannelInstance{}, err
		}
		item.Config = merged
	}
	item.UpdatedAt = s.now().UTC()
	return s.store.UpdateChannelInstance(ctx, item, expectedRevision)
}

func (s *Service) validateChannelInstanceConfig(ctx context.Context, instance domain.ChannelInstance, config map[string]any) error {
	projects, projectsSet := stringListField(config, "allowed_project_ids")
	workspaces, workspacesSet := stringListField(config, "allowed_workspace_ids")
	if projectsSet {
		for _, id := range projects {
			project, err := s.store.Project(ctx, id)
			if err != nil || project.ProjectType == "channel" || project.ProjectType == "knowledge" {
				return fmt.Errorf("invalid channel project allowlist")
			}
		}
	}
	if workspacesSet {
		allowedProjects := sliceSet(projects)
		for _, id := range workspaces {
			workspace, err := s.store.Workspace(ctx, id)
			if err != nil || workspace.ReadOnly || s.store.IsManagedChannelWorkspace(ctx, id) || (projectsSet && !allowedProjects[workspace.ProjectID]) {
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

func (s *Service) ReplaceOwnerKnowledgeGrants(ctx context.Context, ownerID, instanceID, endpoint string, revision int, inputs []KnowledgeGrantInput) ([]map[string]any, error) {
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil || instance.OwnerID != ownerID {
		return nil, sql.ErrNoRows
	}
	grants := make([]domain.ChannelKnowledgeGrant, 0, len(inputs))
	allowedRoots, err := s.channelKnowledgeSourceRoots(ctx, instance)
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
	if err := s.store.ReplaceChannelKnowledgeGrants(ctx, instanceID, endpoint, ownerID, revision, grants, s.now().UTC()); err != nil {
		return nil, err
	}
	return s.store.ChannelKnowledgePolicies(ctx, instanceID)
}

func (s *Service) channelKnowledgeSourceRoots(ctx context.Context, instance domain.ChannelInstance) (map[string]bool, error) {
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
	allowedProjects, restricted := stringListField(instance.Config, "allowed_project_ids")
	projectSet := sliceSet(allowedProjects)
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
		if projectErr == nil && project.ProjectType != "channel" && project.ProjectType != "knowledge" && (!restricted || projectSet[project.ID]) {
			result[entry.ID] = true
		}
	}
	return result, nil
}

func (s *Service) OwnerKnowledgeRecords(ctx context.Context, ownerID, instanceID string) ([]domain.ChannelKnowledgeRecord, error) {
	instance, err := s.store.ChannelInstance(ctx, instanceID)
	if err != nil || instance.OwnerID != ownerID {
		return nil, sql.ErrNoRows
	}
	return s.store.ChannelKnowledgeRecords(ctx, instanceID)
}

func (s *Service) PromoteOwnerKnowledgeRecord(ctx context.Context, ownerID, id, title, body string) (domain.ChannelKnowledgeRecord, error) {
	record, err := s.store.ChannelKnowledgeRecord(ctx, id)
	if err != nil {
		return domain.ChannelKnowledgeRecord{}, err
	}
	instance, err := s.store.ChannelInstance(ctx, record.InstanceID)
	if err != nil || instance.OwnerID != ownerID {
		return domain.ChannelKnowledgeRecord{}, sql.ErrNoRows
	}
	return s.store.PromoteChannelKnowledgeRecord(ctx, id, instance.ID, title, body, s.now().UTC())
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
	return s.store.CompleteChannelCommand(ctx, claims.InstanceID, id, leaseID, success, result, errorCode, s.now().UTC())
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
		items[index].Target, _ = s.store.ChannelDeliveryTarget(ctx, items[index].ConversationID)
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

func (s *Service) UpdateHealth(ctx context.Context, claims RuntimeClaims, status, errorCode string) error {
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
	return s.store.UpdateChannelInstanceHealth(ctx, claims.InstanceID, status, errorCode, s.now().UTC())
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
		endpointKind = domain.ChannelEndpointAssistantDM
		role = "owner"
		identity, err = s.store.ChannelOwnerIdentity(ctx, instance.ID)
		if err != nil || identity.ExternalUserID != envelope.ExternalSenderID || identity.OwnerID != instance.OwnerID {
			return fmt.Errorf("channel owner identity is required")
		}
	} else {
		if !envelope.MentionedBot {
			return fmt.Errorf("group mention is required")
		}
		identity, err = s.store.UpsertChannelParticipant(ctx, domain.ChannelIdentityLink{
			ID: domain.NewID("channel_identity"), InstanceID: instance.ID, ExternalUserID: envelope.ExternalSenderID,
			Role: "participant", Status: "active", LinkedAt: s.now().UTC(),
		})
		if err != nil {
			return err
		}
	}
	endpoint, err := s.store.ChannelEndpoint(ctx, instance.ID, endpointKind)
	if err != nil || !endpoint.Enabled {
		return fmt.Errorf("channel endpoint is unavailable")
	}
	scopeKey, err := s.scopeKey(instance.ID, endpointKind, identity.ID, envelope.ExternalChatID, envelope.ExternalSenderID)
	if err != nil {
		return err
	}
	conversation, err := s.ensureConversation(ctx, instance, endpoint, identity, envelope, scopeKey)
	if err != nil {
		return err
	}
	targetTaskID, routeMode := conversation.HostTaskID, "assistant"
	if endpointKind == domain.ChannelEndpointGroupDigitalHuman {
		routeMode = "group_qa"
	} else if route, routeErr := s.store.ActiveChannelTaskRoute(ctx, conversation.ID); routeErr == nil {
		targetTaskID, routeMode = route.TargetTaskID, "task_route"
	}
	provenance := map[string]any{
		"schema": "aha.channel-context/v1", "instance_id": instance.ID, "provider": instance.ProviderKey,
		"endpoint": endpointKind, "conversation_id": conversation.ID, "actor": map[string]any{"identity_link_id": identity.ID, "role": role},
		"route": map[string]any{"mode": routeMode, "target_task_id": targetTaskID}, "inbound_receipt_id": receipt.ID,
	}
	_, err = s.application.SubmitChannelMessage(ctx, receipt.ID, receipt.LeaseID, conversation.ID, targetTaskID, envelope.Content, provenance)
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
		payload = taskCreateFormPayload(catalog.projects, catalog.workspaces)
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
	return s.ensureConversation(ctx, instance, endpoint, identity, menuEnvelope, scopeKey)
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
	allowedProjects, projectsRestricted := stringListField(instance.Config, "allowed_project_ids")
	allowedWorkspaces, workspacesRestricted := stringListField(instance.Config, "allowed_workspace_ids")
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

func taskCreateFormPayload(projects []domain.Project, workspaces []domain.Workspace) map[string]any {
	return map[string]any{
		"kind": "menu_card", "title": "创建 Task", "template": "blue", "markdown": "请填写结构化字段。Runtime 默认继承渠道设置；提交后仍需一次确认。",
		"fields": []map[string]any{
			{"type": "select", "name": "project_id", "label": "Project", "options": projectOptions(projects)},
			{"type": "select", "name": "workspace_id", "label": "Workspace", "options": workspaceOptions(projects, workspaces)},
			{"type": "text", "name": "title", "label": "标题", "max_length": 200},
			{"type": "multiline", "name": "request", "label": "需求", "max_length": 1000},
		},
		"submit": map[string]any{"label": "生成预览", "value": map[string]any{"kind": "menu_control", "menu_action": "task.create.preview"}},
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
		preview = map[string]any{"operation": operation, "task_id": task.ID, "task_code": task.Code, "title": task.Title, "effect": "建立消息路由，不迁移或改变 Task"}
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
	projects, projectsRestricted := stringListField(config, "allowed_project_ids")
	workspaces, workspacesRestricted := stringListField(config, "allowed_workspace_ids")
	return (!projectsRestricted || sliceSet(projects)[projectID]) && (!workspacesRestricted || sliceSet(workspaces)[workspaceID])
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
		preview := map[string]any{"operation": "takeover", "task_code": target.Code, "title": target.Title, "effect": "建立消息路由，不迁移或改变 Task"}
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
	case "task.create.preview":
		projectID, workspaceID := formString(values, "project_id"), formString(values, "workspace_id")
		title, request := formString(values, "title"), formString(values, "request")
		project, workspace, ok := catalogTaskTarget(catalog, projectID, workspaceID)
		if !ok || title == "" || request == "" || len([]rune(title)) > 200 || len([]rune(request)) > 1000 {
			return fmt.Errorf("menu task creation fields are invalid")
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
		values = append(values, chatID, senderID)
	}
	hash := hmac.New(sha256.New, []byte(pepper))
	for _, value := range values {
		_, _ = fmt.Fprintf(hash, "%d:", len(value))
		_, _ = hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *Service) ensureConversation(ctx context.Context, instance domain.ChannelInstance, endpoint domain.ChannelEndpoint, identity domain.ChannelIdentityLink, envelope domain.ChannelInboundEnvelope, scopeKey string) (domain.ChannelConversation, error) {
	conversation, err := s.store.ChannelConversationByScope(ctx, endpoint.ID, 1, scopeKey)
	if err == nil {
		desiredTitle := s.channelConversationTitle(ctx, instance, endpoint.Kind, envelope)
		nameResolved := endpoint.Kind == domain.ChannelEndpointAssistantDM || (envelope.ChatDisplayName != "" && envelope.SenderDisplayName != "")
		if task, taskErr := s.store.Task(ctx, conversation.HostTaskID); taskErr == nil && nameResolved && desiredTitle != "" && task.Title != desiredTitle {
			_ = s.store.UpdateTaskTitle(ctx, task.ID, desiredTitle, timeStringUTC(s.now().UTC()))
		}
		return conversation, nil
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
		ID: conversationID, InstanceID: instance.ID, EndpointID: endpoint.ID, ScopeKeyVersion: 1, ScopeKey: scopeKey,
		ExternalChatID: envelope.ExternalChatID, ExternalSenderID: envelope.ExternalSenderID, HostTaskID: task.ID,
		Status: "active", CreatedAt: now, UpdatedAt: now,
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
		if existing, lookupErr := s.store.ChannelConversationByScope(ctx, endpoint.ID, 1, scopeKey); lookupErr == nil {
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
	return "飞书群聊 · " + clean(envelope.ChatDisplayName, "未命名群聊") + " · " + clean(envelope.SenderDisplayName, "群成员")
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
