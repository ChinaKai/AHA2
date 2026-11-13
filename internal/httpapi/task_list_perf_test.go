package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// The Web task list is the slowest first-paint request. These fixtures seed a
// database at the production shape reported in task-341 (~91 Tasks, ~4413
// Turns) because the cost that matters is the turn history a page pulls in,
// not the Task rows themselves. Per-Turn prompt_snapshot/result sizes follow
// the production totals (~31MB / ~3.3MB across 4413 Turns).
const (
	perfSeedTaskCount     = 91
	perfSeedTurnCount     = 4413
	perfSeedHeavyTasks    = 50 // newest Tasks; they carry one full list page
	perfSeedHeavyTurns    = 80
	perfSeedPromptChars   = 7000
	perfSeedResultChars   = 750
	perfSeedInstructChars = 500
)

type taskListPerfFixture struct {
	store     *store.Store
	server    *Server
	taskIDs   []string
	projectID string
}

func seedTaskListPerfFixture(tb testing.TB) *taskListPerfFixture {
	tb.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(tb.TempDir(), "aha2.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { database.Close() })

	now := time.Now().UTC()
	project := domain.Project{ID: "perf-project", Name: "Perf", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		tb.Fatal(err)
	}
	workspace := domain.Workspace{ID: "perf-workspace", ProjectID: project.ID, Name: "Perf", Locality: "local", Transport: "native", RootPath: tb.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now, AgentAPIMode: "global", AgentAPIStatus: "unknown"}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		tb.Fatal(err)
	}
	env := domain.EnvGroup{ID: "perf-env", Name: "Env", ProviderID: "claude", Backend: "claude", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		tb.Fatal(err)
	}
	model := domain.Model{ID: "perf-model", DisplayName: "Model", ProviderID: "claude", Backend: "claude", WireModel: "claude-sonnet-5", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		tb.Fatal(err)
	}
	snapshot := domain.RuntimeConfigSnapshot{ID: "perf-snapshot", WorkspaceID: workspace.ID, Backend: "claude", ModelID: model.ID, WireModel: model.WireModel, EnvGroupID: env.ID, EnvGroupRevision: 1, CreatedAt: now}
	if err := database.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
		tb.Fatal(err)
	}

	promptSnapshot := strings.Repeat("p", perfSeedPromptChars)
	result := strings.Repeat("r", perfSeedResultChars)
	instruction := strings.Repeat("i", perfSeedInstructChars)
	heavyTurns := perfSeedHeavyTurns * perfSeedHeavyTasks
	restTurns := perfSeedTurnCount - heavyTurns
	restTasks := perfSeedTaskCount - perfSeedHeavyTasks
	if restTasks <= 0 || restTurns < 0 {
		tb.Fatalf("invalid perf seed shape: tasks=%d turns=%d", perfSeedTaskCount, perfSeedTurnCount)
	}
	restBase, restExtra := restTurns/restTasks, restTurns%restTasks

	fixture := &taskListPerfFixture{store: database, server: &Server{store: database}, projectID: project.ID}
	for index := 0; index < perfSeedTaskCount; index++ {
		createdAt := now.Add(-time.Duration(index) * time.Minute)
		status := domain.TaskCompleted
		if index%7 == 0 {
			status = domain.TaskActive
		}
		task := domain.Task{
			ID: fmt.Sprintf("perf-task-%03d", index), ProjectID: project.ID, WorkspaceID: workspace.ID,
			Title: fmt.Sprintf("Perf task %d", index), OriginalRequest: strings.Repeat("q", 400),
			CurrentGoal: strings.Repeat("g", 200), Status: status, Isolation: "inplace",
			RuntimeConfigSnapshotID: snapshot.ID, CollaborationMode: "single", MaxAgents: 1,
			KnowledgePolicy: "inherit", CreatedAt: createdAt, UpdatedAt: createdAt,
		}
		if err := database.CreateTask(ctx, task); err != nil {
			tb.Fatal(err)
		}
		fixture.taskIDs = append(fixture.taskIDs, task.ID)

		turns := perfSeedHeavyTurns
		if index >= perfSeedHeavyTasks {
			turns = restBase
			if index-perfSeedHeavyTasks < restExtra {
				turns++
			}
		}
		sessionID := "perf-session-" + task.ID
		if err := database.UpsertBackendSession(ctx, domain.BackendSession{
			ID: sessionID, TaskID: task.ID, AgentID: "main", WorkspaceID: workspace.ID,
			Backend: "claude", ModelID: model.ID, Status: "active", ContextUsageJSON: "{}",
			CreatedAt: createdAt, LastUsedAt: createdAt,
		}); err != nil {
			tb.Fatal(err)
		}
		message := domain.Message{ID: "perf-message-" + task.ID, TaskID: task.ID, Role: "user", Sender: "owner", Content: "seed", CreatedAt: createdAt}
		round := domain.TaskRound{ID: "perf-round-" + task.ID, TaskID: task.ID, InputMessageID: message.ID, Status: domain.RoundCompleted, CreatedAt: createdAt}
		firstTurn := domain.Turn{
			ID: "perf-turn-" + task.ID + "-1", TaskID: task.ID, AgentID: "main", InputMessageID: message.ID,
			Status: domain.TurnSucceeded, BackendSessionID: sessionID, RuntimeConfigSnapshotID: snapshot.ID,
			QueuedAt: createdAt, StartedAt: createdAt, FinishedAt: createdAt,
			Title: "turn", Instruction: instruction, Result: result, PromptSnapshot: promptSnapshot,
			Usage: map[string]any{
				"input_tokens": float64(1000), "cache_read_input_tokens": float64(500),
				"cache_creation_input_tokens": float64(20), "output_tokens": float64(100),
				"reasoning_output_tokens": float64(10),
			},
		}
		if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, firstTurn); err != nil {
			tb.Fatal(err)
		}
		for turnIndex := 2; turnIndex <= turns; turnIndex++ {
			turn := firstTurn
			turn.ID = fmt.Sprintf("perf-turn-%s-%d", task.ID, turnIndex)
			turn.RoundID = ""
			turn.Sequence = turnIndex
			if err := database.CreateTurn(ctx, turn); err != nil {
				tb.Fatal(err)
			}
		}
	}
	return fixture
}

func (fixture *taskListPerfFixture) listTasks(tb testing.TB, target string) {
	tb.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", target, nil)
	fixture.server.listTasks(recorder, request)
	if recorder.Code != 200 {
		tb.Fatalf("%s status=%d body=%s", target, recorder.Code, recorder.Body.String())
	}
}

// reportLatencyPercentiles turns the per-iteration samples Go already recorded
// into the p50/p95 the task's acceptance criteria ask for. -benchtime=20x keeps
// the sample count meaningful; the default time-based calibration can stop at
// a single iteration for a slow baseline.
func reportLatencyPercentiles(b *testing.B, samples []time.Duration) {
	if len(samples) == 0 {
		return
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	percentile := func(fraction float64) float64 {
		index := int(float64(len(samples)-1) * fraction)
		return float64(samples[index].Microseconds()) / 1000
	}
	b.ReportMetric(percentile(0.50), "p50-ms")
	b.ReportMetric(percentile(0.95), "p95-ms")
}

func BenchmarkTaskListEndpoint(b *testing.B) {
	fixture := seedTaskListPerfFixture(b)
	paths := []struct {
		name   string
		target string
	}{
		{"paged", "/api/v1/tasks?limit=50"},
		{"attention", "/api/v1/tasks?limit=50&order=attention"},
		{"unpaged", "/api/v1/tasks"},
		{"summary", "/api/v1/tasks?summary=true"},
	}
	for _, path := range paths {
		b.Run(path.name, func(b *testing.B) {
			b.ReportAllocs()
			samples := make([]time.Duration, 0, b.N)
			for iteration := 0; iteration < b.N; iteration++ {
				started := time.Now()
				fixture.listTasks(b, path.target)
				samples = append(samples, time.Since(started))
			}
			reportLatencyPercentiles(b, samples)
		})
	}
}

// The four list paths share the token aggregation, but only the paged ones
// decide how many Tasks it runs for. This test pins both the page semantics
// and the requirement that every returned Task still carries the token total
// the full-turn aggregation produced.
func TestTaskListPathsKeepTokenTotalsAndPaging(t *testing.T) {
	fixture := seedTaskListPerfFixture(t)
	legacy := legacyTaskTokenTotals(t, fixture.store, fixture.taskIDs)
	assertTokenProjectionMatchesFullTurns(t, fixture.store, fixture.taskIDs)

	readTasks := func(target string) (taskListBody, map[string]json.RawMessage) {
		t.Helper()
		recorder := httptest.NewRecorder()
		fixture.server.listTasks(recorder, httptest.NewRequest("GET", target, nil))
		if recorder.Code != 200 {
			t.Fatalf("%s status=%d body=%s", target, recorder.Code, recorder.Body.String())
		}
		raw := recorder.Body.Bytes()
		var body taskListBody
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(raw, &keys); err != nil {
			t.Fatal(err)
		}
		if !body.OK || body.Counts == nil {
			t.Fatalf("%s response lost ok/counts: %s", target, raw)
		}
		for _, item := range body.Tasks {
			want, ok := legacy[item.ID]
			if !ok {
				t.Fatalf("%s returned unexpected task %s", target, item.ID)
			}
			if item.TotalTokens != want {
				t.Fatalf("%s task %s total_tokens=%d want %d", target, item.ID, item.TotalTokens, want)
			}
		}
		return body, keys
	}
	pagedKeys := func(keys map[string]json.RawMessage, want bool) {
		t.Helper()
		_, hasMore := keys["has_more"]
		_, hasCursor := keys["next_cursor"]
		if want && (!hasMore || !hasCursor) {
			t.Fatalf("paged response lost has_more/next_cursor: %v", keys)
		}
		if !want && (hasMore || hasCursor) {
			t.Fatalf("unpaged response grew paging fields: %v", keys)
		}
	}

	page, keys := readTasks("/api/v1/tasks?limit=50")
	if len(page.Tasks) != 50 || !page.HasMore || page.Cursor == "" {
		t.Fatalf("paged path = %#v", page)
	}
	pagedKeys(keys, true)
	attention, keys := readTasks("/api/v1/tasks?limit=50&order=attention")
	if len(attention.Tasks) != 50 || !attention.HasMore {
		t.Fatalf("attention path = %#v", attention)
	}
	pagedKeys(keys, true)
	if got := store.TaskAttentionRank(attention.Tasks[0].Status); got != 0 {
		t.Fatalf("attention page starts with completed task %s", attention.Tasks[0].ID)
	}
	unpaged, keys := readTasks("/api/v1/tasks")
	if len(unpaged.Tasks) != perfSeedTaskCount {
		t.Fatalf("unpaged count = %d", len(unpaged.Tasks))
	}
	pagedKeys(keys, false)
	summary, keys := readTasks("/api/v1/tasks?summary=true")
	if len(summary.Tasks) != perfSeedTaskCount {
		t.Fatalf("summary count = %d", len(summary.Tasks))
	}
	pagedKeys(keys, false)
}

type taskListBody struct {
	OK      bool           `json:"ok"`
	Tasks   []domain.Task  `json:"tasks"`
	HasMore bool           `json:"has_more"`
	Cursor  string         `json:"next_cursor"`
	Counts  map[string]any `json:"counts"`
}

// assertTokenProjectionMatchesFullTurns pins the lightweight list projection
// to the full rows it replaced: same turns in the same order, same session and
// snapshot references, same decoded usage. Only the wide text columns differ,
// and the task list never reads them.
func assertTokenProjectionMatchesFullTurns(t *testing.T, database *store.Store, taskIDs []string) {
	t.Helper()
	ctx := context.Background()
	full, err := database.ListTurnsForTasks(ctx, taskIDs)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := database.ListTurnTokenUsageForTasks(ctx, taskIDs)
	if err != nil {
		t.Fatal(err)
	}
	for _, taskID := range taskIDs {
		fullTurns, projectedTurns := full[taskID], projected[taskID]
		if len(fullTurns) != len(projectedTurns) {
			t.Fatalf("task %s projection has %d turns, full rows %d", taskID, len(projectedTurns), len(fullTurns))
		}
		for index := range fullTurns {
			want, got := fullTurns[index], projectedTurns[index]
			if got.ID != want.ID || got.Sequence != want.Sequence || got.BackendSessionID != want.BackendSessionID ||
				got.RuntimeConfigSnapshotID != want.RuntimeConfigSnapshotID {
				t.Fatalf("task %s turn %d projection = %#v, want %#v", taskID, index, got, want)
			}
			if !reflect.DeepEqual(got.Usage, want.Usage) {
				t.Fatalf("task %s turn %d usage = %#v, want %#v", taskID, index, got.Usage, want.Usage)
			}
		}
	}
}

// legacyTaskTokenTotals is the pre-optimization aggregation: load every Turn
// row (result and prompt_snapshot included) and total from those. It is the
// reference the lightweight list projection must match exactly.
func legacyTaskTokenTotals(t *testing.T, database *store.Store, taskIDs []string) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	turnsByTask, err := database.ListTurnsForTasks(ctx, taskIDs)
	if err != nil {
		t.Fatal(err)
	}
	sessionsByTask, err := database.ListBackendSessionsForTasks(ctx, taskIDs)
	if err != nil {
		t.Fatal(err)
	}
	snapshotIDs := make([]string, 0)
	seen := map[string]bool{}
	for _, turns := range turnsByTask {
		for _, turn := range turns {
			if turn.RuntimeConfigSnapshotID != "" && !seen[turn.RuntimeConfigSnapshotID] {
				seen[turn.RuntimeConfigSnapshotID] = true
				snapshotIDs = append(snapshotIDs, turn.RuntimeConfigSnapshotID)
			}
		}
	}
	snapshots, err := database.RuntimeSnapshotsByIDs(ctx, snapshotIDs)
	if err != nil {
		t.Fatal(err)
	}
	backends := make(map[string]string, len(snapshots))
	for id, snapshot := range snapshots {
		backends[id] = snapshot.Backend
	}
	totals := make(map[string]int64, len(taskIDs))
	for _, taskID := range taskIDs {
		totals[taskID] = int64(taskTotalTokens(turnsByTask[taskID], sessionsByTask[taskID], backends))
	}
	return totals
}

// BenchmarkTaskListStages isolates the work a list request does so a change to
// one stage can be judged against the whole. The full-turn stage is the
// pre-optimization token source and stays here as the reference.
func BenchmarkTaskListStages(b *testing.B) {
	fixture := seedTaskListPerfFixture(b)
	ctx := context.Background()
	ids := append([]string(nil), fixture.taskIDs[:50]...)
	stages := []struct {
		name string
		run  func() error
	}{
		{"remote-mirrors", func() error { _, err := fixture.store.RemoteTaskMirrors(ctx, ""); return err }},
		{"list-tasks", func() error { _, err := fixture.store.ListTasks(ctx, ""); return err }},
		{"turns-full", func() error { _, err := fixture.store.ListTurnsForTasks(ctx, ids); return err }},
		{"turns-token-usage", func() error { _, err := fixture.store.ListTurnTokenUsageForTasks(ctx, ids); return err }},
		{"backend-sessions", func() error { _, err := fixture.store.ListBackendSessionsForTasks(ctx, ids); return err }},
		{"facet-counts", func() error { _, err := fixture.store.TaskFacetCounts(ctx); return err }},
		{"decorate-channel-tasks", func() error {
			items := []domain.Task{{ID: "perf-task-000", ProjectID: fixture.projectID}}
			fixture.server.decorateChannelTasks(ctx, items)
			return nil
		}},
		{"marshal-page", func() error {
			page, _, err := fixture.store.ListTasksPage(ctx, "", time.Time{}, "", 50)
			if err != nil {
				return err
			}
			_, err = json.Marshal(map[string]any{"ok": true, "tasks": page})
			return err
		}},
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
