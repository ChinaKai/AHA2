package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestPetWatchAPIListIgnoreAndRemove(t *testing.T) {
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
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	capabilities := agentapi.NewCapabilities()
	petToken, err := capabilities.Issue("task_pet", "main", "turn-pet", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService, AgentCapabilities: capabilities}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	now := time.Now().UTC()
	project := domain.Project{ID: "project-watch", Name: "Watch", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace-watch", ProjectID: project.ID, Name: "Watch", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	envGroup := domain.EnvGroup{
		ID: "env-watch", Name: "Watch", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, envGroup); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model-watch", DisplayName: "Stub", ProviderID: "stub", Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "runtime-watch", WorkspaceID: workspace.ID, Backend: "stub",
		ModelID: model.ID, WireModel: "stub", EnvGroupID: envGroup.ID,
		EnvGroupRevision: 1, PermissionsJSON: "{}", CreatedAt: now,
	}
	if err := database.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{
		ID: "task-watch", ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Watched task", OriginalRequest: "request", CurrentGoal: "request",
		Status: domain.TaskWaitingUser, RuntimeConfigSnapshotID: snapshot.ID,
		CollaborationMode: "single", MaxAgents: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	task, err = database.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AppendPetEvent(ctx, domain.PetEvent{
		ID: "event-watch-started", Kind: "task_started", Actor: "pet", Detail: task.ID, At: now,
	}); err != nil {
		t.Fatal(err)
	}
	readWorkState := func() app.PetWorkState {
		t.Helper()
		raw := petMCPToolText(t, client, server.URL, petToken, "aha_pet_work_state", map[string]any{})
		var state struct {
			app.PetWorkState
			OpenTasks []struct {
				app.PetOpenTask
				CreatedByPet *bool `json:"created_by_pet"`
			} `json:"open_tasks"`
		}
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			t.Fatal(err)
		}
		for _, item := range state.OpenTasks {
			if item.CreatedByPet == nil || *item.CreatedByPet {
				t.Fatalf("owner task %s should serialize created_by_pet=false: %s", item.ID, raw)
			}
			state.PetWorkState.OpenTasks = append(state.PetWorkState.OpenTasks, item.PetOpenTask)
		}
		return state.PetWorkState
	}
	assertOpen := func(want int) {
		t.Helper()
		state := readWorkState()
		if len(state.IgnoredTasks) != 0 || len(state.OpenTasks) != want ||
			state.Inspection.AttentionTasks != 0 || state.Inspection.OpenTasks != want {
			t.Fatalf("open=%+v ignored=%+v inspection=%+v", state.OpenTasks, state.IgnoredTasks, state.Inspection)
		}
		if want == 1 && (state.OpenTasks[0].ID != task.ID || state.OpenTasks[0].Status != string(domain.TaskWaitingUser) ||
			state.OpenTasks[0].NeedsAttention || state.OpenTasks[0].AttentionReason != "") {
			t.Fatalf("owner-followed waiting task missing: %+v", state.OpenTasks)
		}
		response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/workbench", nil, "")
		var view struct {
			Inspection app.PetInspection `json:"inspection"`
		}
		decodeResponse(t, response, &view)
		if response.StatusCode != http.StatusOK || view.Inspection.OpenTasks != want || view.Inspection.AttentionTasks != 0 {
			t.Fatalf("workbench=%d inspection=%+v, want %d open tasks", response.StatusCode, view.Inspection, want)
		}
	}

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/workbench", nil, "")
	var workbench struct {
		Watches []struct {
			TaskID  string `json:"task_id"`
			Watched bool   `json:"watched"`
			Ignored bool   `json:"ignored"`
		} `json:"watches"`
	}
	decodeResponse(t, response, &workbench)
	if response.StatusCode != http.StatusOK || len(workbench.Watches) != 1 ||
		workbench.Watches[0].TaskID != task.ID || workbench.Watches[0].Watched || workbench.Watches[0].Ignored {
		t.Fatalf("workbench=%d %#v", response.StatusCode, workbench)
	}
	if _, err := database.PetTaskWatch(ctx, task.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unfollowed task should start without a watch row: %v", err)
	}
	assertOpen(0)

	// Ignore directly without following first, then restore back to the
	// unfollowed list instead of silently starting a follow.
	var ignored struct {
		Watch struct {
			Ignored bool `json:"ignored"`
		} `json:"watch"`
	}
	response = requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/pet/watches/"+task.ID,
		map[string]any{"ignored": true}, csrf)
	decodeResponse(t, response, &ignored)
	if response.StatusCode != http.StatusOK || !ignored.Watch.Ignored {
		t.Fatalf("ignore without follow=%d %#v", response.StatusCode, ignored)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/workbench", nil, "")
	var ignoredWorkbench struct {
		Inspection app.PetInspection `json:"inspection"`
		Watches    []struct {
			TaskID    string    `json:"task_id"`
			Watched   bool      `json:"watched"`
			Ignored   bool      `json:"ignored"`
			IgnoredAt time.Time `json:"ignored_at"`
		} `json:"watches"`
	}
	decodeResponse(t, response, &ignoredWorkbench)
	if len(ignoredWorkbench.Watches) != 1 || ignoredWorkbench.Watches[0].Watched || !ignoredWorkbench.Watches[0].Ignored {
		t.Fatalf("ignored-unwatched workbench=%#v", ignoredWorkbench.Watches)
	}
	watch, err := database.PetTaskWatch(ctx, task.ID)
	if err != nil || watch.Reason != "ignored" || watch.IgnoredAt.IsZero() || !watch.IgnoredAt.Equal(ignoredWorkbench.Watches[0].IgnoredAt) {
		t.Fatalf("stored ignore=%+v web=%+v err=%v", watch, ignoredWorkbench.Watches[0], err)
	}
	state := readWorkState()
	if len(state.OpenTasks) != 0 || len(state.Work) != 0 || state.Inspection.OpenTasks != 0 ||
		state.Inspection.AttentionTasks != 0 || state.Inspection.Note != "没有未闭环任务。" || state.Note == "" ||
		ignoredWorkbench.Inspection.OpenTasks != 0 || ignoredWorkbench.Inspection.AttentionTasks != 0 {
		t.Fatalf("ignored task still actionable: open=%+v work=%+v inspection=%+v note=%q web=%+v",
			state.OpenTasks, state.Work, state.Inspection, state.Note, ignoredWorkbench.Inspection)
	}
	wantIgnored := app.PetIgnoredTask{ID: task.ID, Code: task.Code, Title: task.Title, Status: task.Status, IgnoredAt: watch.IgnoredAt}
	if len(state.IgnoredTasks) != 1 || state.IgnoredTasks[0] != wantIgnored {
		t.Fatalf("ignored tasks=%+v want=%+v", state.IgnoredTasks, wantIgnored)
	}
	response = requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/pet/watches/"+task.ID,
		map[string]any{"ignored": false}, csrf)
	var restored struct {
		Watch struct {
			Ignored bool `json:"ignored"`
		} `json:"watch"`
	}
	decodeResponse(t, response, &restored)
	if response.StatusCode != http.StatusOK || restored.Watch.Ignored {
		t.Fatalf("restore without follow=%d %#v", response.StatusCode, restored)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/workbench", nil, "")
	decodeResponse(t, response, &ignoredWorkbench)
	if len(ignoredWorkbench.Watches) != 1 || ignoredWorkbench.Watches[0].Watched || ignoredWorkbench.Watches[0].Ignored {
		t.Fatalf("restored-unwatched workbench=%#v", ignoredWorkbench.Watches)
	}
	response.Body.Close()
	assertOpen(0)

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/watches/"+task.ID, nil, csrf)
	var followed struct {
		Watch struct {
			TaskID string `json:"task_id"`
		} `json:"watch"`
	}
	decodeResponse(t, response, &followed)
	if response.StatusCode != http.StatusOK || followed.Watch.TaskID != task.ID {
		t.Fatalf("follow=%d %#v", response.StatusCode, followed)
	}
	assertOpen(1)

	response = requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/pet/watches/"+task.ID,
		map[string]any{"ignored": true}, csrf)
	decodeResponse(t, response, &ignored)
	if response.StatusCode != http.StatusOK || !ignored.Watch.Ignored {
		t.Fatalf("ignore=%d %#v", response.StatusCode, ignored)
	}

	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/pet/watches/"+task.ID, nil, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("remove=%d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/workbench", nil, "")
	decodeResponse(t, response, &workbench)
	if len(workbench.Watches) != 1 || workbench.Watches[0].TaskID != task.ID || workbench.Watches[0].Watched {
		t.Fatalf("watches after remove=%#v", workbench.Watches)
	}
	assertOpen(0)
}
