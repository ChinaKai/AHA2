package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// The accelerated tier is a runtime choice, so it has to survive an edit that
// does not mention it: a Task created with Fast must not lose it when the
// Agent's model, effort or sandbox is changed later. An edit that does mention
// it still wins, including turning it off.
func TestUpdateAgentConfigCarriesTheAcceleratedTier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-fast-tier", Name: "Fast", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-fast-tier", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-fast-tier", Name: "codex", ProviderID: "stub", Backend: "codex", Revision: 1,
		Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-fast-tier", DisplayName: "Codex", ProviderID: "stub", Backend: "codex",
		WireModel: "gpt-5.6-sol", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
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
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "fast", Request: "run fast",
		ModelID: model.ID, Backend: "codex", Isolation: "inplace", FastMode: true,
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
	if main := mainAgent(); !main.FastMode {
		t.Fatalf("CreateTask dropped the created tier: %#v", main)
	}
	// The Agent view and the snapshot the next Turn reads must both keep it.
	edited, err := service.UpdateAgentConfig(ctx, task.ID, "main", UpdateAgentConfigInput{ReasoningEffort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if !edited.FastMode {
		t.Fatalf("edit without fast_mode cleared the tier: %#v", edited)
	}
	snapshot, err := database.RuntimeSnapshot(ctx, edited.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.FastMode {
		t.Fatalf("stored snapshot lost the tier: %#v", snapshot)
	}
	// An explicit false is obeyed, so the switch turns off as well as on.
	off := false
	edited, err = service.UpdateAgentConfig(ctx, task.ID, "main", UpdateAgentConfigInput{FastMode: &off})
	if err != nil {
		t.Fatal(err)
	}
	if edited.FastMode {
		t.Fatalf("explicit fast_mode=false was ignored: %#v", edited)
	}
	if snapshot, err = database.RuntimeSnapshot(ctx, edited.RuntimeConfigSnapshotID); err != nil || snapshot.FastMode {
		t.Fatalf("stored snapshot kept the tier: %#v %v", snapshot, err)
	}
}
