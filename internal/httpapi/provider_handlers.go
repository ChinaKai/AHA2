package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/gateway"
)

// SecretStore persists provider credentials out-of-band from the browser.
type SecretStore interface {
	PutMany(map[string]string) error
	DeleteMany([]string) error
	Get(string) (string, bool)
}

func (s *Server) listProviders(writer http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListProviders(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list_providers_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "providers": items})
}

func (s *Server) createProvider(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Name             string `json:"name"`
		BaseURL          string `json:"base_url"`
		AnthropicBaseURL string `json:"anthropic_base_url"`
		APIKey           string `json:"api_key"`
		AuthStyle        string `json:"auth_style"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	payload.Name = strings.TrimSpace(payload.Name)
	payload.BaseURL = strings.TrimRight(strings.TrimSpace(payload.BaseURL), "/")
	payload.AnthropicBaseURL = strings.TrimRight(strings.TrimSpace(payload.AnthropicBaseURL), "/")
	if payload.Name == "" || payload.BaseURL == "" {
		writeError(writer, http.StatusBadRequest, "provider_name_and_base_url_required")
		return
	}
	authStyle := strings.TrimSpace(payload.AuthStyle)
	if authStyle == "" {
		authStyle = "auto"
	}
	id := providerID(payload.Name, payload.BaseURL)
	now := time.Now().UTC()
	item := domain.Provider{
		ID: id, Name: payload.Name, BaseURL: payload.BaseURL, AnthropicBaseURL: payload.AnthropicBaseURL, AuthStyle: authStyle,
		CreatedAt: now, UpdatedAt: now,
	}
	if payload.APIKey != "" {
		ref := "provider/" + id + "/credential"
		if s.secrets != nil {
			if err := s.secrets.PutMany(map[string]string{ref: payload.APIKey}); err != nil {
				writeError(writer, http.StatusInternalServerError, "store_secrets_failed")
				return
			}
		}
		item.CredentialRef = ref
		item.CredentialConfigured = true
	} else if existing, err := s.store.Provider(request.Context(), id); err == nil {
		item.CredentialRef = existing.CredentialRef
		item.CredentialConfigured = existing.CredentialConfigured
	}
	if err := s.store.UpsertProvider(request.Context(), item); err != nil {
		writeError(writer, http.StatusInternalServerError, "create_provider_failed")
		return
	}
	s.audit(request, "provider.create", "provider", item.ID, map[string]any{"name": item.Name})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "provider": item})
}

func (s *Server) updateProvider(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	existing, err := s.store.Provider(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "provider_not_found")
		return
	}
	var payload struct {
		Name             string `json:"name"`
		BaseURL          string `json:"base_url"`
		AnthropicBaseURL string `json:"anthropic_base_url"`
		AuthStyle        string `json:"auth_style"`
		APIKey           string `json:"api_key"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	name := strings.TrimSpace(payload.Name)
	baseURL := strings.TrimRight(strings.TrimSpace(payload.BaseURL), "/")
	if name == "" || baseURL == "" {
		writeError(writer, http.StatusBadRequest, "provider_name_and_base_url_required")
		return
	}
	authStyle := strings.TrimSpace(payload.AuthStyle)
	if authStyle == "" {
		authStyle = "auto"
	}
	newID := providerID(name, baseURL)
	now := time.Now().UTC()
	item := domain.Provider{
		ID: newID, Name: name, BaseURL: baseURL,
		AnthropicBaseURL: strings.TrimRight(strings.TrimSpace(payload.AnthropicBaseURL), "/"),
		AuthStyle:        authStyle, CreatedAt: existing.CreatedAt, UpdatedAt: now,
	}
	oldCredentialRef := "provider/" + id + "/credential"
	if strings.TrimSpace(payload.APIKey) != "" {
		ref := "provider/" + newID + "/credential"
		if s.secrets != nil {
			if err := s.secrets.PutMany(map[string]string{ref: payload.APIKey}); err != nil {
				writeError(writer, http.StatusInternalServerError, "store_secrets_failed")
				return
			}
			if newID != id {
				_ = s.secrets.DeleteMany([]string{oldCredentialRef})
			}
		}
		item.CredentialRef = ref
		item.CredentialConfigured = true
	} else if newID == id {
		item.CredentialRef = existing.CredentialRef
		item.CredentialConfigured = existing.CredentialConfigured
	} else if s.secrets != nil {
		if value, ok := s.secrets.Get(oldCredentialRef); ok {
			ref := "provider/" + newID + "/credential"
			if err := s.secrets.PutMany(map[string]string{ref: value}); err == nil {
				_ = s.secrets.DeleteMany([]string{oldCredentialRef})
				item.CredentialRef = ref
				item.CredentialConfigured = true
			}
		}
	}
	if err := s.store.UpsertProvider(request.Context(), item); err != nil {
		writeError(writer, http.StatusInternalServerError, "update_provider_failed")
		return
	}
	if newID != id {
		if err := s.store.RemapProvider(request.Context(), id, newID); err != nil {
			writeError(writer, http.StatusInternalServerError, "update_provider_failed")
			return
		}
		_ = s.store.DeleteProvider(request.Context(), id)
	}
	s.audit(request, "provider.update", "provider", newID, map[string]any{"name": item.Name})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "provider": item})
}

func (s *Server) deleteProvider(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if _, err := s.store.Provider(request.Context(), id); err != nil {
		writeError(writer, http.StatusNotFound, "provider_not_found")
		return
	}
	models, _ := s.store.ListModels(request.Context())
	envGroups, _ := s.store.ListEnvGroups(request.Context())
	var secretKeys []string
	skippedModels := 0
	credentialReferenced := false
	for _, model := range models {
		if model.ProviderID != id {
			continue
		}
		inUse, err := s.store.ModelInUse(request.Context(), model.ID)
		if err == nil && inUse {
			skippedModels++
			credentialReferenced = true
			continue
		}
		_ = s.store.DeleteModel(request.Context(), model.ID)
	}
	for _, group := range envGroups {
		if group.ProviderID != id {
			continue
		}
		inUse, err := s.store.EnvGroupInUse(request.Context(), group.ID)
		if err == nil && inUse {
			credentialReferenced = true
			continue
		}
		for _, ref := range group.SecretRefs {
			secretKeys = append(secretKeys, ref)
		}
		_ = s.store.DeleteEnvGroup(request.Context(), group.ID)
	}
	provider, err := s.store.Provider(request.Context(), id)
	if err == nil && provider.CredentialRef != "" && !credentialReferenced {
		secretKeys = append(secretKeys, provider.CredentialRef)
	}
	if s.secrets != nil {
		_ = s.secrets.DeleteMany(secretKeys)
	}
	if err := s.store.DeleteProvider(request.Context(), id); err != nil {
		writeError(writer, http.StatusInternalServerError, "delete_provider_failed")
		return
	}
	s.audit(request, "provider.delete", "provider", id, map[string]any{"skipped_models": skippedModels})
	message := ""
	if skippedModels > 0 {
		message = "该 Provider 已删除；" + fmt.Sprintf("%d", skippedModels) + " 个被任务占用的模型已保留，其凭据仍可用。"
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "warning": message, "skipped_models": skippedModels})
}

func (s *Server) deleteModel(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	model, err := s.store.Model(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "model_not_found")
		return
	}
	inUse, inUseErr := s.store.ModelInUse(request.Context(), id)
	if inUseErr == nil && inUse {
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "model_in_use", "message": "该模型已被任务使用，无法删除",
		})
		return
	}
	var envGroupsToDelete []string
	if model.DefaultEnvGroupID != "" {
		models, _ := s.store.ListModels(request.Context())
		referenced := false
		for _, other := range models {
			if other.ID != id && other.DefaultEnvGroupID == model.DefaultEnvGroupID {
				referenced = true
				break
			}
		}
		if !referenced {
			envGroupsToDelete = append(envGroupsToDelete, model.DefaultEnvGroupID)
		}
	}
	if err := s.store.DeleteModel(request.Context(), id); err != nil {
		writeError(writer, http.StatusInternalServerError, "delete_model_failed")
		return
	}
	var secretKeys []string
	for _, envID := range envGroupsToDelete {
		envInUse, err := s.store.EnvGroupInUse(request.Context(), envID)
		if err == nil && envInUse {
			continue
		}
		if group, err := s.store.EnvGroup(request.Context(), envID); err == nil {
			for _, ref := range group.SecretRefs {
				// Only per-env secrets belong to this model; the provider
				// credential is shared and must survive a single model delete.
				if strings.HasPrefix(ref, "env/") {
					secretKeys = append(secretKeys, ref)
				}
			}
		}
		_ = s.store.DeleteEnvGroup(request.Context(), envID)
	}
	if s.secrets != nil {
		_ = s.secrets.DeleteMany(secretKeys)
	}
	s.audit(request, "model.delete", "model", id, nil)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) detectModelsHandler(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ProviderID string `json:"provider_id"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	provider, err := s.store.Provider(request.Context(), strings.TrimSpace(payload.ProviderID))
	if err != nil {
		writeError(writer, http.StatusNotFound, "provider_not_found")
		return
	}
	apiKey := ""
	if provider.CredentialRef != "" && s.secrets != nil {
		apiKey, _ = s.secrets.Get(provider.CredentialRef)
	}
	result, err := gateway.DetectModels(provider.BaseURL, apiKey, provider.AuthStyle, 15*time.Second)
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{"ok": false, "error": "detect_models_failed", "message": err.Error()})
		return
	}
	if len(result.Models) == 0 {
		writeJSON(writer, http.StatusBadGateway, map[string]any{"ok": false, "error": "no_models_found"})
		return
	}
	anthropicBase := probeCapabilities(provider, apiKey, result.AuthStyle, result.Models)
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "provider_id": provider.ID, "auth_style": result.AuthStyle, "models": result.Models,
		"anthropic_base_url": anthropicBase,
	})
}

func probeCapabilities(provider domain.Provider, apiKey, authStyle string, models []gateway.DetectedModel) string {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	var mu sync.Mutex
	anthropicBase := ""
	for index := range models {
		modelID := models[index].ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			caps, base := gateway.ProbeModelCapabilities(provider.BaseURL, apiKey, authStyle, modelID, 8*time.Second)
			models[index].Capabilities = caps
			if base != "" {
				mu.Lock()
				if anthropicBase == "" {
					anthropicBase = base
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return anthropicBase
}

func (s *Server) addModelsHandler(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		ProviderID       string   `json:"provider_id"`
		WireAPI          string   `json:"wire_api"`
		AnthropicBaseURL string   `json:"anthropic_base_url"`
		ModelIDs         []string `json:"model_ids"`
		Models           []struct {
			ID              string   `json:"id"`
			WireAPI         string   `json:"wire_api"`
			WireAPIs        []string `json:"wire_apis"`
			MaxInputTokens  int64    `json:"max_input_tokens"`
			MaxOutputTokens int64    `json:"max_output_tokens"`
		} `json:"models"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	provider, err := s.store.Provider(request.Context(), strings.TrimSpace(payload.ProviderID))
	if err != nil {
		writeError(writer, http.StatusNotFound, "provider_not_found")
		return
	}
	if anthropic := strings.TrimRight(strings.TrimSpace(payload.AnthropicBaseURL), "/"); anthropic != "" && provider.AnthropicBaseURL != anthropic {
		provider.AnthropicBaseURL = anthropic
		provider.UpdatedAt = time.Now().UTC()
		_ = s.store.UpsertProvider(request.Context(), provider)
	}
	type pick struct {
		id              string
		wireAPI         string
		maxInputTokens  int64
		maxOutputTokens int64
	}
	var picks []pick
	seenPick := make(map[string]bool)
	for _, item := range payload.Models {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		wapis := item.WireAPIs
		if len(wapis) == 0 && strings.TrimSpace(item.WireAPI) != "" {
			wapis = []string{item.WireAPI}
		}
		if len(wapis) == 0 {
			wapis = []string{"responses"}
		}
		for _, wire := range wapis {
			wire = normalizeWireAPI(wire)
			key := id + "\x00" + wire
			if seenPick[key] {
				continue
			}
			seenPick[key] = true
			picks = append(picks, pick{id, wire, item.MaxInputTokens, item.MaxOutputTokens})
		}
	}
	if len(picks) == 0 {
		wire := normalizeWireAPI(payload.WireAPI)
		for _, id := range payload.ModelIDs {
			if id = strings.TrimSpace(id); id != "" {
				picks = append(picks, pick{id: id, wireAPI: wire})
			}
		}
	}
	if len(picks) == 0 {
		writeError(writer, http.StatusBadRequest, "models_required")
		return
	}
	existing, _ := s.store.ListModels(request.Context())
	existingKey := make(map[string]bool, len(existing))
	for _, item := range existing {
		if item.ProviderID == provider.ID && item.WireModel != "" {
			existingKey[provider.ID+"\x00"+item.WireModel+"\x00"+item.Backend] = true
		}
	}
	now := time.Now().UTC()
	var created []domain.Model
	skipped := 0
	for _, pick := range picks {
		backend := backendForWire(pick.wireAPI)
		key := provider.ID + "\x00" + pick.id + "\x00" + backend
		if existingKey[key] {
			skipped++
			continue
		}
		backendName, envGroup := buildEnvGroup(provider, pick.id, pick.wireAPI, now)
		if err := s.store.UpsertEnvGroup(request.Context(), envGroup); err != nil {
			writeError(writer, http.StatusInternalServerError, "create_env_group_failed")
			return
		}
		item := domain.Model{
			ID: domain.NewID("model"), DisplayName: pick.id, ProviderID: provider.ID, Source: "provider", Backend: backendName,
			WireModel: pick.id, WireAPI: pick.wireAPI, DefaultEnvGroupID: envGroup.ID,
			ContextWindow: pick.maxInputTokens, MaxOutputTokens: pick.maxOutputTokens,
			Capabilities: map[string]any{}, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.store.UpsertModel(request.Context(), item); err != nil {
			writeError(writer, http.StatusInternalServerError, "create_model_failed")
			return
		}
		existingKey[key] = true
		created = append(created, item)
	}
	s.audit(request, "provider.add_models", "model", "", map[string]any{"provider_id": provider.ID, "models": len(created), "skipped": skipped})
	writeJSON(writer, http.StatusCreated, map[string]any{
		"ok": true, "provider_id": provider.ID, "models": created, "skipped": skipped,
	})
}

func backendForWire(wireAPI string) string {
	if wireAPI == "anthropic_messages" {
		return "claude"
	}
	return "codex"
}

func (s *Server) updateModel(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	model, err := s.store.Model(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "model_not_found")
		return
	}
	var payload struct {
		DisplayName     string `json:"display_name"`
		WireAPI         string `json:"wire_api"`
		ContextWindow   int64  `json:"context_window"`
		MaxOutputTokens int64  `json:"max_output_tokens"`
		DefaultEffort   string `json:"default_reasoning_effort"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	now := time.Now().UTC()
	if strings.TrimSpace(payload.DisplayName) != "" {
		model.DisplayName = strings.TrimSpace(payload.DisplayName)
	}
	if strings.TrimSpace(payload.DefaultEffort) != "" {
		model.DefaultEffort = strings.TrimSpace(payload.DefaultEffort)
	}
	model.ContextWindow = payload.ContextWindow
	model.MaxOutputTokens = payload.MaxOutputTokens
	if wire := strings.TrimSpace(payload.WireAPI); wire != "" {
		wire = normalizeWireAPI(wire)
		if wire != model.WireAPI {
			newBackend := "codex"
			if wire == "anthropic_messages" {
				newBackend = "claude"
			}
			provider, err := s.store.Provider(request.Context(), model.ProviderID)
			if err != nil {
				writeJSON(writer, http.StatusBadRequest, map[string]any{
					"ok": false, "error": "provider_not_found", "message": "无法切换协议：该模型的 Provider 已不存在",
				})
				return
			}
			_, envGroup := buildEnvGroup(provider, model.WireModel, wire, now)
			if err := s.store.UpsertEnvGroup(request.Context(), envGroup); err != nil {
				writeError(writer, http.StatusInternalServerError, "create_env_group_failed")
				return
			}
			if model.DefaultEnvGroupID != "" && model.DefaultEnvGroupID != envGroup.ID {
				models, _ := s.store.ListModels(request.Context())
				referenced := false
				for _, other := range models {
					if other.ID != model.ID && other.DefaultEnvGroupID == model.DefaultEnvGroupID {
						referenced = true
						break
					}
				}
				if !referenced {
					if inUse, _ := s.store.EnvGroupInUse(request.Context(), model.DefaultEnvGroupID); !inUse {
						_ = s.store.DeleteEnvGroup(request.Context(), model.DefaultEnvGroupID)
					}
				}
			}
			model.DefaultEnvGroupID = envGroup.ID
			model.Backend = newBackend
			model.WireAPI = wire
		}
	}
	model.UpdatedAt = now
	if err := s.store.UpsertModel(request.Context(), model); err != nil {
		writeError(writer, http.StatusInternalServerError, "update_model_failed")
		return
	}
	s.audit(request, "model.update", "model", id, map[string]any{"backend": model.Backend})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "model": model})
}

func normalizeWireAPI(value string) string {
	wire := strings.ToLower(strings.TrimSpace(value))
	if wire == "" {
		return "responses"
	}
	switch wire {
	case "responses", "chat", "chat_completions", "anthropic_messages":
		if wire == "chat_completions" {
			return "chat"
		}
		return wire
	default:
		return "responses"
	}
}

func buildEnvGroup(provider domain.Provider, modelID, wireAPI string, now time.Time) (string, domain.EnvGroup) {
	secretRefs := map[string]string{}
	if provider.CredentialRef != "" {
		secretRefs["OPENAI_API_KEY"] = provider.CredentialRef
	}
	backendName := "codex"
	environment := map[string]string{
		"OPENAI_BASE_URL": provider.BaseURL,
		"OPENAI_MODEL":    modelID,
		"CODEX_WIRE_API":  wireAPI,
		"CODEX_ENV_KEY":   "OPENAI_API_KEY",
		"AHA_PROVIDER_ID": provider.ID,
	}
	if wireAPI == "anthropic_messages" {
		backendName = "claude"
		anthropicBase := strings.TrimSpace(provider.AnthropicBaseURL)
		if anthropicBase == "" {
			anthropicBase = provider.BaseURL
		}
		credentialEnv := "ANTHROPIC_API_KEY"
		if provider.AuthStyle == "bearer" {
			credentialEnv = "ANTHROPIC_AUTH_TOKEN"
		}
		secretRefs = map[string]string{}
		if provider.CredentialRef != "" {
			secretRefs[credentialEnv] = provider.CredentialRef
		}
		environment = map[string]string{
			"ANTHROPIC_BASE_URL": anthropicBase,
			"ANTHROPIC_MODEL":    modelID,
		}
	}
	envID := domain.NewID("env")
	group := domain.EnvGroup{
		ID: envID, Name: provider.Name + " / " + modelID, ProviderID: provider.ID, Backend: backendName,
		Revision: 1, Environment: environment, SecretRefs: secretRefs,
		SecretConfigured: provider.CredentialConfigured, CreatedAt: now, UpdatedAt: now,
	}
	return backendName, group
}

func providerID(name, baseURL string) string {
	sum := sha256.Sum256([]byte(name + "\x00" + baseURL))
	digest := hex.EncodeToString(sum[:])[:12]
	slug := regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(name), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 36 {
		slug = slug[:36]
	}
	if slug == "" {
		slug = "provider"
	}
	return slug + "-" + digest
}
