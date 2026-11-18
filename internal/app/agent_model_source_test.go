package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type agentSourceFixture struct {
	ctx      context.Context
	store    *store.Store
	service  *Service
	task     domain.Task
	provider domain.Model
	official string
	account  string
}

func newAgentSourceFixture(t *testing.T) *agentSourceFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	project := domain.Project{ID: "project-source", Name: "Source", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-source", ProjectID: project.ID, Name: "Source", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	provider := domain.Provider{
		ID: "provider-source", Name: "Gateway", BaseURL: "https://gateway.test/v1",
		AuthStyle: "bearer", CredentialRef: "provider/source", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-source", Name: "Gateway / gpt-env", ProviderID: provider.ID, Backend: "codex", Revision: 1,
		Environment: map[string]string{"OPENAI_MODEL": "gpt-env"}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-source-env", DisplayName: "Env", ProviderID: provider.ID, Source: domain.ModelSourceProvider,
		Backend: "codex", WireModel: "gpt-env", WireAPI: "responses", DefaultEnvGroupID: envGroup.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	account := domain.CodexAccount{
		ID: "account-source", Label: "Official", Status: "ready", CredentialRef: "codex/source",
		CredentialConfigured: true, AvailableModels: []domain.CodexModelOption{{
			WireModel: "gpt-official", DisplayName: "GPT Official", DefaultEffort: "high",
		}}, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertProvider(ctx, provider) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
		func() error { return database.UpsertCodexAccount(ctx, account) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(database, nil, &stubExecutor{})
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Source switch", Request: "test",
		Isolation: "inplace", Backend: "codex", ModelSource: domain.ModelSourceProvider, ModelID: model.ID,
		ReasoningEffort: "high", Filesystem: "workspace-write", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	task, err = database.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &agentSourceFixture{
		ctx: ctx, store: database, service: service, task: task,
		provider: model, official: "gpt-official", account: account.ID,
	}
}

func TestApplyAgentConfigSwitchesProviderAndOfficialSources(t *testing.T) {
	fixture := newAgentSourceFixture(t)

	official, err := fixture.service.ApplyAgentConfig(fixture.ctx, fixture.task.ID, "main", UpdateAgentConfigInput{
		ModelSource: domain.ModelSourceOfficial, CodexAccountID: fixture.account, WireModel: fixture.official,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !official.Changed || official.Status != "updated" {
		t.Fatalf("official switch was not reported as an update: %#v", official)
	}
	if official.Agent.ModelSource != domain.ModelSourceOfficial || official.Agent.CodexAccountID != fixture.account ||
		official.Agent.WireModel != fixture.official || official.Agent.Backend != "codex" {
		t.Fatalf("agent did not switch to the official source: %#v", official.Agent)
	}
	if official.Previous.ModelSource != domain.ModelSourceProvider || official.Current.ModelSource != domain.ModelSourceOfficial {
		t.Fatalf("audit summary did not capture the source change: %#v", official)
	}
	assertSnapshotSource(t, fixture.store, official.Agent.RuntimeConfigSnapshotID, domain.ModelSourceOfficial, fixture.official)
	task, err := fixture.store.Task(fixture.ctx, fixture.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.RuntimeConfigSnapshotID != official.Agent.RuntimeConfigSnapshotID {
		t.Fatalf("Task did not adopt the new snapshot: task=%s agent=%s", task.RuntimeConfigSnapshotID, official.Agent.RuntimeConfigSnapshotID)
	}
	turn, err := fixture.service.SubmitMessage(fixture.ctx, fixture.task.ID, "next turn must use the official source")
	if err != nil {
		t.Fatal(err)
	}
	if turn.RuntimeConfigSnapshotID != official.Agent.RuntimeConfigSnapshotID {
		t.Fatalf("next Turn did not use the official snapshot: turn=%s want=%s", turn.RuntimeConfigSnapshotID, official.Agent.RuntimeConfigSnapshotID)
	}
	waitForTurnTerminal(t, fixture.store, turn.ID)

	provider, err := fixture.service.ApplyAgentConfig(fixture.ctx, fixture.task.ID, "main", UpdateAgentConfigInput{
		ModelSource: domain.ModelSourceProvider, ModelID: fixture.provider.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !provider.Changed || provider.Agent.ModelSource != domain.ModelSourceProvider ||
		provider.Agent.ModelID != fixture.provider.ID || provider.Agent.CodexAccountID != "" {
		t.Fatalf("agent did not switch back to the provider source: %#v", provider.Agent)
	}
	assertSnapshotSource(t, fixture.store, provider.Agent.RuntimeConfigSnapshotID, domain.ModelSourceProvider, fixture.provider.WireModel)

	// Legacy callers may omit model_source. When the model_id is a managed
	// catalog entry the source is inferred instead of silently keeping provider.
	inferred, err := fixture.service.ApplyAgentConfig(fixture.ctx, fixture.task.ID, "main", UpdateAgentConfigInput{
		ModelID: official.Agent.ModelID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !inferred.Changed || inferred.Agent.ModelSource != domain.ModelSourceOfficial {
		t.Fatalf("managed model_id did not infer the official source: %#v", inferred.Agent)
	}

	// Repeating the exact same request must be reported as unchanged, not as a
	// second write.
	unchanged, err := fixture.service.ApplyAgentConfig(fixture.ctx, fixture.task.ID, "main", UpdateAgentConfigInput{
		ModelID: official.Agent.ModelID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Changed || unchanged.Status != "unchanged" {
		t.Fatalf("repeated request was not reported as unchanged: %#v", unchanged)
	}
	if unchanged.Agent.RuntimeConfigSnapshotID != inferred.Agent.RuntimeConfigSnapshotID {
		t.Fatalf("unchanged request minted a new snapshot: %s -> %s", inferred.Agent.RuntimeConfigSnapshotID, unchanged.Agent.RuntimeConfigSnapshotID)
	}
}

func TestApplyAgentConfigRejectsInvalidSourceCombinations(t *testing.T) {
	fixture := newAgentSourceFixture(t)
	official, err := fixture.service.ApplyAgentConfig(fixture.ctx, fixture.task.ID, "main", UpdateAgentConfigInput{
		ModelSource: domain.ModelSourceOfficial, CodexAccountID: fixture.account, WireModel: fixture.official,
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		input  UpdateAgentConfigInput
		code   string
		status int
	}{
		{
			name:  "unknown source",
			input: UpdateAgentConfigInput{ModelSource: "mystery", ModelID: fixture.provider.ID},
			code:  ErrCodeModelSourceInvalid,
		},
		{
			name:  "provider source with official model",
			input: UpdateAgentConfigInput{ModelSource: domain.ModelSourceProvider, ModelID: official.Agent.ModelID},
			code:  ErrCodeModelSourceMismatch,
		},
		{
			name:  "official source with provider model",
			input: UpdateAgentConfigInput{ModelSource: domain.ModelSourceOfficial, ModelID: fixture.provider.ID, WireModel: fixture.official, CodexAccountID: fixture.account},
			code:  ErrCodeModelSourceMismatch,
		},
		{
			name:  "official model outside account catalog",
			input: UpdateAgentConfigInput{ModelSource: domain.ModelSourceOfficial, WireModel: "gpt-missing", CodexAccountID: fixture.account},
			code:  ErrCodeModelSelectionInvalid,
		},
		{
			name:  "provider source without model",
			input: UpdateAgentConfigInput{ModelSource: domain.ModelSourceProvider},
			code:  ErrCodeModelRequired,
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			_, err := fixture.service.ApplyAgentConfig(fixture.ctx, fixture.task.ID, "main", item.input)
			if err == nil {
				t.Fatal("expected a rejection")
			}
			coded, ok := AsCodedError(err)
			if !ok || coded.Code != item.code {
				t.Fatalf("error = %v, want code %s", err, item.code)
			}
			if coded.Status < 400 || coded.Status >= 500 {
				t.Fatalf("status = %d, want 4xx", coded.Status)
			}
		})
	}
}

func waitForTurnTerminal(t *testing.T, database *store.Store, turnID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := database.Turn(context.Background(), turnID)
		if err == nil && (turn.Status == domain.TurnSucceeded || turn.Status == domain.TurnFailed || turn.Status == domain.TurnInterrupted) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	turn, _ := database.Turn(context.Background(), turnID)
	t.Fatalf("turn did not reach a terminal state: %#v", turn)
}

func assertSnapshotSource(t *testing.T, database *store.Store, snapshotID, wantSource, wantWire string) {
	t.Helper()
	snapshot, err := database.RuntimeSnapshot(context.Background(), snapshotID)
	if err != nil {
		t.Fatal(err)
	}
	source := domain.RuntimeModelSource(snapshot.EnvGroupID, snapshot.CodexAccountID)
	if source != wantSource || snapshot.WireModel != wantWire {
		t.Fatalf("snapshot source=%s wire=%s, want %s/%s: %#v", source, snapshot.WireModel, wantSource, wantWire, snapshot)
	}
}
