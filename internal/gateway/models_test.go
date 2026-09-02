package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
