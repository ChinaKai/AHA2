package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DetectedModel is a model advertised by an OpenAI/Anthropic-compatible
// gateway's /models endpoint.
type DetectedModel struct {
	ID              string            `json:"id"`
	MaxInputTokens  int64             `json:"max_input_tokens,omitempty"`
	MaxOutputTokens int64             `json:"max_output_tokens,omitempty"`
	Capabilities    map[string]string `json:"capabilities,omitempty"`
}

type Result struct {
	AuthStyle string
	Models    []DetectedModel
}

// DetectModels queries a gateway's /v1/models (OpenAI-style) or /models
// (Anthropic-style) endpoint. It tries several URL/auth combinations because
// gateways vary: some want Authorization: Bearer, others want x-api-key.
// Returns the first non-empty model list plus the auth style that succeeded.
func DetectModels(baseURL, apiKey, authStyle string, timeout time.Duration) (Result, error) {
	return DetectModelsContext(context.Background(), baseURL, apiKey, authStyle, timeout)
}

// DetectModelsContext is DetectModels with cancellation propagated to every
// gateway catalog request.
func DetectModelsContext(ctx context.Context, baseURL, apiKey, authStyle string, timeout time.Duration) (Result, error) {
	return DetectModelsContextWithClient(ctx, baseURL, apiKey, authStyle, timeout, nil)
}

// DetectModelsContextWithClient uses the supplied client while preserving the
// legacy direct-client behavior when client is nil.
func DetectModelsContextWithClient(ctx context.Context, baseURL, apiKey, authStyle string, timeout time.Duration, client *http.Client) (Result, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return Result{}, fmt.Errorf("base_url is required")
	}
	key := strings.TrimSpace(apiKey)
	var candidates []string
	if strings.HasSuffix(base, "/v1") {
		candidates = append(candidates, base+"/models")
	} else {
		candidates = append(candidates, base+"/v1/models", base+"/models")
	}
	normalized := strings.ToLower(strings.TrimSpace(authStyle))
	if normalized == "" {
		normalized = "auto"
	}
	type authSet struct {
		style   string
		headers map[string]string
	}
	var authSets []authSet
	switch normalized {
	case "bearer":
		if key != "" {
			authSets = append(authSets, authSet{"bearer", map[string]string{"Authorization": "Bearer " + key}})
		}
	case "x-api-key":
		if key != "" {
			authSets = append(authSets, authSet{"x-api-key", map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}})
		}
	case "none":
		authSets = append(authSets, authSet{"none", map[string]string{}})
	default:
		if key != "" {
			authSets = append(authSets,
				authSet{"bearer", map[string]string{"Authorization": "Bearer " + key}},
				authSet{"x-api-key", map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}},
			)
		}
		authSets = append(authSets, authSet{"none", map[string]string{}})
	}
	if len(authSets) == 0 {
		return Result{}, fmt.Errorf("provider credential is not configured")
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	var lastError string
	for _, endpoint := range candidates {
		for _, set := range authSets {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			models, err := fetchModels(ctx, client, endpoint, set.headers)
			if err != nil {
				lastError = endpoint + ": " + err.Error()
				continue
			}
			if len(models) > 0 {
				return Result{AuthStyle: set.style, Models: models}, nil
			}
		}
	}
	if lastError != "" {
		return Result{}, fmt.Errorf("failed to detect models: %s", lastError)
	}
	return Result{AuthStyle: "none", Models: []DetectedModel{}}, nil
}

func fetchModels(ctx context.Context, client *http.Client, endpoint string, headers map[string]string) ([]DetectedModel, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return extractModels(payload), nil
}

func extractModels(payload map[string]any) []DetectedModel {
	data, _ := payload["data"].([]any)
	if len(data) == 0 {
		models, _ := payload["models"].([]any)
		data = models
	}
	var result []DetectedModel
	seen := make(map[string]bool)
	for _, raw := range data {
		var id string
		var maxInput, maxOutput int64
		switch item := raw.(type) {
		case map[string]any:
			id, _ = item["id"].(string)
			if id == "" {
				id, _ = item["name"].(string)
			}
			maxInput = int64Value(item["max_input_tokens"])
			maxOutput = int64Value(item["max_output_tokens"])
		case string:
			id = item
		}
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		entry := DetectedModel{ID: id}
		if maxInput > 0 {
			entry.MaxInputTokens = maxInput
		}
		if maxOutput > 0 {
			entry.MaxOutputTokens = maxOutput
		}
		result = append(result, entry)
	}
	return result
}

func int64Value(value any) int64 {
	switch item := value.(type) {
	case float64:
		return int64(item)
	case int:
		return int64(item)
	case int64:
		return item
	case json.Number:
		result, _ := item.Int64()
		return result
	default:
		return 0
	}
}

// ProbeModelCapabilities sends a minimal request for each supported wire
// protocol and reports which ones the gateway accepts for the given model.
// Status values mirror the legacy AHA probe: supported / unsupported /
// unauthorized / rate_limited / unavailable / inconclusive.
//
// For anthropic_messages it also tries the gateway's /anthropic suffix (some
// gateways like DeepSeek/MiniMax/Kimi serve the Anthropic protocol there) and
// returns that base URL when it is the one that works.
func ProbeModelCapabilities(baseURL, apiKey, authStyle, modelID string, timeout time.Duration) (map[string]string, string) {
	return ProbeModelCapabilitiesContext(context.Background(), baseURL, apiKey, authStyle, modelID, timeout)
}

// ProbeModelCapabilitiesContext is ProbeModelCapabilities with cancellation
// propagated to every protocol probe request.
func ProbeModelCapabilitiesContext(ctx context.Context, baseURL, apiKey, authStyle, modelID string, timeout time.Duration) (map[string]string, string) {
	return ProbeModelCapabilitiesContextWithClient(ctx, baseURL, apiKey, authStyle, modelID, timeout, nil)
}

// ProbeModelCapabilitiesContextWithClient uses the supplied client while
// preserving the legacy direct-client behavior when client is nil.
func ProbeModelCapabilitiesContextWithClient(ctx context.Context, baseURL, apiKey, authStyle, modelID string, timeout time.Duration, client *http.Client) (map[string]string, string) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	key := strings.TrimSpace(apiKey)
	if base == "" || modelID == "" || key == "" {
		return map[string]string{}, ""
	}
	normalizedAuth := strings.ToLower(strings.TrimSpace(authStyle))
	if normalizedAuth == "" {
		normalizedAuth = "auto"
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	capabilities := make(map[string]string)
	for _, wireAPI := range []string{"responses", "chat_completions"} {
		if ctx.Err() != nil {
			return capabilities, ""
		}
		status, _ := probeWireAPI(ctx, client, base, key, normalizedAuth, modelID, wireAPI)
		capabilities[wireAPI] = status
	}
	anthropicBase := ""
	candidates := anthropicBaseCandidates(base)
	if len(candidates) == 0 {
		capabilities["anthropic_messages"] = "unavailable"
		return capabilities, ""
	}
	status, _ := probeWireAPI(ctx, client, candidates[0], key, normalizedAuth, modelID, "anthropic_messages")
	capabilities["anthropic_messages"] = status
	if status == "unsupported" && len(candidates) > 1 {
		for _, candidate := range candidates[1:] {
			if ctx.Err() != nil {
				return capabilities, ""
			}
			status, _ = probeWireAPI(ctx, client, candidate, key, normalizedAuth, modelID, "anthropic_messages")
			if status == "supported" {
				capabilities["anthropic_messages"] = "supported"
				anthropicBase = candidate
				break
			}
		}
	}
	return capabilities, anthropicBase
}

// anthropicBaseCandidates returns the provider base first, then the /anthropic
// variant when it differs (the suffix some gateways use for Anthropic Messages).
func anthropicBaseCandidates(baseURL string) []string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil
	}
	if strings.HasSuffix(base, "/anthropic") {
		return []string{base}
	}
	root := base
	if strings.HasSuffix(base, "/v1") {
		root = base[:len(base)-3]
	}
	candidates := []string{base}
	anthropic := root + "/anthropic"
	if anthropic != base {
		candidates = append(candidates, anthropic)
	}
	return candidates
}

// probeWireAPI probes a wire protocol across endpoint forms (a bare path for
// bases that already carry an API prefix such as GLM's /api/paas/v4, plus a
// /v1-prefixed path for bare-host bases). Auth is narrowed to the confirmed
// style when known so rate-limited gateways are not flooded with redundant
// requests. A definitive result (supported or rate_limited) stops the probe.
func probeWireAPI(ctx context.Context, client *http.Client, base, apiKey, authStyle, modelID, wireAPI string) (string, int) {
	rel := ""
	var body []byte
	switch wireAPI {
	case "responses":
		rel = "responses"
		body, _ = json.Marshal(map[string]any{"model": modelID, "input": "ping", "max_output_tokens": 1})
	case "chat_completions":
		rel = "chat/completions"
		body, _ = json.Marshal(map[string]any{
			"model": modelID, "messages": []any{map[string]any{"role": "user", "content": "ping"}}, "max_tokens": 1,
		})
	case "anthropic_messages":
		rel = "messages"
		body, _ = json.Marshal(map[string]any{
			"model": modelID, "max_tokens": 1, "messages": []any{map[string]any{"role": "user", "content": "ping"}},
		})
	}
	if rel == "" {
		return "unsupported", 0
	}
	endpoints := []string{base + "/" + rel}
	if !strings.HasSuffix(base, "/v1") && !strings.HasSuffix(base, "/v1/") {
		endpoints = append(endpoints, base+"/v1/"+rel)
	}
	authOrder := []string{"bearer", "x-api-key"}
	if wireAPI == "anthropic_messages" {
		authOrder = []string{"x-api-key", "bearer"}
	}
	switch strings.ToLower(strings.TrimSpace(authStyle)) {
	case "bearer":
		authOrder = []string{"bearer"}
	case "x-api-key":
		authOrder = []string{"x-api-key"}
	case "none":
		authOrder = []string{"none"}
	}
	best := "unsupported"
	bestCode := 0
	for _, endpoint := range endpoints {
		for _, auth := range authOrder {
			if ctx.Err() != nil {
				return "unavailable", 0
			}
			headers := map[string]string{"Content-Type": "application/json"}
			if wireAPI == "anthropic_messages" {
				headers["anthropic-version"] = "2023-06-01"
			}
			switch auth {
			case "x-api-key":
				headers["x-api-key"] = apiKey
			case "none":
				// no auth header
			default:
				headers["Authorization"] = "Bearer " + apiKey
			}
			status, code := probeOneEndpoint(ctx, client, endpoint, headers, body)
			if status == "supported" || status == "rate_limited" {
				return status, code
			}
			if statusRank(status) > statusRank(best) {
				best = status
				bestCode = code
			}
		}
	}
	return best, bestCode
}

func probeOneEndpoint(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, body []byte) (string, int) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "unavailable", 0
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return "unavailable", 0
	}
	defer response.Body.Close()
	code := response.StatusCode
	switch {
	case code >= 200 && code < 300:
		return "supported", code
	case code == 401 || code == 403:
		return "unauthorized", code
	case code == 429:
		return "rate_limited", code
	case code == 404 || code == 405 || code == 501:
		return "unsupported", code
	case code >= 500:
		return "unavailable", code
	default:
		return "inconclusive", code
	}
}

func statusRank(status string) int {
	switch status {
	case "supported":
		return 6
	case "rate_limited":
		return 5
	case "unauthorized":
		return 4
	case "unavailable":
		return 3
	case "inconclusive":
		return 2
	default:
		return 1
	}
}
