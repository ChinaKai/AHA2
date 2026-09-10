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

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/backend"
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

func TestRecoveryContextNeedsUsesSessionAndPreviousTurnState(t *testing.T) {
	t.Parallel()
	current := domain.Turn{ID: "current", AgentID: "main", Sequence: 3, Attempt: 1, Generation: 1}
	succeeded := []domain.Turn{{ID: "previous", AgentID: "main", Sequence: 2, Status: domain.TurnSucceeded, Attempt: 1, Generation: 1}}
	if recent, diagnostics := recoveryContextNeeds(current, succeeded, true, ""); recent || diagnostics {
		t.Fatalf("healthy reused session requested recovery resources: recent=%t diagnostics=%t", recent, diagnostics)
	}
	succeeded[0].Generation = 9
	current.Generation = 10
	if recent, diagnostics := recoveryContextNeeds(current, succeeded, true, ""); recent || diagnostics {
		t.Fatalf("normal session generations requested recovery resources: recent=%t diagnostics=%t", recent, diagnostics)
	}
	if recent, diagnostics := recoveryContextNeeds(current, succeeded, false, ""); !recent || diagnostics {
		t.Fatalf("cold session recovery=%t diagnostics=%t", recent, diagnostics)
	}
	failed := []domain.Turn{{ID: "previous", AgentID: "main", Sequence: 2, Status: domain.TurnFailed, Error: "boom", Attempt: 1, Generation: 1}}
	if recent, diagnostics := recoveryContextNeeds(current, failed, true, ""); !recent || !diagnostics {
		t.Fatalf("failed turn recovery=%t diagnostics=%t", recent, diagnostics)
	}
	if recent, diagnostics := recoveryContextNeeds(current, succeeded, true, "compacted"); !recent || diagnostics {
		t.Fatalf("handoff recovery=%t diagnostics=%t", recent, diagnostics)
	}
}

type multiAgentExecutor struct {
	mu      sync.Mutex
	turns   []domain.Turn
	service *Service
}

type idleThenSuccessExecutor struct {
	mu    sync.Mutex
	calls int
}

func (executor *idleThenSuccessExecutor) Execute(_ context.Context, _ ExecutionRequest, emit func(ExecutionEvent)) (ExecutionResult, error) {
	executor.mu.Lock()
	executor.calls++
	call := executor.calls
	executor.mu.Unlock()
	emit(ExecutionEvent{Type: "agent_progress", Data: map[string]any{"message": fmt.Sprintf("attempt %d progress", call)}})
	emit(ExecutionEvent{Type: "agent_message", Data: map[string]any{"text": fmt.Sprintf("attempt %d partial", call)}})
	if call == 1 {
		return ExecutionResult{}, backend.ErrCodexIdleTimeout
	}
	emit(ExecutionEvent{Type: "agent_message", Data: map[string]any{"text": "recovered"}})
	return ExecutionResult{Reply: "recovered", ProviderSessionID: "retry-session"}, nil
}

func (s *multiAgentExecutor) Execute(_ context.Context, request ExecutionRequest, emit func(ExecutionEvent)) (ExecutionResult, error) {
	s.mu.Lock()
	s.turns = append(s.turns, request.Turn)
	s.mu.Unlock()
	emit(ExecutionEvent{Type: "agent_usage", Data: map[string]any{"usage": map[string]any{"input_tokens": 500, "output_tokens": 25}}})
	emit(ExecutionEvent{Type: "agent_command_started", Data: map[string]any{"command": "verify " + request.Turn.AgentID}})
	emit(ExecutionEvent{Type: "agent_command_finished", Data: map[string]any{"command": "verify " + request.Turn.AgentID, "status": "completed"}})
	emit(ExecutionEvent{Type: "agent_progress", Data: map[string]any{"message": "working " + request.Turn.AgentID}})
	switch {
	case request.Turn.AgentID == "main" && request.Turn.Generation == 0:
		if s.service != nil {
			_, _ = s.service.SubmitAgentCollaboration(context.Background(), agentapi.Claims{TaskID: request.Task.ID, AgentID: request.Turn.AgentID, TurnID: request.Turn.ID}, []AgentAction{
				{AgentID: "sub-001", Title: "Store", Assignment: "implement store", Required: true},
				{AgentID: "sub-002", Title: "Web", Assignment: "implement web", Required: true},
			}, "")
		}
		emit(ExecutionEvent{Type: "agent_message", Data: map[string]any{"text": "delegating"}})
		return ExecutionResult{Reply: "delegating"}, nil
	case request.Turn.AgentID == "main":
		emit(ExecutionEvent{Type: "agent_message", Data: map[string]any{"text": "integrated result"}})
		return ExecutionResult{Reply: "integrated result", ProviderSessionID: "main-session"}, nil
	default:
		emit(ExecutionEvent{Type: "agent_message", Data: map[string]any{"text": request.Turn.AgentID + " result"}})
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
	return ExecutionResult{Reply: "stub reply", ProviderSessionID: s.session}, nil
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
		RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "auto", AgentAPIStatus: "ready",
		AgentAPIResolvedURL: "https://workspace.example.test", CreatedAt: now, UpdatedAt: now,
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
	selectedSkill := domain.Skill{ID: "skill-selected", Scope: "global", Name: "Selected", Description: "Selected skill", Instructions: "Use selected skill.", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	otherSkill := domain.Skill{ID: "skill-other", Scope: "project", ProjectID: project.ID, Name: "Other", Description: "Other skill", Instructions: "Do not use this skill.", Version: 1, Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateSkill(ctx, selectedSkill); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateSkill(ctx, otherSkill); err != nil {
		t.Fatal(err)
	}
	selectedSkill, err = database.Skill(ctx, selectedSkill.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherSkill, err = database.Skill(ctx, otherSkill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selectedSkill.SourcePath, "scripts", "check.sh"), []byte("echo selected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, secretStore, &stubExecutor{})
	capabilities := agentapi.NewCapabilities()
	service.SetAgentAPI(capabilities, "https://aha.example.test")
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "test", Request: "run test",
		ModelID: model.ID, ProxyEnabled: true, SkillIDs: []string{selectedSkill.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertBackendSession(ctx, domain.BackendSession{
		ID: "channel-session", TaskID: task.ID, AgentID: "main", WorkspaceID: workspace.ID,
		Backend: "stub", ModelID: model.ID, EnvGroupRevision: 1,
		IdentityContext: "channel-assistant:external-channel", ProviderSession: "channel-provider",
		Status: "active", CreatedAt: now, LastUsedAt: now,
	}); err != nil {
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
	for _, item := range turns {
		if item.ContextReadyAt.IsZero() || item.SessionReadyAt.IsZero() || item.StartedAt.IsZero() ||
			item.FirstEventAt.IsZero() || item.LastActivityAt.IsZero() || item.BackendFinishedAt.IsZero() {
			t.Fatalf("turn stage timestamps were not persisted: %#v", item)
		}
	}
	prompts, providerInputs := service.executor.(*stubExecutor).requestHistory()
	if len(prompts) < 2 || !strings.Contains(prompts[0], "## AHA Core") ||
		!strings.Contains(prompts[0], ".aha2-context") || !strings.Contains(prompts[0], "agent-api.md") {
		t.Fatalf("routed prompt was not used: %#v", prompts)
	}
	if len(providerInputs) < 2 || providerInputs[0] != "" || providerInputs[1] != "stub-session-1" {
		t.Fatalf("web turn reused a mismatched identity session: %#v", providerInputs)
	}
	sessions, err := database.ListBackendSessionsForAgent(ctx, task.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	statusByID := map[string]string{}
	for _, session := range sessions {
		statusByID[session.ID] = session.Status
	}
	if statusByID["channel-session"] != "superseded" || statusByID[turns[0].BackendSessionID] != "active" {
		t.Fatalf("identity transition did not rotate sessions: %#v", sessions)
	}
	environments := service.executor.(*stubExecutor).environmentHistory()
	if len(environments) < 2 || environments[0]["HTTP_PROXY"] != "http://127.0.0.1:7897" ||
		environments[0]["HTTPS_PROXY"] != "http://127.0.0.1:7897" || !strings.Contains(environments[0]["NO_PROXY"], "workspace.example.test") {
		t.Fatalf("shared proxy was not injected: %#v", environments)
	}
	if environments[0]["AHA2_AGENT_API_URL"] != "https://workspace.example.test" || environments[0]["AHA2_AGENT_API_TOKEN"] == "" ||
		environments[0]["AHA2_AGENT_API_TOKEN"] == environments[1]["AHA2_AGENT_API_TOKEN"] {
		t.Fatalf("per-turn Agent API capability was not injected: %#v", environments)
	}
	if _, err := capabilities.Authenticate(environments[0]["AHA2_AGENT_API_TOKEN"]); err == nil {
		t.Fatal("finished Turn capability was not revoked")
	}
	for index := range prompts {
		if strings.Contains(prompts[index], environments[index]["AHA2_AGENT_API_TOKEN"]) {
			t.Fatal("Agent API capability leaked into the prompt")
		}
	}
	manifest := filepath.Join(workspace.RootPath, ".aha2-context", task.ID, "main", "manifest.json")
	if _, err := os.Stat(manifest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("context manifest was materialized: %v", err)
	}
	taskContextRoot := filepath.Join(workspace.RootPath, ".aha2-context", task.ID)
	contextEntries, err := os.ReadDir(taskContextRoot)
	if err != nil {
		t.Fatal(err)
	}
	sharedRoot := ""
	for _, entry := range contextEntries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "shared-") {
			sharedRoot = filepath.Join(taskContextRoot, entry.Name())
		}
	}
	if sharedRoot == "" {
		t.Fatalf("shared Context snapshot was not materialized: %#v", contextEntries)
	}
	selectedSkillEntry := filepath.Join(sharedRoot, "skills", selectedSkill.PackageSlug, "SKILL.md")
	if data, err := os.ReadFile(selectedSkillEntry); err != nil || !strings.Contains(string(data), "Use selected skill.") {
		t.Fatalf("selected skill entry was not materialized: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(sharedRoot, "skills", selectedSkill.PackageSlug, "scripts", "check.sh")); err != nil || !strings.Contains(string(data), "selected") {
		t.Fatalf("selected skill script was not materialized: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(sharedRoot, "skills", otherSkill.PackageSlug, "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unselected skill was materialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sharedRoot, "agent-api.md")); err != nil {
		t.Fatalf("Agent API guide was not shared: %v", err)
	}
	for _, privatePath := range []string{
		filepath.Join(taskContextRoot, "main", "skills"),
		filepath.Join(taskContextRoot, "main", "agent-api.md"),
		filepath.Join(taskContextRoot, "main", "knowledge"),
	} {
		if _, err := os.Stat(privatePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("shared resource remained Agent-private at %s: %v", privatePath, err)
		}
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

func TestSharedContextCleanupProtectsActiveTurnsAndOtherTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	workspace := domain.Workspace{ID: "workspace-context-cleanup", Transport: "native", RootPath: workspaceRoot}
	taskRoot := filepath.Join(workspaceRoot, ".aha2-context", "task-1")
	root := func(hashByte string) string {
		return filepath.Join(taskRoot, "shared-"+strings.Repeat(hashByte, 64))
	}
	stale, first, second := root("a"), root("b"), root("c")
	otherTask := filepath.Join(workspaceRoot, ".aha2-context", "task-2", "shared-"+strings.Repeat("d", 64))
	agentRoot := filepath.Join(taskRoot, "main")
	for _, directory := range []string{stale, otherTask, agentRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{
		sharedContexts: map[string]*sharedContextState{},
		latestShared:   map[string]string{},
	}
	firstFile := filepath.Join(first, "agent-api.md")
	releaseFirst, err := service.materializeSharedContext(ctx, workspace, workspaceRoot, first, map[string]string{firstFile: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup leftover survived: %v", err)
	}
	secondFile := filepath.Join(second, "agent-api.md")
	releaseSecond, err := service.materializeSharedContext(ctx, workspace, workspaceRoot, second, map[string]string{secondFile: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("active Turn snapshot was removed: %v", err)
	}
	releaseFirst()
	if _, err := os.Stat(first); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released old snapshot survived: %v", err)
	}
	for _, retained := range []string{second, otherTask, agentRoot} {
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("cleanup crossed ownership boundary %s: %v", retained, err)
		}
	}
	releaseSecond()
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("latest snapshot was not retained: %v", err)
	}
	service.sharedContextMu.Lock()
	defer service.sharedContextMu.Unlock()
	if len(service.sharedContexts) != 1 || service.sharedContexts[second] == nil || service.sharedContexts[second].refs != 0 {
		t.Fatalf("shared Context lease state was not compacted: %#v", service.sharedContexts)
	}
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
	executor.service = service
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
	for _, turn := range turns {
		if turn.Status != domain.TurnSucceeded {
			continue
		}
		stream, err := database.ConversationPageForAgent(ctx, task.ID, turn.AgentID, 0, 0, 100, nil)
		if err != nil {
			t.Fatal(err)
		}
		finals, progress, backendStreams := 0, 0, 0
		for _, item := range stream.Items {
			if item.TurnID != turn.ID {
				continue
			}
			if item.RouteKind == "turn_result" && (item.Kind == "agent_message" || item.Kind == "agent_result") {
				finals++
			}
			if item.Kind == "agent_progress" {
				progress++
			}
			if item.RouteKind == "backend_stream" || item.Kind == "agent_message_update" && item.RouteKind == "" {
				backendStreams++
			}
		}
		if finals != 1 || progress != 1 || backendStreams != 0 {
			t.Fatalf("turn %s conversation was not finalized exactly once: finals=%d progress=%d streams=%d items=%#v", turn.ID, finals, progress, backendStreams, stream.Items)
		}
	}
	agents, err := database.ListTaskAgents(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected persistent main and two sub-agents: %#v", agents)
	}
	taskContextRoot := filepath.Join(workspace.RootPath, ".aha2-context", task.ID)
	contextEntries, err := os.ReadDir(taskContextRoot)
	if err != nil {
		t.Fatal(err)
	}
	sharedRoots := 0
	for _, entry := range contextEntries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "shared-") {
			sharedRoots++
		}
	}
	if sharedRoots != 1 {
		t.Fatalf("expected one shared immutable Context snapshot, got %d entries=%#v", sharedRoots, contextEntries)
	}
	for _, agentID := range []string{"main", "sub-001", "sub-002"} {
		for _, relative := range []string{"knowledge", "skills", "agent-api.md"} {
			if _, err := os.Stat(filepath.Join(taskContextRoot, agentID, relative)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("agent %s retained private shared resource %s: %v", agentID, relative, err)
			}
		}
	}
	service.sharedContextMu.Lock()
	materializedSharedContexts := len(service.sharedContexts)
	service.sharedContextMu.Unlock()
	if materializedSharedContexts != 1 {
		t.Fatalf("shared Context snapshot was not deduplicated: %d", materializedSharedContexts)
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
	turnDurationCards := 0
	for _, item := range page.Items {
		if item.Kind == "turn_duration" {
			turnDurationCards++
			if item.Payload["elapsed_ms"] == nil || item.Payload["run_duration_ms"] == nil {
				t.Fatalf("turn duration payload incomplete: %#v", item)
			}
		}
	}
	if turnDurationCards == 0 {
		t.Fatalf("turn duration card missing: %#v", page.Items)
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
	page, err = database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	configCardFound := false
	for _, item := range page.Items {
		if item.Kind == "agent_config_updated" {
			configCardFound = true
			if item.Payload["model_name"] != model2.DisplayName {
				t.Fatalf("agent config card model missing: %#v", item)
			}
		}
	}
	if !configCardFound {
		t.Fatalf("agent config update card missing: %#v", page.Items)
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
	if err := database.DeleteModel(ctx, model2.ID); err != nil {
		t.Fatal(err)
	}
	agents, err = service.TaskAgents(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		if agent.RuntimeConfigValid || !strings.Contains(agent.RuntimeConfigError, "模型已删除") {
			t.Fatalf("deleted model did not invalidate agent config: %#v", agent)
		}
	}
	if _, err := service.SubmitAgentMessage(ctx, task.ID, "main", "must be blocked"); err == nil || !strings.Contains(err.Error(), "模型已删除") {
		t.Fatalf("message submission was not blocked for deleted model: %v", err)
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
	executor := &multiAgentExecutor{}
	service := NewService(database, secretStore, executor)
	executor.service = service
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

func TestTurnUsageSubtractsCodexSessionBaseline(t *testing.T) {
	current := map[string]any{"input_tokens": float64(1000), "cached_input_tokens": float64(800), "output_tokens": float64(50)}
	baseline := map[string]any{"input_tokens": float64(700), "cached_input_tokens": float64(600), "output_tokens": float64(20)}
	delta := turnUsage("codex", current, baseline)
	if usageValue(delta["input_tokens"]) != 300 || usageValue(delta["cached_input_tokens"]) != 200 || usageValue(delta["output_tokens"]) != 30 {
		t.Fatalf("codex turn delta = %#v", delta)
	}
	claude := turnUsage("claude", current, baseline)
	if usageValue(claude["cached_input_tokens"]) != 800 {
		t.Fatalf("claude usage was incorrectly differenced: %#v", claude)
	}
	reset := turnUsage("codex", map[string]any{"input_tokens": float64(100)}, baseline)
	if usageValue(reset["input_tokens"]) != 100 {
		t.Fatalf("reset cumulative usage became negative: %#v", reset)
	}
}

func TestMainTurnRetriesOnceAfterBackendIdleTimeout(t *testing.T) {
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
	project := domain.Project{ID: "project-idle", Name: "Idle", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: "workspace-idle", ProjectID: project.ID, Name: "local", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now}
	env := domain.EnvGroup{ID: "env-idle", Name: "Env", ProviderID: "test", Backend: "codex", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-idle", DisplayName: "Model", ProviderID: "test", Backend: "codex", WireModel: "model", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, env) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	executor := &idleThenSuccessExecutor{}
	service := NewService(database, secretStore, executor)
	task, err := service.CreateTask(ctx, CreateTaskInput{ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "idle", Request: "retry idle", ModelID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitMessage(ctx, task.ID, task.OriginalRequest); err != nil {
		t.Fatal(err)
	}
	waitForTask(t, database, task.ID, domain.TaskWaitingUser)
	turns, err := database.ListTurns(ctx, task.ID)
	if err != nil || len(turns) != 2 {
		t.Fatalf("retry turns=%#v err=%v", turns, err)
	}
	if turns[0].Status != domain.TurnFailed || turns[0].WaitingReason != "backend_idle_timeout" || turns[1].Status != domain.TurnSucceeded || turns[1].Attempt != 2 {
		t.Fatalf("idle retry state=%#v", turns)
	}
	if turns[0].InboxBatchID == "" || turns[1].InboxBatchID != turns[0].InboxBatchID {
		t.Fatalf("retry lost inbox provenance: first=%q retry=%q", turns[0].InboxBatchID, turns[1].InboxBatchID)
	}
	page, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	failedStreams, retryFinals, retryStreams, retryEvents := 0, 0, 0, 0
	progressByTurn := map[string]int{}
	for _, item := range page.Items {
		if item.Kind == "agent_progress" {
			progressByTurn[item.TurnID]++
		}
		if item.TurnID == turns[0].ID && item.RouteKind == "backend_stream" {
			failedStreams++
		}
		if item.TurnID == turns[1].ID && item.RouteKind == "turn_result" {
			retryFinals++
		}
		if item.TurnID == turns[1].ID && item.RouteKind == "backend_stream" {
			retryStreams++
		}
		if item.TurnID == turns[1].ID && item.RouteKind == "retry" && item.Kind == "agent_retry" {
			retryEvents++
		}
	}
	if failedStreams != 1 || retryFinals != 1 || retryStreams != 0 || retryEvents != 1 || progressByTurn[turns[0].ID] != 1 || progressByTurn[turns[1].ID] != 1 {
		t.Fatalf("retry conversation mismatch: failed_streams=%d retry_finals=%d retry_streams=%d retry_events=%d progress=%#v items=%#v", failedStreams, retryFinals, retryStreams, retryEvents, progressByTurn, page.Items)
	}
	messages, err := database.ListMessages(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	assistantReplies := 0
	for _, message := range messages {
		if message.Role == "assistant" {
			assistantReplies++
			if message.Content != "recovered" || message.TurnID != turns[1].ID {
				t.Fatalf("unexpected retry assistant message: %#v", message)
			}
		}
	}
	if assistantReplies != 1 {
		t.Fatalf("retry produced %d assistant replies: %#v", assistantReplies, messages)
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
