package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestBatchTaskAggregationQueriesGroupByTaskAndSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	project := domain.Project{ID: "project-batch", Name: "Batch", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: "workspace-batch", ProjectID: project.ID, Name: "Local", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now}
	env := domain.EnvGroup{ID: "env-batch", Name: "Batch", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretNames: []string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-batch", DisplayName: "Batch", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	snapshot := domain.RuntimeConfigSnapshot{ID: "runtime-batch", WorkspaceID: workspace.ID, Backend: "stub", ModelID: model.ID, WireModel: model.WireModel, EnvGroupID: env.ID, EnvGroupRevision: 1, PermissionsJSON: "{}", CreatedAt: now}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, env) },
		func() error { return database.UpsertModel(ctx, model) },
		func() error { return database.CreateRuntimeSnapshot(ctx, snapshot) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	taskIDs := []string{"task-batch-a", "task-batch-b"}
	for index, taskID := range taskIDs {
		task := domain.Task{ID: taskID, ProjectID: project.ID, WorkspaceID: workspace.ID, Title: taskID, OriginalRequest: "request", CurrentGoal: "request", Status: domain.TaskActive, Isolation: "inplace", RuntimeConfigSnapshotID: snapshot.ID, CreatedAt: now, UpdatedAt: now}
		if err := database.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
		messageID := "message-" + taskID
		if err := database.AddMessage(ctx, domain.Message{ID: messageID, TaskID: taskID, Role: "user", Sender: "owner", Content: "run", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := database.CreateTurn(ctx, domain.Turn{
			ID: "turn-" + taskID, TaskID: taskID, AgentID: "main", Sequence: index + 1,
			InputMessageID: messageID, Status: domain.TurnSucceeded, RuntimeConfigSnapshotID: snapshot.ID,
			Usage: map[string]any{"input_tokens": float64(10 + index)}, QueuedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := database.UpsertBackendSession(ctx, domain.BackendSession{
			ID: "session-" + taskID, TaskID: taskID, AgentID: "main", WorkspaceID: workspace.ID,
			Backend: "stub", ModelID: model.ID, EnvGroupRevision: 1, Status: "active",
			ContextUsageJSON: `{"input_tokens":20}`, CreatedAt: now, LastUsedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	turns, err := database.ListTurnsForTasks(ctx, taskIDs)
	if err != nil || len(turns) != 2 || len(turns[taskIDs[0]]) != 1 || turns[taskIDs[1]][0].TaskID != taskIDs[1] {
		t.Fatalf("turn batch=%#v err=%v", turns, err)
	}
	sessions, err := database.ListBackendSessionsForTasks(ctx, taskIDs)
	if err != nil || len(sessions) != 2 || sessions[taskIDs[0]][0].ID != "session-"+taskIDs[0] {
		t.Fatalf("session batch=%#v err=%v", sessions, err)
	}
	snapshots, err := database.RuntimeSnapshotsByIDs(ctx, []string{snapshot.ID, "missing"})
	if err != nil || len(snapshots) != 1 || snapshots[snapshot.ID].Backend != "stub" {
		t.Fatalf("snapshot batch=%#v err=%v", snapshots, err)
	}
}
