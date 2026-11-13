package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// probeMaxOutputTokens is a legally minimal output budget shared by the
	// three wire protocols. Strict gateways reject a 1-token budget (below
	// their minimum, or an invalid parameter), which used to make an available
	// protocol look unavailable.
	probeMaxOutputTokens = 16
	// probeErrorBodyReadLimit bounds how much of an error response is read;
	// probeErrorBodyMaxRunes bounds how much survives into protocol_errors.
	probeErrorBodyReadLimit = 8 << 10
	probeErrorBodyMaxRunes  = 512
	// probeStreamReadLimit caps how many bytes the first-event reader consumes.
	probeStreamReadLimit = 64 << 10
)

// probeStreamTimeout bounds a streaming probe request: connection, response
// headers and the read of the first SSE event share this deadline, so a gateway
// that opens a stream and then stays silent cannot hold detection open until
// the caller's overall deadline. It is a variable so a test can shorten it and
// prove the internal deadline exists instead of relying on the outer context.
var probeStreamTimeout = 10 * time.Second

var (
	probeURLUserInfoPattern     = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`)
	probeSensitiveQueryPattern  = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|key|token|access_token|auth|password|secret)=)[^&\s"']*`)
	probeBearerTokenPattern     = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/-]+=*`)
	errProbeStreamWithoutEvents = errors.New("stream closed before an SSE data event")
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
	// Keep every failed endpoint/auth attempt: the unauthenticated probe runs
	// last, and its generic 401 used to hide the authenticated attempt's real
	// upstream error (Moonshot's "Invalid Authentication", for example).
	var failures []modelDetectFailure
	for _, endpoint := range candidates {
		for _, set := range authSets {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			models, err := fetchModels(ctx, client, endpoint, set.headers, key)
			if err != nil {
				failures = append(failures, modelDetectFailure{
					endpoint:      endpoint,
					authStyle:     set.style,
					authenticated: set.style != "none",
					detail:        err.Error(),
				})
				continue
			}
			if len(models) > 0 {
				return Result{AuthStyle: set.style, Models: models}, nil
			}
		}
	}
	if len(failures) > 0 {
		return Result{}, fmt.Errorf("failed to detect models: %s", summarizeModelDetectFailures(failures, key))
	}
	return Result{AuthStyle: "none", Models: []DetectedModel{}}, nil
}

// modelDetectFailure records one endpoint/auth attempt that did not produce a
// model list.
type modelDetectFailure struct {
	endpoint      string
	authStyle     string
	authenticated bool
	detail        string
}

// summarizeModelDetectFailures renders failed catalog attempts as a single
// redacted line per distinct attempt. When an API key is configured, only the
// authenticated attempts are reported: the trailing unauthenticated probe
// cannot succeed on a key-protected gateway, and its 401 would otherwise
// overwrite the meaningful auth error. Every line carries the auth style and the
// HTTP status/upstream body snippet so the UI can explain why detection failed.
func summarizeModelDetectFailures(failures []modelDetectFailure, apiKey string) string {
	selected := make([]modelDetectFailure, 0, len(failures))
	for _, failure := range failures {
		if failure.authenticated {
			selected = append(selected, failure)
		}
	}
	if len(selected) == 0 {
		selected = failures
	}
	seen := make(map[string]struct{}, len(selected))
	lines := make([]string, 0, len(selected))
	for _, failure := range selected {
		line := probeRedact(fmt.Sprintf("%s [%s]: %s", failure.endpoint, failure.authStyle, failure.detail), apiKey)
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		lines = append(lines, line)
	}
	return strings.Join(lines, "; ")
}

func fetchModels(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, apiKey string) ([]DetectedModel, error) {
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
		// Carry the upstream body into the aggregate error: a bare "HTTP 401"
		// hides the provider's actual explanation (invalid auth, quota, ...).
		if snippet := probeResponseSnippet(response.Body, apiKey); snippet != "" {
			return nil, fmt.Errorf("HTTP %d: %s", response.StatusCode, snippet)
		}
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
// rejected / unauthorized / rate_limited / unavailable / inconclusive.
// "unsupported" means the endpoint is absent (HTTP 404/405/501); "rejected"
// means the gateway answered HTTP 400, which may be an unsupported protocol
// or an unknown model/parameter/auth problem, so callers must keep the
// wording neutral instead of claiming the protocol is not offered.
//
// For anthropic_messages it also tries the gateway's /anthropic suffix (some
// gateways like DeepSeek/MiniMax/Kimi serve the Anthropic protocol there) and
// returns that base URL when it is the one that works. Callers that know the
// provider's configured Anthropic entry point should use
// ProbeModelCapabilitiesForProviderContext instead.
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
	capabilities, anthropicBase, _ := ProbeModelCapabilitiesContextWithClientDetailed(ctx, baseURL, apiKey, authStyle, modelID, timeout, client)
	return capabilities, anthropicBase
}

// ProbeModelCapabilitiesContextWithClientDetailed also returns the transport
// error behind each non-definitive protocol status, so a manual probe that
// reports every protocol unavailable can explain why instead of hiding it.
func ProbeModelCapabilitiesContextWithClientDetailed(ctx context.Context, baseURL, apiKey, authStyle, modelID string, timeout time.Duration, client *http.Client) (map[string]string, string, map[string]string) {
	return ProbeModelCapabilitiesForProviderContext(ctx, baseURL, "", apiKey, authStyle, modelID, timeout, client)
}

// ProbeModelCapabilitiesForProviderContext probes the wire protocols of a
// provider whose Anthropic entry point can differ from its OpenAI-compatible
// base URL (provider.anthropic_base_url, e.g. Ali token plan's /apps/anthropic).
// The configured Anthropic base is probed before URLs derived from baseURL so a
// gateway-specific path is not missed. It also returns the transport error
// behind each non-definitive protocol status.
func ProbeModelCapabilitiesForProviderContext(ctx context.Context, baseURL, anthropicBaseURL, apiKey, authStyle, modelID string, timeout time.Duration, client *http.Client) (map[string]string, string, map[string]string) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	key := strings.TrimSpace(apiKey)
	if base == "" || modelID == "" || key == "" {
		return map[string]string{}, "", map[string]string{}
	}
	normalizedAuth := strings.ToLower(strings.TrimSpace(authStyle))
	if normalizedAuth == "" {
		normalizedAuth = "auto"
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	capabilities := make(map[string]string)
	protocolErrors := make(map[string]string)
	record := func(wireAPI, status, errText string) {
		capabilities[wireAPI] = status
		if errText != "" && status != "supported" && status != "rate_limited" {
			// Evidence is returned to the UI and written to the audit log, so
			// redact the complete line (candidate URL, transport error, and
			// body snippet) instead of trusting individual producers.
			protocolErrors[wireAPI] = probeRedact(errText, key)
		}
	}
	// Probes are independent: run them concurrently so a slow gateway does not
	// multiply its latency by three before the UI gets a result.
	var responsesStatus, responsesErr, chatStatus, chatErr string
	var anthropicStatus, anthropicErr, anthropicBase string
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		responsesStatus, _, responsesErr = probeWireAPI(ctx, client, base, key, normalizedAuth, modelID, "responses")
	}()
	go func() {
		defer wg.Done()
		chatStatus, _, chatErr = probeWireAPI(ctx, client, base, key, normalizedAuth, modelID, "chat_completions")
	}()
	go func() {
		defer wg.Done()
		candidates := anthropicBaseCandidates(base, anthropicBaseURL)
		if len(candidates) == 0 {
			anthropicStatus, anthropicErr = "unavailable", "provider base URL is empty"
			return
		}
		status, code, errText := probeWireAPI(ctx, client, candidates[0], key, normalizedAuth, modelID, "anthropic_messages")
		evidence := make([]string, 0, len(candidates))
		if line := probeEvidence(candidates[0], status, code, errText); line != "" {
			evidence = append(evidence, line)
		}
		if anthropicCandidateFallback(status) {
			for _, candidate := range candidates[1:] {
				if ctx.Err() != nil {
					status, errText = "unavailable", ctx.Err().Error()
					if line := probeEvidence(candidate, status, 0, errText); line != "" {
						evidence = append(evidence, line)
					}
					break
				}
				retriedStatus, retriedCode, retriedErr := probeWireAPI(ctx, client, candidate, key, normalizedAuth, modelID, "anthropic_messages")
				if retriedStatus == "supported" || retriedStatus == "rate_limited" {
					// 429 proves the endpoint exists and serves the protocol;
					// keep the status but record the base the caller must use.
					status, errText = retriedStatus, ""
					anthropicBase = candidate
					break
				}
				if line := probeEvidence(candidate, retriedStatus, retriedCode, retriedErr); line != "" {
					evidence = append(evidence, line)
				}
				// Keep the most diagnostic result instead of letting whichever
				// candidate ran last rewrite an auth or service failure.
				if statusRank(retriedStatus) > statusRank(status) {
					status = retriedStatus
				}
			}
		}
		if status != "supported" && status != "rate_limited" {
			// Every failed candidate keeps its URL and HTTP status so the UI can
			// tell a missing route (404) from an auth problem (401/403).
			errText = strings.Join(evidence, "; ")
		}
		anthropicStatus, anthropicErr = status, errText
	}()
	wg.Wait()
	record("responses", responsesStatus, responsesErr)
	record("chat_completions", chatStatus, chatErr)
	record("anthropic_messages", anthropicStatus, anthropicErr)
	return capabilities, anthropicBase, protocolErrors
}

// anthropicBaseCandidates returns the configured Anthropic entry point first
// (provider.anthropic_base_url, when set), then the OpenAI-compatible base URL
// and the /anthropic variant when it differs (the suffix some gateways use for
// Anthropic Messages). Duplicate URLs are dropped so each base is probed once.
func anthropicBaseCandidates(baseURL, anthropicBaseURL string) []string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	configured := strings.TrimRight(strings.TrimSpace(anthropicBaseURL), "/")
	candidates := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	add := func(candidate string) {
		if candidate == "" {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}
	add(configured)
	if base == "" {
		if len(candidates) == 0 {
			return nil
		}
		return candidates
	}
	add(base)
	if !strings.HasSuffix(base, "/anthropic") {
		root := base
		if strings.HasSuffix(base, "/v1") {
			root = base[:len(base)-3]
		}
		add(root + "/anthropic")
	}
	return candidates
}

// anthropicCandidateFallback reports whether a candidate's probe result means
// "this base URL is probably the wrong or absent entry point", in which case
// the remaining candidates are still worth probing. A missing route
// (unsupported: 404/405/501) and an ambiguous rejection (rejected: 400) can
// both be fixed by another base URL. Unauthorized, unavailable (5xx/timeout),
// rate_limited, and inconclusive describe the credential or the gateway
// itself, so they keep the first candidate's verdict instead of multiplying
// requests and hiding an auth or service problem.
func anthropicCandidateFallback(status string) bool {
	return status == "unsupported" || status == "rejected"
}

// probeEvidence describes one failed probe candidate as "<candidate URL>:
// HTTP <code>[: <sanitized body snippet>]" so protocol_errors shows which entry
// point was tried and why it failed. Transport errors already name the
// endpoint; they are prefixed with the candidate when they do not. It never
// includes the API key.
func probeEvidence(candidate, status string, code int, detail string) string {
	switch {
	case status == "supported" || status == "rate_limited" || status == "":
		return ""
	case code > 0:
		line := fmt.Sprintf("%s: HTTP %d", candidate, code)
		if snippet := probeEvidenceSnippet(detail, code); snippet != "" {
			return line + ": " + snippet
		}
		return line
	case detail != "":
		if strings.Contains(detail, candidate) {
			return detail
		}
		return candidate + ": " + detail
	default:
		return candidate + ": " + status
	}
}

// probeRequestBody builds the minimal legal request body for a wire protocol
// probe. The Responses body follows the official /responses schema: input is a
// message array whose content parts are typed input_text. A bare input string
// plus a 1-token output budget is rejected by strict gateways even though the
// protocol and model are available.
func probeRequestBody(wireAPI, modelID string, stream bool) []byte {
	var payload map[string]any
	switch wireAPI {
	case "responses":
		payload = map[string]any{
			"model": modelID,
			"input": []any{map[string]any{
				"role":    "user",
				"content": []any{map[string]any{"type": "input_text", "text": "ping"}},
			}},
			"max_output_tokens": probeMaxOutputTokens,
			"stream":            stream,
		}
	case "chat_completions":
		payload = map[string]any{
			"model":      modelID,
			"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
			"max_tokens": probeMaxOutputTokens,
		}
	case "anthropic_messages":
		payload = map[string]any{
			"model":      modelID,
			"max_tokens": probeMaxOutputTokens,
			"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
		}
	default:
		return nil
	}
	body, _ := json.Marshal(payload)
	return body
}

// probeEvidenceSnippet extracts the response-body part from a detail string
// that probeWireAPI already formatted as "<endpoint>: HTTP <code>: <body>", so
// probeEvidence can attribute it to the candidate base URL without repeating
// the endpoint and status.
func probeEvidenceSnippet(detail string, code int) string {
	status := fmt.Sprintf("HTTP %d", code)
	switch {
	case detail == "", detail == status:
		return ""
	case strings.HasPrefix(detail, status+": "):
		return strings.TrimPrefix(detail, status+": ")
	}
	marker := ": " + status + ": "
	if index := strings.Index(detail, marker); index >= 0 {
		return detail[index+len(marker):]
	}
	return detail
}

// probeWireAPI probes a wire protocol across endpoint forms (a bare path for
// bases that already carry an API prefix such as GLM's /api/paas/v4, plus a
// /v1-prefixed path for bare-host bases). Auth is narrowed to the confirmed
// style when known so rate-limited gateways are not flooded with redundant
// requests. A definitive result (supported or rate_limited) stops the probe.
// When a non-streaming Responses request is rejected with 400, the same
// endpoint is retried once with stream=true because some gateways only accept
// this protocol in streaming mode.
func probeWireAPI(ctx context.Context, client *http.Client, base, apiKey, authStyle, modelID, wireAPI string) (string, int, string) {
	rel := ""
	switch wireAPI {
	case "responses":
		rel = "responses"
	case "chat_completions":
		rel = "chat/completions"
	case "anthropic_messages":
		rel = "messages"
	}
	if rel == "" {
		return "unsupported", 0, ""
	}
	body := probeRequestBody(wireAPI, modelID, false)
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
	bestError := ""
	for _, endpoint := range endpoints {
		for _, auth := range authOrder {
			if ctx.Err() != nil {
				return "unavailable", 0, ctx.Err().Error()
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
			status, code, detail := probeOneEndpoint(ctx, client, endpoint, headers, body, false, apiKey)
			if wireAPI == "responses" && code == http.StatusBadRequest && ctx.Err() == nil {
				// Some gateways only accept the Responses protocol in streaming
				// mode; retry the same endpoint with stream=true and require the
				// first SSE event before accepting the retry as support.
				streamStatus, streamCode, streamDetail := probeOneEndpoint(ctx, client, endpoint, headers, probeRequestBody(wireAPI, modelID, true), true, apiKey)
				if streamStatus == "supported" || streamStatus == "rate_limited" {
					return streamStatus, streamCode, ""
				}
				// A 200 stream that never produced a data event is a distinct
				// signal (the endpoint accepted the request but did not stream
				// the protocol), so it replaces the earlier 400 rejection even
				// though both rank as ambiguous. A second 400 keeps the first
				// rejection, whose body is the better evidence.
				if statusRank(streamStatus) > statusRank(status) || (streamCode >= 200 && streamCode < 300) {
					status, code, detail = streamStatus, streamCode, streamDetail
				}
			}
			if status == "supported" || status == "rate_limited" {
				return status, code, ""
			}
			rank := statusRank(status)
			switch {
			case rank > statusRank(best):
				best = status
				bestCode = code
				bestError = probeEndpointError(endpoint, status, code, detail)
			case rank == statusRank(best) && bestCode == 0 && code != 0:
				// unsupported is also the initial best, so a 404/405/501 must be
				// recorded explicitly or callers lose the HTTP status.
				bestCode = code
				bestError = probeEndpointError(endpoint, status, code, detail)
			}
		}
	}
	return best, bestCode, bestError
}

// probeEndpointError carries the endpoint and HTTP status into protocol_errors
// so a failed protocol probe names what it actually tried. detail may add a
// sanitized response-body snippet for 4xx/5xx responses. The API key is never
// part of an endpoint URL.
func probeEndpointError(endpoint, status string, code int, detail string) string {
	if code != 0 {
		if detail != "" {
			return fmt.Sprintf("%s: HTTP %d: %s", endpoint, code, detail)
		}
		return fmt.Sprintf("%s: HTTP %d", endpoint, code)
	}
	if detail != "" {
		if strings.Contains(detail, endpoint) {
			return detail
		}
		return endpoint + ": " + detail
	}
	return ""
}

// probeStatusForCode classifies one probe response. 404/405/501 mean the
// gateway does not expose this wire protocol at that endpoint. 400 is kept
// separate as "rejected": the gateway refused this request, but the status
// code alone cannot distinguish an unsupported protocol from an unknown
// model, bad parameters, auth/gateway policy, or a malformed body. 401/403
// stay auth failures, 429 stays rate limited, and 5xx stays a transient
// service/transport failure. The remaining codes are inconclusive and keep
// their HTTP status as evidence. None of these classifications are transport
// failures unless the request never produced an HTTP response.
func probeStatusForCode(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "supported"
	case code == 401 || code == 403:
		return "unauthorized"
	case code == 429:
		return "rate_limited"
	case code == 400:
		return "rejected"
	case code == 404 || code == 405 || code == 501:
		return "unsupported"
	case code >= 500:
		return "unavailable"
	default:
		return "inconclusive"
	}
}

// probeOneEndpoint sends one probe request and classifies the response. detail
// carries a sanitized, truncated response-body snippet on non-2xx responses so
// a 400 can be told apart from an auth, model, or transport error. A streaming
// probe reads the first SSE data event under probeStreamTimeout, so a gateway
// that opens the stream and stays silent cannot hang detection.
func probeOneEndpoint(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, body []byte, stream bool, apiKey string) (string, int, string) {
	if stream {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, probeStreamTimeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "unavailable", 0, err.Error()
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return "unavailable", 0, err.Error()
	}
	defer response.Body.Close()
	code := response.StatusCode
	if code >= 200 && code < 300 {
		if stream {
			if err := readFirstSSEEvent(response.Body); err != nil {
				return "inconclusive", code, err.Error()
			}
		}
		return "supported", code, ""
	}
	return probeStatusForCode(code), code, probeResponseSnippet(response.Body, apiKey)
}

// readFirstSSEEvent reads until the first SSE data line or the end of the
// stream/limit. A stream that closes without any data line is an error: an
// empty body, a heartbeat-only stream, and a non-SSE body do not prove the
// protocol works. Read errors (including the stream timeout) are returned so
// the caller classifies the probe as inconclusive instead of hanging.
func readFirstSSEEvent(body io.Reader) error {
	reader := bufio.NewReader(io.LimitReader(body, probeStreamReadLimit))
	for {
		line, err := reader.ReadString('\n')
		if isProbeSSEDataLine(line) {
			return nil
		}
		switch {
		case err == nil:
			continue
		case err == io.EOF:
			return errProbeStreamWithoutEvents
		default:
			return err
		}
	}
}

// isProbeSSEDataLine reports whether line is an SSE data field. TrimSpace
// handles CRLF line endings; comment/heartbeat lines (": ...") do not match.
func isProbeSSEDataLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "data:")
}

// probeResponseSnippet renders a bounded, single-line, redacted snippet of an
// error response body for protocol_errors.
func probeResponseSnippet(body io.Reader, apiKey string) string {
	raw, _ := io.ReadAll(io.LimitReader(body, probeErrorBodyReadLimit))
	text := probeRedact(strings.TrimSpace(string(raw)), apiKey)
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > probeErrorBodyMaxRunes {
		text = string(runes[:probeErrorBodyMaxRunes]) + "…"
	}
	return text
}

// probeRedact removes credentials from any text that becomes protocol
// evidence: the API key itself, URL userinfo from provider base URLs, and
// key/token query parameters or bearer tokens echoed by a gateway or a
// transport error. Evidence can reach audit logs, so redaction is applied once
// to the final evidence string as well as to response-body snippets.
func probeRedact(text, apiKey string) string {
	if text == "" {
		return ""
	}
	if apiKey != "" {
		text = strings.ReplaceAll(text, apiKey, "[redacted]")
	}
	text = probeURLUserInfoPattern.ReplaceAllString(text, "${1}[redacted]@")
	text = probeSensitiveQueryPattern.ReplaceAllString(text, "${1}[redacted]")
	text = probeBearerTokenPattern.ReplaceAllString(text, "${1}[redacted]")
	return text
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
	case "inconclusive", "rejected":
		return 2
	default:
		return 1
	}
}
