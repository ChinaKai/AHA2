package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
