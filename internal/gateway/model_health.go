package gateway

import (
	"bytes"
	"context"
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

// CheckModelHealth sends one minimal request for the model's wire protocol and
// classifies the answer by account usability. It reuses probeRequestBody,
// probeResponseSnippet and probeRedact so the request shape and the redaction
// boundary stay identical to protocol detection.
//
// wireAPI selects the endpoint (responses, chat_completions, anthropic_messages).
// anthropic_base_url is preferred when set, because gateways such as Ali Token
// Plan expose Anthropic Messages on a separate path. The caller resolves an
// empty wireAPI from the model's backend before calling.
func CheckModelHealth(ctx context.Context, client *http.Client, baseURL, anthropicBaseURL, apiKey, authStyle, wireModel, wireAPI string, timeout time.Duration) ModelHealth {
	result := ModelHealth{Status: ModelHealthInconclusive, CheckedAt: time.Now().UTC()}
	modelID := strings.TrimSpace(wireModel)
	key := strings.TrimSpace(apiKey)
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	anthropicBase := strings.TrimRight(strings.TrimSpace(anthropicBaseURL), "/")
	wire := strings.ToLower(strings.TrimSpace(wireAPI))
	if modelID == "" {
		result.Detail = "模型没有配置 wire_model"
		return result
	}
	if wire == "" {
		result.Detail = "模型没有配置 wire_api"
		return result
	}
	endpoints := modelHealthEndpoints(base, anthropicBase, wire)
	if len(endpoints) == 0 {
		result.Status = ModelHealthUnavailable
		result.Detail = "Provider 没有配置 Base URL"
		return result
	}
	normalizedAuth := strings.ToLower(strings.TrimSpace(authStyle))
	if normalizedAuth == "" {
		normalizedAuth = "auto"
	}
	if key == "" && normalizedAuth != "none" {
		// A missing credential is an auth condition for this account, not a
		// transport failure: retrying or checking another endpoint cannot help.
		result.Status = ModelHealthUnauthorized
		result.Endpoint = probeRedact(endpoints[0], key)
		result.Detail = "Provider 未配置 API Key"
		return result
	}
	body := probeRequestBody(wire, modelID, false)
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

	best := result
	for _, auth := range modelHealthAuthOrder(wire, normalizedAuth) {
		credentialRejected := false
		for _, endpoint := range endpoints {
			if ctx.Err() != nil {
				return unavailableHealth(endpoint, ctx.Err().Error(), key)
			}
			attempt := checkModelHealthEndpoint(ctx, client, endpoint, modelHealthHeaders(wire, auth, key), body, wire, modelID, key)
			switch attempt.Status {
			case ModelHealthOK, ModelHealthRateLimited, ModelHealthQuotaExhausted, ModelHealthUnavailable, ModelHealthInconclusive:
				return attempt
			case ModelHealthUnauthorized:
				// The credential header was rejected; another header style may
				// still work when auth is auto. Endpoint choice cannot fix auth,
				// so stop trying this style's other endpoint forms.
				best = attempt
				credentialRejected = true
			case ModelHealthModelNotFound, ModelHealthRejected:
				// 404 can also mean this base form lacks the API prefix, and a
				// 400 can be a wrong route; the next endpoint form may answer.
				// Either way the gateway evaluated the credential, so there is
				// no reason to replay every endpoint with another header style.
				if healthRank(attempt.Status) > healthRank(best.Status) {
					best = attempt
				}
			}
			if attempt.Status == ModelHealthUnauthorized {
				break
			}
		}
		if !credentialRejected {
			break
		}
	}
	if best.Status == ModelHealthInconclusive && best.Detail == "" {
		best.Status = ModelHealthModelNotFound
		best.Endpoint = probeRedact(endpoints[0], key)
		best.Detail = "端点未返回可判定的响应"
	}
	return best
}

// modelHealthEndpoints returns the endpoint forms to try. Anthropic Messages
// prefers the configured anthropic_base_url and the same base candidates the
// protocol probe uses; the other protocols use the provider base URL.
func modelHealthEndpoints(base, anthropicBase, wireAPI string) []string {
	rel := ""
	switch wireAPI {
	case "responses":
		rel = "responses"
	case "chat_completions":
		rel = "chat/completions"
	case "anthropic_messages":
		rel = "messages"
	default:
		return nil
	}
	var bases []string
	if wireAPI == "anthropic_messages" {
		bases = anthropicBaseCandidates(base, anthropicBase)
	} else if base != "" {
		bases = []string{base}
	}
	seen := make(map[string]struct{}, len(bases)*2)
	var endpoints []string
	add := func(endpoint string) {
		if endpoint == "" {
			return
		}
		if _, ok := seen[endpoint]; ok {
			return
		}
		seen[endpoint] = struct{}{}
		endpoints = append(endpoints, endpoint)
	}
	for _, candidate := range bases {
		add(candidate + "/" + rel)
		if !strings.HasSuffix(candidate, "/v1") && !strings.HasSuffix(candidate, "/v1/") {
			add(candidate + "/v1/" + rel)
		}
	}
	return endpoints
}

// modelHealthAuthOrder narrows the credential header styles the same way the
// protocol probe does, so a rate-limited gateway is not asked twice.
func modelHealthAuthOrder(wireAPI, authStyle string) []string {
	order := []string{"bearer", "x-api-key"}
	if wireAPI == "anthropic_messages" {
		order = []string{"x-api-key", "bearer"}
	}
	switch authStyle {
	case "bearer", "x-api-key", "none":
		return []string{authStyle}
	default:
		return order
	}
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

// checkModelHealthEndpoint performs exactly one request and classifies it.
// Responses requests rejected with 400 are retried once with stream=true,
// mirroring probeWireAPI: gateways such as Ali Token Plan accept the protocol
// only in streaming mode, and a health check must not report those models as
// rejected while real Tasks work.
func checkModelHealthEndpoint(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, body []byte, wireAPI, modelID, apiKey string) ModelHealth {
	health, code, _ := modelHealthRequest(ctx, client, endpoint, headers, body, false, apiKey)
	if health.Status == ModelHealthRejected && code == http.StatusBadRequest && wireAPI == "responses" && ctx.Err() == nil {
		streamHeaders := make(map[string]string, len(headers))
		for key, value := range headers {
			streamHeaders[key] = value
		}
		streamHealth, _, _ := modelHealthRequest(ctx, client, endpoint, streamHeaders, probeRequestBody(wireAPI, modelID, true), true, apiKey)
		if streamHealth.Status == ModelHealthOK || streamHealth.Status == ModelHealthRateLimited {
			return streamHealth
		}
	}
	return health
}

// modelHealthRequest sends one request and returns the classified result,
// the HTTP status (0 for transport failures), and the raw classified detail.
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
			if err := readFirstSSEEvent(response.Body); err != nil {
				result.Status = ModelHealthInconclusive
				result.Detail = probeRedact(err.Error(), apiKey)
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

// modelHealthStatusForCode maps one HTTP answer to account usability. 404 is
// model_not_found here (unlike the protocol probe, where it means "no such
// endpoint"): the model's endpoint form is already known to exist, so the
// gateway is saying it does not serve this model. A bare 400 stays "rejected"
// unless the redacted body clearly names quota/billing or a missing model.
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

// healthRank orders fallback evidence when no endpoint produced a definitive
// account verdict. A rejected request is more informative than "route absent".
func healthRank(status string) int {
	switch status {
	case ModelHealthQuotaExhausted:
		return 5
	case ModelHealthUnauthorized:
		return 4
	case ModelHealthModelNotFound:
		return 3
	case ModelHealthRejected:
		return 2
	case ModelHealthInconclusive:
		return 1
	default:
		return 0
	}
}
