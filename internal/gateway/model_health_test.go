package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// healthJSONResponse is probeJSONResponse plus optional response headers such
// as Retry-After.
func healthJSONResponse(code int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = http.Header{}
	}
	headers.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: code,
		Status:     fmt.Sprintf("%d %s", code, http.StatusText(code)),
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestCheckModelHealthStatusMapping(t *testing.T) {
	t.Parallel()
	secret := "secret-key"
	cases := []struct {
		name       string
		code       int
		body       string
		headers    http.Header
		wantStatus string
		wantRetry  int
	}{
		{name: "ok", code: http.StatusOK, body: `{"id":"chatcmpl-1"}`, wantStatus: ModelHealthOK},
		{name: "ok empty body", code: http.StatusOK, body: "", wantStatus: ModelHealthOK},
		{name: "rate limited", code: http.StatusTooManyRequests, body: `{"error":{"message":"slow down"}}`, headers: http.Header{"Retry-After": []string{"42"}}, wantStatus: ModelHealthRateLimited, wantRetry: 42},
		{name: "rate limited without retry-after", code: http.StatusTooManyRequests, body: `{"error":{"message":"slow down"}}`, wantStatus: ModelHealthRateLimited, wantRetry: 0},
		{name: "unauthorized", code: http.StatusUnauthorized, body: `{"error":{"message":"invalid key"}}`, wantStatus: ModelHealthUnauthorized},
		{name: "forbidden", code: http.StatusForbidden, body: `{"error":{"message":"no access"}}`, wantStatus: ModelHealthUnauthorized},
		{name: "payment required", code: http.StatusPaymentRequired, body: `{"error":{"message":"pay up"}}`, wantStatus: ModelHealthQuotaExhausted},
		{name: "400 quota keyword", code: http.StatusBadRequest, body: `{"error":{"message":"insufficient_quota: 账户余额不足，请充值"}}`, wantStatus: ModelHealthQuotaExhausted},
		{name: "400 quota with echoed key", code: http.StatusBadRequest, body: `{"error":{"message":"billing hard limit reached","api_key":"` + secret + `"}}`, wantStatus: ModelHealthQuotaExhausted},
		{name: "400 rejected parameters", code: http.StatusBadRequest, body: `{"error":{"message":"invalid max_tokens: must be >= 16"}}`, wantStatus: ModelHealthRejected},
		{name: "400 empty body", code: http.StatusBadRequest, body: "", wantStatus: ModelHealthRejected},
		{name: "400 model missing", code: http.StatusBadRequest, body: `{"error":{"message":"The model ` + "`gpt-x`" + ` does not exist"}}`, wantStatus: ModelHealthModelNotFound},
		{name: "404 model missing", code: http.StatusNotFound, body: `{"error":{"message":"model not found"}}`, wantStatus: ModelHealthModelNotFound},
		{name: "500 unavailable", code: http.StatusInternalServerError, body: `{"error":{"message":"boom"}}`, wantStatus: ModelHealthUnavailable},
		{name: "other 4xx inconclusive", code: http.StatusUnprocessableEntity, body: `{"error":{"message":"unprocessable"}}`, wantStatus: ModelHealthInconclusive},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				return healthJSONResponse(testCase.code, testCase.body, testCase.headers), nil
			})}
			health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", secret,
				"bearer", "codex", "model-x", "chat", 5*time.Second)
			if health.Status != testCase.wantStatus {
				t.Fatalf("status=%q want %q (http=%d detail=%q)", health.Status, testCase.wantStatus, health.HTTPStatus, health.Detail)
			}
			if health.HTTPStatus != testCase.code {
				t.Fatalf("http_status=%d want %d", health.HTTPStatus, testCase.code)
			}
			if health.RetryAfterSeconds != testCase.wantRetry {
				t.Fatalf("retry_after_seconds=%d want %d", health.RetryAfterSeconds, testCase.wantRetry)
			}
			for name, value := range map[string]string{"endpoint": health.Endpoint, "detail": health.Detail} {
				if strings.Contains(value, secret) {
					t.Fatalf("%s leaked the API key: %q", name, value)
				}
			}
			// 429 is a throttled account, never an absent protocol.
			if testCase.code == http.StatusTooManyRequests && strings.Contains(health.Status, "unsupported") {
				t.Fatalf("429 was classified as an unsupported protocol: %q", health.Status)
			}
		})
	}
}

func TestCheckModelHealthTimeoutIsUnavailable(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	started := time.Now()
	health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
		"bearer", "codex", "model-x", "chat", 50*time.Millisecond)
	if health.Status != ModelHealthUnavailable {
		t.Fatalf("status=%q want unavailable (detail=%q)", health.Status, health.Detail)
	}
	if health.HTTPStatus != 0 {
		t.Fatalf("http_status=%d want 0 for a transport failure", health.HTTPStatus)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timeout was not enforced, elapsed=%s", elapsed)
	}
	if !strings.Contains(health.Detail, "deadline") && !strings.Contains(health.Detail, "context") {
		t.Fatalf("detail does not explain the timeout: %q", health.Detail)
	}
}

// TestCheckModelHealthNormalizesStoredWireAPI covers the values the model
// records actually contain: add/update handlers rewrite chat_completions to
// "chat", so a health check that only accepted chat_completions never sent a
// request at all.
func TestCheckModelHealthNormalizesStoredWireAPI(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		backend  string
		wireAPI  string
		wantPath string
	}{
		{name: "chat", backend: "codex", wireAPI: "chat", wantPath: "/v1/chat/completions"},
		{name: "chat_completions", backend: "codex", wireAPI: "chat_completions", wantPath: "/v1/chat/completions"},
		{name: "empty codex wire", backend: "codex", wireAPI: "", wantPath: "/v1/responses"},
		{name: "empty dsh wire", backend: "DSH", wireAPI: "", wantPath: "/v1/responses"},
		{name: "uppercase claude backend", backend: "CLAUDE", wireAPI: "anthropic_messages", wantPath: "/apps/anthropic/v1/messages"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var hits []string
			client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				mu.Lock()
				hits = append(hits, request.URL.Path)
				mu.Unlock()
				return healthJSONResponse(http.StatusOK, `{}`, nil), nil
			})}
			health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1",
				"https://gateway.example/apps/anthropic", "secret-key", "auto", testCase.backend, "model-x", testCase.wireAPI, 5*time.Second)
			if health.Status != ModelHealthOK {
				t.Fatalf("status=%q detail=%q hits=%v", health.Status, health.Detail, hits)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(hits) != 1 || hits[0] != testCase.wantPath {
				t.Fatalf("hits=%v want [%s]", hits, testCase.wantPath)
			}
		})
	}
}

func TestCheckModelHealthRejectsIncompatibleBackendWireAPI(t *testing.T) {
	t.Parallel()
	calls := 0
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return healthJSONResponse(http.StatusOK, `{}`, nil), nil
	})}
	health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
		"bearer", "claude", "model-x", "responses", 5*time.Second)
	if health.Status != ModelHealthInconclusive {
		t.Fatalf("status=%q want inconclusive", health.Status)
	}
	if calls != 0 {
		t.Fatalf("an impossible runtime configuration must not reach the gateway, calls=%d", calls)
	}
	if !strings.Contains(health.Detail, "anthropic_messages") {
		t.Fatalf("detail does not explain the mismatch: %q", health.Detail)
	}
}

// TestCheckModelHealthSendsCanonicalBody asserts the request shape and that
// anthropic_base_url becomes the runtime base (which the SDK extends with
// /v1/messages).
func TestCheckModelHealthSendsCanonicalBody(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	seen := make(map[string]map[string]any)
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return healthJSONResponse(http.StatusBadRequest, `{"error":"bad json"}`, nil), nil
		}
		mu.Lock()
		seen[request.URL.Path] = payload
		mu.Unlock()
		return healthJSONResponse(http.StatusOK, `{}`, nil), nil
	})}

	health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1",
		"https://gateway.example/apps/anthropic", "secret-key", "x-api-key", "claude", "alias-model", "anthropic_messages", 5*time.Second)
	if health.Status != ModelHealthOK {
		t.Fatalf("status=%q detail=%q", health.Status, health.Detail)
	}
	mu.Lock()
	payload, ok := seen["/apps/anthropic/v1/messages"]
	mu.Unlock()
	if !ok {
		t.Fatalf("anthropic_base_url was not used with the runtime path: %v", seen)
	}
	if model, _ := payload["model"].(string); model != "alias-model" {
		t.Fatalf("wire_model=%#v", payload["model"])
	}
	if maxTokens, _ := payload["max_tokens"].(float64); maxTokens < probeMaxOutputTokens {
		t.Fatalf("max_tokens=%#v, want >= %d", payload["max_tokens"], probeMaxOutputTokens)
	}

	// Responses keeps the official typed input array and an explicit
	// non-streaming first attempt.
	mu.Lock()
	seen = make(map[string]map[string]any)
	mu.Unlock()
	health = CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "",
		"secret-key", "bearer", "codex", "alias-model", "responses", 5*time.Second)
	if health.Status != ModelHealthOK {
		t.Fatalf("responses status=%q detail=%q", health.Status, health.Detail)
	}
	mu.Lock()
	responsesBody := seen["/v1/responses"]
	mu.Unlock()
	input, ok := responsesBody["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("responses input=%#v", responsesBody["input"])
	}
	message, _ := input[0].(map[string]any)
	content, _ := message["content"].([]any)
	part, _ := content[0].(map[string]any)
	if part["type"] != "input_text" || part["text"] != "ping" {
		t.Fatalf("responses content part=%#v", part)
	}
	if stream, ok := responsesBody["stream"].(bool); !ok || stream {
		t.Fatalf("first responses request stream=%#v, want false", responsesBody["stream"])
	}
}

// TestCheckModelHealthDoesNotProbeNonRuntimePath pins the review finding: the
// Codex runtime calls OPENAI_BASE_URL + /chat/completions exactly. A /v1
// prefix inferred from a bare host is not a real execution path, so reporting
// it "ok" would hide that the Task still fails.
func TestCheckModelHealthDoesNotProbeNonRuntimePath(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var hits []string
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		hits = append(hits, request.URL.Path)
		mu.Unlock()
		if request.URL.Path == "/v1/chat/completions" {
			return healthJSONResponse(http.StatusOK, `{}`, nil), nil
		}
		return healthJSONResponse(http.StatusNotFound, `{"error":{"message":"route not found"}}`, nil), nil
	})}
	health := CheckModelHealth(context.Background(), client, "https://gateway.example", "", "secret-key",
		"bearer", "codex", "model-x", "chat", 5*time.Second)
	if health.Status != ModelHealthModelNotFound {
		t.Fatalf("status=%q want model_not_found (detail=%q)", health.Status, health.Detail)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 || hits[0] != "/chat/completions" {
		t.Fatalf("health check probed a path the runtime never uses: %v", hits)
	}
}

// TestCheckModelHealthAnthropicUsesConfiguredBaseOnly pins the second half of
// the review finding: when anthropic_base_url is configured, runtime_selection
// uses only that base. A 200 on the plain provider base must not turn into ok.
func TestCheckModelHealthAnthropicUsesConfiguredBaseOnly(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var hits []string
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		hits = append(hits, request.URL.Path)
		mu.Unlock()
		if request.URL.Path == "/v1/messages" {
			return healthJSONResponse(http.StatusOK, `{}`, nil), nil
		}
		return healthJSONResponse(http.StatusNotFound, `{"error":{"message":"not found"}}`, nil), nil
	})}
	health := CheckModelHealth(context.Background(), client, "https://gateway.example",
		"https://gateway.example/apps/anthropic", "secret-key", "x-api-key",
		"claude", "model-x", "anthropic_messages", 5*time.Second)
	if health.Status == ModelHealthOK {
		t.Fatalf("configured entry failed but health reported ok: endpoint=%q hits=%v", health.Endpoint, hits)
	}
	if health.Status != ModelHealthModelNotFound {
		t.Fatalf("status=%q want model_not_found (detail=%q)", health.Status, health.Detail)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 || hits[0] != "/apps/anthropic/v1/messages" {
		t.Fatalf("health check fell back to a base the runtime does not use: %v", hits)
	}
}

// TestCheckModelHealthStreamingRetryKeepsRealTaskSemantics mirrors a gateway
// that only accepts Responses in streaming mode.
func TestCheckModelHealthStreamingRetryKeepsRealTaskSemantics(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var requests []map[string]any
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		mu.Lock()
		requests = append(requests, payload)
		mu.Unlock()
		if stream, _ := payload["stream"].(bool); !stream {
			return healthJSONResponse(http.StatusBadRequest, `{"error":{"message":"this model requires streaming"}}`, nil), nil
		}
		return probeSSEResponse("event: response.created\ndata: {\"type\":\"response.created\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"), nil
	})}
	health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
		"bearer", "codex", "model-x", "responses", 5*time.Second)
	if health.Status != ModelHealthOK {
		t.Fatalf("status=%q detail=%q", health.Status, health.Detail)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("requests=%d want 2 (non-streaming then streaming)", len(requests))
	}
	if stream, _ := requests[0]["stream"].(bool); stream {
		t.Fatalf("first request stream=%#v want false", requests[0]["stream"])
	}
	if stream, _ := requests[1]["stream"].(bool); !stream {
		t.Fatalf("retry stream=%#v want true", requests[1]["stream"])
	}
}

// TestCheckModelHealthGeneric400IsNotRetried keeps avoidable duplicate
// requests out of ordinary parameter rejections.
func TestCheckModelHealthGeneric400IsNotRetried(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	requests := 0
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests++
		mu.Unlock()
		return healthJSONResponse(http.StatusBadRequest, `{"error":{"message":"invalid max_tokens"}}`, nil), nil
	})}
	health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
		"bearer", "codex", "model-x", "responses", 5*time.Second)
	if health.Status != ModelHealthRejected {
		t.Fatalf("status=%q want rejected", health.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("a generic 400 was retried: requests=%d", requests)
	}
}

// TestCheckModelHealthStreamingRetryPreservesAccountErrors covers review
// finding 2: the retry's 401/402/5xx must replace the ambiguous 400.
func TestCheckModelHealthStreamingRetryPreservesAccountErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		code       int
		body       string
		wantStatus string
	}{
		{name: "stream unauthorized", code: http.StatusUnauthorized, body: `{"error":{"message":"invalid api key"}}`, wantStatus: ModelHealthUnauthorized},
		{name: "stream payment required", code: http.StatusPaymentRequired, body: `{"error":{"message":"quota exhausted"}}`, wantStatus: ModelHealthQuotaExhausted},
		{name: "stream server error", code: http.StatusBadGateway, body: `{"error":{"message":"upstream down"}}`, wantStatus: ModelHealthUnavailable},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				var payload map[string]any
				_ = json.NewDecoder(request.Body).Decode(&payload)
				if stream, _ := payload["stream"].(bool); !stream {
					return healthJSONResponse(http.StatusBadRequest, `{"error":{"message":"streaming is required"}}`, nil), nil
				}
				return healthJSONResponse(testCase.code, testCase.body, nil), nil
			})}
			health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
				"bearer", "codex", "model-x", "responses", 5*time.Second)
			if health.Status != testCase.wantStatus {
				t.Fatalf("status=%q want %q (http=%d detail=%q)", health.Status, testCase.wantStatus, health.HTTPStatus, health.Detail)
			}
			if health.HTTPStatus != testCase.code {
				t.Fatalf("http_status=%d want %d", health.HTTPStatus, testCase.code)
			}
		})
	}
}

// TestCheckModelHealthSSEErrorEvent is the other half of finding 2: HTTP 200
// plus data:{"type":"error"} must not be reported as ok.
func TestCheckModelHealthSSEErrorEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		data       string
		wantStatus string
	}{
		{name: "quota", data: `{"type":"error","error":{"message":"insufficient quota: 余额不足"}}`, wantStatus: ModelHealthQuotaExhausted},
		{name: "auth", data: `{"type":"error","error":{"message":"invalid api key","code":"unauthorized"}}`, wantStatus: ModelHealthUnauthorized},
		{name: "generic", data: `{"type":"error","error":{"message":"internal error"}}`, wantStatus: ModelHealthInconclusive},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				var payload map[string]any
				_ = json.NewDecoder(request.Body).Decode(&payload)
				if stream, _ := payload["stream"].(bool); !stream {
					return healthJSONResponse(http.StatusBadRequest, `{"error":{"message":"this model requires streaming"}}`, nil), nil
				}
				return probeSSEResponse("event: error\ndata: " + testCase.data + "\n\n"), nil
			})}
			health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
				"bearer", "codex", "model-x", "responses", 5*time.Second)
			if health.Status != testCase.wantStatus {
				t.Fatalf("status=%q want %q (detail=%q)", health.Status, testCase.wantStatus, health.Detail)
			}
			if !strings.Contains(health.Detail, "quota") && !strings.Contains(health.Detail, "error") && !strings.Contains(health.Detail, "api key") {
				t.Fatalf("SSE error evidence was lost: %q", health.Detail)
			}
		})
	}
}

// TestCheckModelHealthUsesRuntimeAuthHeader keeps the credential header equal
// to what the backend actually sends: OpenAI-compatible always Bearer, and
// Anthropic x-api-key unless the Provider chose bearer.
func TestCheckModelHealthUsesRuntimeAuthHeader(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		wireAPI    string
		authStyle  string
		wantHeader string
		wantValue  string
	}{
		{name: "chat auto uses bearer", wireAPI: "chat", authStyle: "auto", wantHeader: "Authorization", wantValue: "Bearer secret-key"},
		{name: "chat x-api-key still uses bearer", wireAPI: "chat", authStyle: "x-api-key", wantHeader: "Authorization", wantValue: "Bearer secret-key"},
		{name: "anthropic auto uses x-api-key", wireAPI: "anthropic_messages", authStyle: "auto", wantHeader: "x-api-key", wantValue: "secret-key"},
		{name: "anthropic bearer", wireAPI: "anthropic_messages", authStyle: "bearer", wantHeader: "Authorization", wantValue: "Bearer secret-key"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var got http.Header
			client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				got = request.Header.Clone()
				return healthJSONResponse(http.StatusOK, `{}`, nil), nil
			})}
			backend := "codex"
			if testCase.wireAPI == "anthropic_messages" {
				backend = "claude"
			}
			health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1",
				"https://gateway.example/apps/anthropic", "secret-key", testCase.authStyle,
				backend, "model-x", testCase.wireAPI, 5*time.Second)
			if health.Status != ModelHealthOK {
				t.Fatalf("status=%q detail=%q", health.Status, health.Detail)
			}
			if got.Get(testCase.wantHeader) != testCase.wantValue {
				t.Fatalf("%s=%q want %q", testCase.wantHeader, got.Get(testCase.wantHeader), testCase.wantValue)
			}
			other := "x-api-key"
			if testCase.wantHeader == "x-api-key" {
				other = "Authorization"
			}
			if got.Get(other) != "" {
				t.Fatalf("unexpected %s header %q", other, got.Get(other))
			}
		})
	}
}

// TestCheckModelHealthSSEResponseFailed covers the standard Responses failure
// envelope, whose error sits at response.failed.response.error rather than at
// the top level.
func TestCheckModelHealthSSEResponseFailed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		errorJSON  string
		wantStatus string
		wantDetail string
	}{
		{name: "quota", errorJSON: `{"code":"insufficient_quota","message":"You exceeded your current quota"}`, wantStatus: ModelHealthQuotaExhausted, wantDetail: "insufficient_quota"},
		{name: "auth", errorJSON: `{"code":"invalid_api_key","message":"Invalid API key"}`, wantStatus: ModelHealthUnauthorized, wantDetail: "invalid_api_key"},
		{name: "model", errorJSON: `{"code":"model_not_found","message":"The model gpt-x does not exist"}`, wantStatus: ModelHealthModelNotFound, wantDetail: "model_not_found"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				var payload map[string]any
				_ = json.NewDecoder(request.Body).Decode(&payload)
				if stream, _ := payload["stream"].(bool); !stream {
					return healthJSONResponse(http.StatusBadRequest, `{"error":{"message":"this model requires streaming"}}`, nil), nil
				}
				return probeSSEResponse("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_1\",\"status\":\"failed\",\"error\":" + testCase.errorJSON + "}}\n\n"), nil
			})}
			health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
				"bearer", "codex", "model-x", "responses", 5*time.Second)
			if health.Status != testCase.wantStatus {
				t.Fatalf("status=%q want %q (detail=%q)", health.Status, testCase.wantStatus, health.Detail)
			}
			if !strings.Contains(health.Detail, testCase.wantDetail) {
				t.Fatalf("detail lost %q: %q", testCase.wantDetail, health.Detail)
			}
		})
	}
}

// TestCheckModelHealthSSEAfterCreated keeps reading past a successful first
// event: the quota failure arrives only after response.created.
func TestCheckModelHealthSSEAfterCreated(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if stream, _ := payload["stream"].(bool); !stream {
			return healthJSONResponse(http.StatusBadRequest, `{"error":{"message":"streaming mode is required"}}`, nil), nil
		}
		body := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
			"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"insufficient_quota\",\"message\":\"余额不足\"}}}\n\n"
		return probeSSEResponse(body), nil
	})}
	health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
		"bearer", "codex", "model-x", "responses", 5*time.Second)
	if health.Status != ModelHealthQuotaExhausted {
		t.Fatalf("status=%q want quota_exhausted (detail=%q)", health.Status, health.Detail)
	}
}

func TestCheckModelHealthStreamWithoutTerminalIsInconclusive(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if stream, _ := payload["stream"].(bool); !stream {
			return healthJSONResponse(http.StatusBadRequest, `{"error":{"message":"must be streamed"}}`, nil), nil
		}
		return probeSSEResponse("event: response.created\ndata: {\"type\":\"response.created\"}\n\n"), nil
	})}
	health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
		"bearer", "codex", "model-x", "responses", 5*time.Second)
	if health.Status != ModelHealthInconclusive {
		t.Fatalf("status=%q want inconclusive (detail=%q)", health.Status, health.Detail)
	}
	if !strings.Contains(health.Detail, "terminal") {
		t.Fatalf("detail does not explain the missing terminal event: %q", health.Detail)
	}
}

// TestCheckModelHealthStreamOptions400IsNotRetried pins the review finding: a
// 400 that merely mentions a stream-related parameter is an ordinary rejection,
// not "this model requires streaming".
func TestCheckModelHealthStreamOptions400IsNotRetried(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	requests := 0
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests++
		mu.Unlock()
		return healthJSONResponse(http.StatusBadRequest, `{"error":{"message":"invalid stream_options parameter"}}`, nil), nil
	})}
	health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
		"bearer", "codex", "model-x", "responses", 5*time.Second)
	if health.Status != ModelHealthRejected {
		t.Fatalf("status=%q want rejected (detail=%q)", health.Status, health.Detail)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("stream_options 400 was retried: requests=%d", requests)
	}
}

// TestModelHealthRequiresStreamingMatcher pins both directions of the
// heuristic: field-name rejections must not retry, negations must not retry,
// and explicit Chinese "only streaming" wording must retry.
func TestModelHealthRequiresStreamingMatcher(t *testing.T) {
	t.Parallel()
	for _, detail := range []string{
		"this model requires streaming",
		"streaming mode is required",
		"streaming is required",
		"must be streamed",
		"must use stream",
		"only supports streaming",
		"只支持流式",
		"仅支持流式",
		"必须使用流式",
		"需要使用流式",
		"必须流式",
	} {
		if !modelHealthRequiresStreaming(detail) {
			t.Errorf("%q should trigger the streaming retry", detail)
		}
	}
	for _, detail := range []string{
		"",
		`{"error":{"message":"invalid stream_options parameter"}}`,
		`{"error":{"message":"invalid streaming mode parameter"}}`,
		"不需要流式",
		"无需使用流式",
		"不需要使用流式",
		"streaming is not required",
		"no need to stream",
		"this request does not require streaming",
	} {
		if modelHealthRequiresStreaming(detail) {
			t.Errorf("%q must not trigger the streaming retry", detail)
		}
	}
}

// TestCheckModelHealthStreamingRetryRejectionsAreSingleRequest is the request
// count side of the matcher: only a real "must stream" message retries.
func TestCheckModelHealthStreamingRetryRejectionsAreSingleRequest(t *testing.T) {
	t.Parallel()
	for _, message := range []string{
		"invalid stream_options parameter",
		"invalid streaming mode parameter",
		"不需要流式",
	} {
		message := message
		t.Run(message, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			requests := 0
			client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				mu.Lock()
				requests++
				mu.Unlock()
				return healthJSONResponse(http.StatusBadRequest, `{"error":{"message":"`+message+`"}}`, nil), nil
			})}
			health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
				"bearer", "codex", "model-x", "responses", 5*time.Second)
			if health.Status != ModelHealthRejected {
				t.Fatalf("status=%q want rejected (detail=%q)", health.Status, health.Detail)
			}
			mu.Lock()
			defer mu.Unlock()
			if requests != 1 {
				t.Fatalf("message %q was retried: requests=%d", message, requests)
			}
		})
	}
}

func TestCheckModelHealthChineseStreamingOnlyRetries(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	requests := 0
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		mu.Lock()
		requests++
		mu.Unlock()
		if stream, _ := payload["stream"].(bool); !stream {
			return healthJSONResponse(http.StatusBadRequest, `{"error":{"message":"该模型只支持流式调用"}}`, nil), nil
		}
		return probeSSEResponse("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"), nil
	})}
	health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
		"bearer", "codex", "model-x", "responses", 5*time.Second)
	if health.Status != ModelHealthOK {
		t.Fatalf("status=%q detail=%q", health.Status, health.Detail)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("requests=%d want 2 (non-streaming plus one streaming retry)", requests)
	}
}

// TestCheckModelHealthRejectsUnknownBackend covers the runtime rule that an
// unknown or empty backend never starts a Task, so reporting a live endpoint
// as ok would be a guaranteed false positive.
func TestCheckModelHealthRejectsUnknownBackend(t *testing.T) {
	t.Parallel()
	for _, backend := range []string{"openai", ""} {
		backend := backend
		t.Run("backend="+backend, func(t *testing.T) {
			t.Parallel()
			calls := 0
			client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				return healthJSONResponse(http.StatusOK, `{}`, nil), nil
			})}
			health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
				"bearer", backend, "model-x", "", 5*time.Second)
			if health.Status != ModelHealthInconclusive {
				t.Fatalf("status=%q want inconclusive (detail=%q)", health.Status, health.Detail)
			}
			if calls != 0 {
				t.Fatalf("unknown backend %q reached the gateway, calls=%d", backend, calls)
			}
			if !strings.Contains(health.Detail, "backend") {
				t.Fatalf("detail does not name the backend problem: %q", health.Detail)
			}
		})
	}
}

func TestCheckModelHealthRedactionOnNonBodyEvidence(t *testing.T) {
	t.Parallel()
	secret := "secret-key"
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New(`Post "https://user:` + secret + `@gateway.example/v1/chat/completions?api_key=` + secret + `": dial tcp: lookup gateway.example: no such host`)
	})}
	health := CheckModelHealth(context.Background(), client, "https://user:"+secret+"@gateway.example/v1?token="+secret, "",
		secret, "bearer", "codex", "model-x", "chat", 5*time.Second)
	if health.Status != ModelHealthUnavailable {
		t.Fatalf("status=%q want unavailable", health.Status)
	}
	for name, value := range map[string]string{"endpoint": health.Endpoint, "detail": health.Detail} {
		if strings.Contains(value, secret) {
			t.Fatalf("%s leaked the API key: %q", name, value)
		}
	}
	if strings.Contains(health.Detail, "user:") {
		t.Fatalf("detail kept URL userinfo: %q", health.Detail)
	}
}

func TestCheckModelHealthMissingCredentialIsUnauthorized(t *testing.T) {
	t.Parallel()
	health := CheckModelHealth(context.Background(), nil, "https://gateway.example/v1", "", "",
		"bearer", "codex", "model-x", "chat", 5*time.Second)
	if health.Status != ModelHealthUnauthorized {
		t.Fatalf("status=%q want unauthorized", health.Status)
	}
	if health.HTTPStatus != 0 {
		t.Fatalf("http_status=%d want 0", health.HTTPStatus)
	}
}
