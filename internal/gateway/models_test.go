package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExtractModelsOpenAIStyle(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"data": []any{
			map[string]any{"id": "gpt-4o", "max_input_tokens": float64(128000), "max_output_tokens": float64(16384)},
			map[string]any{"id": "gpt-4o-mini"},
			map[string]any{"id": "gpt-4o"},
			"claude-3-5-sonnet",
		},
	}
	models := extractModels(payload)
	if len(models) != 3 {
		t.Fatalf("expected 3 deduplicated models, got %d: %#v", len(models), models)
	}
	if models[0].ID != "gpt-4o" || models[0].MaxInputTokens != 128000 {
		t.Fatalf("unexpected first model: %#v", models[0])
	}
	if models[1].ID != "gpt-4o-mini" {
		t.Fatalf("unexpected second model: %#v", models[1])
	}
	if models[2].ID != "claude-3-5-sonnet" {
		t.Fatalf("unexpected string model: %#v", models[2])
	}
}

func TestDetectModelsTriesAuthAndEndpoints(t *testing.T) {
	t.Parallel()
	// Gateway that only answers /v1/models with a Bearer token.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer secret-key" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprint(writer, `{"data":[{"id":"model-a"},{"id":"model-b"}]}`)
	}))
	defer server.Close()

	result, err := DetectModels(server.URL, "secret-key", "auto", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthStyle != "bearer" {
		t.Fatalf("expected bearer auth style, got %q", result.AuthStyle)
	}
	if len(result.Models) != 2 || result.Models[0].ID != "model-a" {
		t.Fatalf("unexpected models: %#v", result.Models)
	}
}

func TestDetectModelsAnthropicStyle(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/models" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("x-api-key") != "secret-key" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprint(writer, `{"data":[{"id":"claude-opus-5"}]}`)
	}))
	defer server.Close()

	result, err := DetectModels(server.URL, "secret-key", "auto", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthStyle != "x-api-key" {
		t.Fatalf("expected x-api-key auth style, got %q", result.AuthStyle)
	}
	if len(result.Models) != 1 || result.Models[0].ID != "claude-opus-5" {
		t.Fatalf("unexpected models: %#v", result.Models)
	}
}

func TestDetectModelsRejectsEmptyBaseURL(t *testing.T) {
	t.Parallel()
	if _, err := DetectModels("", "key", "auto", time.Second); err == nil {
		t.Fatal("expected error for empty base_url")
	}
}

func TestProbeModelCapabilitiesRunsProtocolsConcurrentlyAndReportsTimeout(t *testing.T) {
	t.Parallel()
	var active, peak int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&active, 1)
		defer atomic.AddInt32(&active, -1)
		for {
			old := atomic.LoadInt32(&peak)
			if current <= old || atomic.CompareAndSwapInt32(&peak, old, current) {
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
		switch r.URL.Path {
		case "/responses", "/chat/completions", "/v1/messages":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	caps, _, errs := ProbeModelCapabilitiesContextWithClientDetailed(
		context.Background(), server.URL, "secret", "bearer", "model", 3*time.Second, nil)
	if caps["responses"] != "supported" || caps["chat_completions"] != "supported" ||
		caps["anthropic_messages"] != "supported" || len(errs) != 0 {
		t.Fatalf("caps=%v errs=%v", caps, errs)
	}
	if atomic.LoadInt32(&peak) < 2 {
		t.Fatalf("probe did not run protocols concurrently: peak=%d", peak)
	}

	caps, _, errs = ProbeModelCapabilitiesContextWithClientDetailed(
		context.Background(), server.URL, "secret", "bearer", "model", 100*time.Millisecond, nil)
	for _, name := range []string{"responses", "chat_completions", "anthropic_messages"} {
		if caps[name] != "unavailable" || errs[name] == "" {
			t.Fatalf("%s caps=%v errs=%v", name, caps, errs)
		}
	}
}

func TestModelDetectionRequestsHonorContextCancellation(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		started <- struct{}{}
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	t.Run("catalog", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := DetectModelsContext(ctx, server.URL, "secret", "bearer", time.Minute)
			done <- err
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("catalog request did not start")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("catalog cancellation error=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("catalog request ignored cancellation")
		}
	})

	t.Run("capability", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			ProbeModelCapabilitiesContext(ctx, server.URL, "secret", "bearer", "model", time.Minute)
			close(done)
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("capability request did not start")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("capability request ignored cancellation")
		}
	})
}

// probeTestServer serves the OpenAI protocols and records which Anthropic
// entry points were hit, so a test can prove which base URL was probed.
func newProbeTestServer(t *testing.T, anthropicStatus map[string]int) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var anthropicPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/responses", "/chat/completions":
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/messages") {
			mu.Lock()
			anthropicPaths = append(anthropicPaths, r.URL.Path)
			mu.Unlock()
			if code, ok := anthropicStatus[r.URL.Path]; ok {
				w.WriteHeader(code)
				return
			}
			http.NotFound(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), anthropicPaths...)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestProbeModelCapabilitiesPrefersConfiguredAnthropicBaseURL(t *testing.T) {
	t.Parallel()
	base, baseAnthropicPaths := newProbeTestServer(t, nil)
	anthropic, anthropicPaths := newProbeTestServer(t, map[string]int{
		"/apps/anthropic/v1/messages": http.StatusOK,
	})

	caps, anthropicBase, errs := ProbeModelCapabilitiesForProviderContext(
		context.Background(), base.URL+"/compatible-mode/v1", anthropic.URL+"/apps/anthropic",
		"secret", "auto", "claude-model", 5*time.Second, nil)
	if caps["anthropic_messages"] != "supported" {
		t.Fatalf("caps=%v errs=%v", caps, errs)
	}
	if !containsString(anthropicPaths(), "/apps/anthropic/v1/messages") {
		t.Fatalf("configured anthropic base was not probed: %v", anthropicPaths())
	}
	if len(baseAnthropicPaths()) != 0 {
		t.Fatalf("base_url was probed for anthropic_messages despite configured anthropic_base_url: %v", baseAnthropicPaths())
	}
	// The configured entry point worked first, so no fallback base is reported.
	if anthropicBase != "" {
		t.Fatalf("anthropicBase=%q, want empty for the configured base", anthropicBase)
	}
}

func TestProbeModelCapabilitiesConfiguredAnthropicBaseEndingWithV1(t *testing.T) {
	t.Parallel()
	base, _ := newProbeTestServer(t, nil)
	anthropic, anthropicPaths := newProbeTestServer(t, map[string]int{
		"/apps/anthropic/v1/messages": http.StatusOK,
	})

	caps, _, errs := ProbeModelCapabilitiesForProviderContext(
		context.Background(), base.URL+"/v1", anthropic.URL+"/apps/anthropic",
		"secret", "bearer", "claude-model", 5*time.Second, nil)
	if caps["anthropic_messages"] != "supported" {
		t.Fatalf("caps=%v errs=%v", caps, errs)
	}
	if !containsString(anthropicPaths(), "/apps/anthropic/v1/messages") {
		t.Fatalf("configured base did not fall back to /v1/messages: %v", anthropicPaths())
	}
}

func TestProbeModelCapabilitiesDerivesAnthropicBaseWhenUnset(t *testing.T) {
	t.Parallel()
	base, basePaths := newProbeTestServer(t, map[string]int{
		"/anthropic/v1/messages": http.StatusOK,
	})

	caps, anthropicBase, errs := ProbeModelCapabilitiesForProviderContext(
		context.Background(), base.URL+"/v1", "", "secret", "auto", "claude-model", 5*time.Second, nil)
	if caps["anthropic_messages"] != "supported" {
		t.Fatalf("caps=%v errs=%v", caps, errs)
	}
	if anthropicBase != base.URL+"/anthropic" {
		t.Fatalf("anthropicBase=%q, want %q", anthropicBase, base.URL+"/anthropic")
	}
	if !containsString(basePaths(), "/anthropic/v1/messages") {
		t.Fatalf("derived /anthropic base was not probed: %v", basePaths())
	}
}

func TestProbeModelCapabilitiesConfiguredAnthropicBaseHonorsAuthStyle(t *testing.T) {
	t.Parallel()
	base, _ := newProbeTestServer(t, nil)
	var mu sync.Mutex
	var seen []http.Header
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apps/anthropic/v1/messages" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		if r.Header.Get("Authorization") == "Bearer secret" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(anthropic.Close)
	headers := func() []http.Header {
		mu.Lock()
		defer mu.Unlock()
		return append([]http.Header(nil), seen...)
	}

	caps, _, errs := ProbeModelCapabilitiesForProviderContext(
		context.Background(), base.URL, anthropic.URL+"/apps/anthropic", "secret", "bearer", "claude-model", 5*time.Second, nil)
	if caps["anthropic_messages"] != "supported" {
		t.Fatalf("caps=%v errs=%v", caps, errs)
	}
	for _, header := range headers() {
		if header.Get("x-api-key") != "" {
			t.Fatalf("auth_style=bearer also sent x-api-key: %v", header)
		}
	}

	mu.Lock()
	seen = nil
	mu.Unlock()
	caps, _, _ = ProbeModelCapabilitiesForProviderContext(
		context.Background(), base.URL, anthropic.URL+"/apps/anthropic", "secret", "x-api-key", "claude-model", 5*time.Second, nil)
	if caps["anthropic_messages"] != "unauthorized" {
		t.Fatalf("caps=%v, want unauthorized for the narrowed auth style", caps)
	}
	for _, header := range headers() {
		if header.Get("Authorization") != "" {
			t.Fatalf("auth_style=x-api-key also sent bearer: %v", header)
		}
	}
}

func TestAnthropicBaseCandidatesOrderAndDedup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		base       string
		configured string
		want       []string
	}{
		{
			name: "configured first then derived",
			base: "https://gateway.example/v1", configured: "https://gateway.example/apps/anthropic",
			want: []string{"https://gateway.example/apps/anthropic", "https://gateway.example/v1", "https://gateway.example/anthropic"},
		},
		{
			name: "empty configured keeps legacy order",
			base: "https://gateway.example/v1", configured: "   ",
			want: []string{"https://gateway.example/v1", "https://gateway.example/anthropic"},
		},
		{
			name: "configured equals base is deduped",
			base: "https://gateway.example/v1", configured: "https://gateway.example/v1/",
			want: []string{"https://gateway.example/v1", "https://gateway.example/anthropic"},
		},
		{
			name: "base already anthropic suffix",
			base: "https://gateway.example/anthropic", configured: "",
			want: []string{"https://gateway.example/anthropic"},
		},
		{
			name: "base already anthropic suffix with duplicate configured",
			base: "https://gateway.example/anthropic", configured: "https://gateway.example/anthropic",
			want: []string{"https://gateway.example/anthropic"},
		},
		{
			name: "empty urls",
			base: "", configured: "",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := anthropicBaseCandidates(tc.base, tc.configured); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("anthropicBaseCandidates(%q, %q) = %v, want %v", tc.base, tc.configured, got, tc.want)
			}
		})
	}
}

// newAnthropicProbeServer serves the OpenAI protocols and answers every
// Anthropic path with the configured status, so a test can drive the
// protocol_errors evidence for each failure mode.
func newAnthropicProbeServer(t *testing.T, anthropicStatus int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/responses", "/chat/completions":
			w.WriteHeader(http.StatusOK)
		default:
			if strings.HasSuffix(r.URL.Path, "/messages") {
				w.WriteHeader(anthropicStatus)
				return
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestProbeModelCapabilitiesReportsAllNotFoundCandidates(t *testing.T) {
	t.Parallel()
	base := newAnthropicProbeServer(t, http.StatusNotFound)
	anthropic := newAnthropicProbeServer(t, http.StatusNotFound)

	caps, _, errs := ProbeModelCapabilitiesForProviderContext(
		context.Background(), base.URL+"/v1", anthropic.URL+"/apps/anthropic",
		"secret", "auto", "claude-model", 5*time.Second, nil)
	if caps["anthropic_messages"] != "unsupported" {
		t.Fatalf("caps=%v errs=%v", caps, errs)
	}
	evidence := errs["anthropic_messages"]
	for _, want := range []string{
		anthropic.URL + "/apps/anthropic: HTTP 404",
		base.URL + "/v1: HTTP 404",
		base.URL + "/anthropic: HTTP 404",
	} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("protocol_errors missing %q: %q", want, evidence)
		}
	}
	if strings.Contains(evidence, "secret") {
		t.Fatalf("protocol_errors leaked the API key: %q", evidence)
	}
}

func TestProbeModelCapabilitiesReportsUnauthorizedCandidate(t *testing.T) {
	t.Parallel()
	base := newAnthropicProbeServer(t, http.StatusNotFound)
	anthropic := newAnthropicProbeServer(t, http.StatusUnauthorized)

	caps, _, errs := ProbeModelCapabilitiesForProviderContext(
		context.Background(), base.URL+"/v1", anthropic.URL+"/apps/anthropic",
		"secret", "auto", "claude-model", 5*time.Second, nil)
	if caps["anthropic_messages"] != "unauthorized" {
		t.Fatalf("caps=%v errs=%v", caps, errs)
	}
	evidence := errs["anthropic_messages"]
	if !strings.Contains(evidence, anthropic.URL+"/apps/anthropic: HTTP 401") {
		t.Fatalf("protocol_errors missing the unauthorized candidate: %q", evidence)
	}
	if strings.Contains(evidence, "secret") {
		t.Fatalf("protocol_errors leaked the API key: %q", evidence)
	}
}

func TestProbeModelCapabilitiesReportsFallbackServerErrors(t *testing.T) {
	t.Parallel()
	base := newAnthropicProbeServer(t, http.StatusInternalServerError)
	anthropic := newAnthropicProbeServer(t, http.StatusNotFound)

	caps, _, errs := ProbeModelCapabilitiesForProviderContext(
		context.Background(), base.URL+"/v1", anthropic.URL+"/apps/anthropic",
		"secret", "auto", "claude-model", 5*time.Second, nil)
	if caps["anthropic_messages"] != "unavailable" {
		t.Fatalf("caps=%v errs=%v", caps, errs)
	}
	evidence := errs["anthropic_messages"]
	for _, want := range []string{
		anthropic.URL + "/apps/anthropic: HTTP 404",
		base.URL + "/v1: HTTP 500",
	} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("protocol_errors missing %q: %q", want, evidence)
		}
	}
	if strings.Contains(evidence, "secret") {
		t.Fatalf("protocol_errors leaked the API key: %q", evidence)
	}
}

// probeRoundTripFunc lets a test answer protocol probes without a real server
// and inspect the request body the probe actually sent.
type probeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f probeRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func probeJSONResponse(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Status:     fmt.Sprintf("%d %s", code, http.StatusText(code)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func probeSSEResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestProbeWireAPIUsesCanonicalRequestBodies(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	seen := make(map[string]map[string]any)
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return probeJSONResponse(http.StatusBadRequest, `{"error":"invalid json"}`), nil
		}
		mu.Lock()
		seen[request.URL.Path] = payload
		mu.Unlock()
		switch {
		case strings.HasSuffix(request.URL.Path, "/responses"), strings.HasSuffix(request.URL.Path, "/chat/completions"):
			return probeJSONResponse(http.StatusOK, `{}`), nil
		default:
			return probeJSONResponse(http.StatusNotFound, `not found`), nil
		}
	})}

	caps, _, _ := ProbeModelCapabilitiesForProviderContext(
		context.Background(), "https://gateway.example/v1", "", "secret-key", "bearer", "model-x", 5*time.Second, client)
	if caps["responses"] != "supported" || caps["chat_completions"] != "supported" {
		t.Fatalf("caps=%v", caps)
	}

	mu.Lock()
	defer mu.Unlock()
	responsesBody, ok := seen["/v1/responses"]
	if !ok {
		t.Fatalf("responses probe was not sent: %v", seen)
	}
	input, ok := responsesBody["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("responses input is not a one-message array: %#v", responsesBody["input"])
	}
	message, _ := input[0].(map[string]any)
	if message["role"] != "user" {
		t.Fatalf("responses input role=%#v", message["role"])
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("responses input content is not a typed part array: %#v", message["content"])
	}
	part, _ := content[0].(map[string]any)
	if part["type"] != "input_text" || part["text"] != "ping" {
		t.Fatalf("responses input part=%#v", part)
	}
	if maxOutput, _ := responsesBody["max_output_tokens"].(float64); maxOutput < probeMaxOutputTokens {
		t.Fatalf("responses max_output_tokens=%#v, want >= %d", responsesBody["max_output_tokens"], probeMaxOutputTokens)
	}
	if stream, ok := responsesBody["stream"]; !ok || stream != false {
		t.Fatalf("responses stream=%#v, want an explicit false", responsesBody["stream"])
	}

	chatBody, ok := seen["/v1/chat/completions"]
	if !ok {
		t.Fatalf("chat probe was not sent: %v", seen)
	}
	if maxTokens, _ := chatBody["max_tokens"].(float64); maxTokens < probeMaxOutputTokens {
		t.Fatalf("chat max_tokens=%#v, want >= %d", chatBody["max_tokens"], probeMaxOutputTokens)
	}
	if messages, ok := chatBody["messages"].([]any); !ok || len(messages) != 1 {
		t.Fatalf("chat messages=%#v", chatBody["messages"])
	}

	anthropicBody, ok := seen["/v1/messages"]
	if !ok {
		t.Fatalf("anthropic probe was not sent: %v", seen)
	}
	if maxTokens, _ := anthropicBody["max_tokens"].(float64); maxTokens < probeMaxOutputTokens {
		t.Fatalf("anthropic max_tokens=%#v, want >= %d", anthropicBody["max_tokens"], probeMaxOutputTokens)
	}
}

// TestProbeResponsesRejectsLegacyBodyAndRetriesStreaming emulates a strict
// gateway: the legacy probe shape (bare input string or a 1-token budget) and
// non-streaming Responses requests are all rejected with 400. Only the
// canonical body plus stream=true succeeds, and the probe must read its first
// SSE event and report the protocol as supported.
func TestProbeResponsesRejectsLegacyBodyAndRetriesStreaming(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var responsesRequests []map[string]any
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return probeJSONResponse(http.StatusBadRequest, `{"error":{"message":"invalid json"}}`), nil
		}
		switch {
		case strings.HasSuffix(request.URL.Path, "/responses"):
			mu.Lock()
			responsesRequests = append(responsesRequests, payload)
			mu.Unlock()
			if _, legacy := payload["input"].(string); legacy {
				return probeJSONResponse(http.StatusBadRequest, `{"error":{"message":"input must be an array"}}`), nil
			}
			if input, ok := payload["input"].([]any); !ok || len(input) == 0 {
				return probeJSONResponse(http.StatusBadRequest, `{"error":{"message":"input must be a non-empty array"}}`), nil
			}
			if maxOutput, _ := payload["max_output_tokens"].(float64); maxOutput < probeMaxOutputTokens {
				return probeJSONResponse(http.StatusBadRequest, `{"error":{"message":"max_output_tokens must be at least 16"}}`), nil
			}
			if stream, _ := payload["stream"].(bool); !stream {
				return probeJSONResponse(http.StatusBadRequest, `{"error":{"message":"this model requires streaming"}}`), nil
			}
			return probeSSEResponse("event: response.created\ndata: {\"type\":\"response.created\"}\n\n"), nil
		case strings.HasSuffix(request.URL.Path, "/chat/completions"):
			if maxTokens, _ := payload["max_tokens"].(float64); maxTokens < probeMaxOutputTokens {
				return probeJSONResponse(http.StatusBadRequest, `{"error":{"message":"max_tokens must be at least 16"}}`), nil
			}
			return probeJSONResponse(http.StatusOK, `{}`), nil
		default:
			return probeJSONResponse(http.StatusNotFound, `not found`), nil
		}
	})}

	caps, _, errs := ProbeModelCapabilitiesForProviderContext(
		context.Background(), "https://gateway.example/v1", "", "secret-key", "bearer", "model-x", 5*time.Second, client)
	if caps["responses"] != "supported" {
		t.Fatalf("caps=%v errs=%v, want responses supported through the streaming retry", caps, errs)
	}
	if caps["chat_completions"] != "supported" {
		t.Fatalf("caps=%v errs=%v, want chat_completions supported", caps, errs)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(responsesRequests) != 2 {
		t.Fatalf("responses requests=%d, want the rejected non-streaming request plus one streaming retry: %#v", len(responsesRequests), responsesRequests)
	}
	if stream, _ := responsesRequests[0]["stream"].(bool); stream {
		t.Fatalf("first responses request stream=%#v, want false", responsesRequests[0]["stream"])
	}
	if stream, _ := responsesRequests[1]["stream"].(bool); !stream {
		t.Fatalf("retry responses request stream=%#v, want true", responsesRequests[1]["stream"])
	}
	if _, ok := responsesRequests[1]["input"].([]any); !ok {
		t.Fatalf("streaming retry lost the canonical input array: %#v", responsesRequests[1]["input"])
	}
	if maxOutput, _ := responsesRequests[1]["max_output_tokens"].(float64); maxOutput < probeMaxOutputTokens {
		t.Fatalf("streaming retry max_output_tokens=%#v", responsesRequests[1]["max_output_tokens"])
	}
}

func TestProbeResponses400EvidenceIsTruncatedAndRedacted(t *testing.T) {
	t.Parallel()
	longMessage := strings.Repeat("参数错误", 300)
	var mu sync.Mutex
	responsesHits := 0
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/responses"):
			mu.Lock()
			responsesHits++
			mu.Unlock()
			return probeJSONResponse(http.StatusBadRequest,
				`{"error":{"api_key":"secret-key","message":"`+longMessage+`"}}`), nil
		case strings.HasSuffix(request.URL.Path, "/chat/completions"):
			return probeJSONResponse(http.StatusOK, `{}`), nil
		default:
			return probeJSONResponse(http.StatusNotFound, `not found`), nil
		}
	})}

	caps, _, errs := ProbeModelCapabilitiesForProviderContext(
		context.Background(), "https://gateway.example/v1", "", "secret-key", "bearer", "model-x", 5*time.Second, client)
	if caps["responses"] != "inconclusive" {
		t.Fatalf("caps=%v errs=%v", caps, errs)
	}
	evidence := errs["responses"]
	if !strings.Contains(evidence, "HTTP 400") || !strings.Contains(evidence, "参数错误") {
		t.Fatalf("evidence lost the 400 body: %q", evidence)
	}
	if strings.Contains(evidence, "secret-key") {
		t.Fatalf("evidence leaked the API key: %q", evidence)
	}
	if !strings.Contains(evidence, "[redacted]") {
		t.Fatalf("evidence did not mark the redacted key: %q", evidence)
	}
	prefix := "https://gateway.example/v1/responses: HTTP 400: "
	if limit := len([]rune(prefix)) + probeErrorBodyMaxRunes + 2; len([]rune(evidence)) > limit {
		t.Fatalf("evidence is not truncated: %d runes > %d", len([]rune(evidence)), limit)
	}
	mu.Lock()
	defer mu.Unlock()
	if responsesHits != 2 {
		t.Fatalf("responses hits=%d, want the 400 plus exactly one streaming retry", responsesHits)
	}
}

// probeBlockingBody never produces an event; it only unblocks when the request
// context expires, so the test can prove the streaming probe has a deadline.
type probeBlockingBody struct{ ctx context.Context }

func (b *probeBlockingBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *probeBlockingBody) Close() error { return nil }

func TestProbeResponsesStreamingProbeHonorsTimeout(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	client := &http.Client{Transport: probeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/responses"):
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				return probeJSONResponse(http.StatusBadRequest, `{"error":"invalid json"}`), nil
			}
			if stream, _ := payload["stream"].(bool); !stream {
				return probeJSONResponse(http.StatusBadRequest, `{"error":{"message":"stream required"}}`), nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       &probeBlockingBody{ctx: request.Context()},
			}, nil
		case strings.HasSuffix(request.URL.Path, "/chat/completions"):
			return probeJSONResponse(http.StatusOK, `{}`), nil
		default:
			return probeJSONResponse(http.StatusNotFound, `not found`), nil
		}
	})}

	start := time.Now()
	caps, _, _ := ProbeModelCapabilitiesForProviderContext(
		ctx, "https://gateway.example/v1", "", "secret-key", "bearer", "model-x", 5*time.Second, client)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("streaming probe did not honor its deadline: %v", elapsed)
	}
	if caps["responses"] != "inconclusive" {
		t.Fatalf("caps=%v, want inconclusive for a stream that never emits an event", caps)
	}
}
