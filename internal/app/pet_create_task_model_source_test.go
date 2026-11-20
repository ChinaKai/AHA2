package app

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// addClaudeCapability gives the workspace a detected Claude catalog entry so the
// native runtime can pick the display name/capabilities instead of synthesizing
// a fallback name.
func addClaudeCapability(t *testing.T, fixture *petTaskFixture, workspace domain.Workspace, wireModel string) {
	t.Helper()
	workspace.Capabilities = map[string]any{"claude": map[string]any{
		"status": "ready", "auth_status": "logged_in",
		"models": []domain.ClaudeModelOption{{
			WireModel: wireModel, ResolvedModel: "claude-" + wireModel, DisplayName: "Claude Native",
		}},
	}}
	workspace.UpdatedAt = fixture.service.now().UTC()
	if err := fixture.database.UpdateWorkspaceDetection(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
}

// taskTitleMissing reports whether a Task with the given title exists, used to
// prove rejected creations did not leave a row behind.
func taskTitleMissing(t *testing.T, fixture *petTaskFixture, title string) bool {
	t.Helper()
	tasks, err := fixture.database.ListTasks(fixture.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Title == title {
			return false
		}
	}
	return true
}

// A managed claude_native catalog model chosen by id must infer its runtime
// source. Before the fix the empty model_source became provider and
// resolveRuntimeSelection rejected the managed model.
func TestPetCreateTaskInfersClaudeNativeSource(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	now := fixture.service.now().UTC()
	nativeModel := domain.Model{
		ID: domain.ClaudeNativeModelID("claude-sonnet-4-5"), DisplayName: "Claude Sonnet",
		ProviderID: domain.OfficialClaudeProviderID, Source: domain.ModelSourceClaudeNative,
		Backend: "claude", WireModel: "claude-sonnet-4-5", WireAPI: "anthropic_messages",
		DefaultEnvGroupID: domain.ClaudeNativeEnvGroupID, CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertModel(fixture.ctx, nativeModel); err != nil {
		t.Fatal(err)
	}
	addClaudeCapability(t, fixture, workspace, "claude-sonnet-4-5")

	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "native 模型任务",
		Request: "用 Claude native 模型执行", ModelID: nativeModel.ID, Start: boolPointer(false),
	})
	if err != nil {
		t.Fatalf("claude_native task creation failed: %v", err)
	}
	snapshot := reviewRuntimeSnapshot(t, fixture, task.ID)
	if source := domain.RuntimeModelSource(snapshot.EnvGroupID, snapshot.CodexAccountID); source != domain.ModelSourceClaudeNative {
		t.Fatalf("snapshot source=%s, want claude_native: %#v", source, snapshot)
	}
	if snapshot.Backend != "claude" || snapshot.WireModel != "claude-sonnet-4-5" ||
		snapshot.ModelID != nativeModel.ID || snapshot.EnvGroupID != domain.ClaudeNativeEnvGroupID ||
		snapshot.CodexAccountID != "" {
		t.Fatalf("unexpected claude_native runtime: %#v", snapshot)
	}
}

// The provider path must keep working exactly as before when no source is
// supplied: the catalog model id stays provider and keeps its env group.
func TestPetCreateTaskProviderSourceDoesNotRegress(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "provider 模型任务",
		Request: "继续使用 provider 模型", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := reviewRuntimeSnapshot(t, fixture, task.ID)
	if source := domain.RuntimeModelSource(snapshot.EnvGroupID, snapshot.CodexAccountID); source != domain.ModelSourceProvider {
		t.Fatalf("snapshot source=%s, want provider: %#v", source, snapshot)
	}
	if snapshot.ModelID != fixture.model.ID || snapshot.WireModel != fixture.model.WireModel ||
		snapshot.Backend != fixture.model.Backend || snapshot.EnvGroupID != fixture.modelEnv.ID {
		t.Fatalf("unexpected provider runtime: %#v", snapshot)
	}
}

// An explicit source that contradicts the catalog entry must fail with the
// stable mismatch error and must not create a Task.
func TestPetCreateTaskRejectsSourceMismatch(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	title := "来源冲突任务"
	_, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: title,
		Request: "provider 模型却指定 native 来源", ModelID: fixture.model.ID,
		ModelSource: domain.ModelSourceClaudeNative, Start: boolPointer(false),
	})
	coded, ok := AsCodedError(err)
	if !ok || coded.Code != ErrCodeModelSourceMismatch {
		t.Fatalf("error = %v, want code %s", err, ErrCodeModelSourceMismatch)
	}
	if !taskTitleMissing(t, fixture, title) {
		t.Fatalf("source mismatch still created Task %q", title)
	}
}

// A unique ready Codex account offering the official wire model is auto-picked,
// so an official catalog entry without an account resolves to the official
// runtime instead of falling back to provider.
func TestPetCreateTaskResolvesOfficialSource(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	now := fixture.service.now().UTC()
	account := domain.CodexAccount{
		ID: "account-create-official", Label: "Create Official", Status: "ready", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-create-official", DisplayName: "GPT Create"}},
		CreatedAt:       now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
		t.Fatal(err)
	}
	officialModel := domain.Model{
		ID: domain.OfficialCodexModelID(account.ID, "gpt-create-official"), DisplayName: "GPT Create",
		ProviderID: domain.OfficialCodexProviderID, Source: domain.ModelSourceOfficial,
		Backend: "codex", WireModel: "gpt-create-official", WireAPI: "responses",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertModel(fixture.ctx, officialModel); err != nil {
		t.Fatal(err)
	}

	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "official 模型任务",
		Request: "用官方 Codex 账号执行", ModelID: officialModel.ID, Start: boolPointer(false),
	})
	if err != nil {
		t.Fatalf("official task creation failed: %v", err)
	}
	snapshot := reviewRuntimeSnapshot(t, fixture, task.ID)
	if source := domain.RuntimeModelSource(snapshot.EnvGroupID, snapshot.CodexAccountID); source != domain.ModelSourceOfficial {
		t.Fatalf("snapshot source=%s, want official: %#v", source, snapshot)
	}
	if snapshot.Backend != "codex" || snapshot.WireModel != "gpt-create-official" ||
		snapshot.CodexAccountID != account.ID || snapshot.ModelID != officialModel.ID {
		t.Fatalf("unexpected official runtime: %#v", snapshot)
	}
}

// Two ready accounts offering the same wire model is ambiguous; the pet tool
// must report it instead of silently binding the Task to one account.
func TestPetCreateTaskRejectsAmbiguousOfficialAccount(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	now := fixture.service.now().UTC()
	for _, account := range []domain.CodexAccount{
		{
			ID: "account-create-a", Label: "Create A", Status: "ready", CredentialConfigured: true,
			AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-create-shared", DisplayName: "Shared"}},
			CreatedAt:       now, UpdatedAt: now,
		},
		{
			ID: "account-create-b", Label: "Create B", Status: "ready", CredentialConfigured: true,
			AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-create-shared", DisplayName: "Shared"}},
			CreatedAt:       now, UpdatedAt: now,
		},
	} {
		if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	officialModel := domain.Model{
		ID: "model-create-official-ambiguous", DisplayName: "Shared",
		ProviderID: domain.OfficialCodexProviderID, Source: domain.ModelSourceOfficial,
		Backend: "codex", WireModel: "gpt-create-shared", WireAPI: "responses",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertModel(fixture.ctx, officialModel); err != nil {
		t.Fatal(err)
	}

	title := "official 账号歧义任务"
	_, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: title,
		Request: "官方模型有两个候选账号", ModelID: officialModel.ID, Start: boolPointer(false),
	})
	coded, ok := AsCodedError(err)
	if !ok || coded.Code != ErrCodeCodexAccountUnavailable {
		t.Fatalf("ambiguous account error = %v, want code %s", err, ErrCodeCodexAccountUnavailable)
	}
	if !strings.Contains(err.Error(), "多个") {
		t.Fatalf("ambiguous account error is not explicit: %v", err)
	}
	if !taskTitleMissing(t, fixture, title) {
		t.Fatalf("ambiguous account still created Task %q", title)
	}
}

// The MCP schema must advertise the new optional fields so the pet can send
// them at all.
func TestPetCreateTaskSchemaExposesModelSource(t *testing.T) {
	fixture := newPetTaskFixture(t)
	var parameters map[string]any
	for _, tool := range fixture.service.PetMCPTools(fixture.ctx) {
		if tool.Name == "aha_create_task" {
			parameters = tool.Parameters
			break
		}
	}
	if parameters == nil {
		t.Fatal("aha_create_task schema not found")
	}
	properties, _ := parameters["properties"].(map[string]any)
	for _, name := range []string{"model_source", "wire_model", "codex_account_id"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("aha_create_task schema is missing %s: %v", name, properties)
		}
	}
	source, _ := properties["model_source"].(map[string]any)
	enum, _ := source["enum"].([]string)
	want := []string{domain.ModelSourceProvider, domain.ModelSourceOfficial, domain.ModelSourceClaudeNative}
	if len(enum) != len(want) {
		t.Fatalf("model_source enum = %v, want %v", enum, want)
	}
	for i := range want {
		if enum[i] != want[i] {
			t.Fatalf("model_source enum = %v, want %v", enum, want)
		}
	}
}
