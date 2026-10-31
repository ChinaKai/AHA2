package app

import (
	"context"
	"fmt"
	"sort"
)

type PetModelBrief struct {
	ID                string `json:"id"`
	DisplayName       string `json:"display_name"`
	ProviderID        string `json:"provider_id"`
	ProviderName      string `json:"provider_name,omitempty"`
	Backend           string `json:"backend"`
	WireModel         string `json:"wire_model"`
	WireAPI           string `json:"wire_api,omitempty"`
	ContextWindow     int64  `json:"context_window,omitempty"`
	MaxOutputTokens   int64  `json:"max_output_tokens,omitempty"`
	AutoCompactWindow int64  `json:"auto_compact_window,omitempty"`
}

type PetProviderBrief struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	BaseURL              string `json:"base_url"`
	AnthropicBaseURL     string `json:"anthropic_base_url,omitempty"`
	AuthStyle            string `json:"auth_style,omitempty"`
	CredentialConfigured bool   `json:"credential_configured"`
}

type PetEnvGroupBrief struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	ProviderID       string   `json:"provider_id"`
	Backend          string   `json:"backend"`
	Revision         int      `json:"revision"`
	EnvNames         []string `json:"env_names,omitempty"`
	SecretNames      []string `json:"secret_names,omitempty"`
	SecretConfigured bool     `json:"secret_configured"`
}

type PetModelCatalog struct {
	Models    []PetModelBrief    `json:"models"`
	Providers []PetProviderBrief `json:"providers"`
	EnvGroups []PetEnvGroupBrief `json:"env_groups"`
}

// PetModelCatalog is a read-only, redacted view of AHA model configuration.
// Environment values and credential references are never returned.
func (s *Service) PetModelCatalog(ctx context.Context) (PetModelCatalog, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return PetModelCatalog{}, err
	}
	if !assistant.PetAllowViewTasks {
		return PetModelCatalog{}, fmt.Errorf("主人未开启查看 AHA 信息权限")
	}
	models, err := s.store.ListModels(ctx)
	if err != nil {
		return PetModelCatalog{}, err
	}
	providers, err := s.store.ListProviders(ctx)
	if err != nil {
		return PetModelCatalog{}, err
	}
	groups, err := s.store.ListEnvGroups(ctx)
	if err != nil {
		return PetModelCatalog{}, err
	}
	providerNames := make(map[string]string, len(providers))
	for _, provider := range providers {
		providerNames[provider.ID] = provider.Name
	}
	catalog := PetModelCatalog{
		Models:    make([]PetModelBrief, 0, len(models)),
		Providers: make([]PetProviderBrief, 0, len(providers)),
		EnvGroups: make([]PetEnvGroupBrief, 0, len(groups)),
	}
	for _, model := range models {
		catalog.Models = append(catalog.Models, PetModelBrief{
			ID: model.ID, DisplayName: model.DisplayName, ProviderID: model.ProviderID,
			ProviderName: providerNames[model.ProviderID], Backend: model.Backend,
			WireModel: model.WireModel, WireAPI: model.WireAPI,
			ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
			AutoCompactWindow: model.AutoCompactWindow,
		})
	}
	for _, provider := range providers {
		catalog.Providers = append(catalog.Providers, PetProviderBrief{
			ID: provider.ID, Name: provider.Name, BaseURL: provider.BaseURL,
			AnthropicBaseURL: provider.AnthropicBaseURL, AuthStyle: provider.AuthStyle,
			CredentialConfigured: provider.CredentialConfigured,
		})
	}
	for _, group := range groups {
		envNames := make([]string, 0, len(group.Environment))
		for name := range group.Environment {
			envNames = append(envNames, name)
		}
		sort.Strings(envNames)
		catalog.EnvGroups = append(catalog.EnvGroups, PetEnvGroupBrief{
			ID: group.ID, Name: group.Name, ProviderID: group.ProviderID,
			Backend: group.Backend, Revision: group.Revision,
			EnvNames: envNames, SecretNames: group.SecretNames,
			SecretConfigured: group.SecretConfigured,
		})
	}
	return catalog, nil
}
