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
		{name: "rate limited", code: http.StatusTooManyRequests, body: `{"error":{"message":"slow down"}}`, headers: http.Header{"Retry-After": []string{"42"}}, wantStatus: ModelHealthRateLimited, wantRetry: 42},
		{name: "unauthorized", code: http.StatusUnauthorized, body: `{"error":{"message":"invalid key"}}`, wantStatus: ModelHealthUnauthorized},
		{name: "forbidden", code: http.StatusForbidden, body: `{"error":{"message":"no access"}}`, wantStatus: ModelHealthUnauthorized},
		{name: "payment required", code: http.StatusPaymentRequired, body: `{"error":{"message":"pay up"}}`, wantStatus: ModelHealthQuotaExhausted},
		{name: "400 quota keyword", code: http.StatusBadRequest, body: `{"error":{"message":"insufficient_quota: 账户余额不足，请充值"}}`, wantStatus: ModelHealthQuotaExhausted},
		{name: "400 quota with echoed key", code: http.StatusBadRequest, body: `{"error":{"message":"billing hard limit reached","api_key":"` + secret + `"}}`, wantStatus: ModelHealthQuotaExhausted},
		{name: "400 rejected parameters", code: http.StatusBadRequest, body: `{"error":{"message":"invalid max_tokens: must be >= 16"}}`, wantStatus: ModelHealthRejected},
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
				"bearer", "model-x", "chat_completions", 5*time.Second)
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
		"bearer", "model-x", "chat_completions", 50*time.Millisecond)
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

// TestCheckModelHealthSendsCanonicalBodyAndPrefersAnthropicBase asserts the
// request shape and the endpoint priority the UI relies on.
func TestCheckModelHealthSendsCanonicalBodyAndPrefersAnthropicBase(t *testing.T) {
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
		"https://gateway.example/apps/anthropic", "secret-key", "x-api-key", "alias-model", "anthropic_messages", 5*time.Second)
	if health.Status != ModelHealthOK {
		t.Fatalf("status=%q detail=%q", health.Status, health.Detail)
	}
	mu.Lock()
	if len(seen) != 1 {
		mu.Unlock()
		t.Fatalf("expected one request, got %v", seen)
	}
	payload, ok := seen["/apps/anthropic/messages"]
	mu.Unlock()
	if !ok {
		t.Fatalf("anthropic_base_url was not preferred: %v", seen)
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
		"secret-key", "bearer", "alias-model", "responses", 5*time.Second)
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

// TestCheckModelHealthFallsBackToV1EndpointForm covers bare-host provider
// bases: the first form 404s, the /v1 form answers.
func TestCheckModelHealthFallsBackToV1EndpointForm(t *testing.T) {
	t.Parallel()
	var hits []string
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		hits = append(hits, request.URL.Path)
		if request.URL.Path == "/v1/chat/completions" {
			return healthJSONResponse(http.StatusOK, `{}`, nil), nil
		}
		return healthJSONResponse(http.StatusNotFound, `{"error":{"message":"route not found"}}`, nil), nil
	})}
	health := CheckModelHealth(context.Background(), client, "https://gateway.example", "", "secret-key",
		"bearer", "model-x", "chat_completions", 5*time.Second)
	if health.Status != ModelHealthOK {
		t.Fatalf("status=%q detail=%q hits=%v", health.Status, health.Detail, hits)
	}
	if len(hits) != 2 || hits[0] != "/chat/completions" || hits[1] != "/v1/chat/completions" {
		t.Fatalf("unexpected endpoint order: %v", hits)
	}
}

// TestCheckModelHealthStreamingRetryKeepsRealTaskSemantics mirrors the
// protocol probe: a gateway that only accepts Responses in streaming mode must
// not look "rejected" to the health check.
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
		return probeSSEResponse("event: response.created\ndata: {\"type\":\"response.created\"}\n\n"), nil
	})}
	health := CheckModelHealth(context.Background(), client, "https://gateway.example/v1", "", "secret-key",
		"bearer", "model-x", "responses", 5*time.Second)
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

func TestCheckModelHealthRedactionOnNonBodyEvidence(t *testing.T) {
	t.Parallel()
	secret := "secret-key"
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New(`Post "https://user:` + secret + `@gateway.example/v1/chat/completions?api_key=` + secret + `": dial tcp: lookup gateway.example: no such host`)
	})}
	health := CheckModelHealth(context.Background(), client, "https://user:"+secret+"@gateway.example/v1?token="+secret, "",
		secret, "bearer", "model-x", "chat_completions", 5*time.Second)
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
		"bearer", "model-x", "chat_completions", 5*time.Second)
	if health.Status != ModelHealthUnauthorized {
		t.Fatalf("status=%q want unauthorized", health.Status)
	}
	if health.HTTPStatus != 0 {
		t.Fatalf("http_status=%d want 0", health.HTTPStatus)
	}
}
