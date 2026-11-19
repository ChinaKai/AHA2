package app

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func petSettingsWithModel(source, modelID, wireModel, accountID string) PetSettingsInput {
	return PetSettingsInput{
		Enabled: true, ChatModelID: "model-pet-task", Actions: []string{"walk"},
		PetModelSource: source, PetModelID: modelID, PetWireModel: wireModel, PetCodexAccountID: accountID,
		IdleMinutes: 5, QuietStart: 23, QuietEnd: 8, HourlyLimit: 6,
	}
}

func petTaskSnapshotForTest(t *testing.T, fixture *petTaskFixture) domain.RuntimeConfigSnapshot {
	t.Helper()
	agent, err := fixture.database.TaskAgent(fixture.ctx, petTaskID, "main")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.database.RuntimeSnapshot(fixture.ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestPetModelSettingsSelectOfficialAccountAndReadBack(t *testing.T) {
	fixture := newPetTaskFixture(t)
	now := fixture.service.now().UTC()
	account := domain.CodexAccount{
		ID: "account-pet-official", Label: "Pet Official", Status: "ready", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-pet-official", DisplayName: "GPT Pet Official"}},
		CreatedAt:       now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
		t.Fatal(err)
	}

	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceOfficial, "", "gpt-pet-official", account.ID,
	))
	if err != nil {
		t.Fatal(err)
	}
	wantModelID := domain.OfficialCodexModelID(account.ID, "gpt-pet-official")
	if view.Assistant.BrainModelID != wantModelID {
		t.Fatalf("pet runtime model=%q, want %q", view.Assistant.BrainModelID, wantModelID)
	}
	if view.Assistant.ModelID != fixture.model.ID {
		t.Fatalf("chat model changed to %q; it must stay on the Provider chat model", view.Assistant.ModelID)
	}
	if view.PetModelSource != domain.ModelSourceOfficial || view.PetWireModel != "gpt-pet-official" ||
		view.PetCodexAccountID != account.ID || view.PetModelName != "GPT Pet Official" {
		t.Fatalf("pet model readback=%#v", view)
	}

	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := petTaskSnapshotForTest(t, fixture)
	if snapshot.Backend != "codex" || snapshot.ModelID != wantModelID || snapshot.CodexAccountID != account.ID {
		t.Fatalf("pet task runtime=%#v, want official codex account", snapshot)
	}
}

func TestPetModelSettingsSelectClaudeNative(t *testing.T) {
	fixture := newPetTaskFixture(t)
	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceClaudeNative, "", "claude-sonnet-4-5", "",
	))
	if err != nil {
		t.Fatal(err)
	}
	wantModelID := domain.ClaudeNativeModelID("claude-sonnet-4-5")
	if view.Assistant.BrainModelID != wantModelID {
		t.Fatalf("pet runtime model=%q, want %q", view.Assistant.BrainModelID, wantModelID)
	}
	if view.PetModelSource != domain.ModelSourceClaudeNative || view.PetWireModel != "claude-sonnet-4-5" || view.PetCodexAccountID != "" {
		t.Fatalf("pet model readback=%#v", view)
	}
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := petTaskSnapshotForTest(t, fixture)
	if snapshot.Backend != "claude" || snapshot.ModelID != wantModelID ||
		snapshot.EnvGroupID != domain.ClaudeNativeEnvGroupID || snapshot.CodexAccountID != "" {
		t.Fatalf("pet task runtime=%#v, want claude native", snapshot)
	}
}

func TestPetModelSettingsProviderDoesNotRegress(t *testing.T) {
	fixture := newPetTaskFixture(t)
	view, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceProvider, fixture.model.ID, "", "",
	))
	if err != nil {
		t.Fatal(err)
	}
	if view.Assistant.BrainModelID != fixture.model.ID || view.PetModelSource != domain.ModelSourceProvider ||
		view.PetWireModel != fixture.model.WireModel {
		t.Fatalf("provider pet selection=%#v", view)
	}
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := petTaskSnapshotForTest(t, fixture)
	if snapshot.ModelID != fixture.model.ID || snapshot.EnvGroupID != fixture.modelEnv.ID {
		t.Fatalf("pet task runtime=%#v, want provider model", snapshot)
	}
}

func TestPetModelSettingsRejectsUnavailableAccountWithoutFallback(t *testing.T) {
	fixture := newPetTaskFixture(t)
	now := fixture.service.now().UTC()
	account := domain.CodexAccount{
		ID: "account-pet-expired", Label: "Expired", Status: "expired", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-expired"}},
		CreatedAt:       now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
		t.Fatal(err)
	}
	// Start from a Provider runtime so a silent fallback would be visible.
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceProvider, fixture.model.ID, "", "",
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceOfficial, "", "gpt-expired", account.ID,
	)); err == nil || !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("unavailable account was accepted: %v", err)
	}
	assistant, err := fixture.database.OwnerAssistant(fixture.ctx, ownerAssistantID)
	if err != nil {
		t.Fatal(err)
	}
	if assistant.BrainModelID != fixture.model.ID {
		t.Fatalf("failed switch changed the runtime to %q", assistant.BrainModelID)
	}
}

func TestPetModelSettingsRejectsSourceModelMismatch(t *testing.T) {
	fixture := newPetTaskFixture(t)
	_, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceOfficial, fixture.model.ID, "", "",
	))
	if err == nil || !strings.Contains(err.Error(), "不能通过") {
		t.Fatalf("source/model mismatch was accepted: %v", err)
	}
}

// A deleted Codex account must not block saving settings that do not change the
// pet runtime selection. Disabling the pet and changing permissions must still
// work, even though the stored model row still references the deleted account.
func TestPetSettingsSaveIgnoresUnchangedDeletedAccount(t *testing.T) {
	fixture := newPetTaskFixture(t)
	now := fixture.service.now().UTC()
	account := domain.CodexAccount{
		ID: "account-pet-deleted", Label: "Deleted Later", Status: "ready", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-deleted-later", DisplayName: "GPT Deleted Later"}},
		CreatedAt:       now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
		t.Fatal(err)
	}
	saved, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceOfficial, "", "gpt-deleted-later", account.ID,
	))
	if err != nil {
		t.Fatal(err)
	}
	storedModel := saved.Assistant.BrainModelID
	if err := fixture.database.DeleteCodexAccount(fixture.ctx, account.ID); err != nil {
		t.Fatal(err)
	}

	// The view still reports the stale selection; that is exactly what the Web
	// sends back on the next save.
	view, err := fixture.service.OwnerAssistant(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := petSettingsWithModel(view.PetModelSource, view.PetModelID, view.PetWireModel, view.PetCodexAccountID)
	input.Enabled = false
	input.AllowViewTasks = boolPointer(false)
	input.AllowManageChannels = boolPointer(false)
	saved, err = fixture.service.UpdateOwnerPetSettings(fixture.ctx, input)
	if err != nil {
		t.Fatalf("an unchanged deleted account blocked unrelated settings: %v", err)
	}
	if saved.Assistant.PetEnabled {
		t.Fatal("pet stayed enabled")
	}
	if saved.Assistant.PetAllowViewTasks || saved.Assistant.PetAllowManageChannels {
		t.Fatalf("permission change was not saved: %#v", saved.Assistant)
	}
	if saved.Assistant.BrainModelID != storedModel {
		t.Fatalf("runtime changed to %q, want %q", saved.Assistant.BrainModelID, storedModel)
	}
}

// A real selection change must still be rejected, but with business wording
// instead of sql: no rows in result set.
func TestPetSettingsRejectsDeletedModelAndAccountWithReadableError(t *testing.T) {
	fixture := newPetTaskFixture(t)

	_, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceProvider, "model-deleted", "", "",
	))
	if err == nil || !strings.Contains(err.Error(), "所选模型") || strings.Contains(err.Error(), "sql:") {
		t.Fatalf("deleted provider model error=%v", err)
	}

	_, err = fixture.service.UpdateOwnerPetSettings(fixture.ctx, petSettingsWithModel(
		domain.ModelSourceOfficial, "", "gpt-missing", "account-missing",
	))
	if err == nil || !strings.Contains(err.Error(), "Codex 官方账号已删除") || strings.Contains(err.Error(), "sql:") {
		t.Fatalf("deleted official account error=%v", err)
	}
}

// The pet's automatic official-account choice must be unambiguous and must only
// consider configured, ready accounts.
func TestPetOfficialAutoSelectRequiresSingleReadyAccount(t *testing.T) {
	fixture := newPetTaskFixture(t)
	now := fixture.service.now().UTC()
	accounts := []domain.CodexAccount{
		{
			ID: "account-pet-ready-a", Label: "Ready A", Status: "ready", CredentialConfigured: true,
			AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-pet-shared"}}, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "account-pet-expired", Label: "Expired", Status: "expired", CredentialConfigured: true,
			AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-pet-shared"}}, CreatedAt: now, UpdatedAt: now,
		},
	}
	for _, account := range accounts {
		if err := fixture.database.UpsertCodexAccount(fixture.ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	// Only one ready account offers the model, so it is selected automatically.
	if _, err := fixture.service.resolvePetRuntimeSelection(fixture.ctx, domain.ModelSourceOfficial, "", "gpt-pet-shared", ""); err != nil {
		t.Fatalf("single ready account was not auto-selected: %v", err)
	}

	second := domain.CodexAccount{
		ID: "account-pet-ready-b", Label: "Ready B", Status: "ready", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-pet-shared"}}, CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertCodexAccount(fixture.ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.resolvePetRuntimeSelection(fixture.ctx, domain.ModelSourceOfficial, "", "gpt-pet-shared", ""); err == nil || !strings.Contains(err.Error(), "多个") {
		t.Fatalf("ambiguous ready accounts were silently resolved: %v", err)
	}

	none := domain.CodexAccount{
		ID: "account-pet-unconfigured", Label: "No Credentials", Status: "ready", CredentialConfigured: false,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-pet-nobody"}}, CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertCodexAccount(fixture.ctx, none); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.resolvePetRuntimeSelection(fixture.ctx, domain.ModelSourceOfficial, "", "gpt-pet-nobody", ""); err == nil || !strings.Contains(err.Error(), "没有可用") {
		t.Fatalf("unconfigured account counted as available: %v", err)
	}
}
