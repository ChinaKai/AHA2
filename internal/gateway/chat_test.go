package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatOpenAICompatible(t *testing.T) {
	t.Parallel()
	var seenAuth, seenModel string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		seenAuth = request.Header.Get("Authorization")
		var payload struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		seenModel = payload.Model
		if len(payload.Messages) < 2 || payload.Messages[0].Role != "system" || payload.Messages[1].Content != "你好" {
			t.Errorf("unexpected messages: %#v", payload.Messages)
		}
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"content":"我在。"}}]}`))
	}))
	defer server.Close()

	reply, err := Chat(context.Background(), ChatOptions{
		BaseURL: server.URL, APIKey: "key", AuthStyle: "bearer", WireAPI: "chat",
		Model: "model-a", System: "system prompt",
		Messages: []ChatMessage{{Role: "user", Content: "你好"}},
	})
	if err != nil || reply != "我在。" {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
	if seenAuth != "Bearer key" || seenModel != "model-a" {
		t.Fatalf("auth=%q model=%q", seenAuth, seenModel)
	}
}

func TestChatAnthropicCompatible(t *testing.T) {
	t.Parallel()
	var seenKey, seenVersion string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" {
			http.NotFound(writer, request)
			return
		}
		seenKey = request.Header.Get("x-api-key")
		seenVersion = request.Header.Get("anthropic-version")
		_, _ = writer.Write([]byte(`{"content":[{"type":"text","text":"收到"},{"type":"text","text":"。"}]}`))
	}))
	defer server.Close()

	reply, err := Chat(context.Background(), ChatOptions{
		BaseURL: server.URL, APIKey: "anthropic-key", AuthStyle: "x-api-key", WireAPI: "anthropic_messages",
		Model: "claude", Messages: []ChatMessage{{Role: "user", Content: "在吗"}},
	})
	if err != nil || reply != "收到。" {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
	if seenKey != "anthropic-key" || seenVersion == "" {
		t.Fatalf("key=%q version=%q", seenKey, seenVersion)
	}
}

func TestChatRejectsUnsupportedWireAPI(t *testing.T) {
	t.Parallel()
	_, err := Chat(context.Background(), ChatOptions{BaseURL: "http://example.invalid", WireAPI: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unsupported wire api") {
		t.Fatalf("err=%v", err)
	}
}
