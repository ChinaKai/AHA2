package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const officialCodexEnvGroupID = "env_codex_official"

type runtimeSelectionInput struct {
	Backend        string
	ModelSource    string
	ModelID        string
	WireModel      string
	CodexAccountID string
}

func (s *Service) resolveRuntimeSelection(
	ctx context.Context,
	input runtimeSelectionInput,
) (domain.Model, domain.EnvGroup, string, error) {
	source := strings.ToLower(strings.TrimSpace(input.ModelSource))
	if source == "" || source == "env" {
		source = domain.ModelSourceProvider
	}
	if source == domain.ModelSourceOfficial {
		return s.resolveOfficialCodexSelection(ctx, input)
	}
	if source != domain.ModelSourceProvider {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("model_source must be env or official")
	}
	model, err := s.store.Model(ctx, strings.TrimSpace(input.ModelID))
	if err != nil {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("model: %w", err)
	}
	if model.Source == domain.ModelSourceOfficial {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("official Codex models must be selected through an account")
	}
	backend := strings.TrimSpace(input.Backend)
	if backend != "" && backend != model.Backend {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("backend does not match model backend")
	}
	if model.DefaultEnvGroupID == "" {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("model has no default env group")
	}
	envGroup, err := s.store.EnvGroup(ctx, model.DefaultEnvGroupID)
	if err != nil {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("env group: %w", err)
	}
	if model.ProviderID != "" && envGroup.ProviderID != "" && model.ProviderID != envGroup.ProviderID {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("env group provider does not match model provider")
	}
	return model, envGroup, "", nil
}

func (s *Service) resolveOfficialCodexSelection(
	ctx context.Context,
	input runtimeSelectionInput,
) (domain.Model, domain.EnvGroup, string, error) {
	if backend := strings.TrimSpace(input.Backend); backend != "" && backend != "codex" {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("official model usage requires the codex backend")
	}
	accountID := strings.TrimSpace(input.CodexAccountID)
	account, err := s.store.CodexAccount(ctx, accountID)
	if err != nil {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("Codex account: %w", err)
	}
	if !account.CredentialConfigured {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("Codex account credentials are not configured")
	}
	wireModel := strings.TrimSpace(input.WireModel)
	var option domain.CodexModelOption
	for _, candidate := range account.AvailableModels {
		if candidate.WireModel == wireModel {
			option = candidate
			break
		}
	}
	if option.WireModel == "" {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("selected model is not available for this Codex account")
	}
	now := s.now().UTC()
	envGroup := domain.EnvGroup{
		ID: officialCodexEnvGroupID, Name: "Official Codex Runtime",
		ProviderID: domain.OfficialCodexProviderID, Backend: "codex", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.UpsertEnvGroup(ctx, envGroup); err != nil {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("official Codex env group: %w", err)
	}
	contextWindow := option.MaxContextWindow
	if contextWindow == 0 {
		contextWindow = option.ContextWindow
	}
	digest := sha256.Sum256([]byte(option.WireModel))
	model := domain.Model{
		ID: fmt.Sprintf("model_codex_official_%x", digest[:8]), DisplayName: option.DisplayName,
		ProviderID: domain.OfficialCodexProviderID, Source: domain.ModelSourceOfficial,
		Backend: "codex", WireModel: option.WireModel, WireAPI: "responses",
		ContextWindow: contextWindow, DefaultEffort: option.DefaultEffort,
		Capabilities:      map[string]any{"reasoning_efforts": option.ReasoningEfforts},
		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	if model.DisplayName == "" {
		model.DisplayName = model.WireModel
	}
	if err := s.store.UpsertModel(ctx, model); err != nil {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("official Codex runtime model: %w", err)
	}
	return model, envGroup, account.ID, nil
}
