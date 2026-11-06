package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

type petTaskFixture struct {
	ctx          context.Context
	database     *store.Store
	databasePath string
	service      *Service
	provider     domain.Provider
	model        domain.Model
	modelEnv     domain.EnvGroup
	createdAt    time.Time
}

func newPetTaskFixture(t *testing.T) *petTaskFixture {
	t.Helper()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	now := time.Now().UTC()
	provider := domain.Provider{
		ID: "provider-pet-task", Name: "Pet Provider", BaseURL: "https://example.test/v1",
		AuthStyle: "bearer", CredentialRef: "provider/pet-task/credential", CredentialConfigured: true,
		CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-pet-task", Name: "Pet Model", ProviderID: provider.ID, Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-pet-task", DisplayName: "Pet Model", ProviderID: provider.ID,
		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "pet", WireAPI: "chat",
		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.UpsertProvider(ctx, provider) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			database.Close()
			t.Fatal(err)
		}
	}
	fixture := &petTaskFixture{
		ctx: ctx, database: database, databasePath: databasePath,
		service:  NewService(database, secretStore, &stubExecutor{}),
		provider: provider, model: model, modelEnv: envGroup, createdAt: now,
	}
	t.Cleanup(func() {
		_ = fixture.service.Shutdown(context.Background())
		_ = database.Close()
	})
	return fixture
}

// createPetDraft creates the system pet project, workspace and a manual draft,
// matching a pet Task left behind by a previous version.
func (f *petTaskFixture) createPetDraft(t *testing.T, request string) domain.Task {
	t.Helper()
	project := domain.Project{
		ID: petProjectID, Name: "宠物", ProjectType: "system", KnowledgePolicy: "none",
		CreatedAt: f.createdAt, UpdatedAt: f.createdAt,
	}
	workspace := domain.Workspace{
		ID: petWorkspaceID, ProjectID: project.ID, Name: "pet-workspace",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		AgentAPIMode: "embedded", CreatedAt: f.createdAt, UpdatedAt: f.createdAt,
	}
	if err := f.database.CreateProject(f.ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := f.database.CreateWorkspace(f.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	task, err := f.service.CreateTask(f.ctx, CreateTaskInput{
		ID: petTaskID, ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "宠物", Request: request, ModelID: f.model.ID,
		Isolation: "inplace", Filesystem: "workspace-write", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
		AllowSystemProject: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (f *petTaskFixture) petTask(t *testing.T) domain.Task {
	t.Helper()
	task, err := f.database.Task(f.ctx, petTaskID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (f *petTaskFixture) turnCount(t *testing.T) int {
	t.Helper()
	count, err := f.database.TaskTurnCount(f.ctx, petTaskID)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestOwnerAssistantStartsNewPetTaskWithIntroduction(t *testing.T) {
	fixture := newPetTaskFixture(t)
	view, err := fixture.service.OwnerAssistant(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.PetTaskID != petTaskID {
		t.Fatalf("pet task id=%q", view.Assistant.PetTaskID)
	}
	task := fixture.petTask(t)
	if task.Status != domain.TaskActive {
		t.Fatalf("pet task status=%s", task.Status)
	}
	if task.OriginalRequest != petTaskIntro {
		t.Fatalf("request=%q, want the introduction", task.OriginalRequest)
	}
	if task.CurrentGoal != petTaskMissionGoal {
		t.Fatalf("goal=%q, want the mission goal", task.CurrentGoal)
	}
	if count := fixture.turnCount(t); count != 1 {
		t.Fatalf("turn count=%d", count)
	}
	turns, err := fixture.database.ListTurns(fixture.ctx, petTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || !strings.Contains(turns[0].Instruction, petTaskIntro) {
		t.Fatalf("first turn did not carry the introduction: %#v", turns)
	}
}

func TestOwnerAssistantMigratesPetRuntimeToOwnerDefaults(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, petTaskIntro)
	if _, err := fixture.service.UpdateAgentConfig(fixture.ctx, petTaskID, "main", UpdateAgentConfigInput{
		Filesystem: "read-only", Approval: "never", ReasoningEffort: "low",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	agents, err := fixture.service.TaskAgents(fixture.ctx, petTaskID)
	if err != nil || len(agents) == 0 {
		t.Fatalf("pet agents=%#v err=%v", agents, err)
	}
	if agents[0].Filesystem != "danger-full-access" || agents[0].Approval != "never" || agents[0].ReasoningEffort != "high" {
		t.Fatalf("legacy pet runtime was not migrated: %#v", agents[0])
	}
}

func TestUpdateOwnerPetSettingsCreatesEnvGroupForModelWithoutDefault(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, petTaskIntro)
	now := time.Now().UTC()
	provider := domain.Provider{
		ID: "provider-pet-no-env", Name: "No Env Provider", BaseURL: "https://no-env.example.test/v1",
		AuthStyle: "bearer", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertProvider(fixture.ctx, provider); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model-pet-no-env", DisplayName: "No Env Model", ProviderID: provider.ID,
		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "pet-no-env", WireAPI: "chat",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertModel(fixture.ctx, model); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: model.ID, Actions: []string{"walk"}, IdleMinutes: 5,
	}); err != nil {
		t.Fatalf("switch pet model without env group: %v", err)
	}
	stored, err := fixture.database.Model(fixture.ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DefaultEnvGroupID == "" {
		t.Fatal("default env group was not created")
	}
	group, err := fixture.database.EnvGroup(fixture.ctx, stored.DefaultEnvGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if group.ProviderID != provider.ID || group.Environment["OPENAI_MODEL"] != model.WireModel {
		t.Fatalf("unexpected env group: %#v", group)
	}
	agent, err := fixture.database.TaskAgent(fixture.ctx, petTaskID, "main")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.database.RuntimeSnapshot(fixture.ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ModelID != model.ID || snapshot.EnvGroupID != stored.DefaultEnvGroupID {
		t.Fatalf("pet runtime was not switched: %#v", snapshot)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: model.ID, Actions: []string{"walk"}, IdleMinutes: 5,
	}); err != nil {
		t.Fatalf("repeat pet model switch: %v", err)
	}
	again, err := fixture.database.Model(fixture.ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.DefaultEnvGroupID != stored.DefaultEnvGroupID {
		t.Fatalf("repeat switch created another env group: %q -> %q", stored.DefaultEnvGroupID, again.DefaultEnvGroupID)
	}
}

func TestUpdateOwnerPetSettingsSwitchesModelBackend(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, petTaskIntro)
	now := time.Now().UTC()
	provider := domain.Provider{
		ID: "provider-pet-claude", Name: "Claude Provider", BaseURL: "https://claude.example.test",
		AnthropicBaseURL: "https://claude.example.test", AuthStyle: "x-api-key",
		CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-pet-claude", DisplayName: "Claude Pet", ProviderID: provider.ID,
		Source: domain.ModelSourceProvider, Backend: "claude", WireModel: "claude-pet", WireAPI: "anthropic_messages",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertProvider(fixture.ctx, provider); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpsertModel(fixture.ctx, model); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: model.ID, Actions: []string{"walk"}, IdleMinutes: 5,
	}); err != nil {
		t.Fatalf("switch pet model backend: %v", err)
	}
	agent, err := fixture.database.TaskAgent(fixture.ctx, petTaskID, "main")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.database.RuntimeSnapshot(fixture.ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Backend != "claude" || snapshot.ModelID != model.ID {
		t.Fatalf("pet runtime did not switch backend: %#v", snapshot)
	}
	group, err := fixture.database.EnvGroup(fixture.ctx, snapshot.EnvGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if group.Environment["ANTHROPIC_MODEL"] != model.WireModel ||
		group.Environment["ANTHROPIC_BASE_URL"] != provider.AnthropicBaseURL {
		t.Fatalf("unexpected claude env group: %#v", group)
	}
}

func TestOwnerAssistantStartsLegacyPetDraftWithIntroduction(t *testing.T) {
	fixture := newPetTaskFixture(t)
	const legacyRequest = "你是宠物，不是编码代理。只和主人对话……"
	fixture.createPetDraft(t, legacyRequest)

	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	task := fixture.petTask(t)
	if task.Status != domain.TaskActive {
		t.Fatalf("pet task status=%s", task.Status)
	}
	if task.OriginalRequest != petTaskIntro {
		t.Fatalf("request=%q, want the introduction", task.OriginalRequest)
	}
	// The intro-only migration must not rewrite a goal that was never the
	// introduction, even for a legacy draft that is being started now.
	if task.CurrentGoal != legacyRequest {
		t.Fatalf("goal=%q, want the untouched legacy goal", task.CurrentGoal)
	}
	if count := fixture.turnCount(t); count != 1 {
		t.Fatalf("turn count=%d", count)
	}
}

func TestOwnerAssistantStartsWaitingPetTaskWithNoTurns(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "旧的宠物需求")
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	now := fixture.service.now().UTC()
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, petTaskID, domain.TaskActive, domain.TaskWaitingUser, timeString(now), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
	task := fixture.petTask(t)
	if task.OriginalRequest != petTaskIntro {
		t.Fatalf("request=%q", task.OriginalRequest)
	}
	if count := fixture.turnCount(t); count != 1 {
		t.Fatalf("turn count=%d", count)
	}
}

func TestOwnerAssistantLeavesStartedPetTaskAlone(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "旧的宠物需求")
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "主人第一条消息"); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	task := fixture.petTask(t)
	if task.OriginalRequest != "旧的宠物需求" {
		t.Fatalf("started pet task request was rewritten: %q", task.OriginalRequest)
	}
	if count := fixture.turnCount(t); count != 1 {
		t.Fatalf("turn count=%d", count)
	}
}

func TestOwnerAssistantMigratesIntroPetGoalToMission(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, petTaskIntro)
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "主人第一条消息"); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)

	// Two page loads: the first migrates, the second must be a no-op.
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
			t.Fatal(err)
		}
		task := fixture.petTask(t)
		if task.CurrentGoal != petTaskMissionGoal {
			t.Fatalf("attempt %d goal=%q, want the mission goal", attempt, task.CurrentGoal)
		}
		if task.OriginalRequest != petTaskIntro {
			t.Fatalf("attempt %d request=%q, want the introduction", attempt, task.OriginalRequest)
		}
		if count := fixture.turnCount(t); count != 1 {
			t.Fatalf("attempt %d turn count=%d", attempt, count)
		}
		memory, err := fixture.database.TaskMemory(fixture.ctx, petTaskID)
		if err != nil {
			t.Fatal(err)
		}
		if memory.CurrentGoal != petTaskMissionGoal {
			t.Fatalf("attempt %d memory goal=%q, want the mission goal", attempt, memory.CurrentGoal)
		}
		history, err := fixture.database.TaskGoalHistory(fixture.ctx, petTaskID, 20)
		if err != nil {
			t.Fatal(err)
		}
		migrations := 0
		for _, event := range history {
			if event.ChangeType == "revise" && event.GoalAfter == petTaskMissionGoal {
				migrations++
			}
		}
		if migrations != 1 {
			t.Fatalf("attempt %d migration events=%d, want 1", attempt, migrations)
		}
	}
}

func TestPetTaskMessageMigratesIntroGoal(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, petTaskIntro)
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "主人直接发消息"); err != nil {
		t.Fatal(err)
	}
	if goal := fixture.petTask(t).CurrentGoal; goal != petTaskMissionGoal {
		t.Fatalf("goal=%q, want the mission goal", goal)
	}
}

func TestOwnerAssistantMigratesLegacyMissionPetGoalToPetLabGoal(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, petTaskLegacyMissionGoal)
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "主人第一条消息"); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if goal := fixture.petTask(t).CurrentGoal; goal != petTaskMissionGoal {
		t.Fatalf("legacy mission goal=%q, want the pet-lab mission goal", goal)
	}
}

func TestOwnerAssistantMigratesLegacyIntroPetGoalToMission(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, petTaskLegacyIntro)
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "主人第一条消息"); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if goal := fixture.petTask(t).CurrentGoal; goal != petTaskMissionGoal {
		t.Fatalf("legacy intro goal=%q, want the mission goal", goal)
	}
}

func TestOwnerAssistantLeavesCustomPetGoalAlone(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "旧的宠物需求")
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "主人第一条消息"); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)

	const customGoal = "只负责整理主人的周报。"
	if err := fixture.database.UpdateTaskGoal(fixture.ctx, petTaskID, customGoal, timeString(fixture.service.now().UTC())); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if goal := fixture.petTask(t).CurrentGoal; goal != customGoal {
		t.Fatalf("custom goal was overwritten: %q", goal)
	}
	history, err := fixture.database.TaskGoalHistory(fixture.ctx, petTaskID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range history {
		if event.GoalAfter == petTaskMissionGoal {
			t.Fatalf("custom goal task received a mission goal event")
		}
	}
}

func TestOwnerAssistantReopensFailedPetTask(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "旧的宠物需求")
	if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "主人第一条消息"); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)

	now := fixture.service.now().UTC()
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, petTaskID, domain.TaskWaitingUser, domain.TaskActive, timeString(now), ""); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateTaskStatus(fixture.ctx, petTaskID, domain.TaskActive, domain.TaskFailed, timeString(now), ""); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	task := fixture.petTask(t)
	if task.Status != domain.TaskWaitingUser {
		t.Fatalf("failed pet task was not reopened: %s", task.Status)
	}
	if count := fixture.turnCount(t); count != 1 {
		t.Fatalf("reopen duplicated the introduction: turn count=%d", count)
	}
}

func TestPetModelSettingsSyncToThePetTask(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
	alternate := fixture.model
	alternate.ID = "model-pet-task-alternate"
	alternate.DisplayName = "Pet Model Alternate"
	alternate.WireModel = "pet-alt"
	alternate.UpdatedAt = fixture.service.now().UTC()
	if err := fixture.database.UpsertModel(fixture.ctx, alternate); err != nil {
		t.Fatal(err)
	}

	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: alternate.ID, Actions: []string{"walk"},
		IdleMinutes: 20, QuietStart: 22, QuietEnd: 8, HourlyLimit: 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.ModelID != alternate.ID || view.Assistant.BrainModelID != alternate.ID {
		t.Fatalf("assistant model was not updated: %#v", view.Assistant)
	}
	agent, err := fixture.database.TaskAgent(fixture.ctx, petTaskID, "main")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.database.RuntimeSnapshot(fixture.ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ModelID != alternate.ID {
		t.Fatalf("pet task runtime model=%s, want %s", snapshot.ModelID, alternate.ID)
	}
}

func TestOwnerAssistantReopensTerminalPetTaskStates(t *testing.T) {
	cases := []struct {
		name   string
		status domain.TaskStatus
		apply  func(t *testing.T, fixture *petTaskFixture, now time.Time)
	}{
		{name: "failed", status: domain.TaskFailed, apply: func(t *testing.T, fixture *petTaskFixture, now time.Time) {
			if err := fixture.database.UpdateTaskStatus(fixture.ctx, petTaskID, domain.TaskWaitingUser, domain.TaskActive, timeString(now), ""); err != nil {
				t.Fatal(err)
			}
			if err := fixture.database.UpdateTaskStatus(fixture.ctx, petTaskID, domain.TaskActive, domain.TaskFailed, timeString(now), ""); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "blocked", status: domain.TaskBlocked, apply: func(t *testing.T, fixture *petTaskFixture, now time.Time) {
			if err := fixture.database.UpdateTaskStatus(fixture.ctx, petTaskID, domain.TaskWaitingUser, domain.TaskActive, timeString(now), ""); err != nil {
				t.Fatal(err)
			}
			if err := fixture.database.UpdateTaskStatus(fixture.ctx, petTaskID, domain.TaskActive, domain.TaskBlocked, timeString(now), ""); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "completed", status: domain.TaskCompleted, apply: func(t *testing.T, fixture *petTaskFixture, now time.Time) {
			if err := fixture.database.UpdateTaskStatus(fixture.ctx, petTaskID, domain.TaskWaitingUser, domain.TaskCompleted, timeString(now), timeString(now)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "cancelled", status: domain.TaskCancelled, apply: func(t *testing.T, fixture *petTaskFixture, now time.Time) {
			if err := fixture.database.UpdateTaskStatus(fixture.ctx, petTaskID, domain.TaskWaitingUser, domain.TaskCancelled, timeString(now), ""); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newPetTaskFixture(t)
			fixture.createPetDraft(t, "旧的宠物需求")
			if _, err := fixture.service.StartTask(fixture.ctx, petTaskID); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.service.SubmitMessage(fixture.ctx, petTaskID, "主人第一条消息"); err != nil {
				t.Fatal(err)
			}
			waitForTask(t, fixture.database, petTaskID, domain.TaskWaitingUser)
			testCase.apply(t, fixture, fixture.service.now().UTC())
			if task := fixture.petTask(t); task.Status != testCase.status {
				t.Fatalf("setup status=%s", task.Status)
			}
			if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
				t.Fatal(err)
			}
			task := fixture.petTask(t)
			if task.Status != domain.TaskWaitingUser {
				t.Fatalf("status=%s, want waiting_user", task.Status)
			}
			if count := fixture.turnCount(t); count != 1 {
				t.Fatalf("reopen changed the turn count: %d", count)
			}
		})
	}
}

func TestOwnerAssistantRemovesGeneratedPetWorkspaceGuides(t *testing.T) {
	fixture := newPetTaskFixture(t)
	root := filepath.Join(fixture.database.DataDir(), "pet-workspace")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(petWorkspaceGuides[0]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("generated guide %s was not removed: %v", name, err)
		}
	}

	custom := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(custom, []byte("owner-customized guide"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(custom); err != nil || string(data) != "owner-customized guide" {
		t.Fatalf("customized guide was removed or changed: %q %v", string(data), err)
	}
}

func TestOwnerAssistantSeedsPetCoreFilesWithoutOverwriting(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(fixture.database.DataDir(), "pet-workspace")
	identityPath := filepath.Join(root, "core", "identity.md")
	principlesPath := filepath.Join(root, "core", "principles.md")
	for _, path := range []string{identityPath, principlesPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("core file %s: %v", path, err)
		}
		if !strings.Contains(string(data), "#") {
			t.Fatalf("core file %s is not a markdown template: %q", path, string(data))
		}
	}
	if _, err := os.Stat(filepath.Join(root, "diary")); err != nil {
		t.Fatalf("diary directory: %v", err)
	}

	custom := "# 身份\n\n我叫小石头，是一只慢慢悠悠的乌龟。\n"
	if err := os.WriteFile(identityPath, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != custom {
		t.Fatalf("existing core identity was overwritten: %q", string(data))
	}

	if err := os.Remove(principlesPath); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(principlesPath); err != nil || !strings.Contains(string(data), "#") {
		t.Fatalf("missing core principles were not reseeded: %q %v", string(data), err)
	}
}

func TestPetTaskPromptUsesCorePathsInsteadOfPersona(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	rendered, err := fixture.service.prompts.RenderTemplate(fixture.ctx, "identity.pet-task", petPromptData{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"core/identity.md", "core/principles.md", "core/loops.md", "core/agenda.md", "diary/YYYY-MM-DD.md"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("pet prompt does not point at %s:\n%s", want, rendered)
		}
	}
	for _, unwanted := range []string{"咕咕嘎嘎", "大肥鱼", "你的职责"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("pet prompt still embeds persona %q:\n%s", unwanted, rendered)
		}
	}
}

func TestOwnerAssistantRollsFailedPetStartBackToDraft(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "旧的宠物需求")
	if err := fixture.database.DeleteProvider(fixture.ctx, fixture.provider.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	task := fixture.petTask(t)
	if task.Status != domain.TaskDraft {
		t.Fatalf("failed start left status=%s", task.Status)
	}
	if count := fixture.turnCount(t); count != 0 {
		t.Fatalf("turn count=%d", count)
	}
	if task.OriginalRequest != petTaskIntro {
		t.Fatalf("request=%q", task.OriginalRequest)
	}
}
