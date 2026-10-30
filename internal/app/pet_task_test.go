package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

type petTaskFixture struct {
	ctx       context.Context
	database  *store.Store
	service   *Service
	provider  domain.Provider
	model     domain.Model
	modelEnv  domain.EnvGroup
	createdAt time.Time
}

func newPetTaskFixture(t *testing.T) *petTaskFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
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
		ctx: ctx, database: database, service: NewService(database, secretStore, &stubExecutor{}),
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
	if task.OriginalRequest != petTaskIntro || task.CurrentGoal != petTaskIntro {
		t.Fatalf("request=%q goal=%q", task.OriginalRequest, task.CurrentGoal)
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

func TestOwnerAssistantStartsLegacyPetDraftWithIntroduction(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "你是宠物，不是编码代理。只和主人对话……")

	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	task := fixture.petTask(t)
	if task.Status != domain.TaskActive {
		t.Fatalf("pet task status=%s", task.Status)
	}
	if task.OriginalRequest != petTaskIntro || task.CurrentGoal != petTaskIntro {
		t.Fatalf("request=%q goal=%q", task.OriginalRequest, task.CurrentGoal)
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
