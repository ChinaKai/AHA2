package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// Live inbox delivery is always on, but old runtime snapshots may still carry
// stream_input=0/1. Loading them must keep working, and the field must stay a
// database compatibility artifact rather than a switch the Agent API exposes.
func TestHistoricalStreamInputSnapshotIsInert(t *testing.T) {
	t.Parallel()
	ctx, database, service, task, model := newStreamCompatFixture(t)
	now := time.Now().UTC()
	historical := domain.RuntimeConfigSnapshot{
		ID: "runtime-historical", WorkspaceID: task.WorkspaceID, Backend: "claude",
		ModelID: model.ID, WireModel: model.WireModel, EnvGroupID: "env-stream",
		EnvGroupRevision: 1, StreamInput: true,
		PermissionsJSON: `{"filesystem":"workspace-write","approval":"never"}`, CreatedAt: now,
	}
	if err := database.CreateRuntimeSnapshot(ctx, historical); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskRuntimeSnapshot(ctx, task.ID, historical.ID, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskAgentSnapshot(ctx, task.ID, "main", historical.ID, false, now); err != nil {
		t.Fatal(err)
	}

	agents, err := service.TaskAgents(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	main := mainAgentOf(t, agents)
	if main.Backend != "claude" || !main.RuntimeConfigValid {
		t.Fatalf("historical snapshot did not load: %#v", main)
	}
	encoded, err := json.Marshal(main)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "stream_input") {
		t.Fatalf("the Agent API still exposes the removed switch: %s", encoded)
	}

	// A later edit must not carry the historical value forward as a live-inbox
	// switch; the new snapshot records no such choice.
	edited, err := service.UpdateAgentConfig(ctx, task.ID, "main", UpdateAgentConfigInput{ReasoningEffort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.RuntimeSnapshot(ctx, edited.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.StreamInput {
		t.Fatalf("historical stream_input was carried forward as a switch: %#v", snapshot)
	}
}

func newStreamCompatFixture(t *testing.T) (context.Context, *store.Store, *Service, domain.Task, domain.Model) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
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
	return ctx, database, service, task, model
}

func mainAgentOf(t *testing.T, agents []domain.TaskAgent) domain.TaskAgent {
	t.Helper()
	for _, agent := range agents {
		if agent.AgentID == "main" {
			return agent
		}
	}
	t.Fatalf("main agent missing: %#v", agents)
	return domain.TaskAgent{}
}
