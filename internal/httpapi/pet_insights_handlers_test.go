package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func newPetInsightsAPI(t *testing.T) (*store.Store, *Server, *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	api := New(Config{Store: database, Auth: auth.NewService(database, "setup-test", time.Hour), App: app.NewService(database, secretStore, app.StubExecutor{})})
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	return database, api, server
}

func TestPetApprovalDetailAndInsightsNeedTheOwnerSession(t *testing.T) {
	t.Parallel()
	database, _, server := newPetInsightsAPI(t)
	ctx := context.Background()
	if err := database.CreatePetApproval(ctx, domain.PetApproval{
		ID: "pet_approval_detail", TaskID: "task_pet", Kind: "owner_decision", Title: "<b>merge?</b>", Summary: "why",
		Risk: "high", Status: "pending", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// No session: both routes refuse before reading anything.
	anonymous := &http.Client{}
	for _, path := range []string{"/api/v1/pet/approvals/pet_approval_detail", "/api/v1/pet/approvals/missing", "/api/v1/pet/workbench/insights"} {
		response := requestJSON(t, anonymous, http.MethodGet, server.URL+path, nil, "")
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET %s without a session = %d, want 401", path, response.StatusCode)
		}
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/approvals/pet_approval_detail", nil, "")
	var detail struct {
		OK       bool               `json:"ok"`
		Approval domain.PetApproval `json:"approval"`
	}
	decodeResponse(t, response, &detail)
	if response.StatusCode != http.StatusOK || !detail.OK || detail.Approval.ID != "pet_approval_detail" || detail.Approval.Title != "<b>merge?</b>" || detail.Approval.Risk != "high" {
		t.Fatalf("approval detail = %d %#v", response.StatusCode, detail)
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/approvals/missing", nil, "")
	var missing struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	decodeResponse(t, response, &missing)
	if response.StatusCode != http.StatusNotFound || missing.OK || missing.Error != "pet_approval_not_found" {
		t.Fatalf("missing approval = %d %#v", response.StatusCode, missing)
	}

	// The pending-count route is still its own, not captured by {id}.
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/approvals/pending-count", nil, "")
	var pending struct {
		Pending int `json:"pending"`
	}
	decodeResponse(t, response, &pending)
	if response.StatusCode != http.StatusOK || pending.Pending != 1 {
		t.Fatalf("pending count = %d %#v", response.StatusCode, pending)
	}

	if err := database.AppendPetEvent(ctx, domain.PetEvent{ID: "pet_event_one", At: time.Now().UTC(), Actor: "pet", Kind: "task_created", Detail: "task-x"}); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/workbench/insights", nil, "")
	var insights struct {
		OK       bool `json:"ok"`
		Insights struct {
			From      time.Time `json:"from"`
			To        time.Time `json:"to"`
			Actions   int       `json:"actions"`
			Approvals struct {
				Total   int `json:"total"`
				Pending int `json:"pending"`
			} `json:"approvals"`
			Wakes struct {
				TokenScope string `json:"token_scope"`
			} `json:"wakes"`
		} `json:"insights"`
	}
	decodeResponse(t, response, &insights)
	if response.StatusCode != http.StatusOK || !insights.OK || insights.Insights.Actions != 1 || insights.Insights.Approvals.Total != 1 || insights.Insights.Approvals.Pending != 1 {
		t.Fatalf("insights = %d %#v", response.StatusCode, insights)
	}
	if span := insights.Insights.To.Sub(insights.Insights.From); span < 7*24*time.Hour || span > 8*24*time.Hour+time.Hour {
		t.Fatalf("window spans %s, want seven whole days plus today", span)
	}
	// The pet Task has no Turns, so nothing is summable and the scope is the
	// labelled cumulative one.
	if insights.Insights.Wakes.TokenScope != app.PetTokenScopeCumulative && insights.Insights.Wakes.TokenScope != app.PetTokenScopeWindow {
		t.Fatalf("token scope = %q", insights.Insights.Wakes.TokenScope)
	}
}

// petTokenTask creates a Task with one snapshot per backend so Turns can be
// pointed at a backend through RuntimeConfigSnapshotID.
func petTokenTask(t *testing.T, database *store.Store, taskID string, backends ...string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-" + taskID, Name: taskID, CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: "workspace-" + taskID, ProjectID: project.ID, Name: taskID, Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{ID: "env-" + taskID, Name: taskID, ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{ID: "model-" + taskID, DisplayName: taskID, ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	snapshot := func(backend string) domain.RuntimeConfigSnapshot {
		return domain.RuntimeConfigSnapshot{ID: taskID + "-" + backend, WorkspaceID: workspace.ID, Backend: backend, ModelID: model.ID, WireModel: "stub", EnvGroupID: env.ID, EnvGroupRevision: 1, CreatedAt: now}
	}
	first := snapshot(backends[0])
	if _, err := database.CreateTaskWithSnapshot(ctx, first, domain.Task{
		ID: taskID, ProjectID: project.ID, WorkspaceID: workspace.ID, Title: taskID, CurrentGoal: taskID,
		Status: domain.TaskActive, RuntimeConfigSnapshotID: first.ID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, backend := range backends[1:] {
		if err := database.CreateRuntimeSnapshot(ctx, snapshot(backend)); err != nil {
			t.Fatal(err)
		}
	}
}

func petTokenTurn(t *testing.T, database *store.Store, taskID string, sequence int, backend, session string, usage map[string]any) domain.Turn {
	t.Helper()
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Duration(sequence) * time.Minute)
	message := domain.Message{ID: fmt.Sprintf("%s-message-%d", taskID, sequence), TaskID: taskID, Role: "user", Sender: "aha", Content: "wake", CreatedAt: at}
	if err := database.AddMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	turn := domain.Turn{
		ID: fmt.Sprintf("%s-turn-%d", taskID, sequence), TaskID: taskID, AgentID: "main", Sequence: sequence,
		InputMessageID: message.ID, Status: domain.TurnSucceeded, BackendSessionID: session, Usage: usage,
		RoundID: fmt.Sprintf("%s-round-%d", taskID, sequence), RuntimeConfigSnapshotID: taskID + "-" + backend,
		QueuedAt: at, StartedAt: at, FinishedAt: at,
	}
	if err := database.CreateTurn(ctx, turn); err != nil {
		t.Fatal(err)
	}
	return turn
}

func TestPetInsightTokensUseTheWindowOnlyWhenEveryTurnIsSummable(t *testing.T) {
	t.Parallel()
	database, api, _ := newPetInsightsAPI(t)
	ctx := context.Background()

	// claude: a session's total is the sum of its Turns, so the window Turn
	// is counted alone even though the session also has an older Turn and a
	// stored running total.
	petTokenTask(t, database, "task-claude", "claude")
	petTokenTurn(t, database, "task-claude", 1, "claude", "session-claude", map[string]any{"input_tokens": float64(1000), "output_tokens": float64(1)})
	window := petTokenTurn(t, database, "task-claude", 2, "claude", "session-claude", map[string]any{"input_tokens": float64(30), "output_tokens": float64(5), "cache_read_input_tokens": float64(2)})
	if err := database.UpsertBackendSession(ctx, domain.BackendSession{ID: "session-claude", TaskID: "task-claude", AgentID: "main", WorkspaceID: "workspace-task-claude", ModelID: "model-task-claude", Backend: "claude", Status: "active", ContextUsageJSON: `{"input_tokens":5000}`, CreatedAt: time.Now().UTC(), LastUsedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	total, scope, err := api.petInsightTokens(ctx, "task-claude", []domain.Turn{window})
	if err != nil || scope != app.PetTokenScopeWindow || total != 37 {
		t.Fatalf("claude window = %d %q %v, want 37 window", total, scope, err)
	}

	// codex: Turn usage is a delta against the session's running total, so a
	// window Turn cannot be summed on its own. The answer is the Task's
	// cumulative total, the same number the Task list shows.
	petTokenTask(t, database, "task-codex", "codex", "claude")
	petTokenTurn(t, database, "task-codex", 1, "codex", "session-codex", map[string]any{"input_tokens": float64(100), "output_tokens": float64(10)})
	codexWindow := petTokenTurn(t, database, "task-codex", 2, "codex", "session-codex", map[string]any{"input_tokens": float64(20), "output_tokens": float64(5)})
	claudeWindow := petTokenTurn(t, database, "task-codex", 3, "claude", "session-codex-claude", map[string]any{"input_tokens": float64(7)})
	if err := database.UpsertBackendSession(ctx, domain.BackendSession{ID: "session-codex", TaskID: "task-codex", AgentID: "main", WorkspaceID: "workspace-task-codex", ModelID: "model-task-codex", Backend: "codex", Status: "active", ContextUsageJSON: `{"input_tokens":120,"output_tokens":15}`, CreatedAt: time.Now().UTC(), LastUsedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	turnsByTask, err := database.ListTurnTokenUsageForTasks(ctx, []string{"task-codex"})
	if err != nil {
		t.Fatal(err)
	}
	sessionsByTask, err := database.ListBackendSessionsForTasks(ctx, []string{"task-codex"})
	if err != nil {
		t.Fatal(err)
	}
	listed := int64(taskTotalTokens(turnsByTask["task-codex"], sessionsByTask["task-codex"], map[string]string{"task-codex-codex": "codex", "task-codex-claude": "claude"}))
	total, scope, err = api.petInsightTokens(ctx, "task-codex", []domain.Turn{codexWindow, claudeWindow})
	if err != nil || scope != app.PetTokenScopeCumulative || total != listed || total != 135+7 {
		t.Fatalf("codex window = %d %q %v, want cumulative %d", total, scope, err, listed)
	}

	// No Turns in the window is trivially summable: zero, window scope.
	if total, scope, err = api.petInsightTokens(ctx, "task-codex", nil); err != nil || total != 0 || scope != app.PetTokenScopeWindow {
		t.Fatalf("empty window = %d %q %v", total, scope, err)
	}
}
