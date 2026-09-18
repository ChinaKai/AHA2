package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

const officialCodexEnvGroupID = "env_codex_official"

type runtimeSelectionInput struct {
	WorkspaceID    string
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
	if source == domain.ModelSourceClaudeNative {
		return s.resolveClaudeNativeSelection(ctx, input)
	}
	if source != domain.ModelSourceProvider {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("model_source must be env, official, or claude_native")
	}
	model, err := s.store.Model(ctx, strings.TrimSpace(input.ModelID))
	if err != nil {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("model: %w", err)
	}
	if model.Source != "" && model.Source != domain.ModelSourceProvider {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("managed models must be selected through their runtime source")
	}
	backend := strings.TrimSpace(input.Backend)
	if backend != "" && backend != model.Backend {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("backend does not match model backend")
	}
	if model.DefaultEnvGroupID == "" {
		envGroup, repairErr := s.repairDefaultEnvGroup(ctx, &model)
		if repairErr != nil {
			return domain.Model{}, domain.EnvGroup{}, "", repairErr
		}
		return model, envGroup, "", nil
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

func (s *Service) resolveClaudeNativeSelection(
	ctx context.Context,
	input runtimeSelectionInput,
) (domain.Model, domain.EnvGroup, string, error) {
	if backend := strings.TrimSpace(input.Backend); backend != "" && backend != "claude" {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("Claude native account requires the claude backend")
	}
	wireModel := strings.ToLower(strings.TrimSpace(input.WireModel))
	if wireModel == "" {
		wireModel = "default"
	}
	if len(wireModel) > 160 || strings.ContainsAny(wireModel, " \t\r\n") {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("invalid Claude official model")
	}
	option, _ := s.claudeModelOption(ctx, input.WorkspaceID, wireModel)
	displayName := option.DisplayName
	if displayName == "" {
		displayName = claudeNativeDisplayName(wireModel)
	}
	now := s.now().UTC()
	envGroup := domain.EnvGroup{
		ID: domain.ClaudeNativeEnvGroupID, Name: "Claude Code Native Runtime",
		ProviderID: domain.OfficialClaudeProviderID, Backend: "claude", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.UpsertEnvGroup(ctx, envGroup); err != nil {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("Claude native env group: %w", err)
	}
	digest := sha256.Sum256([]byte(wireModel))
	capabilities := map[string]any{}
	if option.ResolvedModel != "" {
		capabilities["resolved_model"] = option.ResolvedModel
	}
	if len(option.SupportedEffortLevels) > 0 {
		capabilities["reasoning_efforts"] = option.SupportedEffortLevels
	}
	if option.SupportsAdaptiveThinking {
		capabilities["supports_adaptive_thinking"] = true
	}
	if option.SupportsFastMode {
		capabilities["supports_fast_mode"] = true
	}
	if option.SupportsAutoMode {
		capabilities["supports_auto_mode"] = true
	}
	model := domain.Model{
		ID: fmt.Sprintf("model_claude_native_%x", digest[:8]), DisplayName: displayName,
		ProviderID: domain.OfficialClaudeProviderID, Source: domain.ModelSourceClaudeNative,
		Backend: "claude", WireModel: wireModel, WireAPI: "anthropic_messages",
		Capabilities:      capabilities,
		DefaultEnvGroupID: envGroup.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.UpsertModel(ctx, model); err != nil {
		return domain.Model{}, domain.EnvGroup{}, "", fmt.Errorf("Claude native runtime model: %w", err)
	}
	return model, envGroup, "", nil
}

func (s *Service) claudeModelOption(ctx context.Context, workspaceID, wireModel string) (domain.ClaudeModelOption, bool) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return domain.ClaudeModelOption{}, false
	}
	workspace, err := s.store.Workspace(ctx, workspaceID)
	if err != nil {
		return domain.ClaudeModelOption{}, false
	}
	for _, model := range workspacepkg.ClaudeModelsFromCapabilities(workspace.Capabilities) {
		if strings.EqualFold(strings.TrimSpace(model.WireModel), wireModel) {
			return model, true
		}
	}
	return domain.ClaudeModelOption{}, false
}

func claudeNativeDisplayName(wireModel string) string {
	if displayName := map[string]string{
		"default": "Claude 默认模型",
		"sonnet":  "Claude Sonnet",
		"opus":    "Claude Opus",
		"haiku":   "Claude Haiku",
	}[wireModel]; displayName != "" {
		return displayName
	}
	return wireModel
}

func (s *Service) repairDefaultEnvGroup(ctx context.Context, model *domain.Model) (domain.EnvGroup, error) {
	groups, err := s.store.ListEnvGroups(ctx)
	if err != nil {
		return domain.EnvGroup{}, fmt.Errorf("env groups: %w", err)
	}
	generic := make([]domain.EnvGroup, 0, 1)
	exact := make([]domain.EnvGroup, 0, 1)
	for _, group := range groups {
		if group.ProviderID != model.ProviderID || group.Backend != model.Backend {
			continue
		}
		configuredModel := strings.TrimSpace(group.Environment["OPENAI_MODEL"])
		if configuredModel == "" {
			configuredModel = strings.TrimSpace(group.Environment["ANTHROPIC_MODEL"])
		}
		switch {
		case configuredModel == model.WireModel:
			exact = append(exact, group)
		case configuredModel == "":
			generic = append(generic, group)
		}
	}
	candidates := exact
	if len(candidates) == 0 && len(generic) == 1 {
		candidates = generic
	}
	if len(candidates) != 1 {
		return domain.EnvGroup{}, fmt.Errorf("model has no default env group")
	}
	model.DefaultEnvGroupID = candidates[0].ID
	model.UpdatedAt = s.now().UTC()
	if err := s.store.UpsertModel(ctx, *model); err != nil {
		return domain.EnvGroup{}, fmt.Errorf("save model default env group: %w", err)
	}
	return candidates[0], nil
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
