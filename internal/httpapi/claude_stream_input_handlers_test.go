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

// The Agent config dialog saves the whole runtime selection through one PATCH,
// so the Claude-only stream-json switch has to survive the handler hop. A
// dropped field would look exactly like a checkbox that cannot persist.
func TestAgentConfigPatchCarriesClaudeStreamInput(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "stream-project", Name: "Stream", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "stream-workspace", ProjectID: project.ID, Name: "Stream", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{
		ID: "stream-env", Name: "Claude", ProviderID: "stub", Backend: "claude", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "stream-model", DisplayName: "Claude", ProviderID: "stub", Backend: "claude",
		WireModel: "sonnet", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.UpsertEnvGroup(ctx, env) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	appService := app.NewService(database, nil, app.StubExecutor{})
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authServiceForTest(database), App: appService,
		Secrets: &fakeSecretStore{}, AgentCapabilities: agentapi.NewCapabilities(),
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	task, err := appService.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Stream task", Request: "run",
		Isolation: "inplace", Backend: model.Backend, ModelSource: model.Source,
		ModelID: model.ID, WireModel: model.WireModel,
		Filesystem: "workspace-write", Approval: "never", CollaborationMode: "single", MaxAgents: 1,
		KnowledgePolicy: "inherit",
	})
	if err != nil {
		t.Fatal(err)
	}
	var edit struct {
		Agent domain.TaskAgent `json:"agent"`
	}
	patch := func(payload map[string]any) domain.TaskAgent {
		t.Helper()
		response := requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/tasks/"+task.ID+"/agents/main", payload, csrf)
		decodeResponse(t, response, &edit)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("PATCH %v status=%d", payload, response.StatusCode)
		}
		snapshot, snapshotErr := database.RuntimeSnapshot(ctx, edit.Agent.RuntimeConfigSnapshotID)
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if snapshot.StreamInput != edit.Agent.StreamInput {
			t.Fatalf("PATCH %v reported stream_input=%t but stored %t", payload, edit.Agent.StreamInput, snapshot.StreamInput)
		}
		return edit.Agent
	}
	if agent := patch(map[string]any{"stream_input": true}); !agent.StreamInput || agent.Backend != "claude" {
		t.Fatalf("stream_input=true was not applied: %#v", agent)
	}
	// An edit that does not mention the switch keeps it.
	if agent := patch(map[string]any{"reasoning_effort": "high"}); !agent.StreamInput {
		t.Fatalf("edit without stream_input cleared it: %#v", agent)
	}
	if agent := patch(map[string]any{"stream_input": false}); agent.StreamInput {
		t.Fatalf("stream_input=false was not applied: %#v", agent)
	}
}
