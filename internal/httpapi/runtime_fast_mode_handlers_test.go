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

// The Fast switch is edited through the Agent config dialog, so the field has to
// survive the PATCH hop in both directions. A handler that drops it looks
// exactly like a UI that cannot reach it, which is how this shipped broken the
// first time. The case that matters most is switching an existing Task from
// Claude to Codex and turning the accelerated tier on in the same edit.
func TestAgentConfigPatchCarriesTheFastTier(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "fast-project", Name: "Fast", ProjectType: "folder", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "fast-workspace", ProjectID: project.ID, Name: "Fast", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	claudeEnv := domain.EnvGroup{
		ID: "fast-env-claude", Name: "Claude", ProviderID: "stub", Backend: "claude", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	codexEnv := domain.EnvGroup{
		ID: "fast-env-codex", Name: "Codex", ProviderID: "stub", Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	claudeModel := domain.Model{
		ID: "fast-model-claude", DisplayName: "Claude", ProviderID: "stub", Backend: "claude",
		WireModel: "sonnet", DefaultEnvGroupID: claudeEnv.ID, CreatedAt: now, UpdatedAt: now,
	}
	codexModel := domain.Model{
		ID: "fast-model-codex", DisplayName: "Codex", ProviderID: "stub", Backend: "codex",
		WireModel: "gpt-5.6-sol", DefaultEnvGroupID: codexEnv.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.UpsertEnvGroup(ctx, claudeEnv) },
		func() error { return database.UpsertEnvGroup(ctx, codexEnv) },
		func() error { return database.UpsertModel(ctx, claudeModel) },
		func() error { return database.UpsertModel(ctx, codexModel) },
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
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Fast task", Request: "run fast",
		Isolation: "inplace", Backend: claudeModel.Backend, ModelSource: claudeModel.Source,
		ModelID: claudeModel.ID, WireModel: claudeModel.WireModel,
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
		if snapshot.FastMode != edit.Agent.FastMode {
			t.Fatalf("PATCH %v reported fast=%t but stored %t", payload, edit.Agent.FastMode, snapshot.FastMode)
		}
		return edit.Agent
	}
	// A Claude Task starts without the tier, and switching it to Codex turns the
	// tier on in the same edit -- the dialog sends the whole runtime selection.
	agent := patch(map[string]any{
		"backend": "codex", "model_source": "provider", "model_id": codexModel.ID,
		"wire_model": codexModel.WireModel, "fast_mode": true,
	})
	if !agent.FastMode || agent.Backend != "codex" {
		t.Fatalf("switching to Codex did not apply the tier: %#v", agent)
	}
	// An edit that does not mention Fast keeps it.
	if agent = patch(map[string]any{"reasoning_effort": "high"}); !agent.FastMode {
		t.Fatalf("edit without fast_mode cleared the tier: %#v", agent)
	}
	// Turning it off is a real edit, not a field the handler ignores.
	if agent = patch(map[string]any{"fast_mode": false}); agent.FastMode {
		t.Fatalf("fast_mode=false was not applied: %#v", agent)
	}
	if agent = patch(map[string]any{"fast_mode": true}); !agent.FastMode {
		t.Fatalf("fast_mode=true was not applied: %#v", agent)
	}
}
