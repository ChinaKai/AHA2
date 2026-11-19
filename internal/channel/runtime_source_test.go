package channel

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func newChannelRuntimeStore(t *testing.T) (*store.Store, *Service, domain.Model, domain.CodexAccount) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	provider := domain.Provider{ID: "provider-channel-runtime", Name: "Channel Provider", BaseURL: "https://example.test/v1", CreatedAt: now, UpdatedAt: now}
	env := domain.EnvGroup{
		ID: "env-channel-runtime", Name: "Channel", ProviderID: provider.ID, Backend: "codex", Revision: 1,
		Environment: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-channel-provider", DisplayName: "Provider Model", ProviderID: provider.ID,
		Source: domain.ModelSourceProvider, Backend: "codex", WireModel: "provider-model", WireAPI: "chat",
		DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	account := domain.CodexAccount{
		ID: "account-channel", Label: "Channel Official", Status: "ready", CredentialConfigured: true,
		AvailableModels: []domain.CodexModelOption{{WireModel: "gpt-channel-official", DisplayName: "Channel Official"}},
		CreatedAt:       now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.UpsertProvider(ctx, provider) },
		func() error { return database.UpsertEnvGroup(ctx, env) },
		func() error { return database.UpsertModel(ctx, model) },
		func() error { return database.UpsertCodexAccount(ctx, account) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	return database, New(Config{Store: database, DataDir: t.TempDir()}), model, account
}

func TestNormalizeChannelRuntimeConfigSupportsAllSources(t *testing.T) {
	ctx := context.Background()
	_, service, model, account := newChannelRuntimeStore(t)

	providerConfig, err := service.normalizeChannelRuntimeConfig(ctx, map[string]any{
		"model_source": "provider", "model_id": model.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stringField(providerConfig, "model_source") != domain.ModelSourceProvider ||
		stringField(providerConfig, "model_id") != model.ID || stringField(providerConfig, "codex_account_id") != "" {
		t.Fatalf("provider config=%#v", providerConfig)
	}

	officialConfig, err := service.normalizeChannelRuntimeConfig(ctx, map[string]any{
		"model_source": "official", "wire_model": "gpt-channel-official", "codex_account_id": account.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stringField(officialConfig, "model_source") != domain.ModelSourceOfficial ||
		stringField(officialConfig, "model_id") != domain.OfficialCodexModelID(account.ID, "gpt-channel-official") ||
		stringField(officialConfig, "codex_account_id") != account.ID {
		t.Fatalf("official config=%#v", officialConfig)
	}

	nativeConfig, err := service.normalizeChannelRuntimeConfig(ctx, map[string]any{
		"model_source": "claude_native", "wire_model": "claude-sonnet-4-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stringField(nativeConfig, "model_source") != domain.ModelSourceClaudeNative ||
		stringField(nativeConfig, "model_id") != domain.ClaudeNativeModelID("claude-sonnet-4-5") ||
		stringField(nativeConfig, "codex_account_id") != "" {
		t.Fatalf("native config=%#v", nativeConfig)
	}

	autoConfig, err := service.normalizeChannelRuntimeConfig(ctx, map[string]any{"model_source": "provider", "model_id": ""})
	if err != nil || stringField(autoConfig, "model_id") != "" {
		t.Fatalf("auto config=%#v err=%v", autoConfig, err)
	}
}

func TestNormalizeChannelRuntimeConfigRejectsInvalidCombinations(t *testing.T) {
	ctx := context.Background()
	_, service, model, account := newChannelRuntimeStore(t)

	for name, config := range map[string]map[string]any{
		"official with provider model": {"model_source": "official", "model_id": model.ID, "wire_model": "gpt-channel-official"},
		"provider with official model": {"model_source": "provider", "model_id": domain.OfficialCodexModelID(account.ID, "gpt-channel-official")},
		"official without wire":        {"model_source": "official", "codex_account_id": account.ID},
		"native without wire":          {"model_source": "claude_native"},
		"unknown source":               {"model_source": "mystery", "model_id": model.ID},
		"official with wrong account":  {"model_source": "official", "wire_model": "not-offered", "codex_account_id": account.ID},
	} {
		if _, err := service.normalizeChannelRuntimeConfig(ctx, config); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestResolveChannelRuntimeHonorsExplicitSourceWithoutFallback(t *testing.T) {
	ctx := context.Background()
	database, service, model, account := newChannelRuntimeStore(t)
	instance := domain.ChannelInstance{ID: "channel-runtime-test", Config: map[string]any{}}

	instance.Config = map[string]any{"runtime_default": map[string]any{"model_source": "official", "wire_model": "gpt-channel-official", "codex_account_id": account.ID}}
	choice, err := service.resolveChannelRuntime(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	if choice.ModelSource != domain.ModelSourceOfficial || choice.Model.ID != domain.OfficialCodexModelID(account.ID, "gpt-channel-official") ||
		choice.CodexAccountID != account.ID || choice.WireModel != "gpt-channel-official" {
		t.Fatalf("official choice=%#v", choice)
	}

	instance.Config = map[string]any{"runtime_default": map[string]any{"model_source": "claude_native", "wire_model": "claude-sonnet-4-5"}}
	choice, err = service.resolveChannelRuntime(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	if choice.ModelSource != domain.ModelSourceClaudeNative || choice.Model.ID != domain.ClaudeNativeModelID("claude-sonnet-4-5") ||
		choice.Model.Backend != "claude" || choice.CodexAccountID != "" {
		t.Fatalf("native choice=%#v", choice)
	}

	instance.Config = map[string]any{"runtime_default": map[string]any{"model_source": "official", "wire_model": "gpt-channel-official", "codex_account_id": "account-missing"}}
	if _, err := service.resolveChannelRuntime(ctx, instance); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("missing account did not fail explicitly: %v", err)
	}

	// A configured but unavailable official account must not silently become
	// the Provider model that is still present in the catalog.
	account.Status = "expired"
	account.UpdatedAt = time.Now().UTC()
	if err := database.UpsertCodexAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	instance.Config = map[string]any{"runtime_default": map[string]any{"model_source": "official", "wire_model": "gpt-channel-official", "codex_account_id": account.ID}}
	if _, err := service.resolveChannelRuntime(ctx, instance); err == nil {
		t.Fatal("expired account fell back to another runtime")
	}
	_ = model
}

// Saving unrelated channel settings must not re-validate an unchanged runtime
// selection. Otherwise a later-deleted account or model blocks every save,
// including turning the channel off.
func TestValidateChannelInstanceConfigSkipsUnchangedRetiredRuntime(t *testing.T) {
	ctx := context.Background()
	database, service, model, account := newChannelRuntimeStore(t)
	storedRuntime := map[string]any{
		"model_source": domain.ModelSourceOfficial, "wire_model": "gpt-channel-official",
		"model_id":         domain.OfficialCodexModelID(account.ID, "gpt-channel-official"),
		"codex_account_id": account.ID,
	}
	instance := domain.ChannelInstance{
		ID: "channel-unchanged-runtime", Config: map[string]any{"runtime_default": storedRuntime},
	}

	// Retire the account after it was saved.
	account.Status = "expired"
	account.UpdatedAt = time.Now().UTC()
	if err := database.UpsertCodexAccount(ctx, account); err != nil {
		t.Fatal(err)
	}

	unchanged := map[string]any{
		"runtime_default": map[string]any{
			"model_source": domain.ModelSourceOfficial, "wire_model": "gpt-channel-official",
			"model_id":         domain.OfficialCodexModelID(account.ID, "gpt-channel-official"),
			"codex_account_id": account.ID, "proxy_enabled": true,
		},
		"name": "renamed",
	}
	if err := service.validateChannelInstanceConfig(ctx, instance, unchanged); err != nil {
		t.Fatalf("unchanged retired runtime blocked an unrelated save: %v", err)
	}
	if got, ok := mapField(unchanged, "runtime_default"); !ok || got == nil {
		t.Fatalf("unchanged runtime was dropped: %#v", unchanged)
	}
	if !sameChannelRuntimeSelection(unchanged["runtime_default"].(map[string]any), storedRuntime) {
		t.Fatalf("stored runtime was not recognized as unchanged: %#v", unchanged["runtime_default"])
	}
	if !boolField(unchanged["runtime_default"].(map[string]any), "proxy_enabled") {
		t.Fatalf("non-identity runtime change was lost: %#v", unchanged["runtime_default"])
	}

	// An actual change still resolves and must fail on the retired account.
	changed := map[string]any{"runtime_default": map[string]any{
		"model_source": domain.ModelSourceProvider, "model_id": model.ID,
	}}
	if err := service.validateChannelInstanceConfig(ctx, instance, changed); err != nil {
		t.Fatalf("provider change should still be valid: %v", err)
	}
	changedOfficial := map[string]any{"runtime_default": map[string]any{
		"model_source": domain.ModelSourceOfficial, "wire_model": "gpt-channel-official",
		"codex_account_id": "account-missing",
	}}
	if err := service.validateChannelInstanceConfig(ctx, instance, changedOfficial); err == nil {
		t.Fatal("changed runtime accepted a missing account")
	}
}
