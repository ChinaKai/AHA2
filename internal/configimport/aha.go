package configimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type Result struct {
	Models    []domain.Model    `json:"models"`
	EnvGroups []domain.EnvGroup `json:"env_groups"`
	Secrets   map[string]string `json:"-"`
}

type ahaConfig struct {
	Providers        []map[string]any `json:"providers"`
	ConfiguredModels []map[string]any `json:"configured_models"`
	Codex            struct {
		Env []map[string]any `json:"env"`
	} `json:"codex"`
}

func LoadAHA(path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read AHA config: %w", err)
	}
	var config ahaConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return Result{}, fmt.Errorf("decode AHA config: %w", err)
	}
	now := time.Now().UTC()
	providerCredentials := map[string]string{}
	for _, provider := range config.Providers {
		id := text(provider["id"])
		if id != "" {
			providerCredentials[id] = text(provider["credential"])
		}
	}
	result := Result{Secrets: map[string]string{}}
	envByProviderModel := map[string]string{}
	for _, raw := range config.Codex.Env {
		name := text(raw["name"])
		providerID := text(raw["AHA_PROVIDER_ID"])
		wireModel := text(raw["OPENAI_MODEL"])
		if name == "" {
			name = wireModel
		}
		if providerID == "" {
			providerID = "aha_imported"
		}
		id := deterministicID("env", "codex", providerID, name)
		environment := map[string]string{}
		for key, value := range raw {
			if key == "name" || key == "OPENAI_API_KEY" {
				continue
			}
			if textValue := text(value); textValue != "" {
				environment[key] = textValue
			}
		}
		apiKey := text(raw["OPENAI_API_KEY"])
		if apiKey == "" {
			apiKey = providerCredentials[providerID]
		}
		credentialEnv := text(raw["CODEX_ENV_KEY"])
		if !safeCredentialEnvName(credentialEnv) {
			credentialEnv = "OPENAI_API_KEY"
			environment["CODEX_ENV_KEY"] = credentialEnv
		}
		secretRefs := map[string]string{}
		if apiKey != "" {
			ref := "env/" + id + "/" + credentialEnv
			secretRefs[credentialEnv] = ref
			result.Secrets[ref] = apiKey
		}
		group := domain.EnvGroup{
			ID: id, Name: name, ProviderID: providerID, Backend: "codex", Revision: 1,
			Environment: environment, SecretRefs: secretRefs, SecretNames: sortedKeys(secretRefs),
			SecretConfigured: apiKey != "", CreatedAt: now, UpdatedAt: now,
		}
		result.EnvGroups = append(result.EnvGroups, group)
		if wireModel != "" {
			envByProviderModel[providerID+"\x00"+wireModel] = id
		}
	}
	for _, raw := range config.ConfiguredModels {
		if backend := text(raw["backend"]); backend != "" && backend != "codex" {
			continue
		}
		providerID := text(raw["provider_id"])
		modelID := text(raw["model_id"])
		if modelID == "" {
			continue
		}
		wireModel := modelID
		defaultEnv := envByProviderModel[providerID+"\x00"+modelID]
		if defaultEnv != "" {
			for _, group := range result.EnvGroups {
				if group.ID == defaultEnv && group.Environment["OPENAI_MODEL"] != "" {
					wireModel = group.Environment["OPENAI_MODEL"]
				}
			}
		}
		result.Models = append(result.Models, domain.Model{
			ID: deterministicID("model", "codex", providerID, modelID), DisplayName: modelID,
			ProviderID: providerID, Backend: "codex", WireModel: wireModel, WireAPI: text(raw["wire_api"]),
			ContextWindow: number(raw["context_window"]), MaxOutputTokens: number(raw["max_output_tokens"]),
			DefaultEffort: "high", DefaultEnvGroupID: defaultEnv, CreatedAt: now, UpdatedAt: now,
		})
	}
	if len(result.Models) == 0 {
		for _, group := range result.EnvGroups {
			wireModel := group.Environment["OPENAI_MODEL"]
			if wireModel == "" {
				continue
			}
			result.Models = append(result.Models, domain.Model{
				ID: deterministicID("model", "codex", group.ProviderID, wireModel), DisplayName: wireModel,
				ProviderID: group.ProviderID, Backend: "codex", WireModel: wireModel,
				WireAPI: group.Environment["CODEX_WIRE_API"], DefaultEffort: "high",
				DefaultEnvGroupID: group.ID, CreatedAt: now, UpdatedAt: now,
			})
		}
	}
	sort.Slice(result.EnvGroups, func(i, j int) bool { return result.EnvGroups[i].Name < result.EnvGroups[j].Name })
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].DisplayName < result.Models[j].DisplayName })
	return result, nil
}

func deterministicID(prefix string, values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:8])
}

func text(value any) string {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case json.Number:
		return item.String()
	case float64:
		return fmt.Sprintf("%.0f", item)
	default:
		return ""
	}
}

func number(value any) int64 {
	switch item := value.(type) {
	case float64:
		return int64(item)
	case json.Number:
		value, _ := item.Int64()
		return value
	case string:
		var result int64
		_, _ = fmt.Sscan(item, &result)
		return result
	default:
		return 0
	}
}

func sortedKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func safeCredentialEnvName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		valid := character == '_' || character >= 'A' && character <= 'Z' || index > 0 && character >= '0' && character <= '9'
		if !valid {
			return false
		}
	}
	return strings.Contains(value, "KEY") || strings.Contains(value, "TOKEN")
}
