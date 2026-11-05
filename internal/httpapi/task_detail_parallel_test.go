package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type taskDetailFixture struct {
	database *store.Store
	app      *app.Service
	server   *httptest.Server
	client   *http.Client
	task     domain.Task
}

func newTaskDetailFixture(t *testing.T, startRound bool) taskDetailFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Now().UTC()
	project := domain.Project{ID: "project-detail-parallel", Name: "Detail", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-detail-parallel", ProjectID: project.ID, Name: "Detail", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", AgentAPIMode: "global",
		AgentAPIStatus: "unknown", CreatedAt: now, UpdatedAt: now,
	}
	env := domain.EnvGroup{ID: "env-detail-parallel", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-detail-parallel", DisplayName: "Model", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
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
	appService := app.NewService(database, nil, app.StubExecutor{})
	task, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Parallel detail", Request: "keep the same JSON",
		Isolation: "inplace", ModelID: model.ID, CollaborationMode: "single", MaxAgents: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpsertTaskAgent(ctx, domain.TaskAgent{
		TaskID: task.ID, AgentID: "sub-001", Role: "sub", Status: "idle", Title: "Child",
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, InheritMain: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{{
		ID: "board", Description: "Board", Mode: domain.HardwareModeSerial,
		Serial: domain.HardwareSerialConfig{Device: "/dev/ttyUSB0", Baudrate: 115200},
		Access: domain.HardwareAccessReadOnly, CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := appService.CreateTaskPermissionRequest(ctx, app.TaskPermissionRequestInput{
		TaskID: task.ID, Action: "serial write", Scope: "/dev/ttyUSB0", Reason: "parallel detail test", Risk: "low",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AppendEvent(ctx, domain.Event{
		ID: domain.NewID("event"), AggregateType: "task", AggregateID: task.ID,
		Type: "task_goal_updated", Data: map[string]any{}, OccurredAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if startRound {
		message := domain.Message{ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner", Content: "hello", CreatedAt: now}
		round := domain.TaskRound{
			ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
			Status: domain.RoundCompleted, CreatedAt: now, StartedAt: now, FinishedAt: now,
		}
		turn := domain.Turn{
			ID: domain.NewID("turn"), TaskID: task.ID, AgentID: "main", InputMessageID: message.ID,
			Status: domain.TurnSucceeded, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
			QueuedAt: now, StartedAt: now, FinishedAt: now, ContextWindow: 1000,
			Usage: map[string]any{"input_tokens": float64(100)},
		}
		if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn); err != nil {
			t.Fatal(err)
		}
	}

	authService := authServiceForTest(database)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	_ = registerOwner(t, client, server.URL)
	return taskDetailFixture{database: database, app: appService, server: server, client: client, task: task}
}

func getTaskDetailSections(t *testing.T, fixture taskDetailFixture) map[string]json.RawMessage {
	t.Helper()
	response := requestJSON(t, fixture.client, http.MethodGet, fixture.server.URL+"/api/v1/tasks/"+fixture.task.ID, nil, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("task detail status=%d", response.StatusCode)
	}
	var sections map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&sections); err != nil {
		t.Fatal(err)
	}
	return sections
}

// A panic inside one fan-out goroutine must not escape: net/http recovers only
// the handler goroutine, so the wrapper has to return normally and report the
// task and query through its recorder.
func TestRunTaskDetailQueryRecoversPanic(t *testing.T) {
	var (
		recordedTaskID string
		recordedQuery  string
		recordedPanic  any
		records        int
	)
	runTaskDetailQuery("task-parallel-panic", "TaskAgents", func(taskID, query string, recovered any) {
		records++
		recordedTaskID, recordedQuery, recordedPanic = taskID, query, recovered
	}, func() {
		panic("query exploded")
	})
	if records != 1 {
		t.Fatalf("panic recorder calls = %d, want 1", records)
	}
	if recordedTaskID != "task-parallel-panic" || recordedQuery != "TaskAgents" {
		t.Fatalf("recorded task/query = %q/%q", recordedTaskID, recordedQuery)
	}
	if recordedPanic != "query exploded" {
		t.Fatalf("recorded panic = %#v", recordedPanic)
	}

	// The production hook writes the task ID, query name, and stack to the log.
	var logged bytes.Buffer
	server := &Server{logger: slog.New(slog.NewTextHandler(&logged, nil))}
	runTaskDetailQuery("task-logged", "TaskMemory", server.recordTaskDetailPanic, func() { panic("boom") })
	output := logged.String()
	if !strings.Contains(output, "task-logged") || !strings.Contains(output, "TaskMemory") {
		t.Fatalf("panic log lost task/query: %s", output)
	}
	if !strings.Contains(output, "goroutine") {
		t.Fatalf("panic log lost stack: %s", output)
	}

	// A nil recorder still contains the panic, and a query that does not panic
	// never calls the recorder.
	runTaskDetailQuery("task-nil-hook", "HardwareGroups", nil, func() { panic("boom") })
	ran := false
	runTaskDetailQuery("task-ok", "HardwareGroups", func(string, string, any) {
		t.Fatal("recorder called for a successful query")
	}, func() { ran = true })
	if !ran {
		t.Fatal("successful query did not run")
	}
}

// A Server without an App makes TaskAgents and TaskPermissionRequests panic.
// The handler must still answer 200, keep the unaffected sections, and leave
// the two panicking sections at their zero values.
func TestTaskDetailSurvivesPanickingSection(t *testing.T) {
	fixture := newTaskDetailFixture(t, true)
	var logged bytes.Buffer
	server := New(Config{Store: fixture.database, Logger: slog.New(slog.NewTextHandler(&logged, nil))})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+fixture.task.ID, nil)
	request.SetPathValue("id", fixture.task.ID)
	recorder := httptest.NewRecorder()
	server.taskDetail(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("task detail status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var detail struct {
		Ok                 bool                           `json:"ok"`
		Task               domain.Task                    `json:"task"`
		Turns              []domain.Turn                  `json:"turns"`
		Agents             []domain.TaskAgent             `json:"agents"`
		Memory             domain.TaskMemory              `json:"memory"`
		PermissionRequests []domain.TaskPermissionRequest `json:"permission_requests"`
		EventCursor        int64                          `json:"event_cursor"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if !detail.Ok || detail.Task.ID != fixture.task.ID || detail.Memory.CurrentGoal != "keep the same JSON" {
		t.Fatalf("unaffected sections changed: ok=%t task=%q memory=%q", detail.Ok, detail.Task.ID, detail.Memory.CurrentGoal)
	}
	if len(detail.Turns) != 1 || detail.Turns[0].Usage["context_tokens"] != float64(100) {
		t.Fatalf("turn decoration changed: %#v", detail.Turns)
	}
	if detail.EventCursor == 0 {
		t.Fatal("event cursor was not filled by the surviving query")
	}
	if len(detail.Agents) != 0 || len(detail.PermissionRequests) != 0 {
		t.Fatalf("panicking sections were not zero: agents=%#v requests=%#v", detail.Agents, detail.PermissionRequests)
	}

	// The production recorder must actually fire once per panic site with the
	// expected Task ID and query names. Zero-value assertions alone would keep
	// passing if a future dependency became nil-safe and stopped panicking.
	output := logged.String()
	if records := strings.Count(output, "task detail query panicked"); records != 2 {
		t.Fatalf("panic recorder records = %d, want 2: %s", records, output)
	}
	for _, want := range []string{fixture.task.ID, "TaskAgents", "TaskPermissionRequests", "goroutine"} {
		if !strings.Contains(output, want) {
			t.Fatalf("panic log missing %q: %s", want, output)
		}
	}
}

// The detail handler fans independent queries out into goroutines. The response
// contract must not change: every section stays present, agent/hardware and
// permission data still arrive, and the latest round's turn keeps its context
// usage decoration.
func TestTaskDetailParallelQueriesKeepResponseSemantics(t *testing.T) {
	fixture := newTaskDetailFixture(t, true)
	sections := getTaskDetailSections(t, fixture)
	for _, key := range []string{
		"ok", "task", "latest_round", "turns", "agents", "memory", "hardware",
		"hardware_resources", "permission_requests", "event_cursor", "server_time_ms",
	} {
		if _, ok := sections[key]; !ok {
			t.Fatalf("task detail lost field %q: %#v", key, sections)
		}
	}
	var detail struct {
		Task               domain.Task                    `json:"task"`
		LatestRound        domain.TaskRound               `json:"latest_round"`
		Turns              []domain.Turn                  `json:"turns"`
		Agents             []domain.TaskAgent             `json:"agents"`
		Memory             domain.TaskMemory              `json:"memory"`
		Hardware           []domain.HardwareGroup         `json:"hardware"`
		Resources          []domain.HardwareResource      `json:"hardware_resources"`
		PermissionRequests []domain.TaskPermissionRequest `json:"permission_requests"`
		EventCursor        int64                          `json:"event_cursor"`
		ServerTimeMS       int64                          `json:"server_time_ms"`
	}
	encoded, err := json.Marshal(sections)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Task.ID != fixture.task.ID || detail.Memory.CurrentGoal != "keep the same JSON" {
		t.Fatalf("task/memory sections changed: %#v %#v", detail.Task, detail.Memory)
	}
	if detail.LatestRound.ID == "" || len(detail.Turns) != 1 || detail.Turns[0].RoundID != detail.LatestRound.ID {
		t.Fatalf("round/turn relationship changed: round=%#v turns=%#v", detail.LatestRound, detail.Turns)
	}
	if detail.Turns[0].ContextWindow != 1000 || detail.Turns[0].Usage["context_tokens"] != float64(100) {
		t.Fatalf("turn context usage decoration changed: %#v", detail.Turns[0].Usage)
	}
	if len(detail.Agents) < 2 {
		t.Fatalf("agents section = %#v", detail.Agents)
	}
	if len(detail.Hardware) != 1 || detail.Hardware[0].Access != domain.HardwareAccessReadOnly {
		t.Fatalf("hardware section = %#v", detail.Hardware)
	}
	if len(detail.Resources) == 0 {
		t.Fatalf("hardware resources section is empty")
	}
	if len(detail.PermissionRequests) != 1 || detail.PermissionRequests[0].Action != "serial write" {
		t.Fatalf("permission requests section = %#v", detail.PermissionRequests)
	}
	wantCursor := fixture.database.MaxEventSequence(context.Background(), fixture.task.ID)
	if detail.EventCursor != wantCursor || detail.EventCursor == 0 {
		t.Fatalf("event cursor = %d, want %d", detail.EventCursor, wantCursor)
	}
	if detail.ServerTimeMS == 0 {
		t.Fatalf("server_time_ms missing")
	}
}

// A Task without a round makes LatestRound fail while the other goroutines
// succeed. The handler must still answer 200 with zero values, exactly like the
// previous serial code that ignored that error.
func TestTaskDetailToleratesMissingRound(t *testing.T) {
	fixture := newTaskDetailFixture(t, false)
	sections := getTaskDetailSections(t, fixture)
	var round domain.TaskRound
	if err := json.Unmarshal(sections["latest_round"], &round); err != nil {
		t.Fatal(err)
	}
	if round.ID != "" {
		t.Fatalf("unexpected latest round: %#v", round)
	}
	if string(sections["turns"]) != "null" {
		t.Fatalf("turns without a round = %s", sections["turns"])
	}
	var task domain.Task
	if err := json.Unmarshal(sections["task"], &task); err != nil || task.ID != fixture.task.ID {
		t.Fatalf("task section = %#v err=%v", task, err)
	}
}

// Concurrent detail requests share the Server (and its transcript cache); this
// is the regression test for the fan-out's shared variables when run with -race.
func TestTaskDetailServesConcurrentRequests(t *testing.T) {
	fixture := newTaskDetailFixture(t, true)
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for attempt := 0; attempt < 4; attempt++ {
				response := requestJSON(t, fixture.client, http.MethodGet, fixture.server.URL+"/api/v1/tasks/"+fixture.task.ID, nil, "")
				if response.StatusCode != http.StatusOK {
					t.Errorf("concurrent task detail status=%d", response.StatusCode)
					response.Body.Close()
					continue
				}
				var payload struct {
					Task domain.Task `json:"task"`
				}
				if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
					t.Errorf("concurrent task detail decode: %v", err)
				}
				if payload.Task.ID != fixture.task.ID {
					t.Errorf("concurrent task detail id = %q", payload.Task.ID)
				}
				response.Body.Close()
			}
		}()
	}
	wait.Wait()
}

// BenchmarkTaskDetail measures the handler after the fan-out: one Task, a
// handful of agents, and one round's turns. It runs warm (the transcript cache
// and SQLite pages are shared between iterations); see the report for the
// first-request cold measurement.
func BenchmarkTaskDetail(b *testing.B) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(b.TempDir(), "aha2.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-detail-bench", Name: "Detail", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-detail-bench", ProjectID: project.ID, Name: "Detail", Locality: "local",
		Transport: "native", RootPath: b.TempDir(), Health: "ready", AgentAPIMode: "global",
		AgentAPIStatus: "unknown", CreatedAt: now, UpdatedAt: now,
	}
	env := domain.EnvGroup{ID: "env-detail-bench", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "model-detail-bench", DisplayName: "Model", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, env) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			b.Fatal(err)
		}
	}
	appService := app.NewService(database, nil, app.StubExecutor{})
	task, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Detail benchmark", Request: "benchmark",
		Isolation: "inplace", ModelID: model.ID, CollaborationMode: "auto", MaxAgents: 9,
	})
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 8; index++ {
		if _, err := database.UpsertTaskAgent(ctx, domain.TaskAgent{
			TaskID: task.ID, AgentID: fmt.Sprintf("sub-%03d", index), Role: "sub", Status: "idle",
			Title: "Child", RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
			InheritMain: true, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			b.Fatal(err)
		}
	}
	message := domain.Message{ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner", Content: "hello", CreatedAt: now}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundCompleted, CreatedAt: now, StartedAt: now, FinishedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, AgentID: "main", InputMessageID: message.ID,
		Status: domain.TurnSucceeded, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		QueuedAt: now, StartedAt: now, FinishedAt: now, ContextWindow: 200000,
		Usage: map[string]any{"input_tokens": float64(1000)},
	}
	if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn); err != nil {
		b.Fatal(err)
	}
	server := New(Config{Store: database, App: appService})
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+task.ID, nil)
		request.SetPathValue("id", task.ID)
		recorder := httptest.NewRecorder()
		server.taskDetail(recorder, request)
		if recorder.Code != http.StatusOK {
			b.Fatalf("task detail status=%d", recorder.Code)
		}
	}
}
