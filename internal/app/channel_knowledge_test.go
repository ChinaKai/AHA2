package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

const (
	channelVaultProjectID  = "project_pet"
	channelLegacyProjectID = "project_legacy"
	channelHostProjectID   = "project_channel_host"
)

type channelKnowledgeFixture struct {
	service       *Service
	database      *store.Store
	ctx           context.Context
	owner         domain.Owner
	instance      domain.ChannelInstance
	vaultProject  domain.Project
	legacyProject domain.Project
	hostProject   domain.Project
	model         domain.Model
}

func newChannelKnowledgeFixture(t *testing.T) *channelKnowledgeFixture {
	t.Helper()
	return newChannelKnowledgeFixtureWithPolicies(t, true)
}

// newChannelKnowledgeFixtureWithoutPolicies mirrors a channel instance created
// while the vault is the read source: no legacy host root and no channel
// policy. The read path must treat that as an explicit empty selection.
func newChannelKnowledgeFixtureWithoutPolicies(t *testing.T) *channelKnowledgeFixture {
	t.Helper()
	return newChannelKnowledgeFixtureWithPolicies(t, false)
}

func newChannelKnowledgeFixtureWithPolicies(t *testing.T, seedPolicies bool) *channelKnowledgeFixture {
	t.Helper()
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(database, secretStore, &stubExecutor{})
	manager := buildVaultReadManager(t, true, vaultCanaryFiles())
	service.SetKnowledgeVault(manager, true)
	now := time.Now().UTC()

	owner := domain.Owner{ID: "owner-channel-vault", Username: "owner", PasswordHash: "hash", CreatedAt: now}
	if err := database.CreateOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	plugin := domain.ChannelPlugin{
		ID: "channel-plugin-vault", ProviderKey: "feishu", DisplayName: "Feishu",
		ManifestVersion: 1, PackageVersion: "1.0.0", ProtocolMin: 1, ProtocolMax: 1,
		ExecutablePath: "/bin/true", ExecutableSHA256: "digest", InstallState: "installed",
		Enabled: true, Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}
	if err := database.UpsertChannelPlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	provider := domain.Provider{
		ID: "provider-channel-vault", Name: "Provider", BaseURL: "https://example.test/v1",
		AuthStyle: "bearer", CredentialRef: "provider/channel-vault/credential", CredentialConfigured: true,
		CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-channel-vault", Name: "Env", ProviderID: provider.ID, Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-channel-vault", DisplayName: "Model", ProviderID: provider.ID,
		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "model", WireAPI: "chat",
		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.UpsertProvider(ctx, provider) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}

	vaultProject := domain.Project{ID: channelVaultProjectID, Name: "Pet", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	legacyProject := domain.Project{ID: channelLegacyProjectID, Name: "Legacy", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	for _, project := range []domain.Project{vaultProject, legacyProject} {
		if err := database.CreateProject(ctx, project); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []domain.Workspace{
		{ID: "workspace-channel-vault", ProjectID: vaultProject.ID, Name: "vault", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto", CreatedAt: now, UpdatedAt: now},
		{ID: "workspace-channel-legacy", ProjectID: legacyProject.ID, Name: "legacy", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto", CreatedAt: now, UpdatedAt: now},
	} {
		if err := database.CreateWorkspace(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	vaultRoot, err := database.EnsureKnowledgeRoot(ctx, "project", vaultProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyRoot, err := database.EnsureKnowledgeRoot(ctx, "project", legacyProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	// These legacy rows must never be served for the vault-bound project, but
	// remain the source for the unbound project.
	for _, item := range []domain.KnowledgeEntry{
		{
			ID: "knowledge_pet_legacy_only", Scope: "project", ProjectID: vaultProject.ID, ParentID: vaultRoot.ID,
			Slug: "legacy-only", Type: "practice", Title: "Legacy only", Body: "must stay invisible",
			Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "knowledge_legacy_readable", Scope: "project", ProjectID: legacyProject.ID, ParentID: legacyRoot.ID,
			Slug: "legacy-readable", Type: "practice", Title: "Legacy readable", Body: "still legacy",
			Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
		},
	} {
		if err := database.CreateKnowledge(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	hostProject := domain.Project{
		ID: channelHostProjectID, Name: "Channel", ProjectType: "channel",
		DefaultWorkspaceID: "workspace-channel-host", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	hostWorkspace := domain.Workspace{
		ID: "workspace-channel-host", ProjectID: hostProject.ID, Name: "host", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto", CreatedAt: now, UpdatedAt: now,
	}
	instance := domain.ChannelInstance{
		ID: "channel-instance-vault", PluginID: plugin.ID, OwnerID: owner.ID, RuntimeDeviceID: "device",
		Name: "feishu", Status: "ready", Revision: 1, HostProjectID: hostProject.ID,
		HostWorkspaceID: hostWorkspace.ID, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	endpoints := []domain.ChannelEndpoint{
		{ID: "channel-endpoint-dm", InstanceID: instance.ID, Kind: domain.ChannelEndpointAssistantDM, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now},
		{ID: "channel-endpoint-group", InstanceID: instance.ID, Kind: domain.ChannelEndpointGroupDigitalHuman, Enabled: true, Config: map[string]any{}, CreatedAt: now, UpdatedAt: now},
	}
	if err := database.CreateChannelInstanceWithHost(ctx, hostProject, hostWorkspace, instance, endpoints); err != nil {
		t.Fatal(err)
	}
	if seedPolicies {
		hostRoot, err := database.EnsureKnowledgeRoot(ctx, "project", hostProject.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := database.EnsureChannelKnowledgePolicies(ctx, instance.ID, hostRoot.ID, now); err != nil {
			t.Fatal(err)
		}
	}
	return &channelKnowledgeFixture{
		service: service, database: database, ctx: ctx, owner: owner, instance: instance,
		vaultProject: vaultProject, legacyProject: legacyProject, hostProject: hostProject, model: model,
	}
}

func (f *channelKnowledgeFixture) runningTurn(t *testing.T, project domain.Project, workspaceID string) (domain.Task, domain.Turn) {
	t.Helper()
	task, err := f.service.CreateTask(f.ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspaceID, Title: "knowledge", Request: "knowledge",
		ModelID: f.model.ID, StartMode: "manual", Isolation: "inplace", Filesystem: "workspace-write", Approval: "never",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	message := domain.Message{ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner", Content: "knowledge", CreatedAt: now}
	round := domain.TaskRound{ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID, Status: domain.RoundRunning, CreatedAt: now, StartedAt: now}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnRunning, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		Attempt: 1, Required: true, QueuedAt: now, PreparedAt: now,
	}
	if _, _, err := f.database.CreateMessageRoundAndTurn(f.ctx, message, round, turn); err != nil {
		t.Fatal(err)
	}
	stored, err := f.database.Turn(f.ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	return task, stored
}

func knowledgeEntryIDs(items []domain.KnowledgeEntry) map[string]bool {
	result := map[string]bool{}
	for _, item := range items {
		result[item.ID] = true
	}
	return result
}

func TestChannelKnowledgeReadsVaultForBoundProjectAndLegacyForUnbound(t *testing.T) {
	fixture := newChannelKnowledgeFixture(t)
	entries, err := fixture.service.channelAllowedKnowledge(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := knowledgeEntryIDs(entries)
	for _, want := range []string{"knowledge_pet_note", "knowledge_legacy_readable"} {
		if !ids[want] {
			t.Fatalf("channel knowledge missing %s: %#v", want, entries)
		}
	}
	if ids["knowledge_pet_legacy_only"] {
		t.Fatalf("channel knowledge served the legacy row of a vault-routed project: %#v", entries)
	}
}

func TestChannelKnowledgeSelectedGrantsFollowTheReadSource(t *testing.T) {
	fixture := newChannelKnowledgeFixture(t)
	policies, err := fixture.database.ChannelKnowledgePolicies(fixture.ctx, fixture.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision := 0
	for _, policy := range policies {
		if policy["endpoint"] == domain.ChannelEndpointGroupDigitalHuman {
			revision = policy["revision"].(int)
		}
	}
	if revision == 0 {
		t.Fatal("group knowledge policy missing")
	}
	// A legacy grant for a vault-routed project must stop granting its old row.
	legacyGrant := []domain.ChannelKnowledgeGrant{{KnowledgeEntryID: "knowledge_pet_legacy_only", GrantScope: "node"}}
	if err := fixture.database.ReplaceChannelKnowledgeGrants(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, fixture.owner.ID, "selected", revision, legacyGrant, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	entries, err := fixture.service.channelAllowedKnowledge(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, "")
	if err != nil {
		t.Fatal(err)
	}
	if knowledgeEntryIDs(entries)["knowledge_pet_legacy_only"] {
		t.Fatalf("expired legacy grant still granted old content: %#v", entries)
	}

	// The vault root is grantable and covers its subtree.
	vaultGrant := []domain.ChannelKnowledgeGrant{{KnowledgeEntryID: "knowledge_root_project_pet", GrantScope: "subtree"}}
	vaultIDs := map[string]bool{"knowledge_root_project_pet": true}
	if err := fixture.database.ReplaceChannelKnowledgeGrants(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, fixture.owner.ID, "selected", revision+1, vaultGrant, vaultIDs, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	entries, err = fixture.service.channelAllowedKnowledge(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := knowledgeEntryIDs(entries)
	if !ids["knowledge_root_project_pet"] || !ids["knowledge_pet_note"] {
		t.Fatalf("vault grant did not cover the vault subtree: %#v", entries)
	}
	if ids["knowledge_pet_legacy_only"] {
		t.Fatalf("vault grant leaked a legacy row: %#v", entries)
	}
}

func TestKnowledgeIndexSourcesFollowTheReadSource(t *testing.T) {
	fixture := newChannelKnowledgeFixture(t)
	sources, err := fixture.service.KnowledgeIndexSources(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	byProject := map[string]KnowledgeIndexSource{}
	for _, source := range sources {
		byProject[source.Entry.ProjectID] = source
	}
	vaultSource, ok := byProject[fixture.vaultProject.ID]
	if !ok || !vaultSource.Vault || vaultSource.Entry.ID != "knowledge_root_project_pet" {
		t.Fatalf("vault project root=%#v", vaultSource)
	}
	legacySource, ok := byProject[fixture.legacyProject.ID]
	if !ok || legacySource.Vault || legacySource.Entry.ID != knowledgeRootIDForTest(fixture, fixture.legacyProject.ID) {
		t.Fatalf("legacy project root=%#v", legacySource)
	}
}

func knowledgeRootIDForTest(fixture *channelKnowledgeFixture, projectID string) string {
	root, err := fixture.database.KnowledgeRootForProject(fixture.ctx, projectID)
	if err != nil {
		return ""
	}
	return root.ID
}

func TestGlobalKnowledgeGateIsIndependentOfProjectBinding(t *testing.T) {
	fixture := newChannelKnowledgeFixture(t)
	global, globalStale, source, err := fixture.service.globalKnowledgeForTask(fixture.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if source != "vault" {
		t.Fatalf("global source=%s, want vault", source)
	}
	if !knowledgeEntryIDs(global)["knowledge_shared"] || len(globalStale) != 0 {
		t.Fatalf("global entries=%#v stale=%#v", global, globalStale)
	}
	// The unbound project still reads legacy, while global comes from the vault.
	project, _, projectSource, err := fixture.service.projectKnowledgeForTask(fixture.ctx, fixture.legacyProject.ID, domain.ProductLine{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if projectSource != "legacy" || !knowledgeEntryIDs(project)["knowledge_legacy_readable"] {
		t.Fatalf("legacy project source=%s entries=%#v", projectSource, project)
	}
	if knowledgeEntryIDs(project)["knowledge_shared"] {
		t.Fatal("legacy project read leaked a vault global entry")
	}
}

type failingKnowledgeVault struct {
	err error
}

func (f failingKnowledgeVault) Config() knowledgevault.Config { return knowledgevault.Config{} }
func (f failingKnowledgeVault) Enabled() bool                 { return true }
func (f failingKnowledgeVault) Guide() (knowledgevault.Guide, error) {
	return knowledgevault.Guide{}, knowledgevault.ErrGuideNotFound
}
func (f failingKnowledgeVault) WithReader(func(*knowledgevault.Reader) error) error { return f.err }
func (f failingKnowledgeVault) SetRuntimeWrite(bool)                                {}
func (f failingKnowledgeVault) RuntimeWriteEnabled() bool                           { return true }
func (f failingKnowledgeVault) WriteKnowledge(context.Context, knowledgevault.WriteRequest) (knowledgevault.WriteResult, error) {
	return knowledgevault.WriteResult{}, f.err
}
func (f failingKnowledgeVault) KnowledgeFeedback(context.Context, knowledgevault.FeedbackRequest) (knowledgevault.FeedbackResult, error) {
	return knowledgevault.FeedbackResult{}, f.err
}
func (f failingKnowledgeVault) CheckProjectSpacePrerequisites() error { return f.err }
func (f failingKnowledgeVault) ProvisionProjectSpace(context.Context, string, string) (knowledgevault.ProjectSpaceResult, error) {
	return knowledgevault.ProjectSpaceResult{}, f.err
}
func (f failingKnowledgeVault) Rebuild(context.Context) (knowledgevault.RebuildReport, error) {
	return knowledgevault.RebuildReport{}, f.err
}
func (f failingKnowledgeVault) RenameProjectSpace(string, string) error { return f.err }

func TestKnowledgeReadFailsClosedWhenTheVaultIsUnreadable(t *testing.T) {
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-fail-closed", Name: "Fail", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	root, err := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy := domain.KnowledgeEntry{
		ID: "knowledge-fail-closed-legacy", Scope: "project", ProjectID: project.ID, ParentID: root.ID,
		Slug: "legacy", Type: "practice", Title: "Legacy", Body: "must not be used",
		Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateKnowledge(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, &stubExecutor{})
	readerErr := errors.New("index connection lost")
	service.SetKnowledgeVault(failingKnowledgeVault{err: readerErr}, true)
	if !service.vaultReadEnabled(project.ID) {
		t.Fatal("unreadable vault was not treated as the authoritative source")
	}
	if _, vault, err := service.readVaultProjectKnowledge(ctx, project.ID); err == nil || !vault {
		t.Fatalf("project read did not fail closed: vault=%v err=%v", vault, err)
	}
	if _, _, vault, err := service.readVaultGlobalKnowledge(ctx); err == nil || !vault {
		t.Fatalf("global read did not fail closed: vault=%v err=%v", vault, err)
	}
	if _, _, _, err := service.projectKnowledgeForTask(ctx, project.ID, domain.ProductLine{}, false); err == nil {
		t.Fatal("task project knowledge silently fell back to legacy")
	}
	if _, _, _, err := service.globalKnowledgeForTask(ctx, false); err == nil {
		t.Fatal("task global knowledge silently fell back to legacy")
	}
	if snapshot, ok := service.vaultKnowledgeForProject(ctx, project.ID); ok ||
		len(snapshot.Project)+len(snapshot.Global)+len(snapshot.Stale) != 0 {
		t.Fatalf("vaultKnowledgeForProject fell back to legacy: ok=%v snapshot=%#v", ok, snapshot)
	}
}

func TestApplicableAgentKnowledgeRoutesVaultAndLegacyProjects(t *testing.T) {
	fixture := newChannelKnowledgeFixture(t)
	vaultTask, vaultTurn := fixture.runningTurn(t, fixture.vaultProject, "workspace-channel-vault")
	items, err := fixture.service.ApplicableAgentKnowledge(fixture.ctx, agentapi.Claims{
		TaskID: vaultTask.ID, AgentID: vaultTurn.AgentID, TurnID: vaultTurn.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := knowledgeEntryIDs(items)
	if !ids["knowledge_pet_note"] || !ids["knowledge_shared"] {
		t.Fatalf("vault-bound agent knowledge missing vault entries: %#v", items)
	}
	if ids["knowledge_pet_legacy_only"] {
		t.Fatalf("agent API served a legacy row for a vault-bound project: %#v", items)
	}

	legacyTask, legacyTurn := fixture.runningTurn(t, fixture.legacyProject, "workspace-channel-legacy")
	items, err = fixture.service.ApplicableAgentKnowledge(fixture.ctx, agentapi.Claims{
		TaskID: legacyTask.ID, AgentID: legacyTurn.AgentID, TurnID: legacyTurn.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	ids = knowledgeEntryIDs(items)
	if !ids["knowledge_legacy_readable"] {
		t.Fatalf("unbound project did not read legacy knowledge: %#v", items)
	}
	// Global is gated independently, so the unbound project still gets the
	// vault global library instead of the legacy one.
	if !ids["knowledge_shared"] {
		t.Fatalf("unbound project did not use the vault global source: %#v", items)
	}
}

func TestChannelKnowledgeWithoutReadyIndexKeepsLegacyAndDoesNotPanic(t *testing.T) {
	fixture := newChannelKnowledgeFixture(t)
	// An enabled manager whose index has not been built yet has a nil index.
	// Channel knowledge must treat every ID as not-found in the vault and stay
	// on the legacy path instead of panicking.
	fixture.service.SetKnowledgeVault(buildVaultSkillManagerWithoutIndex(t), true)
	if _, found, err := fixture.service.vaultEntryByID(fixture.ctx, "knowledge_pet_legacy_only"); err != nil || found {
		t.Fatalf("unready vault lookup found=%v err=%v", found, err)
	}
	entries, err := fixture.service.channelAllowedKnowledge(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := knowledgeEntryIDs(entries)
	for _, want := range []string{"knowledge_pet_legacy_only", "knowledge_legacy_readable"} {
		if !ids[want] {
			t.Fatalf("unready index did not keep the legacy path for %s: %#v", want, entries)
		}
	}
}

func TestChannelKnowledgeRuntimeReadOffBypassesBrokenVaultProbe(t *testing.T) {
	fixture := newChannelKnowledgeFixture(t)
	readerErr := errors.New("index connection lost")
	// runtimeRead=off forces the legacy source. The resolver must not touch a
	// broken vault reader, otherwise a forced legacy read becomes an error.
	fixture.service.SetKnowledgeVault(failingKnowledgeVault{err: readerErr}, false)
	entries, err := fixture.service.channelAllowedKnowledge(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, "")
	if err != nil {
		t.Fatalf("runtimeRead=off channel read failed: %v", err)
	}
	if !knowledgeEntryIDs(entries)["knowledge_pet_legacy_only"] {
		t.Fatalf("runtimeRead=off did not use the legacy source: %#v", entries)
	}
	// With runtimeRead on the same broken reader is authoritative, so the
	// channel read must fail closed rather than serving the legacy rows.
	fixture.service.SetKnowledgeVault(failingKnowledgeVault{err: readerErr}, true)
	if _, err := fixture.service.channelAllowedKnowledge(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, ""); err == nil {
		t.Fatal("runtimeRead=on channel read silently fell back to legacy")
	}
}

func TestPetKnowledgeProposalsFollowTheReadSource(t *testing.T) {
	service, database, _, project := knowledgeRouteFixture(t, "project_other")
	ctx := context.Background()
	setKnowledgeAutoApprove(t, database, false)
	now := time.Now().UTC()
	root, err := database.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyEntry := domain.KnowledgeEntry{
		ID: "knowledge_pet_legacy_proposal_target", Scope: "project", ProjectID: project.ID, ParentID: root.ID,
		Slug: "legacy-proposal", Type: "practice", Title: "Legacy proposal", Body: "old proposal target",
		Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateKnowledge(ctx, legacyEntry); err != nil {
		t.Fatal(err)
	}
	legacyProposal, err := database.CreateKnowledgeProposal(ctx, domain.KnowledgeProposal{
		ID: "proposal-legacy-visible", EntryID: legacyEntry.ID, BaseRevision: legacyEntry.Revision, Proposed: legacyEntry,
		Status: domain.KnowledgeProposalPending, ReviewMode: "manual", TargetSource: domain.KnowledgeProposalTargetLegacy,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := service.PetSubmitKnowledge(ctx, "project", project.ID, []KnowledgeCandidate{{
		Type: "practice", Title: "Vault proposal", Body: vaultRouteBody(), Confidence: 0.9,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(submitted.Proposals) != 1 {
		t.Fatalf("vault proposals=%#v", submitted.Proposals)
	}
	view, err := service.PetListKnowledge(ctx, "project", project.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Proposals) != 1 || view.Proposals[0].ID != submitted.Proposals[0].ID {
		t.Fatalf("vault-routed project listed mixed proposals: %#v", view.Proposals)
	}
	if service.KnowledgeProposalVisible(legacyProposal) {
		t.Fatal("legacy proposal was visible for a vault-routed project")
	}

	// The reverse direction: an unbound project keeps the legacy proposal and
	// must not show a vault proposal it cannot publish.
	legacyService, legacyDatabase, _, legacyProject := knowledgeRouteFixture(t, "project_unbound_proposals")
	setKnowledgeAutoApprove(t, legacyDatabase, false)
	legacySubmitted, err := legacyService.PetSubmitKnowledge(ctx, "project", legacyProject.ID, []KnowledgeCandidate{{
		Type: "practice", Title: "Legacy proposal", Body: "unbound project keeps legacy proposals", Confidence: 0.9,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(legacySubmitted.Proposals) != 1 {
		t.Fatalf("legacy proposals=%#v", legacySubmitted.Proposals)
	}
	legacyView, err := legacyService.PetListKnowledge(ctx, "project", legacyProject.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyView.Proposals) != 1 || legacyView.Proposals[0].ID != legacySubmitted.Proposals[0].ID {
		t.Fatalf("unbound project did not list its legacy proposal: %#v", legacyView.Proposals)
	}
}

// A channel created while the vault is the read source has no policy. The read
// must be an explicit empty selection even though the vault and legacy tables
// both hold entries: no implicit grant and no generic ErrNoRows failure.
func TestChannelKnowledgeUnconfiguredVaultInstanceReturnsExplicitEmptySelection(t *testing.T) {
	fixture := newChannelKnowledgeFixtureWithoutPolicies(t)
	policies, err := fixture.database.ChannelKnowledgePolicies(fixture.ctx, fixture.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 0 {
		t.Fatalf("fixture unexpectedly seeded policies: %#v", policies)
	}
	for _, endpoint := range []string{domain.ChannelEndpointGroupDigitalHuman, domain.ChannelEndpointAssistantDM} {
		entries, err := fixture.service.channelAllowedKnowledge(fixture.ctx, fixture.instance.ID, endpoint, "")
		if err != nil {
			t.Fatalf("endpoint %s unconfigured read failed instead of returning an empty selection: %v", endpoint, err)
		}
		if len(entries) != 0 {
			t.Fatalf("endpoint %s unconfigured read granted %d entries: %#v", endpoint, len(entries), entries)
		}
	}
}

// The owner's first explicit vault-root selection creates the policy and is
// the only source of channel knowledge; other vault projects and legacy rows
// stay out of the selection.
func TestExplicitVaultRootSelectionConfiguresUnconfiguredChannel(t *testing.T) {
	fixture := newChannelKnowledgeFixtureWithoutPolicies(t)
	now := time.Now().UTC()
	vaultRootID := "knowledge_root_project_pet"
	vaultIDs := map[string]bool{vaultRootID: true}
	grants := []domain.ChannelKnowledgeGrant{{KnowledgeEntryID: vaultRootID, GrantScope: "subtree"}}
	if err := fixture.database.ReplaceChannelKnowledgeGrants(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, fixture.owner.ID, "selected", 0, grants, vaultIDs, now); err != nil {
		t.Fatal(err)
	}
	entries, err := fixture.service.channelAllowedKnowledge(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := knowledgeEntryIDs(entries)
	if !ids[vaultRootID] || !ids["knowledge_pet_note"] {
		t.Fatalf("explicit vault root did not grant its verified subtree: %#v", entries)
	}
	if ids["knowledge_legacy_readable"] || ids["knowledge_pet_legacy_only"] {
		t.Fatalf("explicit vault selection leaked legacy or unselected knowledge: %#v", entries)
	}
	// The private chat is the bound Owner and keeps the documented "always all"
	// rule once the channel has a policy; it still never serves the vault
	// project's legacy rows.
	dmEntries, err := fixture.service.channelAllowedKnowledge(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointAssistantDM, "")
	if err != nil {
		t.Fatal(err)
	}
	dmIDs := knowledgeEntryIDs(dmEntries)
	if !dmIDs["knowledge_pet_note"] || !dmIDs["knowledge_legacy_readable"] {
		t.Fatalf("private chat did not read all sources after configuration: %#v", dmEntries)
	}
	if dmIDs["knowledge_pet_legacy_only"] {
		t.Fatalf("private chat served a vault project's legacy row: %#v", dmEntries)
	}
	selection, err := fixture.database.ChannelKnowledgeSelection(fixture.ctx, fixture.instance.ID, domain.ChannelEndpointGroupDigitalHuman)
	if err != nil {
		t.Fatal(err)
	}
	if !selection.Configured || selection.FixedIndexEntryID != "" || selection.ScopeMode != "selected" {
		t.Fatalf("explicit selection stored an unexpected policy: %#v", selection)
	}
}
