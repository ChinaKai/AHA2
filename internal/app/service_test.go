package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

type stubExecutor struct {
	session string
	block   <-chan struct{}
}

func (s *stubExecutor) Execute(_ context.Context, request ExecutionRequest, emit func(ExecutionEvent)) (ExecutionResult, error) {
	if s.block != nil {
		<-s.block
	}
	emit(ExecutionEvent{Type: "agent_progress", Data: map[string]any{"phase": "testing"}})
	if request.ProviderSessionID != "" {
		s.session = request.ProviderSessionID
	}
	if s.session == "" {
		s.session = "stub-session-1"
	}
	return ExecutionResult{
		Reply:             "stub reply",
		ProviderSessionID: s.session,
		MemoryPatch:       MemoryPatch{Facts: []string{"stub completed"}},
		KnowledgeCandidates: []KnowledgeCandidate{{
			Scope: "project", Type: "practice", Title: "Stub fact", Body: "The stub completed.", Confidence: 0.8,
		}},
	}, nil
}

func TestConcurrentTurnIsRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{ID: "project-concurrent", Name: "AHA2", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-concurrent", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{ID: "env-concurrent", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-concurrent", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertModel(ctx, model) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	block := make(chan struct{})
	service := NewService(database, secretStore, &stubExecutor{block: block})
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "test", Request: "run test",
		ModelID: model.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.SubmitMessage(ctx, task.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitMessage(ctx, task.ID, "second"); !errors.Is(err, store.ErrActiveTurn) {
		t.Fatalf("expected active turn error, got %v", err)
	}
	if err := service.CompleteTask(ctx, task.ID); !errors.Is(err, store.ErrActiveTurn) {
		t.Fatalf("expected active turn completion error, got %v", err)
	}
	close(block)
	waitForTurn(t, database, first.ID, domain.TurnSucceeded)
}

func TestTaskMultiTurnFlow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{ID: "project-1", Name: "AHA2", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-1", ProjectID: project.ID, Name: "local", Locality: "local", Transport: "native",
		RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	envGroup := domain.EnvGroup{ID: "env-1", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-1", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertEnvGroup(ctx, envGroup); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, secretStore, &stubExecutor{})
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "test", Request: "run test",
		ModelID: model.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		turn, err := service.SubmitMessage(ctx, task.ID, "continue")
		if err != nil {
			t.Fatal(err)
		}
		waitForTurn(t, database, turn.ID, domain.TurnSucceeded)
		waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	}
	turns, err := database.ListTurns(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[1].BackendSessionID != turns[0].BackendSessionID {
		t.Fatalf("session was not reused: %#v", turns)
	}
	knowledge, err := database.ListKnowledge(ctx, "project", project.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(knowledge) != 2 {
		t.Fatalf("expected 2 turn candidates, got %d", len(knowledge))
	}
}

func waitForTask(t *testing.T, database *store.Store, taskID string, expected domain.TaskStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, err := database.Task(context.Background(), taskID)
		if err == nil && task.Status == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := database.Task(context.Background(), taskID)
	t.Fatalf("task did not reach %s: %#v", expected, task)
}

func waitForTurn(t *testing.T, database *store.Store, turnID string, expected domain.TurnStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := database.Turn(context.Background(), turnID)
		if err == nil && turn.Status == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	turn, _ := database.Turn(context.Background(), turnID)
	t.Fatalf("turn did not reach %s: %#v", expected, turn)
}
