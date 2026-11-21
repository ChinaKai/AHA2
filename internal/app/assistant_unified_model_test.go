package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

// recordingExecutor captures the one-shot backend chat request so the test can
// assert chat and the Pet Task resolve the same catalog row.
type recordingExecutor struct {
	mu       sync.Mutex
	requests []ExecutionRequest
	reply    string
	err      error
}

func (executor *recordingExecutor) Execute(_ context.Context, request ExecutionRequest, _ func(ExecutionEvent)) (ExecutionResult, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.requests = append(executor.requests, request)
	if executor.err != nil {
		return ExecutionResult{}, executor.err
	}
	return ExecutionResult{Reply: executor.reply}, nil
}

func (executor *recordingExecutor) lastRequest(t *testing.T) ExecutionRequest {
	t.Helper()
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.requests) == 0 {
		t.Fatal("backend chat did not call the executor")
	}
	return executor.requests[len(executor.requests)-1]
}

func secondProviderModel(t *testing.T, fixture *petTaskFixture) domain.Model {
	t.Helper()
	model := domain.Model{
		ID: "model-pet-second", DisplayName: "Second Pet Model", ProviderID: fixture.provider.ID,
		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "pet-second", WireAPI: "chat",
		DefaultEnvGroupID: fixture.modelEnv.ID, CreatedAt: fixture.createdAt, UpdatedAt: fixture.createdAt,
	}
	if err := fixture.database.UpsertModel(fixture.ctx, model); err != nil {
		t.Fatal(err)
	}
	return model
}

func storedAssistant(t *testing.T, fixture *petTaskFixture) domain.OwnerAssistant {
	t.Helper()
	assistant, err := fixture.database.OwnerAssistant(fixture.ctx, ownerAssistantID)
	if err != nil {
		t.Fatal(err)
	}
	return assistant
}

// Legacy rows could hold a chat model and a pet runtime model that disagreed.
// On read the pet runtime wins and the chat column becomes its mirror.
func TestOwnerAssistantNormalizesLegacyDivergentModels(t *testing.T) {
	fixture := newPetTaskFixture(t)
	second := secondProviderModel(t, fixture)
	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, fixture.service.now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	assistant.ModelID = second.ID
	if err := fixture.database.UpdateOwnerAssistant(fixture.ctx, assistant); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.UpdateOwnerPetSettings(fixture.ctx, assistant.ID, true, fixture.model.ID, []string{"walk"}, fixture.service.now().UTC()); err != nil {
		t.Fatal(err)
	}

	view, err := fixture.service.OwnerAssistant(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.BrainModelID != fixture.model.ID || view.Assistant.ModelID != fixture.model.ID {
		t.Fatalf("legacy divergence was not normalized: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
	}
	if stored := storedAssistant(t, fixture); stored.BrainModelID != fixture.model.ID || stored.ModelID != fixture.model.ID {
		t.Fatalf("normalization was not persisted: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
	}
}

// A legacy row with only the old chat field maps it into the unified field.
func TestOwnerAssistantNormalizesLegacyChatOnlyRow(t *testing.T) {
	fixture := newPetTaskFixture(t)
	second := secondProviderModel(t, fixture)
	assistant, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, fixture.service.now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	assistant.ModelID = second.ID
	if err := fixture.database.UpdateOwnerAssistant(fixture.ctx, assistant); err != nil {
		t.Fatal(err)
	}
	view, err := fixture.service.OwnerAssistant(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.BrainModelID != second.ID || view.Assistant.ModelID != second.ID {
		t.Fatalf("chat-only legacy row was not unified: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
	}
}

// The old assistant endpoint used to switch the chat model on its own. It must
// refuse a different model instead of creating a second source of truth.
func TestUpdateOwnerAssistantRejectsIndependentChatModel(t *testing.T) {
	fixture := newPetTaskFixture(t)
	second := secondProviderModel(t, fixture)
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceProvider, fixture.model.ID, "", "",
	)); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.service.UpdateOwnerAssistant(fixture.ctx, second.ID, "", "")
	if err == nil || !strings.Contains(err.Error(), "宠物模型") {
		t.Fatalf("independent chat model change was accepted: %v", err)
	}
	stored := storedAssistant(t, fixture)
	if stored.BrainModelID != fixture.model.ID || stored.ModelID != fixture.model.ID {
		t.Fatalf("rejected change mutated the model: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
	}
	// Naming and appearance changes still work with no model in the payload.
	if _, err := fixture.service.UpdateOwnerAssistant(fixture.ctx, "", "小A", "deepseek_fish"); err != nil {
		t.Fatalf("non-model assistant update failed: %v", err)
	}
}

// A legacy settings client that only sends chat_model_id selects the unified
// Provider model; it is a compatibility mapping, not a second chat model.
func TestPetSettingsLegacyChatModelMapsToUnifiedModel(t *testing.T) {
	fixture := newPetTaskFixture(t)
	second := secondProviderModel(t, fixture)
	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel("", "", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.BrainModelID != fixture.model.ID {
		t.Fatalf("empty selection did not keep the unified model: %q", view.Assistant.BrainModelID)
	}

	input := petSettingsWithModel("", "", "", "")
	input.ChatModelID = second.ID
	view, err = fixture.service.UpdateOwnerPetSettings(fixture.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.BrainModelID != second.ID || view.Assistant.ModelID != second.ID {
		t.Fatalf("legacy chat_model_id did not map to the unified model: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
	}
}

// A hostile/old client sending both fields must not let chat_model_id override
// the source-first pet selection.
func TestPetSettingsPetSelectionWinsOverChatModelID(t *testing.T) {
	fixture := newPetTaskFixture(t)
	second := secondProviderModel(t, fixture)
	input := petSettingsWithModel(domain.ModelSourceProvider, fixture.model.ID, "", "")
	input.ChatModelID = second.ID
	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.BrainModelID != fixture.model.ID || view.Assistant.ModelID != fixture.model.ID {
		t.Fatalf("chat_model_id overrode the pet selection: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
	}
}

// Chat with a non-Provider source must use the same catalog row and backend the
// Pet Task runs, not silently fall back to a Provider model.
func TestAssistantChatUsesUnifiedClaudeNativeModel(t *testing.T) {
	executor := &recordingExecutor{reply: "claude 的回答"}
	fixture := newPetTaskFixtureWithExecutor(t, executor)
	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceClaudeNative, "", "claude-sonnet-4-5", "",
	))
	if err != nil {
		t.Fatal(err)
	}
	wantModelID := domain.ClaudeNativeModelID("claude-sonnet-4-5")
	if view.Assistant.BrainModelID != wantModelID || view.Assistant.ModelID != wantModelID {
		t.Fatalf("unified model=(brain=%q chat=%q), want %q", view.Assistant.BrainModelID, view.Assistant.ModelID, wantModelID)
	}

	reply, err := fixture.service.AskOwnerAssistant(fixture.ctx, "陪我聊聊")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Reply.Content != "claude 的回答" {
		t.Fatalf("reply=%q", reply.Reply.Content)
	}
	request := executor.lastRequest(t)
	if request.Snapshot.ModelID != wantModelID || request.Snapshot.Backend != "claude" ||
		request.Model.ID != wantModelID || request.Model.Source != domain.ModelSourceClaudeNative {
		t.Fatalf("chat backend request did not use the unified model: snapshot=%#v model=%#v", request.Snapshot, request.Model)
	}
	if !strings.Contains(request.Prompt, "陪我聊聊") || !strings.Contains(request.Prompt, "<tool_call>") {
		t.Fatalf("chat prompt is missing the user message or the text tool protocol:\n%s", request.Prompt)
	}
	if request.Filesystem != "read-only" {
		t.Fatalf("chat backend ran with filesystem=%q", request.Filesystem)
	}
}

// The unified model and the Pet Task runtime snapshot must survive a service
// restart, and chat after the restart must resolve the same row.
func TestUnifiedModelSurvivesRestart(t *testing.T) {
	executor := &recordingExecutor{reply: "重启后回答"}
	fixture := newPetTaskFixtureWithExecutor(t, executor)
	wantModelID := domain.ClaudeNativeModelID("claude-sonnet-4-5")
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceClaudeNative, "", "claude-sonnet-4-5", "",
	)); err != nil {
		t.Fatal(err)
	}
	// The save path returns the assistant view, which creates the Pet Task.
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.Open(fixture.ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewService(reopened, secretStore, executor)
	defer func() { _ = restarted.Shutdown(context.Background()) }()

	view, err := restarted.OwnerAssistant(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.BrainModelID != wantModelID || view.Assistant.ModelID != wantModelID ||
		view.PetModelSource != domain.ModelSourceClaudeNative {
		t.Fatalf("model after restart=(brain=%q chat=%q source=%q)", view.Assistant.BrainModelID, view.Assistant.ModelID, view.PetModelSource)
	}
	agent, err := reopened.TaskAgent(fixture.ctx, petTaskID, "main")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reopened.RuntimeSnapshot(fixture.ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ModelID != wantModelID || snapshot.Backend != "claude" {
		t.Fatalf("pet task runtime after restart=%#v", snapshot)
	}

	reply, err := restarted.AskOwnerAssistant(fixture.ctx, "重启后还在吗")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Reply.Content != "重启后回答" {
		t.Fatalf("reply after restart=%q", reply.Reply.Content)
	}
	if request := executor.lastRequest(t); request.Snapshot.ModelID != wantModelID || request.Model.ID != wantModelID {
		t.Fatalf("chat after restart used %q/%q, want %q", request.Snapshot.ModelID, request.Model.ID, wantModelID)
	}
}

// A deleted unified model is an explicit error. It must never be replaced with
// a different catalog row.
func TestAssistantChatDeletedUnifiedModelDoesNotFallBack(t *testing.T) {
	fixture := newPetTaskFixture(t)
	second := secondProviderModel(t, fixture)
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceProvider, second.ID, "", "",
	)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.DeleteModel(fixture.ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.service.AskOwnerAssistant(fixture.ctx, "你好")
	if err == nil || !strings.Contains(err.Error(), "已删除") {
		t.Fatalf("deleted unified model was not an explicit error: %v", err)
	}
	if messages, messageErr := fixture.database.AssistantMessages(fixture.ctx, ownerAssistantID, 10); messageErr != nil || len(messages) != 0 {
		t.Fatalf("failed chat persisted messages: %#v err=%v", messages, messageErr)
	}
	view, viewErr := fixture.service.OwnerAssistant(fixture.ctx)
	if viewErr != nil {
		t.Fatal(viewErr)
	}
	if view.Assistant.BrainModelID != second.ID || view.Assistant.ModelID != second.ID {
		t.Fatalf("read fell back to another model: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
	}
	if view.PetModelAvailable || !strings.Contains(view.PetModelUnavailableReason, "删除") {
		t.Fatalf("unavailable readback=%q available=%t", view.PetModelUnavailableReason, view.PetModelAvailable)
	}
}

// The read-only chat label describes source and account, so the Web field can
// show exactly what the picker represents.
func TestOwnerAssistantModelLabelIncludesSourceAndAccount(t *testing.T) {
	fixture := newPetTaskFixture(t)
	now := fixture.service.now().UTC()
	account := domain.CodexAccount{
		ID: "account-pet-label", Label: "Pet Plus", Status: "ready", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-label", DisplayName: "GPT Label"}},
		CreatedAt:       now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
		t.Fatal(err)
	}
	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceOfficial, "", "gpt-label", account.ID,
	))
	if err != nil {
		t.Fatal(err)
	}
	if !view.PetModelAvailable || !strings.Contains(view.PetModelLabel, "Codex 官方账号") ||
		!strings.Contains(view.PetModelLabel, "Pet Plus") || !strings.Contains(view.PetModelLabel, "GPT Label") {
		t.Fatalf("official label=%q available=%t reason=%q", view.PetModelLabel, view.PetModelAvailable, view.PetModelUnavailableReason)
	}

	providerView, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceProvider, fixture.model.ID, "", "",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !providerView.PetModelAvailable || !strings.Contains(providerView.PetModelLabel, "Provider 模型") ||
		!strings.Contains(providerView.PetModelLabel, fixture.model.DisplayName) {
		t.Fatalf("provider label=%q available=%t", providerView.PetModelLabel, providerView.PetModelAvailable)
	}
}
