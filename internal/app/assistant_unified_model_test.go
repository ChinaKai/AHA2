package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/codexaccount"
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

// refreshingCodexAuthExecutor simulates Codex refreshing its OAuth token
// during a chat: it reads the profile written by PrepareProfile, records the
// access token it saw, then writes a refreshed token back to auth.json.
type refreshingCodexAuthExecutor struct {
	mu             sync.Mutex
	seenTokens     []string
	refreshedToken string
	failErr        error
	// blockUntilCancel makes Execute wait for its context after refreshing, so
	// a canceled chat still exercises the credential write-back path.
	blockUntilCancel bool
	refreshed        chan struct{}
	refreshedOnce    sync.Once
}

func (executor *refreshingCodexAuthExecutor) Execute(ctx context.Context, request ExecutionRequest, _ func(ExecutionEvent)) (ExecutionResult, error) {
	home := strings.TrimSpace(request.Environment["CODEX_HOME"])
	path := filepath.Join(home, "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ExecutionResult{}, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return ExecutionResult{}, err
	}
	tokens, _ := raw["tokens"].(map[string]any)
	token, _ := tokens["access_token"].(string)
	tokens["access_token"] = executor.refreshedToken
	tokens["refresh_token"] = "refreshed-refresh-token"
	updated, err := json.Marshal(raw)
	if err != nil {
		return ExecutionResult{}, err
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		return ExecutionResult{}, err
	}
	executor.mu.Lock()
	executor.seenTokens = append(executor.seenTokens, token)
	executor.mu.Unlock()
	if executor.refreshed != nil {
		executor.refreshedOnce.Do(func() { close(executor.refreshed) })
	}
	if executor.blockUntilCancel {
		<-ctx.Done()
		return ExecutionResult{}, ctx.Err()
	}
	if executor.failErr != nil {
		return ExecutionResult{}, executor.failErr
	}
	return ExecutionResult{Reply: "官方账号回答"}, nil
}

func (executor *refreshingCodexAuthExecutor) tokens() []string {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return append([]string(nil), executor.seenTokens...)
}

// newCodexChatFixture wires a ready official account, the real codexaccount
// manager over a temp secret store, and an official unified model selection
// with the pet disabled so only chat reaches the executor.
func newCodexChatFixture(t *testing.T, executor Executor) (*petTaskFixture, *secrets.FileStore, string) {
	t.Helper()
	fixture := newPetTaskFixtureWithExecutor(t, executor)
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "codex-secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	credentialRef := "codex/account/refresh"
	const initialAuth = `{"tokens":{"access_token":"initial-access-token","refresh_token":"initial-refresh-token"}}`
	if err := secretStore.PutMany(map[string]string{credentialRef: initialAuth}); err != nil {
		t.Fatal(err)
	}
	fixture.service.SetCodexAccountManager(codexaccount.New(fixture.ctx, fixture.database, secretStore, "", "codex"))
	now := fixture.service.now().UTC()
	account := domain.CodexAccount{
		ID: "account-refresh", Label: "Refresh Account", Status: "ready", CredentialConfigured: true,
		CredentialRef:   credentialRef,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-refresh", DisplayName: "GPT Refresh"}},
		CreatedAt:       now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
		t.Fatal(err)
	}
	disabled := petSettingsWithModel(domain.ModelSourceOfficial, "", "gpt-refresh", account.ID)
	disabled.Enabled = false
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, disabled); err != nil {
		t.Fatal(err)
	}
	return fixture, secretStore, credentialRef
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

// The legacy precedence is a pure decision: the pet runtime column wins and
// the chat column is only a fallback for rows written before unification.
func TestUnifiedAssistantModelIDPrefersPetRuntime(t *testing.T) {
	assistant := domain.OwnerAssistant{ModelID: "chat-only", BrainModelID: "pet-runtime"}
	if got := unifiedAssistantModelID(assistant); got != "pet-runtime" {
		t.Fatalf("unified model=%q, want pet-runtime", got)
	}
	assistant.BrainModelID = ""
	if got := unifiedAssistantModelID(assistant); got != "chat-only" {
		t.Fatalf("chat-only fallback=%q", got)
	}
}

// M1 regression: the reviewer reproduced a torn read (new chat model, old pet
// model) while a save was in flight, then the read-time repair wrote the old
// pet model back over the completed save. The compare-and-swap now sees that
// the row moved and reloads it instead.
func TestNormalizeOwnerAssistantModelDoesNotOverwriteConcurrentSave(t *testing.T) {
	fixture := newPetTaskFixture(t)
	second := secondProviderModel(t, fixture)
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceProvider, fixture.model.ID, "", "",
	)); err != nil {
		t.Fatal(err)
	}
	stale := storedAssistant(t, fixture)
	if stale.BrainModelID != fixture.model.ID || stale.ModelID != fixture.model.ID {
		t.Fatalf("initial model=(brain=%q chat=%q)", stale.BrainModelID, stale.ModelID)
	}
	// The read that normalization is about to repair saw the new chat column
	// before the pet column was updated.
	stale.ModelID = second.ID

	// Another request completes the save after this snapshot was read.
	if err := fixture.database.UpdateOwnerPetSettings(fixture.ctx, stale.ID, true, second.ID, []string{"walk"}, fixture.service.now().UTC()); err != nil {
		t.Fatal(err)
	}
	if afterSave := storedAssistant(t, fixture); afterSave.BrainModelID != second.ID || afterSave.ModelID != second.ID {
		t.Fatalf("direct save did not write both columns: brain=%q chat=%q", afterSave.BrainModelID, afterSave.ModelID)
	}
	if err := fixture.service.normalizeAssistantUnifiedModel(fixture.ctx, &stale); err != nil {
		t.Fatal(err)
	}
	stored := storedAssistant(t, fixture)
	if stored.BrainModelID != second.ID || stored.ModelID != second.ID {
		t.Fatalf("stale normalization rolled the save back: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
	}
	if stale.BrainModelID != second.ID || stale.ModelID != second.ID {
		t.Fatalf("normalization did not reload the saved row: brain=%q chat=%q", stale.BrainModelID, stale.ModelID)
	}
}

// The name/appearance path must not write the model columns at all. A model
// save that lands between its read and write stays in effect.
func TestUpdateOwnerAssistantDoesNotWriteModel(t *testing.T) {
	fixture := newPetTaskFixture(t)
	second := secondProviderModel(t, fixture)
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceProvider, second.ID, "", "",
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerAssistant(fixture.ctx, "", "新名字", "deepseek_fish"); err != nil {
		t.Fatal(err)
	}
	stored := storedAssistant(t, fixture)
	if stored.BrainModelID != second.ID || stored.ModelID != second.ID {
		t.Fatalf("profile update changed the model: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
	}
	if stored.Name != "新名字" || stored.Character != "deepseek_fish" {
		t.Fatalf("profile update was not saved: name=%q character=%q", stored.Name, stored.Character)
	}
}

// The startup migration is idempotent: it seeds an empty row once and leaves
// later runs untouched.
func TestMigrateOwnerAssistantUnifiedModelSeedsOnce(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.database.EnsureOwnerAssistant(fixture.ctx, fixture.service.now().UTC()); err != nil {
		t.Fatal(err)
	}
	fixture.service.MigrateOwnerAssistantUnifiedModel(fixture.ctx)
	stored := storedAssistant(t, fixture)
	if stored.BrainModelID != fixture.model.ID || stored.ModelID != fixture.model.ID {
		t.Fatalf("migration seeded model=(brain=%q chat=%q)", stored.BrainModelID, stored.ModelID)
	}
	firstUpdated := stored.UpdatedAt
	fixture.service.MigrateOwnerAssistantUnifiedModel(fixture.ctx)
	stored = storedAssistant(t, fixture)
	if !stored.UpdatedAt.Equal(firstUpdated) {
		t.Fatalf("second migration wrote again: updated_at %s -> %s", firstUpdated, stored.UpdatedAt)
	}
	if stored.BrainModelID != fixture.model.ID || stored.ModelID != fixture.model.ID {
		t.Fatalf("second migration changed the model: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
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
	// Chat and autonomy share one permission policy. The owner chose full
	// access for the pet, and chat must not be downgraded to read-only.
	agent, err := fixture.database.TaskAgent(fixture.ctx, petTaskID, "main")
	if err != nil {
		t.Fatal(err)
	}
	petSnapshot, err := fixture.database.RuntimeSnapshot(fixture.ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	petFilesystem, petApproval := parsePermissionsJSON(petSnapshot.PermissionsJSON)
	if request.Filesystem != petFilesystem || request.Approval != petApproval {
		t.Fatalf("chat permissions=%s/%s, pet task=%s/%s", request.Filesystem, request.Approval, petFilesystem, petApproval)
	}
	if request.Filesystem != petTaskFilesystem || request.Approval != "never" {
		t.Fatalf("chat permissions=%s/%s, want full access with approval never", request.Filesystem, request.Approval)
	}
	if request.Snapshot.ReasoningEffort != petSnapshot.ReasoningEffort ||
		request.Snapshot.FastMode != petSnapshot.FastMode ||
		request.Snapshot.StreamIdleTimeoutMS != petSnapshot.StreamIdleTimeoutMS ||
		request.Snapshot.StreamMaxRetries != petSnapshot.StreamMaxRetries {
		t.Fatalf("chat runtime policy diverged from the pet task: chat=%#v pet=%#v", request.Snapshot, petSnapshot)
	}
	if request.Snapshot.EnvGroupID != request.EnvGroup.ID || request.Snapshot.CodexAccountID != request.Model.CodexAccountID {
		t.Fatalf("chat snapshot does not share the resolved runtime: %#v", request.Snapshot)
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

// M3 regression: Codex refreshes its OAuth token during a chat. The refreshed
// auth.json must be written back to the account, and the next chat must prepare
// its profile from that refreshed value instead of the original one.
func TestAssistantChatPersistsRefreshedCodexCredential(t *testing.T) {
	executor := &refreshingCodexAuthExecutor{refreshedToken: "refreshed-access-token"}
	fixture, secretStore, credentialRef := newCodexChatFixture(t, executor)

	if _, err := fixture.service.AskOwnerAssistant(fixture.ctx, "第一次"); err != nil {
		t.Fatal(err)
	}
	stored, ok := secretStore.Get(credentialRef)
	if !ok || !strings.Contains(stored, "refreshed-access-token") {
		t.Fatalf("refreshed credential was not written back: %q", stored)
	}

	if _, err := fixture.service.AskOwnerAssistant(fixture.ctx, "第二次"); err != nil {
		t.Fatal(err)
	}
	tokens := executor.tokens()
	if len(tokens) != 2 {
		t.Fatalf("chat runs=%d, want 2", len(tokens))
	}
	if tokens[0] != "initial-access-token" {
		t.Fatalf("first chat token=%q, want the stored initial token", tokens[0])
	}
	if tokens[1] != "refreshed-access-token" {
		t.Fatalf("second chat token=%q, want the refreshed token", tokens[1])
	}
}

// A failed chat still refreshed the token before the backend error. The
// refreshed credential must be written back, exactly like the success path.
func TestAssistantChatPersistsRefreshedCodexCredentialOnFailure(t *testing.T) {
	executor := &refreshingCodexAuthExecutor{
		refreshedToken: "refreshed-after-error",
		failErr:        errors.New("provider rejected the turn"),
	}
	fixture, secretStore, credentialRef := newCodexChatFixture(t, executor)
	reply, err := fixture.service.AskOwnerAssistant(fixture.ctx, "会失败")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply.Reply.Content, "我暂时没连上模型") {
		t.Fatalf("failed chat reply=%q", reply.Reply.Content)
	}
	stored, ok := secretStore.Get(credentialRef)
	if !ok || !strings.Contains(stored, "refreshed-after-error") {
		t.Fatalf("refreshed credential after a failed chat was not written back: %q", stored)
	}
}

// Canceling/timing out a chat must still write the refreshed credential back;
// the normal Turn path syncs on every exit, and chat has to match.
func TestAssistantChatPersistsRefreshedCodexCredentialOnCancel(t *testing.T) {
	executor := &refreshingCodexAuthExecutor{
		refreshedToken:   "refreshed-after-cancel",
		blockUntilCancel: true,
		refreshed:        make(chan struct{}),
	}
	fixture, secretStore, credentialRef := newCodexChatFixture(t, executor)
	ctx, cancel := context.WithCancel(fixture.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = fixture.service.AskOwnerAssistant(ctx, "会被取消")
	}()
	select {
	case <-executor.refreshed:
	case <-time.After(10 * time.Second):
		t.Fatal("chat backend was never reached")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("canceled chat did not return")
	}
	stored, ok := secretStore.Get(credentialRef)
	if !ok || !strings.Contains(stored, "refreshed-after-cancel") {
		t.Fatalf("refreshed credential after cancellation was not written back: %q", stored)
	}
}

// Concurrent saves and reads must never leave the two model columns disagreeing
// or resurrect a model that was already replaced.
func TestConcurrentPetModelSavesAndReadsStayUnified(t *testing.T) {
	fixture := newPetTaskFixture(t)
	second := secondProviderModel(t, fixture)
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceProvider, fixture.model.ID, "", "",
	)); err != nil {
		t.Fatal(err)
	}
	models := []string{fixture.model.ID, second.ID}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for writer := 0; writer < 2; writer++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for attempt := 0; attempt < 8; attempt++ {
				input := petSettingsWithModel(domain.ModelSourceProvider, models[(index+attempt)%2], "", "")
				if _, err := fixture.service.UpdateOwnerPetSettings(context.Background(), input); err != nil {
					errs <- err
					return
				}
			}
		}(writer)
	}
	for reader := 0; reader < 2; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 16; attempt++ {
				if _, err := fixture.service.OwnerAssistant(context.Background()); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent operation failed: %v", err)
	}
	stored := storedAssistant(t, fixture)
	if stored.ModelID != stored.BrainModelID {
		t.Fatalf("concurrent operations left a torn model: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
	}
	if stored.BrainModelID != fixture.model.ID && stored.BrainModelID != second.ID {
		t.Fatalf("concurrent operations left an unexpected model: %q", stored.BrainModelID)
	}
}

// An official account that becomes unavailable after selection must fail chat
// with its reason before the backend is called; it never falls back.
func TestAssistantChatUnavailableOfficialAccountDoesNotRun(t *testing.T) {
	executor := &recordingExecutor{reply: "should not run"}
	fixture := newPetTaskFixtureWithExecutor(t, executor)
	now := fixture.service.now().UTC()
	account := domain.CodexAccount{
		ID: "account-expires", Label: "Expires", Status: "ready", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-expires", DisplayName: "GPT Expires"}},
		CreatedAt:       now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceOfficial, "", "gpt-expires", account.ID,
	)); err != nil {
		t.Fatal(err)
	}
	account.Status = "expired"
	account.UpdatedAt = fixture.service.now().UTC()
	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
		t.Fatal(err)
	}
	reply, err := fixture.service.AskOwnerAssistant(fixture.ctx, "还在吗")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply.Reply.Content, "不可用") {
		t.Fatalf("unavailable account reply=%q", reply.Reply.Content)
	}
	executor.mu.Lock()
	runs := len(executor.requests)
	executor.mu.Unlock()
	if runs != 0 {
		t.Fatalf("backend ran %d times for an unavailable account", runs)
	}
}

// Chat reads the same Env Group and proxy switch as the Pet Task runtime.
func TestAssistantChatUsesPetEnvGroupAndProxyPolicy(t *testing.T) {
	executor := &recordingExecutor{reply: "ok"}
	fixture := newPetTaskFixtureWithExecutor(t, executor)
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceClaudeNative, "", "claude-sonnet-4-5", "",
	)); err != nil {
		t.Fatal(err)
	}
	envGroup, err := fixture.database.EnvGroup(fixture.ctx, domain.ClaudeNativeEnvGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.UpdateProxySettings(fixture.ctx, domain.ProxySettings{
		Mode: "external", HTTPProxy: "http://127.0.0.1:9", HTTPSProxy: "http://127.0.0.1:9",
		ManagedRefreshIntervalMins: 1440,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateAgentConfig(fixture.ctx, petTaskID, "main", UpdateAgentConfigInput{
		ProxyEnabled: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	// Runtime selection upserts the native Env Group, so add the marker after
	// the last operation that resolves it.
	envGroup, err = fixture.database.EnvGroup(fixture.ctx, domain.ClaudeNativeEnvGroupID)
	if err != nil {
		t.Fatal(err)
	}
	envGroup.Environment["AHA_CHAT_TEST"] = "env-value"
	if err := fixture.database.UpsertEnvGroup(fixture.ctx, envGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.AskOwnerAssistant(fixture.ctx, "环境检查"); err != nil {
		t.Fatal(err)
	}
	request := executor.lastRequest(t)
	if request.EnvGroup.ID != envGroup.ID || request.Snapshot.EnvGroupID != envGroup.ID {
		t.Fatalf("chat env group=%q snapshot=%q, want %q", request.EnvGroup.ID, request.Snapshot.EnvGroupID, envGroup.ID)
	}
	if request.Environment["AHA_CHAT_TEST"] != "env-value" {
		t.Fatalf("chat did not inherit the Env Group environment: %#v", request.Environment)
	}
	if request.Environment["HTTP_PROXY"] != "http://127.0.0.1:9" {
		t.Fatalf("chat did not follow the pet task proxy switch: HTTP_PROXY=%q", request.Environment["HTTP_PROXY"])
	}
	if !request.Snapshot.ProxyEnabled {
		t.Fatalf("chat snapshot proxy switch=%t, want true", request.Snapshot.ProxyEnabled)
	}
}

// The settings availability flag and the chat runtime must agree. A Provider
// with no configured credential is not marked unavailable; one whose credential
// was configured but is missing from the secret store fails in both places.
func TestProviderAvailabilityMatchesChatRuntime(t *testing.T) {
	fixture := newPetTaskFixture(t)
	provider, err := fixture.database.Provider(fixture.ctx, fixture.provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	model, err := fixture.database.Model(fixture.ctx, fixture.model.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's provider says a credential is configured but the secret
	// store is empty.
	if ok, reason := fixture.service.assistantModelAvailability(fixture.ctx, model, domain.ModelSourceProvider); ok || !strings.Contains(reason, "凭据") {
		t.Fatalf("missing configured credential availability=%t reason=%q", ok, reason)
	}
	if _, _, _, _, err := fixture.service.assistantRuntime(fixture.ctx, &domain.OwnerAssistant{
		ModelID: fixture.model.ID, BrainModelID: fixture.model.ID,
	}); err == nil || !strings.Contains(err.Error(), "凭据") {
		t.Fatalf("chat runtime accepted a missing configured credential: %v", err)
	}

	// A provider that never configured a credential is usable with an empty
	// key, and the settings page must not call it unavailable.
	provider.CredentialConfigured = false
	provider.CredentialRef = ""
	if err := fixture.database.UpsertProvider(fixture.ctx, provider); err != nil {
		t.Fatal(err)
	}
	if ok, reason := fixture.service.assistantModelAvailability(fixture.ctx, model, domain.ModelSourceProvider); !ok {
		t.Fatalf("no-credential provider marked unavailable: %s", reason)
	}
	if _, _, _, _, err := fixture.service.assistantRuntime(fixture.ctx, &domain.OwnerAssistant{
		ModelID: fixture.model.ID, BrainModelID: fixture.model.ID,
	}); err != nil {
		t.Fatalf("chat runtime rejected a no-credential provider: %v", err)
	}
}

// A stale profile object must not roll the model columns back. The old store
// method wrote model_id from the caller's snapshot; this test would fail then.
func TestStaleProfileUpdateCannotRevertModel(t *testing.T) {
	fixture := newPetTaskFixture(t)
	second := secondProviderModel(t, fixture)
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceProvider, fixture.model.ID, "", "",
	)); err != nil {
		t.Fatal(err)
	}
	stale := storedAssistant(t, fixture)
	if err := fixture.database.UpdateOwnerPetSettings(fixture.ctx, stale.ID, true, second.ID, []string{"walk"}, fixture.service.now().UTC()); err != nil {
		t.Fatal(err)
	}
	// The stale snapshot still names the old model; only its profile fields are
	// meant to be written.
	stale.Character = "deepseek_fish"
	stale.UpdatedAt = fixture.service.now().UTC()
	if err := fixture.database.UpdateOwnerAssistant(fixture.ctx, stale); err != nil {
		t.Fatal(err)
	}
	stored := storedAssistant(t, fixture)
	if stored.BrainModelID != second.ID || stored.ModelID != second.ID {
		t.Fatalf("stale profile update reverted the model: brain=%q chat=%q", stored.BrainModelID, stored.ModelID)
	}
	if stored.Character != "deepseek_fish" {
		t.Fatalf("profile update was not saved: character=%q", stored.Character)
	}
}

func createProbeWorkspace(t *testing.T, fixture *petTaskFixture, id, locality, transport, authStatus string) {
	t.Helper()
	const projectID = "project-probe-workspaces"
	if _, err := fixture.database.Project(fixture.ctx, projectID); err != nil {
		now := fixture.service.now().UTC()
		project := domain.Project{
			ID: projectID, Name: "Probe Workspaces", ProjectType: "folder",
			KnowledgePolicy: "none", CreatedAt: now, UpdatedAt: now,
		}
		if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
			t.Fatal(err)
		}
	}
	now := fixture.service.now().UTC()
	workspace := domain.Workspace{
		ID: id, ProjectID: projectID, Name: id, Locality: locality, Transport: transport,
		RootPath: t.TempDir(), Health: "ready",
		Capabilities: map[string]any{"claude": map[string]any{"status": "ready", "auth_status": authStatus}},
		CreatedAt:    now, UpdatedAt: now,
	}
	if err := fixture.database.CreateWorkspace(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
}

func setPetWorkspaceProbe(t *testing.T, fixture *petTaskFixture, authStatus string) {
	t.Helper()
	workspace, err := fixture.service.ensurePetWorkspace(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Capabilities = map[string]any{"claude": map[string]any{"status": "ready", "auth_status": authStatus}}
	if err := fixture.database.UpdateWorkspaceDetection(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
}

// M4: only the actual execution host's probe may gate local Claude chat. A
// remote SSH/WSL Workspace's not_logged_in must never block a working local
// account, regardless of list order.
func TestClaudeNativeAvailabilityIgnoresRemoteProbe(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, *petTaskFixture)
		want  bool
	}{
		{
			name: "remote not_logged_in before a working local probe",
			setup: func(t *testing.T, fixture *petTaskFixture) {
				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "not_logged_in")
				createProbeWorkspace(t, fixture, "workspace-local", "local", "native", "logged_in")
			},
			want: true,
		},
		{
			name: "working local probe before a remote negative",
			setup: func(t *testing.T, fixture *petTaskFixture) {
				createProbeWorkspace(t, fixture, "workspace-local", "local", "native", "logged_in")
				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "not_logged_in")
			},
			want: true,
		},
		{
			name: "only a remote negative probe",
			setup: func(t *testing.T, fixture *petTaskFixture) {
				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "not_logged_in")
			},
			want: true,
		},
		{
			name: "local negative with a remote positive",
			setup: func(t *testing.T, fixture *petTaskFixture) {
				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "logged_in")
				createProbeWorkspace(t, fixture, "workspace-local", "local", "native", "not_logged_in")
			},
			want: false,
		},
		{
			name: "WSL is a different login",
			setup: func(t *testing.T, fixture *petTaskFixture) {
				createProbeWorkspace(t, fixture, "workspace-wsl", "local", "wsl", "not_logged_in")
			},
			want: true,
		},
		{
			name: "unprobed pet workspace falls back to the local native probe",
			setup: func(t *testing.T, fixture *petTaskFixture) {
				if _, err := fixture.service.ensurePetWorkspace(fixture.ctx); err != nil {
					t.Fatal(err)
				}
				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "not_logged_in")
				createProbeWorkspace(t, fixture, "workspace-local", "local", "native", "logged_in")
			},
			want: true,
		},
		{
			name: "pet workspace probe is authoritative for its execution host",
			setup: func(t *testing.T, fixture *petTaskFixture) {
				setPetWorkspaceProbe(t, fixture, "not_logged_in")
				createProbeWorkspace(t, fixture, "workspace-local", "local", "native", "logged_in")
			},
			want: false,
		},
		{
			name: "unprobed pet workspace ignores remote-only negatives",
			setup: func(t *testing.T, fixture *petTaskFixture) {
				if _, err := fixture.service.ensurePetWorkspace(fixture.ctx); err != nil {
					t.Fatal(err)
				}
				createProbeWorkspace(t, fixture, "workspace-remote", "remote", "ssh", "not_logged_in")
			},
			want: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newPetTaskFixture(t)
			testCase.setup(t, fixture)
			available, reason := fixture.service.claudeNativeAvailability(fixture.ctx)
			if available != testCase.want {
				t.Fatalf("available=%t reason=%q, want %t", available, reason, testCase.want)
			}
			if !testCase.want && !strings.Contains(reason, "尚未登录") {
				t.Fatalf("unavailable reason=%q", reason)
			}
		})
	}
}

// Claude native availability follows the Workspace probe when there is one, and
// stays unknown (available) when no probe exists so the CLI reports the real
// authentication result.
func TestClaudeNativeAvailabilityUsesWorkspaceProbe(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if ok, reason := fixture.service.claudeNativeAvailability(fixture.ctx); !ok {
		t.Fatalf("no probe should stay available, got %q", reason)
	}
	workspace, err := fixture.service.ensurePetWorkspace(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Capabilities = map[string]any{"claude": map[string]any{"status": "ready", "auth_status": "not_logged_in"}}
	if err := fixture.database.UpdateWorkspaceDetection(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if ok, reason := fixture.service.claudeNativeAvailability(fixture.ctx); ok || !strings.Contains(reason, "尚未登录") {
		t.Fatalf("not-logged-in probe availability=%t reason=%q", ok, reason)
	}
	workspace.Capabilities = map[string]any{"claude": map[string]any{"status": "ready", "auth_status": "logged_in"}}
	if err := fixture.database.UpdateWorkspaceDetection(fixture.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if ok, reason := fixture.service.claudeNativeAvailability(fixture.ctx); !ok {
		t.Fatalf("logged-in probe marked unavailable: %s", reason)
	}
}

// Legacy brain_model_id is a compatibility input, and a source-first pet
// selection always wins over any legacy field in the same request.
func TestPetSettingsLegacyBrainModelAndConflictMatrix(t *testing.T) {
	fixture := newPetTaskFixture(t)
	second := secondProviderModel(t, fixture)
	input := petSettingsWithModel("", "", "", "")
	input.ChatModelID = ""
	input.BrainModelID = second.ID
	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.BrainModelID != second.ID || view.Assistant.ModelID != second.ID {
		t.Fatalf("legacy brain_model_id did not map: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
	}

	conflict := petSettingsWithModel(domain.ModelSourceProvider, fixture.model.ID, "", "")
	conflict.BrainModelID = second.ID
	conflict.ChatModelID = second.ID
	view, err = fixture.service.UpdateOwnerPetSettings(fixture.ctx, conflict)
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.BrainModelID != fixture.model.ID || view.Assistant.ModelID != fixture.model.ID {
		t.Fatalf("legacy fields overrode the source-first selection: brain=%q chat=%q", view.Assistant.BrainModelID, view.Assistant.ModelID)
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

	// The fixture's provider claims a configured credential without storing one.
	// Mark it as the supported no-credential Provider case so the label and
	// availability assertions describe the provider itself.
	provider := fixture.provider
	provider.CredentialConfigured = false
	provider.CredentialRef = ""
	if err := fixture.database.UpsertProvider(fixture.ctx, provider); err != nil {
		t.Fatal(err)
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
