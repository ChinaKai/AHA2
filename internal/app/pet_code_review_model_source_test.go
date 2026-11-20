package app

import (
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// reviewSourceTask creates a provider-model source Task that a Review Task can
// be requested against.
func reviewSourceTask(t *testing.T, fixture *petTaskFixture, project, workspace string) domain.Task {
	t.Helper()
	source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project, WorkspaceID: workspace, Title: "待评审代码",
		Request: "修改代码", ModelID: fixture.model.ID, Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func reviewRuntimeSnapshot(t *testing.T, fixture *petTaskFixture, taskID string) domain.RuntimeConfigSnapshot {
	t.Helper()
	agents, err := fixture.service.TaskAgents(fixture.ctx, taskID)
	if err != nil || len(agents) == 0 {
		t.Fatalf("review agents err=%v", err)
	}
	snapshot, err := fixture.database.RuntimeSnapshot(fixture.ctx, agents[0].RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// A claude_native review model must carry its runtime source into CreateTask.
// Before the fix the empty model_source was normalized to provider, and the
// provider lookup rejected the managed model with "managed models must be
// selected through their runtime source".
func TestPetRequestCodeReviewResolvesClaudeNativeSource(t *testing.T) {
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
	source := reviewSourceTask(t, fixture, project.ID, workspace.ID)

	review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
		TaskID: source.ID, ReviewModelID: nativeModel.ID,
	})
	if err != nil {
		t.Fatalf("claude_native review creation failed: %v", err)
	}
	snapshot := reviewRuntimeSnapshot(t, fixture, review.ID)
	if source := domain.RuntimeModelSource(snapshot.EnvGroupID, snapshot.CodexAccountID); source != domain.ModelSourceClaudeNative {
		t.Fatalf("review snapshot source=%s, want claude_native: %#v", source, snapshot)
	}
	if snapshot.Backend != "claude" || snapshot.WireModel != "claude-sonnet-4-5" ||
		snapshot.ModelID != nativeModel.ID || snapshot.EnvGroupID != domain.ClaudeNativeEnvGroupID {
		t.Fatalf("unexpected claude_native review runtime: %#v", snapshot)
	}
}

// An official review model resolves its Codex account from the catalog entry so
// the review Task runs on the official runtime instead of the provider source.
func TestPetRequestCodeReviewResolvesOfficialSource(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	now := fixture.service.now().UTC()
	account := domain.CodexAccount{
		ID: "account-review-official", Label: "Review Official", Status: "ready", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-review-official", DisplayName: "GPT Review"}},
		CreatedAt:       now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
		t.Fatal(err)
	}
	officialModel := domain.Model{
		ID: domain.OfficialCodexModelID(account.ID, "gpt-review-official"), DisplayName: "GPT Review",
		ProviderID: domain.OfficialCodexProviderID, Source: domain.ModelSourceOfficial,
		CodexAccountID: account.ID, Backend: "codex", WireModel: "gpt-review-official", WireAPI: "responses",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertModel(fixture.ctx, officialModel); err != nil {
		t.Fatal(err)
	}
	source := reviewSourceTask(t, fixture, project.ID, workspace.ID)

	review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
		TaskID: source.ID, ReviewModelID: officialModel.ID,
	})
	if err != nil {
		t.Fatalf("official review creation failed: %v", err)
	}
	snapshot := reviewRuntimeSnapshot(t, fixture, review.ID)
	if source := domain.RuntimeModelSource(snapshot.EnvGroupID, snapshot.CodexAccountID); source != domain.ModelSourceOfficial {
		t.Fatalf("review snapshot source=%s, want official: %#v", source, snapshot)
	}
	if snapshot.Backend != "codex" || snapshot.WireModel != "gpt-review-official" ||
		snapshot.CodexAccountID != account.ID || snapshot.ModelID != officialModel.ID {
		t.Fatalf("unexpected official review runtime: %#v", snapshot)
	}
}

// Provider review models keep working exactly as before: the catalog model id
// is used directly and the source stays provider.
func TestPetRequestCodeReviewProviderSourceDoesNotRegress(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	reviewModel := addReviewModel(t, fixture)
	source := reviewSourceTask(t, fixture, project.ID, workspace.ID)

	review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
		TaskID: source.ID, ReviewModelID: reviewModel.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := reviewRuntimeSnapshot(t, fixture, review.ID)
	if source := domain.RuntimeModelSource(snapshot.EnvGroupID, snapshot.CodexAccountID); source != domain.ModelSourceProvider {
		t.Fatalf("review snapshot source=%s, want provider: %#v", source, snapshot)
	}
	if snapshot.ModelID != reviewModel.ID || snapshot.WireModel != reviewModel.WireModel ||
		snapshot.Backend != reviewModel.Backend || snapshot.EnvGroupID != reviewModel.DefaultEnvGroupID {
		t.Fatalf("unexpected provider review runtime: %#v", snapshot)
	}
}

// A managed catalog entry without a wire model cannot be resolved and must fail
// with the stable wire-model error instead of silently degrading to provider.
func TestPetRequestCodeReviewRejectsNativeModelWithoutWireModel(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	now := fixture.service.now().UTC()
	broken := domain.Model{
		ID: "model-review-native-missing-wire", DisplayName: "Broken Native",
		ProviderID: domain.OfficialClaudeProviderID, Source: domain.ModelSourceClaudeNative,
		Backend: "claude", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertModel(fixture.ctx, broken); err != nil {
		t.Fatal(err)
	}
	source := reviewSourceTask(t, fixture, project.ID, workspace.ID)

	_, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
		TaskID: source.ID, ReviewModelID: broken.ID,
	})
	coded, ok := AsCodedError(err)
	if !ok || coded.Code != ErrCodeWireModelRequired {
		t.Fatalf("error = %v, want code %s", err, ErrCodeWireModelRequired)
	}
}

// An official catalog entry whose wire model no configured account offers must
// return the Codex-account error rather than falling back to provider.
func TestPetRequestCodeReviewRejectsOfficialModelWithoutAccount(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	now := fixture.service.now().UTC()
	orphan := domain.Model{
		ID: "model-review-official-orphan", DisplayName: "Orphan Official",
		ProviderID: domain.OfficialCodexProviderID, Source: domain.ModelSourceOfficial,
		Backend: "codex", WireModel: "gpt-review-orphan", WireAPI: "responses",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertModel(fixture.ctx, orphan); err != nil {
		t.Fatal(err)
	}
	source := reviewSourceTask(t, fixture, project.ID, workspace.ID)

	_, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
		TaskID: source.ID, ReviewModelID: orphan.ID,
	})
	coded, ok := AsCodedError(err)
	if !ok || coded.Code != ErrCodeCodexAccountUnavailable {
		t.Fatalf("error = %v, want code %s", err, ErrCodeCodexAccountUnavailable)
	}
}
