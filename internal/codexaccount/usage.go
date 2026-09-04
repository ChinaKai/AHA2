package codexaccount

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/proxyconfig"
)

const fallbackCodexClientVersion = "0.146.0"

type RefreshResult struct {
	Account  domain.CodexAccount `json:"account"`
	Warnings []string            `json:"warnings,omitempty"`
}

type accountCredentials struct {
	Raw          map[string]any
	AccessToken  string
	RefreshToken string
	IDToken      string
	AccountID    string
}

type accountHTTPError struct {
	status  int
	message string
}

func (e *accountHTTPError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("HTTP %d", e.status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.status, e.message)
}

func detectCodexClientVersion(binary string) string {
	if strings.TrimSpace(binary) == "" {
		binary = "codex"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return fallbackCodexClientVersion
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return fallbackCodexClientVersion
	}
	version := strings.TrimPrefix(fields[len(fields)-1], "v")
	if version == "" {
		return fallbackCodexClientVersion
	}
	return version
}

func (m *Manager) RefreshAccount(ctx context.Context, id string) (RefreshResult, error) {
	unlock := m.LockAccount(id)
	defer unlock()

	account, err := m.store.CodexAccount(ctx, id)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("Codex 账号不存在: %w", err)
	}
	authJSON, ok := m.secrets.Get(account.CredentialRef)
	if !ok || strings.TrimSpace(authJSON) == "" {
		return RefreshResult{}, fmt.Errorf("Codex 账号凭据未配置")
	}
	credentials, err := parseAccountCredentials([]byte(authJSON))
	if err != nil {
		return RefreshResult{}, err
	}
	client, err := m.clientForAccount(ctx, account)
	if err != nil {
		return RefreshResult{}, err
	}

	usage, planType, usageErr := m.fetchUsage(ctx, client, credentials)
	if statusCode(usageErr) == http.StatusUnauthorized && credentials.RefreshToken != "" {
		credentials, err = m.refreshCredentials(ctx, client, account, credentials)
		if err != nil {
			usageErr = fmt.Errorf("刷新 OAuth Token 失败: %w", err)
		} else {
			metadata, parseErr := parseAuth(mustJSON(credentials.Raw))
			if parseErr == nil {
				if metadata.Email != "" {
					account.Email = metadata.Email
				}
				if metadata.AccountID != "" {
					account.AccountID = metadata.AccountID
				}
				if metadata.PlanType != "" {
					account.PlanType = metadata.PlanType
				}
			}
			usage, planType, usageErr = m.fetchUsage(ctx, client, credentials)
		}
	}

	models, modelsErr := m.fetchModels(ctx, client, credentials)
	now := time.Now().UTC()
	warnings := make([]string, 0, 2)
	account.UsageUpdatedAt = now
	if usageErr != nil {
		account.UsageError = safeRemoteError(usageErr)
		warnings = append(warnings, "额度查询失败: "+account.UsageError)
	} else {
		account.Usage = &usage
		account.UsageError = ""
		if planType != "" {
			account.PlanType = planType
		}
	}
	account.ModelsUpdatedAt = now
	if modelsErr != nil {
		account.ModelsError = safeRemoteError(modelsErr)
		warnings = append(warnings, "模型目录查询失败: "+account.ModelsError)
	} else {
		account.AvailableModels = models
		account.ModelsError = ""
	}
	account.UpdatedAt = now
	if err := m.store.UpsertCodexAccount(ctx, account); err != nil {
		return RefreshResult{}, err
	}
	return RefreshResult{Account: account, Warnings: warnings}, nil
}

func (m *Manager) clientForAccount(ctx context.Context, account domain.CodexAccount) (*http.Client, error) {
	if !account.ProxyEnabled {
		return m.httpClient, nil
	}
	settings, err := m.store.ProxySettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取代理设置失败: %w", err)
	}
	client, err := proxyconfig.Client(m.httpClient, settings)
	if err != nil {
		return nil, fmt.Errorf("代理设置无效: %w", err)
	}
	return client, nil
}

func (m *Manager) fetchUsage(
	ctx context.Context,
	client *http.Client,
	credentials accountCredentials,
) (domain.CodexUsage, string, error) {
	body, err := m.accountGET(ctx, client, m.usageURL, credentials)
	if err != nil {
		return domain.CodexUsage{}, "", err
	}
	var payload usageResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return domain.CodexUsage{}, "", fmt.Errorf("解析额度响应失败")
	}
	limits := make([]domain.CodexRateLimit, 0, 2+len(payload.AdditionalRateLimits))
	if item, ok := normalizedRateLimit("codex", "Codex", payload.RateLimit); ok {
		limits = append(limits, item)
	}
	if item, ok := normalizedRateLimit("code_review", "Code Review", payload.CodeReviewRateLimit); ok {
		limits = append(limits, item)
	}
	for index, additional := range payload.AdditionalRateLimits {
		name := strings.TrimSpace(additional.LimitName)
		if name == "" {
			name = strings.TrimSpace(additional.NormalModelSlug)
		}
		if name == "" {
			name = fmt.Sprintf("Additional %d", index+1)
		}
		id := strings.TrimSpace(additional.MeteredFeature)
		if id == "" {
			id = strings.TrimSpace(additional.NormalModelSlug)
		}
		if id == "" {
			id = fmt.Sprintf("additional_%d", index+1)
		}
		if item, ok := normalizedRateLimit(id, name, additional.RateLimit); ok {
			limits = append(limits, item)
		}
	}
	return domain.CodexUsage{
		RateLimits: limits,
		Credits: domain.CodexCredits{
			HasCredits: payload.Credits.HasCredits, Unlimited: payload.Credits.Unlimited,
			OverageLimitReached: payload.Credits.OverageLimitReached,
			Balance:             valueString(payload.Credits.Balance),
		},
		ResetCreditsAvailable: payload.RateLimitResetCredits.AvailableCount,
	}, strings.TrimSpace(payload.PlanType), nil
}

func (m *Manager) fetchModels(
	ctx context.Context,
	client *http.Client,
	credentials accountCredentials,
) ([]domain.CodexModelOption, error) {
	endpoint, err := url.Parse(m.modelsURL)
	if err != nil {
		return nil, fmt.Errorf("模型目录地址无效")
	}
	query := endpoint.Query()
	query.Set("client_version", m.clientVersion)
	endpoint.RawQuery = query.Encode()
	body, err := m.accountGET(ctx, client, endpoint.String(), credentials)
	if err != nil {
		return nil, err
	}
	var payload modelsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("解析模型目录失败")
	}
	models := make([]domain.CodexModelOption, 0, len(payload.Models))
	for _, model := range payload.Models {
		if strings.TrimSpace(model.Slug) == "" || model.Visibility != "list" {
			continue
		}
		efforts := make([]string, 0, len(model.SupportedReasoningLevels))
		for _, effort := range model.SupportedReasoningLevels {
			if value := strings.TrimSpace(effort.Effort); value != "" {
				efforts = append(efforts, value)
			}
		}
		models = append(models, domain.CodexModelOption{
			WireModel: model.Slug, DisplayName: model.DisplayName, Description: model.Description,
			ContextWindow: model.ContextWindow, MaxContextWindow: model.MaxContextWindow,
			DefaultEffort: model.DefaultReasoningLevel, ReasoningEfforts: efforts,
		})
	}
	sort.SliceStable(models, func(i, j int) bool {
		return models[i].DisplayName < models[j].DisplayName
	})
	if len(models) == 0 {
		return nil, fmt.Errorf("官方模型目录为空")
	}
	return models, nil
}

func (m *Manager) accountGET(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	credentials accountCredentials,
) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "codex-cli/"+m.clientVersion)
	accountID := strings.TrimSpace(credentials.AccountID)
	if accountID != "" {
		request.Header.Set("ChatGPT-Account-Id", accountID)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		return nil, fmt.Errorf("读取响应失败: %w", readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &accountHTTPError{status: response.StatusCode, message: remoteErrorMessage(body)}
	}
	return body, nil
}

func (m *Manager) refreshCredentials(
	ctx context.Context,
	client *http.Client,
	account domain.CodexAccount,
	credentials accountCredentials,
) (accountCredentials, error) {
	payload := map[string]string{
		"client_id": oauthClientID, "grant_type": "refresh_token", "refresh_token": credentials.RefreshToken,
	}
	body, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.tokenURL, bytes.NewReader(body))
	if err != nil {
		return accountCredentials{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "codex-cli/"+m.clientVersion)
	response, err := client.Do(request)
	if err != nil {
		return accountCredentials{}, fmt.Errorf("请求失败: %w", err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return accountCredentials{}, readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return accountCredentials{}, &accountHTTPError{status: response.StatusCode, message: remoteErrorMessage(responseBody)}
	}
	var refreshed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(responseBody, &refreshed); err != nil || strings.TrimSpace(refreshed.AccessToken) == "" {
		return accountCredentials{}, fmt.Errorf("OAuth Token 刷新响应无效")
	}
	tokens, _ := credentials.Raw["tokens"].(map[string]any)
	if tokens == nil {
		tokens = map[string]any{}
		credentials.Raw["tokens"] = tokens
	}
	tokens["access_token"] = refreshed.AccessToken
	if refreshed.RefreshToken != "" {
		tokens["refresh_token"] = refreshed.RefreshToken
	}
	if refreshed.IDToken != "" {
		tokens["id_token"] = refreshed.IDToken
	}
	claims := jwtClaims(refreshed.IDToken)
	if len(claims) == 0 {
		claims = jwtClaims(refreshed.AccessToken)
	}
	authClaims, _ := claims["https://api.openai.com/auth"].(map[string]any)
	if accountID := stringClaim(authClaims, "chatgpt_account_id", "account_id"); accountID != "" {
		tokens["account_id"] = accountID
	}
	credentials.Raw["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	normalized := mustJSON(credentials.Raw)
	if err := m.secrets.PutMany(map[string]string{account.CredentialRef: string(normalized)}); err != nil {
		return accountCredentials{}, fmt.Errorf("保存刷新后的 Token 失败: %w", err)
	}
	return parseAccountCredentials(normalized)
}

func parseAccountCredentials(data []byte) (accountCredentials, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return accountCredentials{}, fmt.Errorf("解析 Codex 凭据失败")
	}
	tokens, _ := raw["tokens"].(map[string]any)
	credentials := accountCredentials{
		Raw:          raw,
		AccessToken:  stringClaim(tokens, "access_token"),
		RefreshToken: stringClaim(tokens, "refresh_token"),
		IDToken:      stringClaim(tokens, "id_token"),
		AccountID:    stringClaim(tokens, "account_id"),
	}
	if credentials.AccessToken == "" {
		return accountCredentials{}, fmt.Errorf("Codex 凭据缺少 Access Token")
	}
	if credentials.AccountID == "" {
		claims := jwtClaims(credentials.AccessToken)
		authClaims, _ := claims["https://api.openai.com/auth"].(map[string]any)
		credentials.AccountID = stringClaim(authClaims, "chatgpt_account_id", "account_id")
	}
	return credentials, nil
}

func statusCode(err error) int {
	if item, ok := err.(*accountHTTPError); ok {
		return item.status
	}
	return 0
}

func safeRemoteError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 300 {
		message = message[:300]
	}
	return message
}

func remoteErrorMessage(body []byte) string {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, source := range []map[string]any{payload, nestedMap(payload, "error"), nestedMap(payload, "detail")} {
		for _, key := range []string{"code", "type", "message"} {
			if value := stringClaim(source, key); value != "" && !containsString(parts, value) {
				parts = append(parts, value)
			}
		}
	}
	return safeRemoteError(fmt.Errorf("%s", strings.Join(parts, ": ")))
}

func nestedMap(values map[string]any, key string) map[string]any {
	result, _ := values[key].(map[string]any)
	return result
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func normalizedRateLimit(id, name string, raw *rawRateLimit) (domain.CodexRateLimit, bool) {
	if raw == nil || raw.PrimaryWindow == nil && raw.SecondaryWindow == nil {
		return domain.CodexRateLimit{}, false
	}
	return domain.CodexRateLimit{
		ID: id, Name: name, Allowed: raw.Allowed, LimitReached: raw.LimitReached,
		PrimaryWindow:   normalizedWindow(raw.PrimaryWindow),
		SecondaryWindow: normalizedWindow(raw.SecondaryWindow),
	}, true
}

func normalizedWindow(raw *rawUsageWindow) *domain.CodexUsageWindow {
	if raw == nil {
		return nil
	}
	used := raw.UsedPercent
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	resetAt := raw.ResetAt
	if resetAt == 0 && raw.ResetAfterSeconds > 0 {
		resetAt = time.Now().Unix() + raw.ResetAfterSeconds
	}
	return &domain.CodexUsageWindow{
		UsedPercent: used, LimitWindowSeconds: raw.LimitWindowSeconds, ResetAt: resetAt,
	}
}

func valueString(value any) string {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case float64:
		return strconv.FormatFloat(item, 'f', -1, 64)
	case json.Number:
		return item.String()
	default:
		return ""
	}
}

type rawUsageWindow struct {
	UsedPercent        int   `json:"used_percent"`
	LimitWindowSeconds int64 `json:"limit_window_seconds"`
	ResetAfterSeconds  int64 `json:"reset_after_seconds"`
	ResetAt            int64 `json:"reset_at"`
}

type rawRateLimit struct {
	Allowed         bool            `json:"allowed"`
	LimitReached    bool            `json:"limit_reached"`
	PrimaryWindow   *rawUsageWindow `json:"primary_window"`
	SecondaryWindow *rawUsageWindow `json:"secondary_window"`
}

type usageResponse struct {
	PlanType             string        `json:"plan_type"`
	RateLimit            *rawRateLimit `json:"rate_limit"`
	CodeReviewRateLimit  *rawRateLimit `json:"code_review_rate_limit"`
	AdditionalRateLimits []struct {
		LimitName       string        `json:"limit_name"`
		MeteredFeature  string        `json:"metered_feature"`
		NormalModelSlug string        `json:"normal_model_slug"`
		RateLimit       *rawRateLimit `json:"rate_limit"`
	} `json:"additional_rate_limits"`
	Credits struct {
		HasCredits          bool `json:"has_credits"`
		Unlimited           bool `json:"unlimited"`
		OverageLimitReached bool `json:"overage_limit_reached"`
		Balance             any  `json:"balance"`
	} `json:"credits"`
	RateLimitResetCredits struct {
		AvailableCount int `json:"available_count"`
	} `json:"rate_limit_reset_credits"`
}

type modelsResponse struct {
	Models []struct {
		Slug                     string `json:"slug"`
		DisplayName              string `json:"display_name"`
		Description              string `json:"description"`
		Visibility               string `json:"visibility"`
		ContextWindow            int64  `json:"context_window"`
		MaxContextWindow         int64  `json:"max_context_window"`
		DefaultReasoningLevel    string `json:"default_reasoning_level"`
		SupportedReasoningLevels []struct {
			Effort string `json:"effort"`
		} `json:"supported_reasoning_levels"`
	} `json:"models"`
}
