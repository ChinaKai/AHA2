package httpapi

import (
	"context"
	"fmt"
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

const codexWindow = 258400

// codexWindowFixture builds a Task whose main Turn ran a Codex model with a
// transcript reporting the model's context window, which is the shape the
// context page reads for that backend.
func codexWindowFixture(t *testing.T, transcriptWindow int) windowFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := windowBaseTime
	// A Codex session with no official account keeps its rollout under the
	// operator's own ~/.codex, so the fixture has to own that home.
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := domain.Project{ID: "project-codex", Name: "Codex", ProjectType: "folder"}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	workspace := domain.Workspace{ID: "workspace-codex", ProjectID: project.ID, Name: "W", Locality: "local", Transport: "native", RootPath: workDir}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	group := domain.EnvGroup{ID: "env-codex", Name: "Codex", ProviderID: "provider-codex", Backend: "codex", Revision: 1, Environment: map[string]string{"OPENAI_BASE_URL": "https://api.example"}, SecretRefs: map[string]string{}}
	if err := database.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{ID: "model-codex", DisplayName: "GPT", ProviderID: "provider-codex", Backend: "codex", WireModel: "gpt-5.6-sol", WireAPI: "responses", ContextWindow: codexWindow, DefaultEnvGroupID: group.ID}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, nil, app.StubExecutor{})
	task, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, ID: windowTaskID,
		Title: "Codex window task", Request: "go", StartMode: "manual",
		Isolation: "inplace", Backend: model.Backend, ModelSource: model.Source, ModelID: model.ID, WireModel: model.WireModel,
		Filesystem: "workspace-write", Approval: "never", CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "inherit",
	})
	if err != nil {
		t.Fatal(err)
	}
	session := domain.BackendSession{
		ID: "bs-codex", TaskID: task.ID, AgentID: "main", WorkspaceID: workspace.ID,
		Backend: "codex", ModelID: model.ID, EnvGroupRevision: 1, Status: "active",
		ProviderSession: "ps-codex", CreatedAt: now.Add(-time.Hour), LastUsedAt: now,
	}
	if err := database.UpsertBackendSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Codex keeps its rollout under the default sessions root, which is the
	// home set above.
	info := `"last_token_usage":{"input_tokens":128592}`
	if transcriptWindow > 0 {
		info = fmt.Sprintf(`"model_context_window":%d,`, transcriptWindow) + info
	}
	record := `{"payload":{"type":"token_count","info":{` + info + `}}}`
	file := filepath.Join(home, ".codex", "sessions", "2026", "09", "20", "rollout-"+session.ProviderSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(record+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	message := domain.Message{ID: "message-codex", TaskID: task.ID, Role: "user", Sender: "owner", Content: "go", CreatedAt: now}
	round := domain.TaskRound{ID: "round-codex", TaskID: task.ID, Sequence: 1, InputMessageID: message.ID, Status: domain.RoundRunning, CreatedAt: now}
	turn := domain.Turn{
		ID: "turn-codex", TaskID: task.ID, AgentID: "main", Sequence: 1, Status: domain.TurnSucceeded,
		InputMessageID: message.ID, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, BackendSessionID: session.ID,
	}
	if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn); err != nil {
		t.Fatalf("create turn: %v", err)
	}
	server := httptest.NewServer(New(Config{Store: database, Auth: authServiceForTest(database), App: appService, Secrets: &fakeSecretStore{}, AgentCapabilities: agentapi.NewCapabilities()}).Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	return windowFixture{server: server, client: client, csrf: registerOwner(t, client, server.URL), task: task, database: database}
}

// The Codex branch reads its window from the transcript, and that is the value
// the page must show. Widening the Claude path must not have changed it.
func TestContextPageKeepsCodexWindowFromTranscript(t *testing.T) {
	fixture := codexWindowFixture(t, codexWindow)
	payload := fixture.readContext(t)
	if payload.Context.ContextWindow != codexWindow {
		t.Fatalf("context_window = %d, want the transcript's %d", payload.Context.ContextWindow, codexWindow)
	}
	if got := payload.Context.Metrics["context_window"]; got != float64(codexWindow) {
		t.Fatalf("metrics context_window = %v, want %d (%v)", got, codexWindow, payload.Context.Metrics)
	}
	if got, _ := payload.Context.Usage["context_tokens"].(float64); got != 128592 {
		t.Fatalf("context_tokens = %v, want the transcript's 128592 (usage=%v)", payload.Context.Usage["context_tokens"], payload.Context.Usage)
	}
	if payload.Context.ContextPercent <= 0 {
		t.Fatalf("context_percent = %v, want the share of the window in use", payload.Context.ContextPercent)
	}
}

// The Codex parser requires a window in the transcript, and a real Codex
// token_count always carries one. That is why the application-level guard can
// accept a sample on its occupancy alone without changing Codex behaviour: the
// parser has already rejected anything less.
func TestCodexSampleStillRequiresAWindow(t *testing.T) {
	t.Parallel()
	record := map[string]any{"payload": map[string]any{
		"type": "token_count",
		"info": map[string]any{"last_token_usage": map[string]any{"input_tokens": float64(128592)}},
	}}
	if sample, ok := codexContextSample(record); ok {
		t.Fatalf("a windowless token_count yielded a sample: %#v", sample)
	}
}
