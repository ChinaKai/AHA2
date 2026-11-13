package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Model health statuses describe whether the configured account can call a
// model right now. They are deliberately different from the protocol probe
// statuses in models.go: "unsupported"/"supported" answer "does this endpoint
// speak the protocol", while these answer "would a real Task request succeed,
// and if not, whose problem is it".
const (
	ModelHealthOK             = "ok"
	ModelHealthRateLimited    = "rate_limited"
	ModelHealthUnauthorized   = "unauthorized"
	ModelHealthQuotaExhausted = "quota_exhausted"
	ModelHealthModelNotFound  = "model_not_found"
	ModelHealthRejected       = "rejected"
	ModelHealthUnavailable    = "unavailable"
	ModelHealthInconclusive   = "inconclusive"
)

// ModelHealth is the result of one minimal real call against a model's
// configured endpoint. Endpoint and Detail are already redacted when they
// leave this package: they can be rendered in the browser and written to the
// audit log, so they must never carry the API key or URL credentials.
type ModelHealth struct {
	Status            string    `json:"status"`
	HTTPStatus        int       `json:"http_status,omitempty"`
	Endpoint          string    `json:"endpoint,omitempty"`
	RetryAfterSeconds int       `json:"retry_after_seconds,omitempty"`
	Detail            string    `json:"detail,omitempty"`
	CheckedAt         time.Time `json:"checked_at"`
}

// NormalizeModelHealthWireAPI maps a stored model wire_api onto the value the
// runtime actually executes. Model records and the Web form store "chat" (the
// add/update handlers rewrite "chat_completions" to "chat"), so accepting only
// "chat_completions" would leave real models uncheckable. An empty value follows
// the same backend defaults as internal/app.defaultModelWireAPI.
func NormalizeModelHealthWireAPI(backend, wireAPI string) (string, error) {
	wire := strings.ToLower(strings.TrimSpace(wireAPI))
	if wire == "chat_completions" {
		wire = "chat"
	}
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "claude":
		if wire == "" {
			wire = "anthropic_messages"
		}
		if wire != "anthropic_messages" {
			return "", fmt.Errorf("Claude Backend 只支持 anthropic_messages")
		}
	case "codex":
		if wire == "" {
			wire = "responses"
		}
		if wire != "responses" && wire != "chat" {
			return "", fmt.Errorf("Codex Backend 不支持 wire_api %q", wireAPI)
		}
	case "dsh":
		if wire == "" {
			wire = "responses"
		}
		switch wire {
		case "responses", "chat", "anthropic_messages":
		default:
			return "", fmt.Errorf("DSH Backend 不支持 wire_api %q", wireAPI)
		}
	default:
		if wire == "" {
			wire = "responses"
		}
		switch wire {
		case "responses", "chat", "anthropic_messages":
		default:
			return "", fmt.Errorf("不支持的 wire_api：%q", wireAPI)
		}
	}
	return wire, nil
}

// CheckModelHealth sends one minimal request on the endpoint the runtime would
// actually call and classifies the answer by account usability. It reuses
// probeRequestBody, probeResponseSnippet and probeRedact so the request shape
// and the redaction boundary stay identical to protocol detection.
//
// The endpoint mirrors runtime_selection.go: OpenAI-compatible providers use
// provider.base_url plus /responses or /chat/completions, and Anthropic
// Messages uses anthropic_base_url (or base_url when unset) plus /v1/messages —
// the same base the Claude Code SDK appends /v1/messages to. Probe-only
// fallbacks such as base/v1 derived paths or /anthropic candidates are
// deliberately not used: reporting them "ok" would hide that a real Task fails.
func CheckModelHealth(ctx context.Context, client *http.Client, baseURL, anthropicBaseURL, apiKey, authStyle, backend, wireModel, wireAPI string, timeout time.Duration) ModelHealth {
	result := ModelHealth{Status: ModelHealthInconclusive, CheckedAt: time.Now().UTC()}
	modelID := strings.TrimSpace(wireModel)
	key := strings.TrimSpace(apiKey)
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	anthropicBase := strings.TrimRight(strings.TrimSpace(anthropicBaseURL), "/")
	wire, err := NormalizeModelHealthWireAPI(backend, wireAPI)
	if err != nil {
		result.Detail = err.Error()
		return result
	}
	if modelID == "" {
		result.Detail = "模型没有配置 wire_model"
		return result
	}
	if wire == "anthropic_messages" && anthropicBase != "" {
		base = anthropicBase
	}
	if base == "" {
		result.Status = ModelHealthUnavailable
		result.Detail = "Provider 没有配置 Base URL"
		return result
	}
	endpoint := modelHealthEndpoint(base, wire)
	normalizedAuth := strings.ToLower(strings.TrimSpace(authStyle))
	if normalizedAuth == "" {
		normalizedAuth = "auto"
	}
	if key == "" && normalizedAuth != "none" {
		// A missing credential is an auth condition for this account, not a
		// transport failure: retrying or another endpoint cannot help.
		result.Status = ModelHealthUnauthorized
		result.Endpoint = probeRedact(endpoint, key)
		result.Detail = "Provider 未配置 API Key"
		return result
	}
	probeWire := wire
	if probeWire == "chat" {
		// probeRequestBody names this protocol chat_completions; the stored
		// model value and the Codex runtime call it "chat".
		probeWire = "chat_completions"
	}
	body := probeRequestBody(probeWire, modelID, false)
	if body == nil {
		result.Detail = "不支持的 wire_api：" + wire
		return result
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Use the credential header the runtime actually sends, not a probe-style
	// search across header styles: an "auto" provider whose x-api-key works
	// while the runtime sends a different header would otherwise report ok
	// while every Task fails.
	auth := modelHealthAuthStyle(wire, normalizedAuth)
	if ctx.Err() != nil {
		return unavailableHealth(endpoint, ctx.Err().Error(), key)
	}
	return checkModelHealthEndpoint(ctx, client, endpoint, modelHealthHeaders(wire, auth, key), body, wire, modelID, key)
}

// modelHealthEndpoint is the exact path the backend appends to its configured
// base URL. Do not add probe-style variants here: the health check must
// predict what a real Task request will do.
func modelHealthEndpoint(base, wireAPI string) string {
	switch wireAPI {
	case "responses":
		return base + "/responses"
	case "chat":
		return base + "/chat/completions"
	case "anthropic_messages":
		return base + "/v1/messages"
	default:
		return ""
	}
}

// modelHealthAuthStyle mirrors runtime_selection.go: Anthropic uses x-api-key
// (ANTHROPIC_API_KEY) unless the Provider explicitly chose bearer
// (ANTHROPIC_AUTH_TOKEN); OpenAI-compatible backends always use
// Authorization: Bearer through their env_key.
func modelHealthAuthStyle(wireAPI, authStyle string) string {
	if authStyle == "none" {
		return "none"
	}
	if wireAPI == "anthropic_messages" {
		if authStyle == "bearer" {
			return "bearer"
		}
		return "x-api-key"
	}
	return "bearer"
}

func modelHealthHeaders(wireAPI, auth, apiKey string) map[string]string {
	headers := map[string]string{"Content-Type": "application/json"}
	if wireAPI == "anthropic_messages" {
		headers["anthropic-version"] = "2023-06-01"
	}
	switch auth {
	case "x-api-key":
		headers["x-api-key"] = apiKey
	case "none":
	default:
		headers["Authorization"] = "Bearer " + apiKey
	}
	return headers
}

// checkModelHealthEndpoint performs one request and optionally one streaming
// retry. The retry is limited to Responses 400 answers whose body says the
// model requires streaming (gateways such as Ali Token Plan only accept this
// protocol in streaming mode). A generic 400 parameter error is not retried.
func checkModelHealthEndpoint(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, body []byte, wireAPI, modelID, apiKey string) ModelHealth {
	health, code, detail := modelHealthRequest(ctx, client, endpoint, headers, body, false, apiKey)
	if health.Status != ModelHealthRejected || code != http.StatusBadRequest || wireAPI != "responses" || ctx.Err() != nil {
		return health
	}
	if !modelHealthRequiresStreaming(detail) {
		return health
	}
	streamHeaders := make(map[string]string, len(headers))
	for key, value := range headers {
		streamHeaders[key] = value
	}
	streamHealth, _, _ := modelHealthRequest(ctx, client, endpoint, streamHeaders, probeRequestBody(wireAPI, modelID, true), true, apiKey)
	// A 401/402/404/5xx (or an SSE error event) during the retry is more
	// informative than the original 400 and must not be hidden by it.
	if healthMergeRank(streamHealth.Status) > healthMergeRank(health.Status) {
		return streamHealth
	}
	return health
}

// modelHealthRequiresStreaming reports whether the 400 evidence points at a
// streaming-only model rather than a generic parameter/schema rejection.
func modelHealthRequiresStreaming(detail string) bool {
	return strings.Contains(strings.ToLower(detail), "stream")
}

// modelHealthRequest sends one request and returns the classified result,
// the HTTP status (0 for transport failures), and the classified detail.
func modelHealthRequest(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, body []byte, stream bool, apiKey string) (ModelHealth, int, string) {
	result := ModelHealth{CheckedAt: time.Now().UTC(), Endpoint: probeRedact(endpoint, apiKey)}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		result.Status = ModelHealthUnavailable
		result.Detail = probeRedact(err.Error(), apiKey)
		return result, 0, result.Detail
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		result.Status = ModelHealthUnavailable
		result.Detail = probeRedact(err.Error(), apiKey)
		return result, 0, result.Detail
	}
	defer response.Body.Close()
	code := response.StatusCode
	result.HTTPStatus = code
	result.RetryAfterSeconds = modelHealthRetryAfter(response.Header)
	if code >= 200 && code < 300 {
		if stream {
			data, readErr := readFirstSSEDataLine(response.Body)
			if readErr != nil {
				result.Status = ModelHealthInconclusive
				result.Detail = probeRedact(readErr.Error(), apiKey)
				return result, code, result.Detail
			}
			if status, message := modelHealthSSEErrorStatus(data); status != "" {
				// A 200 SSE response can carry the real failure in an error
				// event; reporting that as "ok" would tell the Owner a model
				// works while every Task fails with the same quota message.
				result.Status = status
				result.Detail = probeRedact(message, apiKey)
				result.RetryAfterSeconds = 0
				return result, code, result.Detail
			}
		}
		result.Status = ModelHealthOK
		return result, code, ""
	}
	detail := probeResponseSnippet(response.Body, apiKey)
	result.Status = modelHealthStatusForCode(code, detail)
	result.Detail = detail
	if result.Status != ModelHealthRateLimited {
		// Retry-After is only meaningful for throttling; other statuses keep
		// the field empty so the UI does not imply a retry window.
		result.RetryAfterSeconds = 0
	}
	return result, code, detail
}

// readFirstSSEDataLine reads until the first SSE data line and returns its
// payload. Comment/heartbeat lines do not count. A stream that closes without
// data is an error, matching the protocol probe's definition of a live stream.
func readFirstSSEDataLine(body io.Reader) (string, error) {
	reader := bufio.NewReader(io.LimitReader(body, probeStreamReadLimit))
	for {
		line, err := reader.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data:") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")), nil
		}
		switch {
		case err == nil:
			continue
		case err == io.EOF:
			return "", errProbeStreamWithoutEvents
		default:
			return "", err
		}
	}
}

// modelHealthSSEErrorStatus inspects the first SSE data payload. A non-error
// event returns ("", ""). An error event is classified with the same quota /
// auth / model wording as an HTTP body, because gateways sometimes report an
// exhausted balance or a revoked key as HTTP 200 plus an SSE error.
func modelHealthSSEErrorStatus(payload string) (string, string) {
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return "", ""
	}
	typeName, _ := event["type"].(string)
	errorValue, hasError := event["error"]
	if !hasError && !strings.EqualFold(strings.TrimSpace(typeName), "error") {
		return "", ""
	}
	text := payload
	if details, ok := errorValue.(map[string]any); ok {
		parts := make([]string, 0, 3)
		for _, name := range []string{"message", "code", "type"} {
			if value, ok := details[name].(string); ok && strings.TrimSpace(value) != "" {
				parts = append(parts, value)
			}
		}
		if len(parts) > 0 {
			text = strings.Join(parts, " ")
		}
	}
	switch {
	case modelHealthQuotaExhausted(text):
		return ModelHealthQuotaExhausted, text
	case modelHealthAuthError(text):
		return ModelHealthUnauthorized, text
	case modelHealthModelMissing(text):
		return ModelHealthModelNotFound, text
	default:
		return ModelHealthInconclusive, text
	}
}

// modelHealthAuthError recognizes auth wording carried by an SSE error event.
func modelHealthAuthError(text string) bool {
	lower := strings.ToLower(text)
	for _, keyword := range []string{
		"unauthorized", "unauthenticated", "authentication", "not authorized",
		"invalid api key", "invalid_api_key", "permission", "forbidden",
		"access denied", "鉴权", "认证", "401", "403",
	} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// modelHealthStatusForCode maps one HTTP answer to account usability. 404 is
// model_not_found here (unlike the protocol probe, where it means "no such
// endpoint"): the endpoint is the one the runtime calls, so the gateway is
// saying it does not serve this model. A bare 400 stays "rejected" unless the
// redacted body clearly names quota/billing or a missing model.
func modelHealthStatusForCode(code int, detail string) string {
	switch {
	case code >= 200 && code < 300:
		return ModelHealthOK
	case code == http.StatusTooManyRequests:
		return ModelHealthRateLimited
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return ModelHealthUnauthorized
	case code == http.StatusPaymentRequired:
		return ModelHealthQuotaExhausted
	case code == http.StatusNotFound:
		return ModelHealthModelNotFound
	case code >= 500:
		return ModelHealthUnavailable
	case code == http.StatusBadRequest:
		switch {
		case modelHealthModelMissing(detail):
			return ModelHealthModelNotFound
		case modelHealthQuotaExhausted(detail):
			return ModelHealthQuotaExhausted
		default:
			return ModelHealthRejected
		}
	case code >= 400:
		switch {
		case modelHealthModelMissing(detail):
			return ModelHealthModelNotFound
		case modelHealthQuotaExhausted(detail):
			return ModelHealthQuotaExhausted
		default:
			return ModelHealthInconclusive
		}
	default:
		return ModelHealthInconclusive
	}
}

// modelHealthQuotaExhausted looks for billing/quota wording in an already
// redacted body. It is intentionally not applied to 429: a rate limit is a
// temporary throttle, not an exhausted balance.
func modelHealthQuotaExhausted(detail string) bool {
	text := strings.ToLower(detail)
	if text == "" {
		return false
	}
	for _, keyword := range []string{
		"insufficient_quota", "insufficient quota", "quota", "billing",
		"balance", "credit", "余额", "欠费", "充值", "配额",
	} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// modelHealthModelMissing looks for explicit "this model does not exist"
// wording. Requiring a model/模型 token avoids turning a generic "endpoint does
// not exist" message into a model verdict.
func modelHealthModelMissing(detail string) bool {
	text := strings.ToLower(detail)
	if text == "" {
		return false
	}
	if !strings.Contains(text, "model") && !strings.Contains(text, "模型") {
		return false
	}
	for _, keyword := range []string{
		"not found", "not exist", "does not exist", "unknown", "no such",
		"not available", "not supported", "不存在", "未找到", "不支持", "未开通",
	} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// modelHealthRetryAfter parses Retry-After seconds or an HTTP date. A stale
// date yields 0 rather than a negative delay.
func modelHealthRetryAfter(header http.Header) int {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0
		}
		return seconds
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return int(delay.Seconds())
		}
	}
	return 0
}

func unavailableHealth(endpoint, detail, apiKey string) ModelHealth {
	return ModelHealth{
		Status:    ModelHealthUnavailable,
		Endpoint:  probeRedact(endpoint, apiKey),
		Detail:    probeRedact(detail, apiKey),
		CheckedAt: time.Now().UTC(),
	}
}

// healthMergeRank orders the streaming retry against the original 400. Any
// account-level answer (ok, throttle, quota, auth, model, service) outranks the
// ambiguous 400; a second 400 does not replace the first rejection's evidence.
func healthMergeRank(status string) int {
	switch status {
	case ModelHealthOK:
		return 8
	case ModelHealthRateLimited:
		return 7
	case ModelHealthQuotaExhausted, ModelHealthUnauthorized:
		return 6
	case ModelHealthModelNotFound:
		return 5
	case ModelHealthUnavailable:
		return 4
	case ModelHealthInconclusive:
		return 3
	case ModelHealthRejected:
		return 2
	default:
		return 0
	}
}
