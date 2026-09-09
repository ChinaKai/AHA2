package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestCreateTaskSelectsOfficialCodexModelFromAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if err := database.CreateProject(ctx, domain.Project{
		ID: "project-official", Name: "Official", ProjectType: "folder", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, domain.Workspace{
		ID: "workspace-official", ProjectID: "project-official", Name: "Official", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertCodexAccount(ctx, domain.CodexAccount{
		ID: "account-official", Label: "Work", Status: "ready", CredentialRef: "codex/account/auth",
		CredentialConfigured: true, AvailableModels: []domain.CodexModelOption{{
			WireModel: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol", MaxContextWindow: 1050000,
			DefaultEffort: "high", ReasoningEfforts: []string{"medium", "high"},
		}}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, nil)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: "project-official", WorkspaceID: "workspace-official", Title: "Official", Request: "test",
		Isolation: "inplace", Backend: "codex", ModelSource: domain.ModelSourceOfficial,
		CodexAccountID: "account-official", WireModel: "gpt-5.6-sol",
		ReasoningEffort: "high", Filesystem: "workspace-write", Approval: "never",
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.RuntimeSnapshot(ctx, task.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CodexAccountID != "account-official" || snapshot.WireModel != "gpt-5.6-sol" || snapshot.Backend != "codex" {
		t.Fatalf("unexpected official runtime snapshot: %#v", snapshot)
	}
	model, err := database.Model(ctx, snapshot.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	if model.Source != domain.ModelSourceOfficial || model.CodexAccountID != "" || model.ContextWindow != 1050000 {
		t.Fatalf("unexpected hidden runtime model: %#v", model)
	}
}

func TestOfficialCodexSelectionRejectsModelOutsideAccountCatalog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if err := database.UpsertCodexAccount(ctx, domain.CodexAccount{
		ID: "account-limited", Status: "ready", CredentialRef: "codex/account/auth", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-5.6-sol"}}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, nil)
	if _, _, _, err := service.resolveRuntimeSelection(ctx, runtimeSelectionInput{
		Backend: "codex", ModelSource: domain.ModelSourceOfficial,
		CodexAccountID: "account-limited", WireModel: "gpt-not-available",
	}); err == nil {
		t.Fatal("expected unavailable account model to be rejected")
	}
}

func TestUpdateAgentConfigSwitchesOfficialToLegacySyncedEnvModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-switch-env", Name: "Switch env", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-switch-env", ProjectID: project.ID, Name: "Switch env", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	account := domain.CodexAccount{
		ID: "account-switch-env", Status: "ready", CredentialRef: "codex/account/auth", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-official", DisplayName: "Official"}}, CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-switch-env", Name: "Gateway / gpt-env", ProviderID: "gateway", Backend: "codex", Revision: 3,
		Environment: map[string]string{"OPENAI_MODEL": "gpt-env"}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	// Legacy sync payloads omitted this association even though the matching env
	// group was present on the receiving device.
	envModel := domain.Model{
		ID: "model-switch-env", DisplayName: "Env", ProviderID: "gateway", Source: domain.ModelSourceProvider,
		Backend: "codex", WireModel: "gpt-env", CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertCodexAccount(ctx, account) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, envModel) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(database, nil, nil)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Official", Request: "test", Isolation: "inplace",
		Backend: "codex", ModelSource: domain.ModelSourceOfficial, CodexAccountID: account.ID, WireModel: "gpt-official",
		ReasoningEffort: "high", Filesystem: "workspace-write", Approval: "never",
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := service.UpdateAgentConfig(ctx, task.ID, "main", UpdateAgentConfigInput{
		Backend: "codex", ModelSource: "env", ModelID: envModel.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.ModelSource != domain.ModelSourceProvider || agent.ModelID != envModel.ID || agent.CodexAccountID != "" {
		t.Fatalf("agent did not switch to env runtime: %#v", agent)
	}
	snapshot, err := database.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EnvGroupID != envGroup.ID || snapshot.EnvGroupRevision != envGroup.Revision || snapshot.CodexAccountID != "" {
		t.Fatalf("unexpected env runtime snapshot: %#v", snapshot)
	}
	repaired, err := database.Model(ctx, envModel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.DefaultEnvGroupID != envGroup.ID {
		t.Fatalf("default env group was not repaired: %#v", repaired)
	}
}

func TestEnvModelRepairRejectsGroupConfiguredForDifferentModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	group := domain.EnvGroup{
		ID: "env-other-model", Name: "Gateway / other", ProviderID: "gateway", Backend: "codex", Revision: 1,
		Environment: map[string]string{"OPENAI_MODEL": "other-model"}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-missing-group", DisplayName: "Requested", ProviderID: "gateway", Backend: "codex",
		WireModel: "requested-model", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, nil)
	if _, _, _, err := service.resolveRuntimeSelection(ctx, runtimeSelectionInput{
		Backend: "codex", ModelSource: "env", ModelID: model.ID,
	}); err == nil {
		t.Fatal("group configured for a different wire model was accepted")
	}
	stored, err := database.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DefaultEnvGroupID != "" {
		t.Fatalf("mismatched group was persisted as the default: %#v", stored)
	}
}
