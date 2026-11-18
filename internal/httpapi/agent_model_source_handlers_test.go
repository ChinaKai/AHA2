package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type agentConfigHTTPResponse struct {
	OK      bool   `json:"ok"`
	Status  string `json:"status"`
	Changed bool   `json:"changed"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

func patchAgentConfigForTest(t *testing.T, server *Server, taskID, agentID string, payload any) (int, agentConfigHTTPResponse) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+taskID+"/agents/"+agentID, strings.NewReader(string(body)))
	request.SetPathValue("id", taskID)
	request.SetPathValue("agent", agentID)
	recorder := httptest.NewRecorder()
	server.updateAgentConfig(recorder, request)
	var response agentConfigHTTPResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v (%s)", err, recorder.Body.String())
	}
	return recorder.Code, response
}

func TestUpdateAgentConfigHTTPReportsSourceChangesAndRejections(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	project := domain.Project{ID: "project-http-source", Name: "HTTP source", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-http-source", ProjectID: project.ID, Name: "HTTP source", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	provider := domain.Provider{
		ID: "provider-http-source", Name: "Gateway", BaseURL: "https://gateway.test/v1",
		AuthStyle: "bearer", CredentialRef: "provider/http-source", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-http-source", Name: "Gateway / gpt-env", ProviderID: provider.ID, Backend: "codex", Revision: 1,
		Environment: map[string]string{"OPENAI_MODEL": "gpt-env"}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-http-source", DisplayName: "Env", ProviderID: provider.ID, Source: domain.ModelSourceProvider,
		Backend: "codex", WireModel: "gpt-env", WireAPI: "responses", DefaultEnvGroupID: envGroup.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	account := domain.CodexAccount{
		ID: "account-http-source", Label: "Official", Status: "ready", CredentialRef: "codex/http-source",
		CredentialConfigured: true, AvailableModels: []domain.CodexModelOption{{
			WireModel: "gpt-official", DisplayName: "GPT Official",
		}}, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertProvider(ctx, provider) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
		func() error { return database.UpsertCodexAccount(ctx, account) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	service := app.NewService(database, nil, nil)
	task, err := service.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "HTTP source task", Request: "test",
		Isolation: "inplace", Backend: "codex", ModelSource: domain.ModelSourceProvider, ModelID: model.ID,
		ReasoningEffort: "high", Filesystem: "workspace-write", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := New(Config{Store: database, App: service})

	status, response := patchAgentConfigForTest(t, server, task.ID, "main", map[string]any{
		"model_source": "official", "wire_model": "gpt-official", "codex_account_id": account.ID,
	})
	if status != http.StatusOK || !response.OK || response.Status != "updated" || !response.Changed {
		t.Fatalf("official switch status=%d response=%#v", status, response)
	}
	agents, err := service.TaskAgents(ctx, task.ID)
	if err != nil || len(agents) == 0 {
		t.Fatalf("agents=%#v err=%v", agents, err)
	}
	officialModelID := agents[0].ModelID

	status, response = patchAgentConfigForTest(t, server, task.ID, "main", map[string]any{
		"model_source": "official", "wire_model": "gpt-official", "codex_account_id": account.ID,
	})
	if status != http.StatusOK || !response.OK || response.Status != "unchanged" || response.Changed {
		t.Fatalf("repeated update status=%d response=%#v", status, response)
	}

	cases := []struct {
		name    string
		payload map[string]any
		status  int
		code    string
	}{
		{"unknown source", map[string]any{"model_source": "mystery", "model_id": model.ID}, http.StatusBadRequest, app.ErrCodeModelSourceInvalid},
		{"source mismatch", map[string]any{"model_source": "provider", "model_id": officialModelID}, http.StatusBadRequest, app.ErrCodeModelSourceMismatch},
		{"missing provider model", map[string]any{"model_source": "provider"}, http.StatusBadRequest, app.ErrCodeModelRequired},
		{"unavailable official model", map[string]any{"model_source": "official", "wire_model": "gpt-missing", "codex_account_id": account.ID}, http.StatusBadRequest, app.ErrCodeModelSelectionInvalid},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			status, response := patchAgentConfigForTest(t, server, task.ID, "main", item.payload)
			if status != item.status || response.Error != item.code {
				t.Fatalf("status=%d response=%#v, want %d/%s", status, response, item.status, item.code)
			}
			if response.Message == "" {
				t.Fatalf("rejection had no readable message: %#v", response)
			}
		})
	}

	status, response = patchAgentConfigForTest(t, server, task.ID, "missing", map[string]any{"model_id": model.ID})
	if status != http.StatusNotFound || response.Error != app.ErrCodeAgentNotFound {
		t.Fatalf("missing agent status=%d response=%#v", status, response)
	}
}
