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

// The Agent config dialog used to save a per-Task stream_input switch. Live
// inbox delivery is always on now, so an old client that still sends the field
// must be accepted but ignored, and the response must not expose it again.
func TestAgentConfigPatchIgnoresDeprecatedStreamInput(t *testing.T) {
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
	patch := func(payload map[string]any) map[string]any {
		t.Helper()
		response := requestJSON(t, client, http.MethodPatch, server.URL+"/api/v1/tasks/"+task.ID+"/agents/main", payload, csrf)
		if response.StatusCode != http.StatusOK {
			var failure map[string]any
			decodeResponse(t, response, &failure)
			t.Fatalf("PATCH %v status=%d body=%#v", payload, response.StatusCode, failure)
		}
		var body map[string]any
		decodeResponse(t, response, &body)
		agent, ok := body["agent"].(map[string]any)
		if !ok {
			t.Fatalf("PATCH %v returned no agent: %#v", payload, body)
		}
		if _, exposed := agent["stream_input"]; exposed {
			t.Fatalf("PATCH %v still exposes stream_input: %#v", payload, agent)
		}
		snapshotID, _ := agent["runtime_config_snapshot_id"].(string)
		snapshot, snapshotErr := database.RuntimeSnapshot(ctx, snapshotID)
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if snapshot.StreamInput {
			t.Fatalf("PATCH %v stored the deprecated switch: %#v", payload, snapshot)
		}
		return agent
	}
	if agent := patch(map[string]any{"stream_input": true}); agent["backend"] != "claude" {
		t.Fatalf("legacy stream_input broke the runtime edit: %#v", agent)
	}
	// The same field must still be ignored on a later edit that omits it.
	if agent := patch(map[string]any{"reasoning_effort": "high"}); agent["reasoning_effort"] != "high" {
		t.Fatalf("runtime edit without stream_input failed: %#v", agent)
	}
}
