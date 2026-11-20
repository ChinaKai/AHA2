package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetProviderModelEnvGroupAdmin(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
		AllowManageModels: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	provider, err := fixture.service.PetCreateProvider(fixture.ctx, PetProviderInput{
		Name: "宠物 Provider", BaseURL: "https://example.test/v1", APIKey: "secret-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !provider.CredentialConfigured {
		t.Fatalf("provider=%#v", provider)
	}
	group, err := fixture.service.PetCreateEnvGroup(fixture.ctx, PetEnvGroupInput{
		Name: "宠物 Env", ProviderID: provider.ID, Backend: "codex",
		Environment: map[string]string{"TOKEN": "secret-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := fixture.service.PetCreateModel(fixture.ctx, PetModelInput{
		DisplayName: "宠物模型", ProviderID: provider.ID, Backend: "codex",
		WireModel: "pet-model", WireAPI: "chat", ContextWindow: 200000,
		DefaultEnvGroupID: group.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if model.ProviderID != provider.ID || model.MaxOutputTokens == 0 {
		t.Fatalf("model=%#v", model)
	}
	if _, err := fixture.service.PetUpdateProvider(fixture.ctx, PetProviderInput{
		ProviderID: provider.ID, Name: "普通改名", BaseURL: provider.BaseURL,
	}); err != nil {
		t.Fatalf("non-credential provider update unnecessarily required reason: %v", err)
	}
	if _, err := fixture.service.PetUpdateProvider(fixture.ctx, PetProviderInput{
		ProviderID: provider.ID, Name: "轮换凭据", BaseURL: provider.BaseURL, APIKey: "new-secret",
	}); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("credential update without reason was accepted: %v", err)
	}
	if _, err := fixture.service.PetUpdateProvider(fixture.ctx, PetProviderInput{
		ProviderID: provider.ID, Name: "轮换凭据", BaseURL: provider.BaseURL, APIKey: "new-secret", Reason: "定期轮换 Provider 凭据",
	}); err != nil {
		t.Fatalf("credential update with reason failed: %v", err)
	}
	if _, err := fixture.service.PetUpdateEnvGroup(fixture.ctx, PetEnvGroupInput{
		EnvGroupID: group.ID, Name: "普通改名",
	}); err != nil {
		t.Fatalf("ordinary env group update unnecessarily required reason: %v", err)
	}
	if _, err := fixture.service.PetUpdateEnvGroup(fixture.ctx, PetEnvGroupInput{
		EnvGroupID: group.ID, Environment: map[string]string{"TOKEN": "rotated"},
	}); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("environment update without reason was accepted: %v", err)
	}
	if _, err := fixture.service.PetUpdateEnvGroup(fixture.ctx, PetEnvGroupInput{
		EnvGroupID: group.ID, Environment: map[string]string{"TOKEN": "rotated"}, Reason: "轮换环境变量凭据",
	}); err != nil {
		t.Fatalf("environment update with reason failed: %v", err)
	}
	// Both overwrites kept the value they replaced, so the pet can undo them.
	credentialRef := "provider/" + provider.ID + "/credential"
	if current, _ := fixture.service.secrets.Get(credentialRef); current != "new-secret" {
		t.Fatalf("provider key=%q, want the rotated key", current)
	}
	restored, err := fixture.service.PetRestorePreviousCredential(fixture.ctx, "provider", provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(restored), "secret") {
		t.Fatalf("the restore result leaked a credential: %#v", restored)
	}
	if current, _ := fixture.service.secrets.Get(credentialRef); current != "secret-value" {
		t.Fatalf("restored provider key=%q, want the original", current)
	}
	// A restore can be undone as well.
	if _, err := fixture.service.PetRestorePreviousCredential(fixture.ctx, "provider", provider.ID); err != nil {
		t.Fatal(err)
	}
	if current, _ := fixture.service.secrets.Get(credentialRef); current != "new-secret" {
		t.Fatalf("second restore key=%q, want the rotated key back", current)
	}
	groupRestored, err := fixture.service.PetRestorePreviousCredential(fixture.ctx, "env_group", group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(groupRestored), "secret-value") {
		t.Fatalf("the env group restore result leaked a value: %#v", groupRestored)
	}
	if reloaded, _ := fixture.database.EnvGroup(fixture.ctx, group.ID); reloaded.Environment["TOKEN"] != "secret-value" {
		t.Fatalf("restored environment=%#v", reloaded.Environment)
	}
	if _, err := fixture.service.PetRestorePreviousCredential(fixture.ctx, "model", model.ID); err == nil {
		t.Fatal("an unknown restore target was accepted")
	}
	name := "宠物模型改名"
	updated, err := fixture.service.PetUpdateModel(fixture.ctx, PetModelInput{
		ModelID: model.ID, DisplayName: name, ContextWindow: 300000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != name {
		t.Fatalf("updated model=%#v", updated)
	}
	approve := func(approval domain.PetApproval, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.service.PetRequestDeleteModel(fixture.ctx, model.ID); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("delete model without reason was accepted: %v", err)
	}
	approval, err := fixture.service.PetRequestDeleteModel(fixture.ctx, model.ID, "清理测试模型需要")
	if err != nil || approval.Kind != "delete_model" {
		t.Fatalf("delete model approval=%#v err=%v", approval, err)
	}
	approve(approval, nil)
	if _, err := fixture.database.Model(fixture.ctx, model.ID); err == nil {
		t.Fatal("approved model deletion did not delete the model")
	}
	if _, err := fixture.service.PetRequestDeleteEnvGroup(fixture.ctx, group.ID); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("delete env group without reason was accepted: %v", err)
	}
	approval, err = fixture.service.PetRequestDeleteEnvGroup(fixture.ctx, group.ID, "清理测试环境需要")
	if err != nil || approval.Kind != "delete_env_group" {
		t.Fatalf("delete env group approval=%#v err=%v", approval, err)
	}
	approve(approval, nil)
	groups, err := fixture.database.ListEnvGroups(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range groups {
		if item.ID == group.ID {
			t.Fatal("approved env group deletion left the group visible")
		}
	}
	if _, err := fixture.service.PetRequestDeleteProvider(fixture.ctx, provider.ID); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("delete provider without reason was accepted: %v", err)
	}
	approval, err = fixture.service.PetRequestDeleteProvider(fixture.ctx, provider.ID, "清理测试提供商需要")
	if err != nil || approval.Kind != "delete_provider" {
		t.Fatalf("delete provider approval=%#v err=%v", approval, err)
	}
	approve(approval, nil)
	if _, err := fixture.database.Provider(fixture.ctx, provider.ID); err == nil {
		t.Fatal("approved provider deletion did not delete the provider")
	}
}
