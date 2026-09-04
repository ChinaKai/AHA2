package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

type stubExecutor struct {
	mu             sync.Mutex
	session        string
	sessionCounter int
	block          <-chan struct{}
	prompts        []string
	providerInputs []string
	environments   []map[string]string
}

type multiAgentExecutor struct {
	mu    sync.Mutex
	turns []domain.Turn
}

func (s *multiAgentExecutor) Execute(_ context.Context, request ExecutionRequest, emit func(ExecutionEvent)) (ExecutionResult, error) {
	s.mu.Lock()
	s.turns = append(s.turns, request.Turn)
	s.mu.Unlock()
	emit(ExecutionEvent{Type: "agent_usage", Data: map[string]any{"usage": map[string]any{"input_tokens": 500, "output_tokens": 25}}})
	emit(ExecutionEvent{Type: "agent_command_started", Data: map[string]any{"command": "verify " + request.Turn.AgentID}})
	emit(ExecutionEvent{Type: "agent_command_finished", Data: map[string]any{"command": "verify " + request.Turn.AgentID, "status": "completed"}})
	switch {
	case request.Turn.AgentID == "main" && request.Turn.Generation == 0:
		return ExecutionResult{
			Reply: "delegating",
			AgentActions: []AgentAction{
				{AgentID: "sub-001", Title: "Store", Assignment: "implement store", Required: true},
				{AgentID: "sub-002", Title: "Web", Assignment: "implement web", Required: true},
			},
		}, nil
	case request.Turn.AgentID == "main":
		return ExecutionResult{Reply: "integrated result", ProviderSessionID: "main-session"}, nil
	default:
		return ExecutionResult{Reply: request.Turn.AgentID + " result", ProviderSessionID: request.Turn.AgentID + "-session"}, nil
	}
}

func (s *stubExecutor) Execute(_ context.Context, request ExecutionRequest, emit func(ExecutionEvent)) (ExecutionResult, error) {
	if s.block != nil {
		<-s.block
	}
	emit(ExecutionEvent{Type: "agent_progress", Data: map[string]any{"phase": "testing"}})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append(s.prompts, request.Prompt)
	s.providerInputs = append(s.providerInputs, request.ProviderSessionID)
	environment := make(map[string]string, len(request.Environment))
	for key, value := range request.Environment {
		environment[key] = value
	}
	s.environments = append(s.environments, environment)
	if request.ProviderSessionID != "" {
		s.session = request.ProviderSessionID
	} else {
		s.sessionCounter++
		s.session = fmt.Sprintf("stub-session-%d", s.sessionCounter)
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

func (s *stubExecutor) requestHistory() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.prompts...), append([]string(nil), s.providerInputs...)
}

func (s *stubExecutor) environmentHistory() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]string(nil), s.environments...)
}

func TestMessagesQueueWhileAgentIsBusy(t *testing.T) {
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
	second, err := service.SubmitMessage(ctx, task.ID, "second")
	if err != nil {
		t.Fatalf("second message was not queued: %v", err)
	}
	if second.ID != "" {
		t.Fatalf("queued message unexpectedly started a concurrent main turn: %#v", second)
	}
	if pending, err := database.PendingInboxCount(ctx, task.ID); err != nil || pending != 1 {
		t.Fatalf("expected one queued inbox message, pending=%d err=%v", pending, err)
	}
	if err := service.CompleteTask(ctx, task.ID); !errors.Is(err, store.ErrActiveTurn) {
		t.Fatalf("expected active turn completion error, got %v", err)
	}
	close(block)
	waitForTurn(t, database, first.ID, domain.TurnSucceeded)
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	turns, err := database.ListTurns(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[1].Instruction == "" || turns[1].Status != domain.TurnSucceeded {
		t.Fatalf("queued message was not consumed after main became idle: %#v", turns)
	}
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
		ModelID: model.ID, ProxyEnabled: true,
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
	prompts, _ := service.executor.(*stubExecutor).requestHistory()
	if len(prompts) < 2 || !strings.Contains(prompts[0], "## AHA Core") ||
		!strings.Contains(prompts[0], ".aha2-context") {
		t.Fatalf("routed prompt was not used: %#v", prompts)
	}
	environments := service.executor.(*stubExecutor).environmentHistory()
	if len(environments) < 2 || environments[0]["HTTP_PROXY"] != "http://127.0.0.1:7897" ||
		environments[0]["HTTPS_PROXY"] != "http://127.0.0.1:7897" || environments[0]["NO_PROXY"] == "" {
		t.Fatalf("shared proxy was not injected: %#v", environments)
	}
	manifest := filepath.Join(workspace.RootPath, ".aha2-context", task.ID, "main", "manifest.json")
	if data, err := os.ReadFile(manifest); err != nil || strings.Contains(string(data), "stub completed") {
		t.Fatalf("context manifest invalid: %q %v", data, err)
	}
	knowledge, err := database.ListKnowledge(ctx, "project", project.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(knowledge) != 2 {
		t.Fatalf("expected 2 turn candidates, got %d", len(knowledge))
	}
	current, err := database.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskStatus(ctx, task.ID, current.Status, domain.TaskActive, time.Now().UTC().Format(time.RFC3339Nano), ""); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskStatus(ctx, task.ID, domain.TaskActive, domain.TaskFailed, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	retry, err := service.SubmitMessage(ctx, task.ID, "retry failed task")
	if err != nil {
		t.Fatalf("failed task did not resume: %v", err)
	}
	waitForTurn(t, database, retry.ID, domain.TurnSucceeded)
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	if err := service.CompleteTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, database, task.ID, domain.TaskCompleted)
	if err := service.ReopenTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
}

func TestAgentSessionCompactAndReset(t *testing.T) {
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
	project := domain.Project{ID: "project-session", Name: "Session", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-session", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-session", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-session", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub",
		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
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
	executor := &stubExecutor{}
	service := NewService(database, secretStore, executor)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "session",
		Request: "session controls", ModelID: model.ID, CollaborationMode: "auto", MaxAgents: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.SubmitMessage(ctx, task.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, database, first.ID, domain.TurnSucceeded)
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	first, _ = database.Turn(ctx, first.ID)

	compacted, err := service.CompactAgentSession(ctx, task.ID, "main")
	if err != nil || compacted.ID != first.BackendSessionID || compacted.Status != "compacted" {
		t.Fatalf("compact failed: %#v %v", compacted, err)
	}
	if handoff, err := database.PendingAgentSessionHandoff(ctx, task.ID, "main"); err != nil || handoff.SourceBackendSessionID != compacted.ID {
		t.Fatalf("compact handoff missing: %#v %v", handoff, err)
	}
	second, err := service.SubmitMessage(ctx, task.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, database, second.ID, domain.TurnSucceeded)
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	second, _ = database.Turn(ctx, second.ID)
	if second.BackendSessionID == first.BackendSessionID {
		t.Fatalf("compact reused old backend session: %#v %#v", first, second)
	}
	waitForNoPendingHandoff(t, database, task.ID, "main")

	reset, err := service.ResetAgentSession(ctx, task.ID, "main")
	if err != nil || reset.ID != second.BackendSessionID || reset.Status != "reset" {
		t.Fatalf("reset failed: %#v %v", reset, err)
	}
	third, err := service.SubmitMessage(ctx, task.ID, "third")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, database, third.ID, domain.TurnSucceeded)
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	third, _ = database.Turn(ctx, third.ID)
	if third.BackendSessionID == second.BackendSessionID {
		t.Fatalf("reset reused old backend session: %#v %#v", second, third)
	}

	prompts, providerInputs := executor.requestHistory()
	if len(prompts) < 3 || !strings.Contains(prompts[1], "AHA Backend Session Handoff") || providerInputs[1] != "" {
		t.Fatalf("compact did not start a fresh session with handoff: prompts=%d inputs=%#v", len(prompts), providerInputs)
	}
	if strings.Contains(prompts[2], "The previous Backend Session was compacted by AHA") || providerInputs[2] != "" {
		t.Fatalf("reset unexpectedly carried a handoff or resumed a session: input=%q", providerInputs[2])
	}

	if _, err := database.UpsertTaskAgent(ctx, domain.TaskAgent{
		TaskID: task.ID, AgentID: "sub-001", Role: "sub", Status: "idle", Title: "Child",
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InheritMain: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	childTurn, err := service.SubmitAgentMessage(ctx, task.ID, "sub-001", "child")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, database, childTurn.ID, domain.TurnSucceeded)
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	if _, err := service.ResetAgentSession(ctx, task.ID, "sub-001"); err != nil {
		t.Fatal(err)
	}
	mainSessions, _ := database.ListBackendSessionsForAgent(ctx, task.ID, "main")
	var mainActive string
	for _, session := range mainSessions {
		if session.Status == "active" {
			mainActive = session.ID
		}
	}
	if mainActive != third.BackendSessionID {
		t.Fatalf("resetting child changed main session: active=%q want=%q", mainActive, third.BackendSessionID)
	}

	block := make(chan struct{})
	executor.block = block
	active, err := service.SubmitMessage(ctx, task.ID, "active")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompactAgentSession(ctx, task.ID, "main"); !errors.Is(err, store.ErrActiveTurn) {
		t.Fatalf("active compact error = %v", err)
	}
	if _, err := service.ResetAgentSession(ctx, task.ID, "main"); !errors.Is(err, store.ErrActiveTurn) {
		t.Fatalf("active reset error = %v", err)
	}
	close(block)
	waitForTurn(t, database, active.ID, domain.TurnSucceeded)
}

func TestMultiAgentRoundCreatesIntegrationTurn(t *testing.T) {
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
	project := domain.Project{ID: "project-agents", Name: "AHA2", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-agents", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-agents", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-agents", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub",
		ContextWindow: 1000, DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
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
	executor := &multiAgentExecutor{}
	service := NewService(database, secretStore, executor)
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "agents", Request: "coordinate agents",
		ModelID: model.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitMessage(ctx, task.ID, "coordinate agents"); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	round, err := database.LatestRound(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if round.Status != domain.RoundCompleted {
		t.Fatalf("round did not complete: %#v", round)
	}
	turns, err := database.TurnsForRound(ctx, round.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 4 {
		t.Fatalf("expected one merged main integration turn after two sub-agents; got %#v", turns)
	}
	var integrations int
	for _, turn := range turns {
		if turn.AgentID == "main" && turn.Generation > 0 && turn.Result == "integrated result" {
			integrations++
		}
		if turn.ContextWindow != 1000 || usageNumberForTest(turn.Usage, "input_tokens") != 500 {
			t.Fatalf("turn context usage missing: %#v", turn)
		}
	}
	if integrations != 1 {
		t.Fatalf("expected exactly one merged integration turn: %#v", turns)
	}
	agents, err := database.ListTaskAgents(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected persistent main and two sub-agents: %#v", agents)
	}
	if pending, err := database.PendingInboxCount(ctx, task.ID); err != nil || pending != 0 {
		t.Fatalf("inbox was not fully consumed: pending=%d err=%v", pending, err)
	}
	page, err := database.ConversationPage(ctx, task.ID, 0, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	categories := map[string]bool{}
	var orchestrationCards int
	for _, item := range page.Items {
		categories[item.Category] = true
		if item.Kind == "agent_batch_dispatched" {
			orchestrationCards++
			routes, _ := item.Payload["agent_routes"].([]any)
			if len(routes) != 2 {
				t.Fatalf("orchestration routes missing: %#v", item.Payload)
			}
			for _, rawRoute := range routes {
				route, _ := rawRoute.(map[string]any)
				if route["status"] != string(domain.TurnSucceeded) {
					t.Fatalf("route status was not frozen per card: %#v", route)
				}
			}
		}
	}
	for _, category := range []string{"chat", "update", "tool"} {
		if !categories[category] {
			t.Fatalf("conversation category %q missing: %#v", category, page.Items)
		}
	}
	if orchestrationCards != 1 {
		t.Fatalf("AHA orchestration card missing: %#v", page.Items)
	}
	childPage, err := database.ConversationPageForAgent(ctx, task.ID, "sub-001", 0, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range childPage.Items {
		if item.StreamAgentID != "sub-001" {
			t.Fatalf("child conversation leaked another stream: %#v", item)
		}
		if item.Kind == "agent_batch_dispatched" {
			t.Fatalf("AHA orchestration card leaked into child conversation: %#v", item)
		}
	}
	envGroup2 := envGroup
	envGroup2.ID = "env-agents-2"
	envGroup2.Name = "stub 2"
	model2 := model
	model2.ID = "model-agents-2"
	model2.DisplayName = "Stub 2"
	model2.WireModel = "stub-2"
	model2.DefaultEnvGroupID = envGroup2.ID
	if err := database.UpsertEnvGroup(ctx, envGroup2); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, model2); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateAgentConfig(ctx, task.ID, "main", UpdateAgentConfigInput{ModelID: model2.ID}); err != nil {
		t.Fatal(err)
	}
	agents, err = service.TaskAgents(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		if agent.ModelID != model2.ID {
			t.Fatalf("main config did not propagate to inherited agent: %#v", agents)
		}
		if agent.AgentID != "main" && !agent.InheritMain {
			t.Fatalf("new sub-agent should inherit main config: %#v", agent)
		}
	}
}

func TestSingleModeRejectsAgentActions(t *testing.T) {
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
	project := domain.Project{ID: "project-single", Name: "Single", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-single", ProjectID: project.ID, Name: "local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-single", Name: "stub", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-single", DisplayName: "Stub", ProviderID: "stub", Backend: "stub", WireModel: "stub",
		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
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
	service := NewService(database, secretStore, &multiAgentExecutor{})
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "single",
		Request: "do not delegate", ModelID: model.ID, CollaborationMode: "single", MaxAgents: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitMessage(ctx, task.ID, task.OriginalRequest); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	agents, err := database.ListTaskAgents(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].AgentID != "main" {
		t.Fatalf("single mode created sub-agents: %#v", agents)
	}
	page, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, []string{"error"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) == 0 || page.Items[0].Kind != "agent_action_rejected" {
		t.Fatalf("single-mode rejection was not visible: %#v", page.Items)
	}
}

func usageNumberForTest(usage map[string]any, key string) float64 {
	value, _ := usage[key].(float64)
	return value
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

func waitForNoPendingHandoff(t *testing.T, database *store.Store, taskID, agentID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := database.PendingAgentSessionHandoff(context.Background(), taskID, agentID); errors.Is(err, sql.ErrNoRows) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	handoff, err := database.PendingAgentSessionHandoff(context.Background(), taskID, agentID)
	t.Fatalf("pending handoff was not consumed: %#v %v", handoff, err)
}
