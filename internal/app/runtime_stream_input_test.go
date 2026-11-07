package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// Stream input is a per-Task runtime choice frozen into the snapshot each Turn
// reads. Toggling it must create a new snapshot without rewriting the one a
// running or historical Turn already holds, and an edit that does not mention
// it must not silently clear it.
func TestUpdateAgentConfigCarriesClaudeStreamInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-stream", Name: "Stream", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-stream", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-stream", Name: "claude", ProviderID: "stub", Backend: "claude", Revision: 1,
		Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-stream", DisplayName: "Claude", ProviderID: "stub", Backend: "claude",
		WireModel: "claude-sonnet", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(database, nil, nil)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "stream", Request: "run",
		ModelID: model.ID, Backend: "claude", Isolation: "inplace",
	})
	if err != nil {
		t.Fatal(err)
	}
	mainAgent := func() domain.TaskAgent {
		t.Helper()
		agents, listErr := service.TaskAgents(ctx, task.ID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, agent := range agents {
			if agent.AgentID == "main" {
				return agent
			}
		}
		t.Fatalf("main agent missing: %#v", agents)
		return domain.TaskAgent{}
	}
	first := mainAgent()
	if first.StreamInput {
		t.Fatalf("stream input defaulted on at create: %#v", first)
	}
	// A running Turn keeps the old snapshot even after the Task is switched on.
	on := true
	edited, err := service.UpdateAgentConfig(ctx, task.ID, "main", UpdateAgentConfigInput{StreamInput: &on})
	if err != nil {
		t.Fatal(err)
	}
	if !edited.StreamInput {
		t.Fatalf("stream_input=true was ignored: %#v", edited)
	}
	if edited.RuntimeConfigSnapshotID == first.RuntimeConfigSnapshotID {
		t.Fatal("toggling stream input reused the old snapshot instead of freezing a new Turn")
	}
	oldSnapshot, err := database.RuntimeSnapshot(ctx, first.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if oldSnapshot.StreamInput {
		t.Fatalf("the previous Turn's snapshot was rewritten: %#v", oldSnapshot)
	}
	snapshot, err := database.RuntimeSnapshot(ctx, edited.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.StreamInput {
		t.Fatalf("stored snapshot lost stream input: %#v", snapshot)
	}
	// An edit that does not mention the switch keeps it.
	edited, err = service.UpdateAgentConfig(ctx, task.ID, "main", UpdateAgentConfigInput{ReasoningEffort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if !edited.StreamInput {
		t.Fatalf("edit without stream_input cleared it: %#v", edited)
	}
	// An explicit false is obeyed.
	off := false
	edited, err = service.UpdateAgentConfig(ctx, task.ID, "main", UpdateAgentConfigInput{StreamInput: &off})
	if err != nil {
		t.Fatal(err)
	}
	if edited.StreamInput {
		t.Fatalf("explicit stream_input=false was ignored: %#v", edited)
	}
	if snapshot, err = database.RuntimeSnapshot(ctx, edited.RuntimeConfigSnapshotID); err != nil || snapshot.StreamInput {
		t.Fatalf("stored snapshot kept stream input: %#v %v", snapshot, err)
	}
	// The off state is a real state, not the absence of an edit. An edit that
	// does not mention the switch must keep it off instead of inheriting a
	// hard-coded default.
	edited, err = service.UpdateAgentConfig(ctx, task.ID, "main", UpdateAgentConfigInput{ReasoningEffort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	if edited.StreamInput {
		t.Fatalf("edit without stream_input turned an off switch on: %#v", edited)
	}
	if snapshot, err = database.RuntimeSnapshot(ctx, edited.RuntimeConfigSnapshotID); err != nil || snapshot.StreamInput {
		t.Fatalf("stored snapshot turned the off switch on: %#v %v", snapshot, err)
	}
}

// Creation may set the per-Task switch directly, which is what a pet does when
// it creates a disposable Claude test Task.
func TestCreateTaskCarriesClaudeStreamInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-stream-create", Name: "Stream", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-stream-create", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-stream-create", Name: "claude", ProviderID: "stub", Backend: "claude", Revision: 1,
		Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-stream-create", DisplayName: "Claude", ProviderID: "stub", Backend: "claude",
		WireModel: "claude-sonnet", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(database, nil, nil)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "stream", Request: "run",
		ModelID: model.ID, Backend: "claude", Isolation: "inplace", StreamInput: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.RuntimeSnapshot(ctx, task.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.StreamInput {
		t.Fatalf("CreateTask dropped stream input: %#v", snapshot)
	}
}

// The pet creates a disposable Claude test Task and then flips the experiment
// through aha_update_task_agent, so that input path must carry the field too.
func TestPetUpdateTaskAgentCarriesClaudeStreamInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-pet-stream", Name: "Stream", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-pet-stream", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-pet-stream", Name: "claude", ProviderID: "stub", Backend: "claude", Revision: 1,
		Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-pet-stream", DisplayName: "Claude", ProviderID: "stub", Backend: "claude",
		WireModel: "claude-sonnet", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	assistant, err := database.EnsureOwnerAssistant(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateOwnerPetPermissions(ctx, assistant.ID, false, true, false, false, false, false, false, false, now); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, nil)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "pet stream", Request: "run",
		ModelID: model.ID, Backend: "claude", Isolation: "inplace",
	})
	if err != nil {
		t.Fatal(err)
	}
	on := true
	agent, err := service.PetUpdateTaskAgent(ctx, PetUpdateTaskAgentInput{TaskID: task.ID, StreamInput: &on})
	if err != nil {
		t.Fatal(err)
	}
	if !agent.StreamInput {
		t.Fatalf("aha_update_task_agent dropped stream_input: %#v", agent)
	}
	snapshot, err := database.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.StreamInput {
		t.Fatalf("stored snapshot lost the pet's stream_input: %#v", snapshot)
	}
}
