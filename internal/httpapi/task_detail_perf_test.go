package httpapi

import (
	"context"
	"database/sql"
	"fmt"
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

// The Web Task detail and conversation endpoints were reported at ~658ms and
// ~446ms per request on a Task whose conversation holds ~49,955 items and
// ~30MB of payload_json. These fixtures reproduce that shape so a change to
// either endpoint can be judged against the same data volume.
const (
	perfSeedConversationItems = 49955
	perfSeedPayloadChars      = 560
	perfSeedEvents            = 120
)

type taskDetailPerfFixture struct {
	database *store.Store
	server   *Server
	task     domain.Task
}

func seedTaskDetailPerfFixture(tb testing.TB) *taskDetailPerfFixture {
	tb.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(tb.TempDir(), "aha2.db")
	database, err := store.Open(ctx, dbPath)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { database.Close() })

	now := time.Now().UTC()
	project := domain.Project{ID: "perf-project", Name: "Perf", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		tb.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "perf-workspace", ProjectID: project.ID, Name: "Perf", Locality: "local",
		Transport: "native", RootPath: tb.TempDir(), Health: "ready", AgentAPIMode: "global",
		AgentAPIStatus: "unknown", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		tb.Fatal(err)
	}
	env := domain.EnvGroup{ID: "perf-env", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		tb.Fatal(err)
	}
	model := domain.Model{ID: "perf-model", DisplayName: "Model", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		tb.Fatal(err)
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "perf-snapshot", WorkspaceID: workspace.ID, Backend: "stub", ModelID: model.ID,
		WireModel: model.WireModel, EnvGroupID: env.ID, EnvGroupRevision: 1, CreatedAt: now,
	}
	task := domain.Task{
		ID: "perf-task", ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Perf detail", OriginalRequest: "perf", CurrentGoal: "perf goal",
		Status: domain.TaskActive, Isolation: "inplace", RuntimeConfigSnapshotID: snapshot.ID,
		CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "inherit",
		CreatedAt: now, UpdatedAt: now,
	}
	created, err := database.CreateTaskWithSnapshot(ctx, snapshot, task)
	if err != nil {
		tb.Fatal(err)
	}
	task = created
	if err := database.UpsertTaskMemory(ctx, domain.TaskMemory{
		TaskID: task.ID, CurrentGoal: "perf goal", Decisions: []string{"decide"},
		Facts: []string{"fact"}, Progress: []string{"progress"}, UpdatedAt: now,
	}); err != nil {
		tb.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if err := database.UpsertHardwareResource(ctx, domain.HardwareResource{
			ID: fmt.Sprintf("resource-%d", index), Description: fmt.Sprintf("Resource %d", index),
			Mode: domain.HardwareModeSerial, Serial: domain.HardwareSerialConfig{Device: "/dev/ttyUSB0", Baudrate: 115200},
			Access: domain.HardwareAccessReadOnly, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			tb.Fatal(err)
		}
	}
	if err := database.CreateTaskPermissionRequest(ctx, domain.TaskPermissionRequest{
		ID: domain.NewID("permission"), TaskID: task.ID, Action: "serial write", Scope: "/dev/ttyUSB0",
		Reason: "perf seed", Risk: "low", Status: "pending", CreatedAt: now, UpdatedAt: now,
		ExpiresAt: now.Add(15 * time.Minute),
	}); err != nil {
		tb.Fatal(err)
	}
	for index := 0; index < perfSeedEvents; index++ {
		if _, err := database.AppendEvent(ctx, domain.Event{
			ID: domain.NewID("event"), AggregateType: "task", AggregateID: task.ID,
			Type: "task_goal_updated", Data: map[string]any{}, OccurredAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			tb.Fatal(err)
		}
	}

	message := domain.Message{ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner", Content: "hello", CreatedAt: now}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundCompleted, CreatedAt: now, StartedAt: now, FinishedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, AgentID: "main", InputMessageID: message.ID,
		Status: domain.TurnSucceeded, RuntimeConfigSnapshotID: snapshot.ID,
		QueuedAt: now, StartedAt: now, FinishedAt: now, ContextWindow: 200000,
		Usage: map[string]any{"input_tokens": float64(1200)},
	}
	if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn); err != nil {
		tb.Fatal(err)
	}

	if err := seedConversationItems(tb, dbPath, task.ID, round.ID, turn.ID, now); err != nil {
		tb.Fatal(err)
	}
	// Every item was already read before this page load; the unread counter is
	// the COUNT(*) subquery, and the conversation endpoint's mark-read becomes a
	// no-op write. An all-unread variant is measured separately below.
	if err := database.MarkAgentConversationRead(ctx, task.ID, "main"); err != nil {
		tb.Fatal(err)
	}
	appService := app.NewService(database, nil, app.StubExecutor{})
	return &taskDetailPerfFixture{database: database, server: New(Config{Store: database, App: appService}), task: task}
}

// seedConversationItems bulk-inserts through a second connection so the fixture
// build does not depend on a store API that exists only for tests.
func seedConversationItems(tb testing.TB, dbPath, taskID, roundID, turnID string, now time.Time) error {
	tb.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)
	tx, err := raw.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statement, err := tx.Prepare(`
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,
			route_kind,category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer statement.Close()
	payload := `{"text":"` + strings.Repeat("p", perfSeedPayloadChars) + `"}`
	summary := strings.Repeat("s", 120)
	for index := 0; index < perfSeedConversationItems; index++ {
		category, kind := "update", "tool_call"
		switch index % 3 {
		case 0:
			category, kind = "chat", "agent_message"
		case 1:
			category, kind = "update", "turn_duration"
		}
		createdAt := now.Add(time.Duration(index) * time.Millisecond)
		if _, err := statement.Exec(
			fmt.Sprintf("perf-conversation-%06d", index), taskID, roundID, turnID, "main",
			"main", "main", "main", "agent_message", category, kind, summary, payload,
			createdAt.Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (fixture *taskDetailPerfFixture) getTaskDetail(tb testing.TB) {
	tb.Helper()
	request := httptest.NewRequest("GET", "/api/v1/tasks/"+fixture.task.ID, nil)
	request.SetPathValue("id", fixture.task.ID)
	recorder := httptest.NewRecorder()
	fixture.server.taskDetail(recorder, request)
	if recorder.Code != 200 {
		tb.Fatalf("task detail status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func (fixture *taskDetailPerfFixture) getConversation(tb testing.TB, target string) {
	tb.Helper()
	request := httptest.NewRequest("GET", target, nil)
	request.SetPathValue("id", fixture.task.ID)
	request.SetPathValue("agent", "main")
	recorder := httptest.NewRecorder()
	fixture.server.conversationForAgent(recorder, request, "main")
	if recorder.Code != 200 {
		tb.Fatalf("conversation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func BenchmarkTaskDetailEndpoint(b *testing.B) {
	fixture := seedTaskDetailPerfFixture(b)
	b.Run("detail", func(b *testing.B) {
		b.ReportAllocs()
		samples := make([]time.Duration, 0, b.N)
		for iteration := 0; iteration < b.N; iteration++ {
			started := time.Now()
			fixture.getTaskDetail(b)
			samples = append(samples, time.Since(started))
		}
		reportLatencyPercentiles(b, samples)
	})
	b.Run("conversation-initial", func(b *testing.B) {
		b.ReportAllocs()
		samples := make([]time.Duration, 0, b.N)
		for iteration := 0; iteration < b.N; iteration++ {
			started := time.Now()
			fixture.getConversation(b, "/api/v1/tasks/perf-task/agents/main/conversation?limit=20&categories=chat,update,error")
			samples = append(samples, time.Since(started))
		}
		reportLatencyPercentiles(b, samples)
	})
	b.Run("conversation-page-50", func(b *testing.B) {
		b.ReportAllocs()
		samples := make([]time.Duration, 0, b.N)
		for iteration := 0; iteration < b.N; iteration++ {
			started := time.Now()
			fixture.getConversation(b, "/api/v1/tasks/perf-task/agents/main/conversation?limit=50")
			samples = append(samples, time.Since(started))
		}
		reportLatencyPercentiles(b, samples)
	})
}

// BenchmarkTaskDetailStages isolates the work one detail request does so a
// change can be judged against the whole instead of a single aggregated number.
func BenchmarkTaskDetailStages(b *testing.B) {
	fixture := seedTaskDetailPerfFixture(b)
	ctx := context.Background()
	taskID := fixture.task.ID
	stages := []struct {
		name string
		run  func() error
	}{
		{"latest-round", func() error { _, err := fixture.database.LatestRound(ctx, taskID); return err }},
		{"task-agents", func() error { _, err := fixture.server.app.TaskAgents(ctx, taskID); return err }},
		{"task-memory", func() error { _, err := fixture.database.TaskMemory(ctx, taskID); return err }},
		{"hardware-groups", func() error { _, err := fixture.database.HardwareGroups(ctx, taskID); return err }},
		{"hardware-resources", func() error { _, err := fixture.database.HardwareResources(ctx); return err }},
		{"permission-requests", func() error {
			_, err := fixture.server.app.TaskPermissionRequests(ctx, taskID, 50)
			return err
		}},
		{"max-event-sequence", func() error {
			_ = fixture.database.MaxEventSequence(ctx, taskID)
			return nil
		}},
		{"conversation-page", func() error {
			_, err := fixture.database.ConversationPageForAgent(ctx, taskID, "main", 0, 0, 20, []string{"chat", "update", "error"})
			return err
		}},
		{"mark-read", func() error { return fixture.database.MarkAgentConversationRead(ctx, taskID, "main") }},
	}
	for _, stage := range stages {
		b.Run(stage.name, func(b *testing.B) {
			b.ReportAllocs()
			for iteration := 0; iteration < b.N; iteration++ {
				if err := stage.run(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func (fixture *taskDetailPerfFixture) fetchTaskDetail() (int, string) {
	request := httptest.NewRequest("GET", "/api/v1/tasks/"+fixture.task.ID, nil)
	request.SetPathValue("id", fixture.task.ID)
	recorder := httptest.NewRecorder()
	fixture.server.taskDetail(recorder, request)
	return recorder.Code, recorder.Body.String()
}

func (fixture *taskDetailPerfFixture) fetchConversation(target string) (int, string) {
	request := httptest.NewRequest("GET", target, nil)
	request.SetPathValue("id", fixture.task.ID)
	request.SetPathValue("agent", "main")
	recorder := httptest.NewRecorder()
	fixture.server.conversationForAgent(recorder, request, "main")
	return recorder.Code, recorder.Body.String()
}

// The reported page load hit both endpoints five times. store.Open exposes one
// SQLite connection, so identical concurrent reads queue behind each other and
// every one of them waits for the whole queue. This measures that amplification
// directly: the wall time of the duplicate burst a page load actually paid.
func BenchmarkTaskDetailDuplicateBurst(b *testing.B) {
	fixture := seedTaskDetailPerfFixture(b)
	b.Run("detail-x5", func(b *testing.B) {
		b.ReportAllocs()
		samples := make([]time.Duration, 0, b.N)
		for iteration := 0; iteration < b.N; iteration++ {
			started := time.Now()
			var wait sync.WaitGroup
			for repeat := 0; repeat < 5; repeat++ {
				wait.Add(1)
				go func() {
					defer wait.Done()
					if code, body := fixture.fetchTaskDetail(); code != 200 {
						b.Errorf("task detail status=%d body=%s", code, body)
					}
				}()
			}
			wait.Wait()
			samples = append(samples, time.Since(started))
		}
		reportLatencyPercentiles(b, samples)
	})
	b.Run("conversation-x5", func(b *testing.B) {
		b.ReportAllocs()
		samples := make([]time.Duration, 0, b.N)
		target := "/api/v1/tasks/perf-task/agents/main/conversation?limit=20&categories=chat,update,error"
		for iteration := 0; iteration < b.N; iteration++ {
			started := time.Now()
			var wait sync.WaitGroup
			for repeat := 0; repeat < 5; repeat++ {
				wait.Add(1)
				go func() {
					defer wait.Done()
					if code, body := fixture.fetchConversation(target); code != 200 {
						b.Errorf("conversation status=%d body=%s", code, body)
					}
				}()
			}
			wait.Wait()
			samples = append(samples, time.Since(started))
		}
		reportLatencyPercentiles(b, samples)
	})
}
