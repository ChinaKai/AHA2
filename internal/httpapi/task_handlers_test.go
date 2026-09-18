package httpapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// A completed Task is frozen for editing: its title, Agent runtime, attachment
// upload and session rotation must all be refused, and only /reopen may lift
// the freeze. Without that, the Web UI would be the only thing enforcing it.
func TestCompletedTaskRejectsEditsUntilReopened(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "frozen-project", Name: "Frozen", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{ID: "frozen-workspace", ProjectID: project.ID, Name: "Frozen", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now, AgentAPIMode: "global", AgentAPIStatus: "unknown"}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{ID: "frozen-env", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{ID: "frozen-model", DisplayName: "Model", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, nil, app.StubExecutor{})
	server := httptest.NewServer(New(Config{Store: database, Auth: authServiceForTest(database), App: appService, Secrets: &fakeSecretStore{}, AgentCapabilities: agentapi.NewCapabilities()}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	task, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Frozen task", Request: "do the thing",
		Isolation: "inplace", Backend: model.Backend, ModelSource: model.Source, ModelID: model.ID, WireModel: model.WireModel,
		Filesystem: "workspace-write", Approval: "never", CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "inherit",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Editing is allowed while the Task is still running.
	response := requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/tasks/"+task.ID, map[string]any{"title": "Renamed"}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("active task title update status=%d", response.StatusCode)
	}
	response.Body.Close()
	if err := appService.CompleteTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	writes := []struct {
		name    string
		method  string
		path    string
		payload map[string]any
	}{
		{"title", http.MethodPatch, "/api/v1/tasks/" + task.ID, map[string]any{"title": "Must not apply"}},
		{"collaboration", http.MethodPatch, "/api/v1/tasks/" + task.ID, map[string]any{"collaboration_mode": "auto", "max_agents": 3}},
		{"skills", http.MethodPatch, "/api/v1/tasks/" + task.ID, map[string]any{"skill_ids": []string{}}},
		{"knowledge", http.MethodPatch, "/api/v1/tasks/" + task.ID, map[string]any{"knowledge_policy": "disabled"}},
		{"agent", http.MethodPatch, "/api/v1/tasks/" + task.ID + "/agents/main", map[string]any{"backend": "stub", "model_source": "provider", "model_id": model.ID, "wire_model": "stub"}},
		{"session_reset", http.MethodPost, "/api/v1/tasks/" + task.ID + "/agents/main/session/reset", map[string]any{}},
		{"session_compact", http.MethodPost, "/api/v1/tasks/" + task.ID + "/agents/main/session/compact", map[string]any{}},
		{"hardware", http.MethodPut, "/api/v1/tasks/" + task.ID + "/hardware", map[string]any{"groups": []any{}}},
	}
	for _, write := range writes {
		response := requestJSON(t, client, write.method, server.URL+write.path, write.payload, csrf)
		var payload struct {
			Error string `json:"error"`
		}
		decodeResponse(t, response, &payload)
		if response.StatusCode != http.StatusConflict || payload.Error != "task_completed_read_only" {
			t.Fatalf("%s on completed task status=%d error=%q", write.name, response.StatusCode, payload.Error)
		}
	}
	// The message endpoint refuses too, and the goal is unchanged.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+task.ID+"/messages", map[string]any{"content": "must not queue"}, csrf)
	if response.StatusCode == http.StatusAccepted {
		t.Fatal("completed task accepted a new message")
	}
	response.Body.Close()
	stored, err := database.Task(ctx, task.ID)
	if err != nil || stored.Title != "Renamed" || stored.KnowledgePolicy != "inherit" {
		t.Fatalf("completed task was modified: %#v err=%v", stored, err)
	}
	// Reopening thaws it, and editing works again.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+task.ID+"/reopen", map[string]any{}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reopen status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/tasks/"+task.ID, map[string]any{"title": "Reopened"}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reopened task title update status=%d", response.StatusCode)
	}
	response.Body.Close()
}

// The task list is paged, so the filter popovers must be counted from the whole
// set. Counting the loaded page reports wrong numbers as soon as the list
// outgrows one page, and drops devices that are not on the first page.
func TestTaskListFacetCountsCoverTheWholeSetBeyondOnePage(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "counts-project", Name: "Counts", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{ID: "counts-workspace", ProjectID: project.ID, Name: "Counts", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now, AgentAPIMode: "global", AgentAPIStatus: "unknown"}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{ID: "counts-env", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{ID: "counts-model", DisplayName: "Model", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, nil, app.StubExecutor{})
	// More rows than one page, so a page-derived count is visibly wrong. CreateTask
	// activates the Task, which is one of the statuses under test; the rest are
	// reached by walking the allowed transitions so the counts cover real states.
	statuses := []domain.TaskStatus{domain.TaskActive, domain.TaskActive, domain.TaskWaitingUser, domain.TaskCompleted, domain.TaskBlocked, domain.TaskCancelled}
	for _, status := range statuses {
		item, err := appService.CreateTask(ctx, app.CreateTaskInput{
			ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Task", Request: "Task",
			Isolation: "inplace", Backend: model.Backend, ModelSource: model.Source, ModelID: model.ID, WireModel: model.WireModel,
			Filesystem: "workspace-write", Approval: "never", CollaborationMode: "single", MaxAgents: 1,
			KnowledgePolicy: "inherit",
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range taskStatusPath(domain.TaskActive, status) {
			if err := database.UpdateTaskStatus(ctx, item.ID, step.from, step.to, time.Now().UTC().Format(time.RFC3339Nano), ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	counts, err := database.TaskFacetCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Status["active"] != 2 || counts.Status["waiting_user"] != 1 || counts.Status["completed"] != 1 || counts.Status["blocked"] != 1 || counts.Status["cancelled"] != 1 {
		t.Fatalf("status counts=%#v", counts.Status)
	}
	if counts.Project[project.ID] != len(statuses) {
		t.Fatalf("project count=%d want %d", counts.Project[project.ID], len(statuses))
	}
	if counts.Device[store.TaskDeviceLocalKey] != len(statuses) {
		t.Fatalf("device count=%#v", counts.Device)
	}

	// The counts must actually reach the API response, and the create endpoint
	// must accept the project-level grants the Web panel now offers.
	server := httptest.NewServer(New(Config{Store: database, Auth: authServiceForTest(database), App: appService, Secrets: &fakeSecretStore{}, AgentCapabilities: agentapi.NewCapabilities()}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks?limit=2", nil, "")
	var list struct {
		Tasks  []domain.Task `json:"tasks"`
		Counts struct {
			Status  map[string]int `json:"status"`
			Project map[string]int `json:"project"`
			Device  map[string]int `json:"device"`
		} `json:"counts"`
		HasMore bool `json:"has_more"`
	}
	decodeResponse(t, response, &list)
	if !list.HasMore || len(list.Tasks) != 2 {
		t.Fatalf("expected a paged response, got %d tasks has_more=%v", len(list.Tasks), list.HasMore)
	}
	if list.Counts.Status["active"] != 2 || list.Counts.Project[project.ID] != len(statuses) || list.Counts.Device["local"] != len(statuses) {
		t.Fatalf("counts do not cover the whole set: %#v", list.Counts)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks", map[string]any{
		"project_id": project.ID, "workspace_id": workspace.ID, "title": "With grants", "request": "With grants",
		"isolation": "inplace", "backend": model.Backend, "model_source": model.Source, "model_id": model.ID, "wire_model": model.WireModel,
		"filesystem": "workspace-write", "approval": "never", "collaboration_mode": "single", "max_agents": 1,
		"start_mode":         "manual",
		"agent_capabilities": map[string]bool{"clone_hardware": true, "task_create": true, "workspace_read": true, "not_a_real_grant": true},
	}, csrf)
	var created struct {
		Task domain.Task `json:"task"`
	}
	decodeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create with grants status=%d", response.StatusCode)
	}
	// normalizeTaskAgentCapabilities drops unknown names, and clone_hardware
	// implies the two narrower grants.
	if !created.Task.AgentCapabilities["workspace_read"] || !created.Task.AgentCapabilities["task_create"] || !created.Task.AgentCapabilities["clone_hardware"] {
		t.Fatalf("grants were not stored: %#v", created.Task.AgentCapabilities)
	}
	if created.Task.AgentCapabilities["not_a_real_grant"] {
		t.Fatalf("an unknown grant was accepted: %#v", created.Task.AgentCapabilities)
	}
	// Omitting the field must leave every grant off, matching the panel default.
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks", map[string]any{
		"project_id": project.ID, "workspace_id": workspace.ID, "title": "No grants", "request": "No grants",
		"isolation": "inplace", "backend": model.Backend, "model_source": model.Source, "model_id": model.ID, "wire_model": model.WireModel,
		"filesystem": "workspace-write", "approval": "never", "collaboration_mode": "single", "max_agents": 1,
		"start_mode": "manual",
	}, csrf)
	var plain struct {
		Task domain.Task `json:"task"`
	}
	decodeResponse(t, response, &plain)
	if len(plain.Task.AgentCapabilities) != 0 {
		t.Fatalf("a task created without grants got %#v", plain.Task.AgentCapabilities)
	}
}

type taskStatusStep struct{ from, to domain.TaskStatus }

// taskStatusPath walks the allowed task transitions from one status to another,
// so a test can reach any status the domain permits instead of writing illegal
// rows straight into the table.
func taskStatusPath(from, to domain.TaskStatus) []taskStatusStep {
	if from == to {
		return nil
	}
	queue := []domain.TaskStatus{from}
	previous := map[domain.TaskStatus]domain.TaskStatus{}
	seen := map[domain.TaskStatus]bool{from: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range []domain.TaskStatus{domain.TaskWaitingUser, domain.TaskActive, domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskCancelled} {
			if domain.ValidateTaskTransition(current, next) != nil || seen[next] {
				continue
			}
			seen[next] = true
			previous[next] = current
			if next == to {
				var reversed []taskStatusStep
				for at := to; at != from; at = previous[at] {
					reversed = append(reversed, taskStatusStep{from: previous[at], to: at})
				}
				for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
					reversed[left], reversed[right] = reversed[right], reversed[left]
				}
				return reversed
			}
			queue = append(queue, next)
		}
	}
	return nil
}
