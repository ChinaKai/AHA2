package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/gateway"
)

func (s *Service) petManageModels(ctx context.Context) (domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowManageModels {
		return domain.OwnerAssistant{}, fmt.Errorf("主人未开启管理模型/Provider 权限")
	}
	return assistant, nil
}

type PetProviderInput struct {
	ProviderID       string `json:"provider_id"`
	Reason           string `json:"reason"`
	Name             string `json:"name"`
	BaseURL          string `json:"base_url"`
	AnthropicBaseURL string `json:"anthropic_base_url"`
	AuthStyle        string `json:"auth_style"`
	APIKey           string `json:"api_key"`
}

func (s *Service) PetCreateProvider(ctx context.Context, input PetProviderInput) (PetProviderBrief, error) {
	if _, err := s.petManageModels(ctx); err != nil {
		return PetProviderBrief{}, err
	}
	name := strings.TrimSpace(input.Name)
	baseURL := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	if name == "" || baseURL == "" {
		return PetProviderBrief{}, fmt.Errorf("Provider 名称和 Base URL 不能为空")
	}
	authStyle := strings.TrimSpace(input.AuthStyle)
	if authStyle == "" {
		authStyle = "auto"
	}
	now := s.now().UTC()
	item := domain.Provider{
		ID: domain.NewID("provider"), Name: name, BaseURL: baseURL,
		AnthropicBaseURL: strings.TrimRight(strings.TrimSpace(input.AnthropicBaseURL), "/"),
		AuthStyle:        authStyle, CreatedAt: now, UpdatedAt: now,
	}
	if apiKey := strings.TrimSpace(input.APIKey); apiKey != "" {
		ref := "provider/" + item.ID + "/credential"
		if s.secrets != nil {
			if err := s.secrets.PutMany(map[string]string{ref: apiKey}); err != nil {
				return PetProviderBrief{}, err
			}
		}
		item.CredentialRef = ref
		item.CredentialConfigured = true
	}
	if err := s.store.UpsertProvider(ctx, item); err != nil {
		return PetProviderBrief{}, err
	}
	return PetProviderBrief{
		ID: item.ID, Name: item.Name, BaseURL: item.BaseURL,
		AnthropicBaseURL: item.AnthropicBaseURL, AuthStyle: item.AuthStyle,
		CredentialConfigured: item.CredentialConfigured,
	}, nil
}

func (s *Service) PetUpdateProvider(ctx context.Context, input PetProviderInput) (PetProviderBrief, error) {
	if _, err := s.petManageModels(ctx); err != nil {
		return PetProviderBrief{}, err
	}
	reasonText := optionalPetApprovalReason(input.Reason)
	if strings.TrimSpace(input.APIKey) != "" {
		var reasonErr error
		reasonText, reasonErr = requirePetApprovalReason(input.Reason)
		if reasonErr != nil {
			return PetProviderBrief{}, reasonErr
		}
	}
	_ = reasonText
	providerID := strings.TrimSpace(input.ProviderID)
	existing, err := s.store.Provider(ctx, providerID)
	if err != nil {
		return PetProviderBrief{}, fmt.Errorf("Provider 不存在")
	}
	name := strings.TrimSpace(input.Name)
	baseURL := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	if name == "" || baseURL == "" {
		return PetProviderBrief{}, fmt.Errorf("Provider 名称和 Base URL 不能为空")
	}
	authStyle := strings.TrimSpace(input.AuthStyle)
	if authStyle == "" {
		authStyle = "auto"
	}
	existing.Name = name
	existing.BaseURL = baseURL
	existing.AnthropicBaseURL = strings.TrimRight(strings.TrimSpace(input.AnthropicBaseURL), "/")
	existing.AuthStyle = authStyle
	existing.UpdatedAt = s.now().UTC()
	if apiKey := strings.TrimSpace(input.APIKey); apiKey != "" {
		ref := "provider/" + existing.ID + "/credential"
		if s.secrets != nil {
			if err := s.secrets.PutMany(map[string]string{ref: apiKey}); err != nil {
				return PetProviderBrief{}, err
			}
		}
		existing.CredentialRef = ref
		existing.CredentialConfigured = true
	}
	if err := s.store.UpsertProvider(ctx, existing); err != nil {
		return PetProviderBrief{}, err
	}
	return PetProviderBrief{
		ID: existing.ID, Name: existing.Name, BaseURL: existing.BaseURL,
		AnthropicBaseURL: existing.AnthropicBaseURL, AuthStyle: existing.AuthStyle,
		CredentialConfigured: existing.CredentialConfigured,
	}, nil
}

func (s *Service) PetRequestDeleteProvider(ctx context.Context, providerID string, reason ...string) (domain.PetApproval, error) {
	reasonText, reasonErr := requiredPetApprovalReason(reason...)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	if _, err := s.petManageModels(ctx); err != nil {
		return domain.PetApproval{}, err
	}
	provider, err := s.store.Provider(ctx, strings.TrimSpace(providerID))
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("Provider 不存在")
	}
	if err := s.providerUnused(ctx, provider.ID); err != nil {
		return domain.PetApproval{}, err
	}
	payload, _ := json.Marshal(map[string]string{"provider_id": provider.ID})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "delete_provider", Title: "删除 Provider：" + provider.Name,
		Summary: fmt.Sprintf("宠物申请删除 Provider「%s」（%s）。批准后才执行，并清理其凭据。", provider.Name, provider.ID),
		Reason:  reasonText, Risk: "high", Payload: string(payload),
	})
}

func (s *Service) deleteProviderNow(ctx context.Context, providerID string) error {
	provider, err := s.store.Provider(ctx, strings.TrimSpace(providerID))
	if err != nil {
		return fmt.Errorf("Provider 不存在")
	}
	if err := s.providerUnused(ctx, provider.ID); err != nil {
		return err
	}
	if err := s.store.DeleteProvider(ctx, provider.ID); err != nil {
		return err
	}
	if s.secrets != nil && provider.CredentialRef != "" {
		_ = s.secrets.DeleteMany([]string{provider.CredentialRef})
	}
	return nil
}

func (s *Service) providerUnused(ctx context.Context, providerID string) error {
	models, err := s.store.ListModels(ctx)
	if err != nil {
		return err
	}
	for _, model := range models {
		if model.ProviderID == providerID {
			return fmt.Errorf("Provider 仍被模型 %s 使用，不能删除", model.DisplayName)
		}
	}
	return nil
}

type PetModelInput struct {
	ModelID           string `json:"model_id"`
	DisplayName       string `json:"display_name"`
	ProviderID        string `json:"provider_id"`
	Backend           string `json:"backend"`
	WireModel         string `json:"wire_model"`
	WireAPI           string `json:"wire_api"`
	ContextWindow     int64  `json:"context_window"`
	MaxOutputTokens   int64  `json:"max_output_tokens"`
	AutoCompactWindow int64  `json:"auto_compact_window"`
	DefaultEffort     string `json:"default_reasoning_effort"`
	DefaultEnvGroupID string `json:"default_env_group_id"`
}

func (s *Service) PetCreateModel(ctx context.Context, input PetModelInput) (PetModelBrief, error) {
	if _, err := s.petManageModels(ctx); err != nil {
		return PetModelBrief{}, err
	}
	displayName := strings.TrimSpace(input.DisplayName)
	providerID := strings.TrimSpace(input.ProviderID)
	backend := strings.TrimSpace(input.Backend)
	wireModel := strings.TrimSpace(input.WireModel)
	if displayName == "" || providerID == "" || backend == "" || wireModel == "" {
		return PetModelBrief{}, fmt.Errorf("模型名称、Provider、Backend 和 Wire Model 不能为空")
	}
	provider, err := s.store.Provider(ctx, providerID)
	if err != nil {
		return PetModelBrief{}, fmt.Errorf("Provider 不存在")
	}
	envGroupID := strings.TrimSpace(input.DefaultEnvGroupID)
	if envGroupID != "" {
		group, groupErr := s.store.EnvGroup(ctx, envGroupID)
		if groupErr != nil {
			return PetModelBrief{}, fmt.Errorf("Env Group 不存在")
		}
		if group.Backend != "" && group.Backend != backend {
			return PetModelBrief{}, fmt.Errorf("Env Group 与模型 Backend 不匹配")
		}
	}
	now := s.now().UTC()
	maxOutput := domain.DefaultMaxOutputTokens(input.ContextWindow, input.MaxOutputTokens)
	compact := input.AutoCompactWindow
	if compact <= 0 {
		compact = domain.DefaultAutoCompactWindow(input.ContextWindow, maxOutput)
	}
	model := domain.Model{
		ID: domain.NewID("model"), DisplayName: displayName, ProviderID: providerID,
		Source: domain.ModelSourceProvider, Backend: backend, WireModel: wireModel,
		WireAPI: strings.TrimSpace(input.WireAPI), ContextWindow: input.ContextWindow,
		MaxOutputTokens: maxOutput, AutoCompactWindow: compact,
		DefaultEffort: strings.TrimSpace(input.DefaultEffort), DefaultEnvGroupID: envGroupID,
		Capabilities: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.UpsertModel(ctx, model); err != nil {
		return PetModelBrief{}, err
	}
	return petModelBrief(model, provider.Name), nil
}

func (s *Service) PetUpdateModel(ctx context.Context, input PetModelInput) (PetModelBrief, error) {
	if _, err := s.petManageModels(ctx); err != nil {
		return PetModelBrief{}, err
	}
	modelID := strings.TrimSpace(input.ModelID)
	existing, err := s.store.Model(ctx, modelID)
	if err != nil {
		return PetModelBrief{}, fmt.Errorf("模型不存在")
	}
	if value := strings.TrimSpace(input.DisplayName); value != "" {
		existing.DisplayName = value
	}
	if value := strings.TrimSpace(input.ProviderID); value != "" {
		provider, providerErr := s.store.Provider(ctx, value)
		if providerErr != nil {
			return PetModelBrief{}, fmt.Errorf("Provider 不存在")
		}
		existing.ProviderID = provider.ID
	}
	if value := strings.TrimSpace(input.Backend); value != "" {
		existing.Backend = value
	}
	if value := strings.TrimSpace(input.WireModel); value != "" {
		existing.WireModel = value
	}
	if value := strings.TrimSpace(input.WireAPI); value != "" {
		existing.WireAPI = value
	}
	if input.ContextWindow > 0 {
		existing.ContextWindow = input.ContextWindow
	}
	if input.MaxOutputTokens > 0 {
		existing.MaxOutputTokens = domain.DefaultMaxOutputTokens(existing.ContextWindow, input.MaxOutputTokens)
	}
	if input.AutoCompactWindow > 0 {
		existing.AutoCompactWindow = input.AutoCompactWindow
	} else {
		existing.AutoCompactWindow = domain.DefaultAutoCompactWindow(existing.ContextWindow, existing.MaxOutputTokens)
	}
	if value := strings.TrimSpace(input.DefaultEffort); value != "" {
		existing.DefaultEffort = value
	}
	if value := strings.TrimSpace(input.DefaultEnvGroupID); value != "" {
		group, groupErr := s.store.EnvGroup(ctx, value)
		if groupErr != nil {
			return PetModelBrief{}, fmt.Errorf("Env Group 不存在")
		}
		if group.Backend != "" && group.Backend != existing.Backend {
			return PetModelBrief{}, fmt.Errorf("Env Group 与模型 Backend 不匹配")
		}
		existing.DefaultEnvGroupID = value
	}
	existing.UpdatedAt = s.now().UTC()
	if err := s.store.UpsertModel(ctx, existing); err != nil {
		return PetModelBrief{}, err
	}
	provider, _ := s.store.Provider(ctx, existing.ProviderID)
	return petModelBrief(existing, provider.Name), nil
}

func (s *Service) PetRequestDeleteModel(ctx context.Context, modelID string, reason ...string) (domain.PetApproval, error) {
	reasonText, reasonErr := requiredPetApprovalReason(reason...)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	if _, err := s.petManageModels(ctx); err != nil {
		return domain.PetApproval{}, err
	}
	model, err := s.store.Model(ctx, strings.TrimSpace(modelID))
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("模型不存在")
	}
	payload, _ := json.Marshal(map[string]string{"model_id": model.ID})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "delete_model", Title: "删除模型：" + model.DisplayName,
		Summary: fmt.Sprintf("宠物申请删除模型「%s」（%s）。批准后才执行；历史 Task 再使用该模型时会提示模型无效。", model.DisplayName, model.ID),
		Reason:  reasonText, Risk: "high", Payload: string(payload),
	})
}

func (s *Service) deleteModelNow(ctx context.Context, modelID string) error {
	model, err := s.store.Model(ctx, strings.TrimSpace(modelID))
	if err != nil {
		return fmt.Errorf("模型不存在")
	}
	return s.store.DeleteModel(ctx, model.ID)
}

type PetEnvGroupInput struct {
	EnvGroupID  string            `json:"env_group_id"`
	Reason      string            `json:"reason"`
	Name        string            `json:"name"`
	ProviderID  string            `json:"provider_id"`
	Backend     string            `json:"backend"`
	Environment map[string]string `json:"environment"`
}

func (s *Service) PetCreateEnvGroup(ctx context.Context, input PetEnvGroupInput) (PetEnvGroupBrief, error) {
	if _, err := s.petManageModels(ctx); err != nil {
		return PetEnvGroupBrief{}, err
	}
	name := strings.TrimSpace(input.Name)
	providerID := strings.TrimSpace(input.ProviderID)
	backend := strings.TrimSpace(input.Backend)
	if name == "" || providerID == "" || backend == "" {
		return PetEnvGroupBrief{}, fmt.Errorf("Env Group 名称、Provider 和 Backend 不能为空")
	}
	if _, err := s.store.Provider(ctx, providerID); err != nil {
		return PetEnvGroupBrief{}, fmt.Errorf("Provider 不存在")
	}
	environment := map[string]string{}
	for key, value := range input.Environment {
		if strings.TrimSpace(key) != "" {
			environment[key] = value
		}
	}
	now := s.now().UTC()
	item := domain.EnvGroup{
		ID: domain.NewID("env"), Name: name, ProviderID: providerID, Backend: backend,
		Revision: 1, Environment: environment, SecretRefs: map[string]string{},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.UpsertEnvGroup(ctx, item); err != nil {
		return PetEnvGroupBrief{}, err
	}
	return petEnvGroupBrief(item), nil
}

func (s *Service) PetUpdateEnvGroup(ctx context.Context, input PetEnvGroupInput) (PetEnvGroupBrief, error) {
	if _, err := s.petManageModels(ctx); err != nil {
		return PetEnvGroupBrief{}, err
	}
	if input.Environment != nil {
		if _, reasonErr := requirePetApprovalReason(input.Reason); reasonErr != nil {
			return PetEnvGroupBrief{}, reasonErr
		}
	}
	groupID := strings.TrimSpace(input.EnvGroupID)
	existing, err := s.store.EnvGroup(ctx, groupID)
	if err != nil {
		return PetEnvGroupBrief{}, fmt.Errorf("Env Group 不存在")
	}
	if value := strings.TrimSpace(input.Name); value != "" {
		existing.Name = value
	}
	if value := strings.TrimSpace(input.ProviderID); value != "" {
		if _, providerErr := s.store.Provider(ctx, value); providerErr != nil {
			return PetEnvGroupBrief{}, fmt.Errorf("Provider 不存在")
		}
		existing.ProviderID = value
	}
	if value := strings.TrimSpace(input.Backend); value != "" {
		existing.Backend = value
	}
	if input.Environment != nil {
		environment := map[string]string{}
		for key, value := range input.Environment {
			if strings.TrimSpace(key) != "" {
				environment[key] = value
			}
		}
		existing.Environment = environment
	}
	existing.Revision++
	existing.UpdatedAt = s.now().UTC()
	if err := s.store.UpsertEnvGroup(ctx, existing); err != nil {
		return PetEnvGroupBrief{}, err
	}
	return petEnvGroupBrief(existing), nil
}

func (s *Service) PetRequestDeleteEnvGroup(ctx context.Context, envGroupID string, reason ...string) (domain.PetApproval, error) {
	reasonText, reasonErr := requiredPetApprovalReason(reason...)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	if _, err := s.petManageModels(ctx); err != nil {
		return domain.PetApproval{}, err
	}
	group, err := s.store.EnvGroup(ctx, strings.TrimSpace(envGroupID))
	if err != nil {
		return domain.PetApproval{}, fmt.Errorf("Env Group 不存在")
	}
	payload, _ := json.Marshal(map[string]string{"env_group_id": group.ID})
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "delete_env_group", Title: "删除 Env Group：" + group.Name,
		Summary: fmt.Sprintf("宠物申请删除 Env Group「%s」（%s）。批准后才执行。", group.Name, group.ID),
		Reason:  reasonText, Risk: "high", Payload: string(payload),
	})
}

func (s *Service) deleteEnvGroupNow(ctx context.Context, envGroupID string) error {
	group, err := s.store.EnvGroup(ctx, strings.TrimSpace(envGroupID))
	if err != nil {
		return fmt.Errorf("Env Group 不存在")
	}
	return s.store.DeleteEnvGroup(ctx, group.ID)
}

func (s *Service) PetDetectModels(ctx context.Context, providerID string) (gateway.Result, error) {
	if _, err := s.petManageModels(ctx); err != nil {
		return gateway.Result{}, err
	}
	provider, err := s.store.Provider(ctx, strings.TrimSpace(providerID))
	if err != nil {
		return gateway.Result{}, fmt.Errorf("Provider 不存在")
	}
	apiKey := ""
	if provider.CredentialRef != "" && s.secrets != nil {
		apiKey, _ = s.secrets.Get(provider.CredentialRef)
	}
	result, err := gateway.DetectModelsContext(ctx, provider.BaseURL, apiKey, provider.AuthStyle, 15*time.Second)
	if err != nil {
		return gateway.Result{}, err
	}
	if len(result.Models) == 0 {
		return gateway.Result{}, fmt.Errorf("Provider 没有返回可用模型")
	}
	return result, nil
}

func petModelBrief(model domain.Model, providerName string) PetModelBrief {
	return PetModelBrief{
		ID: model.ID, DisplayName: model.DisplayName, ProviderID: model.ProviderID,
		ProviderName: providerName, Backend: model.Backend, WireModel: model.WireModel,
		WireAPI: model.WireAPI, ContextWindow: model.ContextWindow,
		MaxOutputTokens: model.MaxOutputTokens, AutoCompactWindow: model.AutoCompactWindow,
	}
}

func petEnvGroupBrief(group domain.EnvGroup) PetEnvGroupBrief {
	names := make([]string, 0, len(group.Environment))
	for key := range group.Environment {
		names = append(names, key)
	}
	return PetEnvGroupBrief{
		ID: group.ID, Name: group.Name, ProviderID: group.ProviderID,
		Backend: group.Backend, Revision: group.Revision, EnvNames: names,
		SecretNames: group.SecretNames, SecretConfigured: group.SecretConfigured,
	}
}
