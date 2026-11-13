package gateway

import (
	"context"
	"errors"
	"fmt"
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

func TestProbeStatusForCodeClassifiesProtocolRejections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code int
		want string
	}{
		{http.StatusOK, "supported"},
		{http.StatusUnauthorized, "unauthorized"},
		{http.StatusForbidden, "unauthorized"},
		{http.StatusTooManyRequests, "rate_limited"},
		// A bare 400 is ambiguous: it can be an unsupported protocol, an
		// unknown model, bad parameters, or an auth/gateway policy rejection.
		// It must not be classified as an absent endpoint.
		{http.StatusBadRequest, "rejected"},
		// Missing route/method/implementation means the protocol endpoint is
		// not offered; none of these are network faults.
		{http.StatusNotFound, "unsupported"},
		{http.StatusMethodNotAllowed, "unsupported"},
		{http.StatusNotImplemented, "unsupported"},
		{http.StatusInternalServerError, "unavailable"},
		{http.StatusServiceUnavailable, "unavailable"},
		{http.StatusUnprocessableEntity, "inconclusive"},
	}
	for _, tc := range cases {
		if got := probeStatusForCode(tc.code); got != tc.want {
			t.Errorf("probeStatusForCode(%d)=%q, want %q", tc.code, got, tc.want)
		}
	}
}

// An invalid model or bad parameter can also produce HTTP 400. The probe only
// sees the status code, so it must report an ambiguous rejection instead of
// claiming the protocol endpoint is absent.
func TestProbeModelCapabilitiesKeepsHTTP400Ambiguous(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/responses":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model not found","type":"invalid_request_error"}}`))
		case "/chat/completions", "/v1/messages":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	caps, _, errs := ProbeModelCapabilitiesContextWithClientDetailed(
		context.Background(), server.URL, "secret", "auto", "unknown-model", 5*time.Second, nil)
	if caps["responses"] != "rejected" {
		t.Fatalf("responses=%q errs=%v, want rejected for an ambiguous HTTP 400", caps["responses"], errs)
	}
	if caps["chat_completions"] != "supported" || caps["anthropic_messages"] != "supported" {
		t.Fatalf("caps=%v, want chat and anthropic supported", caps)
	}
	evidence := errs["responses"]
	if !strings.Contains(evidence, server.URL+"/responses: HTTP 400") {
		t.Fatalf("protocol_errors missing the HTTP 400 candidate: %q", evidence)
	}
	if strings.Contains(evidence, "secret") {
		t.Fatalf("protocol_errors leaked the API key: %q", evidence)
	}
}

// A 400 on the configured Anthropic entry point must not stop the probe: the
// gateway may still serve Anthropic Messages under another candidate base.
func TestProbeModelCapabilitiesAnthropicFallbackAfterRejectedCandidate(t *testing.T) {
	t.Parallel()
	base, paths := newProbeTestServer(t, map[string]int{
		"/wrong/messages":        http.StatusBadRequest,
		"/wrong/v1/messages":     http.StatusBadRequest,
		"/anthropic/v1/messages": http.StatusOK,
	})

	caps, anthropicBase, errs := ProbeModelCapabilitiesForProviderContext(
		context.Background(), base.URL+"/v1", base.URL+"/wrong", "secret", "auto", "claude-model", 5*time.Second, nil)
	if caps["anthropic_messages"] != "supported" {
		t.Fatalf("caps=%v errs=%v, want the later Anthropic candidate to succeed", caps, errs)
	}
	if anthropicBase != base.URL+"/anthropic" {
		t.Fatalf("anthropicBase=%q, want %q", anthropicBase, base.URL+"/anthropic")
	}
	if !containsString(paths(), "/anthropic/v1/messages") {
		t.Fatalf("the later Anthropic candidate was not probed: %v", paths())
	}
}

// Unauthorized, service, and inconclusive results describe the credential or
// gateway rather than the base path, so the probe keeps the first candidate's
// verdict and does not multiply requests across every candidate.
func TestProbeModelCapabilitiesAnthropicFallbackStopsOnNonPathFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		want   string
	}{
		{"unauthorized", http.StatusUnauthorized, "unauthorized"},
		{"unavailable", http.StatusInternalServerError, "unavailable"},
		{"inconclusive", http.StatusUnprocessableEntity, "inconclusive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, paths := newProbeTestServer(t, map[string]int{
				"/wrong/messages":    tc.status,
				"/wrong/v1/messages": tc.status,
			})
			caps, anthropicBase, errs := ProbeModelCapabilitiesForProviderContext(
				context.Background(), base.URL+"/v1", base.URL+"/wrong", "secret", "auto", "claude-model", 5*time.Second, nil)
			if caps["anthropic_messages"] != tc.want {
				t.Fatalf("caps=%v errs=%v, want %q", caps, errs, tc.want)
			}
			if anthropicBase != "" {
				t.Fatalf("anthropicBase=%q, want empty for a non-path failure", anthropicBase)
			}
			for _, path := range paths() {
				if !strings.HasPrefix(path, "/wrong/") {
					t.Fatalf("probe fell back to %q after %s", path, tc.name)
				}
			}
		})
	}
}

// A rate-limited fallback candidate is still the working Anthropic entry
// point: keep the rate_limited status, return that base, and stop probing.
func TestProbeModelCapabilitiesAnthropicFallbackRecordsRateLimitedBase(t *testing.T) {
	t.Parallel()
	base, paths := newProbeTestServer(t, map[string]int{
		"/wrong/messages":    http.StatusBadRequest,
		"/wrong/v1/messages": http.StatusBadRequest,
		"/v1/messages":       http.StatusTooManyRequests,
	})

	caps, anthropicBase, errs := ProbeModelCapabilitiesForProviderContext(
		context.Background(), base.URL+"/v1", base.URL+"/wrong", "secret", "auto", "claude-model", 5*time.Second, nil)
	if caps["anthropic_messages"] != "rate_limited" {
		t.Fatalf("caps=%v errs=%v, want rate_limited", caps, errs)
	}
	if anthropicBase != base.URL+"/v1" {
		t.Fatalf("anthropicBase=%q, want the rate-limited candidate %q", anthropicBase, base.URL+"/v1")
	}
	if !containsString(paths(), "/v1/messages") {
		t.Fatalf("the rate-limited fallback candidate was not probed: %v", paths())
	}
	if containsString(paths(), "/anthropic/v1/messages") {
		t.Fatalf("probe continued after a rate_limited success: %v", paths())
	}
}
