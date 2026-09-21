package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const (
	windowTaskID  = "task-window"
	windowContext = "/api/v1/tasks/" + windowTaskID + "/context"
	// The occupancy the fixture transcript reports: one request's whole input.
	windowMeasured = float64(445000 + 1100)
)

var windowBaseTime = time.Date(2026, 9, 20, 5, 0, 0, 0, time.UTC)

type windowFixture struct {
	server   *httptest.Server
	client   *http.Client
	csrf     string
	task     domain.Task
	database *store.Store
}

// claudeWindowFixture builds a Task whose first Turn ran a Claude model through a
// gateway Env group -- the shape the context page has to render. The Env group
// carries the model name, which is where the "[1m]" marker is read from, and the
// backend Session has a transcript holding a measured occupancy.
func claudeWindowFixture(t *testing.T, wireModel, envModel string) windowFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := windowBaseTime
	project := domain.Project{ID: "project-window", Name: "Window", ProjectType: "folder"}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	workspace := domain.Workspace{ID: "workspace-window", ProjectID: project.ID, Name: "W", Locality: "local", Transport: "native", RootPath: workDir}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{"ANTHROPIC_BASE_URL": "https://tokens-api.example"}
	if envModel != "" {
		environment["ANTHROPIC_MODEL"] = envModel
	}
	group := domain.EnvGroup{ID: "env-window", Name: "Gateway", ProviderID: "provider-window", Backend: "claude", Revision: 1, Environment: environment, SecretRefs: map[string]string{}}
	if err := database.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{ID: "model-window", DisplayName: wireModel, ProviderID: "provider-window", Backend: "claude", WireModel: wireModel, WireAPI: "anthropic_messages", DefaultEnvGroupID: group.ID}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, nil, app.StubExecutor{})
	task, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, ID: windowTaskID,
		Title: "Window task", Request: "go", StartMode: "manual",
		Isolation: "inplace", Backend: model.Backend, ModelSource: model.Source, ModelID: model.ID, WireModel: model.WireModel,
		Filesystem: "workspace-write", Approval: "never", CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "inherit",
	})
	if err != nil {
		t.Fatal(err)
	}
	session := domain.BackendSession{
		ID: "bs-window", TaskID: task.ID, AgentID: "main", WorkspaceID: workspace.ID,
		Backend: "claude", ModelID: model.ID, EnvGroupRevision: 1, Status: "active",
		ProviderSession: "ps-window", CreatedAt: now.Add(-time.Hour), LastUsedAt: now,
	}
	if err := database.UpsertBackendSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	writeClaudeTranscript(t, workDir, session)
	message := domain.Message{ID: "message-window-1", TaskID: task.ID, Role: "user", Sender: "owner", Content: "go", CreatedAt: now}
	round := domain.TaskRound{ID: "round-window-1", TaskID: task.ID, Sequence: 1, InputMessageID: message.ID, Status: domain.RoundRunning, CreatedAt: now}
	turn := domain.Turn{
		ID: "turn-window-1", TaskID: task.ID, AgentID: "main", Sequence: 1, Status: domain.TurnSucceeded,
		InputMessageID: message.ID, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, BackendSessionID: session.ID,
	}
	if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn); err != nil {
		t.Fatalf("create turn: %v", err)
	}
	server := httptest.NewServer(New(Config{Store: database, Auth: authServiceForTest(database), App: appService, Secrets: &fakeSecretStore{}, AgentCapabilities: agentapi.NewCapabilities()}).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	return windowFixture{
		server: server, client: client, csrf: registerOwner(t, client, server.URL),
		task: task, database: database,
	}
}

// writeClaudeTranscript places a transcript where a Claude session keeps it. Its
// newest assistant record carries the one request's whole input, which is the
// occupancy the context page reports.
func writeClaudeTranscript(t *testing.T, workDir string, session domain.BackendSession) {
	t.Helper()
	record, err := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{"usage": map[string]any{
			"input_tokens":            float64(1100),
			"cache_read_input_tokens": float64(445000),
			"output_tokens":           float64(120),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(
		isolatedClaudeSessionRoot(session, domain.Workspace{Transport: "native"}, workDir),
		"-home-repo", session.ProviderSession+".jsonl",
	)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(record, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

type windowPayload struct {
	Context struct {
		ContextWindow  int64          `json:"context_window"`
		ContextPercent float64        `json:"context_percent"`
		Usage          map[string]any `json:"usage"`
		Metrics        map[string]any `json:"metrics"`
	} `json:"context"`
}

func (f windowFixture) readContext(t *testing.T) windowPayload {
	t.Helper()
	response := requestJSON(t, f.client, http.MethodGet, f.server.URL+windowContext, nil, f.csrf)
	var payload windowPayload
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("context status=%d", response.StatusCode)
	}
	return payload
}

func (f windowFixture) startNextTurn(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	// Round 1 has to close first: a Task accepts one running round at a time.
	first, err := f.database.LatestRound(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	finished := windowBaseTime.Add(time.Minute)
	first.Status = domain.RoundCompleted
	first.FinishedAt = finished
	if err := f.database.UpdateRound(ctx, first, domain.RoundRunning); err != nil {
		t.Fatalf("finish round: %v", err)
	}
	now := finished.Add(time.Minute)
	message := domain.Message{ID: "message-window-2", TaskID: f.task.ID, Role: "user", Sender: "owner", Content: "more", CreatedAt: now}
	round := domain.TaskRound{ID: "round-window-2", TaskID: f.task.ID, Sequence: 2, InputMessageID: message.ID, Status: domain.RoundRunning, CreatedAt: now}
	// A Turn that has just started records no usage of its own yet.
	running := domain.Turn{
		ID: "turn-window-2", TaskID: f.task.ID, AgentID: "main", Sequence: 2, Status: domain.TurnRunning,
		InputMessageID: message.ID, RuntimeConfigSnapshotID: f.task.RuntimeConfigSnapshotID,
	}
	if _, _, err := f.database.CreateMessageRoundAndTurn(ctx, message, round, running); err != nil {
		t.Fatalf("create running turn: %v", err)
	}
}

// The context page is where the Owner reads the window and the share of it in
// use. A gateway run whose model name carries the 1M marker is budgeted at 1M,
// so the page must report 1M and the measured share of it.
func TestContextPageReportsWindowAndPercentForGatewayRun(t *testing.T) {
	fixture := claudeWindowFixture(t, "claude-deepseek-v4.1-flash[1m]", "claude-deepseek-v4.1-flash[1m]")
	payload := fixture.readContext(t)
	if payload.Context.ContextWindow != 1_000_000 {
		t.Fatalf("context_window = %d, want 1000000", payload.Context.ContextWindow)
	}
	if got := payload.Context.Metrics["context_window"]; got != float64(1_000_000) {
		t.Fatalf("metrics context_window = %v, want 1000000 (%v)", got, payload.Context.Metrics)
	}
	if got, _ := payload.Context.Usage["context_tokens"].(float64); got != windowMeasured {
		t.Fatalf("context_tokens = %v, want the measured %v (usage=%v)", payload.Context.Usage["context_tokens"], windowMeasured, payload.Context.Usage)
	}
	if want := 44.6; payload.Context.ContextPercent < want-0.1 || payload.Context.ContextPercent > want+0.1 {
		t.Fatalf("context_percent = %v, want about %v", payload.Context.ContextPercent, want)
	}
	// The page reads metrics.context_percent first, so a zero there shows the
	// window with no share of it used -- "未知 / 1.0M", which is the report.
	if got := payload.Context.Metrics["context_percent"]; got != payload.Context.ContextPercent {
		t.Fatalf("metrics context_percent = %v, want %v (the page prefers the metrics value)",
			got, payload.Context.ContextPercent)
	}
	if got := payload.Context.Metrics["context_tokens"]; got != windowMeasured {
		t.Fatalf("metrics context_tokens = %v, want the measured %v", got, windowMeasured)
	}
}

// A Turn that has just started has recorded no usage of its own, but the backend
// Session it continues already occupies a measured share of the window. Falling
// back to the Task's cumulative session totals would exceed the window, and the
// page would blank the row for the whole duration of every Turn.
func TestContextPageKeepsTheSessionsMeasuredContextWhileTurnRuns(t *testing.T) {
	fixture := claudeWindowFixture(t, "claude-deepseek-v4.1-flash[1m]", "claude-deepseek-v4.1-flash[1m]")
	fixture.startNextTurn(t)
	payload := fixture.readContext(t)
	if payload.Context.ContextWindow != 1_000_000 {
		t.Fatalf("context_window = %d, want 1000000", payload.Context.ContextWindow)
	}
	if got, _ := payload.Context.Usage["context_tokens"].(float64); got != windowMeasured {
		t.Fatalf("context_tokens = %v, want the session's measured %v (metrics=%v)",
			payload.Context.Usage["context_tokens"], windowMeasured, payload.Context.Metrics)
	}
	if payload.Context.ContextPercent <= 0 {
		t.Fatalf("a running Turn reports no percent: usage=%v metrics=%v", payload.Context.Usage, payload.Context.Metrics)
	}
}

// A gateway run with no 1M marker is budgeted at the fallback window, so the page
// must not promise the model's capable 1M.
func TestContextPageKeepsUnmarkedGatewayRunAtFallback(t *testing.T) {
	fixture := claudeWindowFixture(t, "claude-deepseek-v4.1-flash", "claude-deepseek-v4.1-flash")
	payload := fixture.readContext(t)
	if payload.Context.ContextWindow != 200_000 {
		t.Fatalf("context_window = %d, want the 200000 fallback", payload.Context.ContextWindow)
	}
	// The measured occupancy exceeds the fallback, so the page must not present it
	// as a share of a window it does not fit in.
	if payload.Context.Usage["context_inconsistent"] == nil && payload.Context.ContextPercent > 100 {
		t.Fatalf("percent = %v exceeds 100 without being flagged", payload.Context.ContextPercent)
	}
}
