package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChatMessage is one turn in a small, non-streaming chat request. The owner
// assistant only needs short read-only answers, so it deliberately does not
// reuse the full agent backend adapters (which require a workspace and tools).
type ChatMessage struct {
	Role    string
	Content string
}

type ChatOptions struct {
	BaseURL          string
	AnthropicBaseURL string
	APIKey           string
	AuthStyle        string
	WireAPI          string
	Model            string
	System           string
	Messages         []ChatMessage
	MaxTokens        int
	Timeout          time.Duration
	ForceTool        bool
}

// Chat sends one non-streaming completion request to an OpenAI-compatible,
// Anthropic-compatible, or Responses API endpoint. It returns the assistant
// text and never logs the API key.
func Chat(ctx context.Context, opts ChatOptions) (string, error) {
	kind := chatWireKind(opts.WireAPI)
	if kind == "" {
		return "", fmt.Errorf("unsupported wire api %q", opts.WireAPI)
	}
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if kind == "anthropic" {
		if anthropic := strings.TrimRight(strings.TrimSpace(opts.AnthropicBaseURL), "/"); anthropic != "" {
			base = anthropic
		}
	}
	if base == "" {
		return "", fmt.Errorf("provider base url is empty")
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		return "", fmt.Errorf("model is empty")
	}
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 || maxTokens > 4096 {
		maxTokens = 700
	}
	timeout := opts.Timeout
	if timeout <= 0 || timeout > 3*time.Minute {
		timeout = 60 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	endpoints := chatEndpoints(kind, base)
	if len(endpoints) == 0 {
		return "", fmt.Errorf("no chat endpoint")
	}
	body, err := chatBody(kind, model, opts.System, opts.Messages, maxTokens)
	if err != nil {
		return "", err
	}
	auths := chatAuths(kind, opts.AuthStyle, opts.APIKey)
	var lastError string
	for _, endpoint := range endpoints {
		for _, auth := range auths {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			text, status, requestErr := postChat(ctx, client, endpoint, kind, auth, body)
			if requestErr == nil {
				return text, nil
			}
			lastError = requestErr.Error()
			switch {
			case status == http.StatusUnauthorized || status == http.StatusForbidden:
				continue
			case status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented:
				break
			default:
				return "", requestErr
			}
		}
	}
	if lastError == "" {
		lastError = "no endpoint accepted the request"
	}
	return "", fmt.Errorf("chat request failed: %s", lastError)
}

// SupportsChat reports whether the wire API can be used by the lightweight
// owner-assistant chat client.
func SupportsChat(value string) bool {
	return chatWireKind(value) != ""
}

func chatWireKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "chat", "chat_completions", "openai_chat":
		return "chat"
	case "responses":
		return "responses"
	case "anthropic", "anthropic_messages":
		return "anthropic"
	default:
		return ""
	}
}

func chatEndpoints(kind, base string) []string {
	path := "chat/completions"
	switch kind {
	case "responses":
		path = "responses"
	case "anthropic":
		path = "messages"
	}
	var endpoints []string
	if strings.HasSuffix(base, "/v1") {
		endpoints = append(endpoints, base+"/"+path)
	} else {
		endpoints = append(endpoints, base+"/"+path, base+"/v1/"+path)
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint == "" || seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		result = append(result, endpoint)
	}
	return result
}

type chatAuth struct {
	headers map[string]string
}

func chatAuths(kind, style, apiKey string) []chatAuth {
	key := strings.TrimSpace(apiKey)
	base := map[string]string{}
	if kind == "anthropic" {
		base["anthropic-version"] = "2023-06-01"
	}
	with := func(headers map[string]string) chatAuth {
		merged := map[string]string{}
		for key, value := range base {
			merged[key] = value
		}
		for key, value := range headers {
			merged[key] = value
		}
		return chatAuth{headers: merged}
	}
	normalized := strings.ToLower(strings.TrimSpace(style))
	if key == "" || normalized == "none" {
		return []chatAuth{with(nil)}
	}
	bearer := with(map[string]string{"Authorization": "Bearer " + key})
	xAPIKey := with(map[string]string{"x-api-key": key})
	switch normalized {
	case "bearer":
		return []chatAuth{bearer}
	case "x-api-key":
		return []chatAuth{xAPIKey}
	case "auto":
		if kind == "anthropic" {
			return []chatAuth{xAPIKey, bearer}
		}
		return []chatAuth{bearer, xAPIKey}
	default:
		return []chatAuth{bearer, xAPIKey}
	}
}

func chatBody(kind, model, system string, messages []ChatMessage, maxTokens int) ([]byte, error) {
	switch kind {
	case "chat":
		items := make([]map[string]string, 0, len(messages)+1)
		if strings.TrimSpace(system) != "" {
			items = append(items, map[string]string{"role": "system", "content": system})
		}
		for _, message := range messages {
			role := strings.TrimSpace(message.Role)
			if role != "assistant" {
				role = "user"
			}
			items = append(items, map[string]string{"role": role, "content": message.Content})
		}
		return json.Marshal(map[string]any{
			"model": model, "messages": items, "max_tokens": maxTokens,
			"temperature": 0.2, "stream": false,
		})
	case "anthropic":
		items := make([]map[string]string, 0, len(messages))
		for _, message := range messages {
			role := strings.TrimSpace(message.Role)
			if role != "assistant" {
				role = "user"
			}
			items = append(items, map[string]string{"role": role, "content": message.Content})
		}
		payload := map[string]any{
			"model": model, "messages": items, "max_tokens": maxTokens, "temperature": 0.2,
		}
		if strings.TrimSpace(system) != "" {
			payload["system"] = system
		}
		return json.Marshal(payload)
	case "responses":
		// Flatten history into one input item: the Responses API accepts a
		// simple input string and avoids depending on assistant item shapes.
		var input strings.Builder
		if strings.TrimSpace(system) != "" {
			input.WriteString(system)
			input.WriteString("\n\n")
		}
		for _, message := range messages {
			label := "User"
			if strings.TrimSpace(message.Role) == "assistant" {
				label = "Assistant"
			}
			input.WriteString(label)
			input.WriteString(": ")
			input.WriteString(message.Content)
			input.WriteString("\n")
		}
		return json.Marshal(map[string]any{
			"model": model, "input": strings.TrimSpace(input.String()), "max_output_tokens": maxTokens,
		})
	default:
		return nil, fmt.Errorf("unsupported wire api %q", kind)
	}
}

func postChat(ctx context.Context, client *http.Client, endpoint, kind string, auth chatAuth, body []byte) (string, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "AHA2-Assistant/1.0")
	for key, value := range auth.headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", 0, fmt.Errorf("%s: %w", endpoint, err)
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if readErr != nil {
		return "", response.StatusCode, fmt.Errorf("%s: read response: %w", endpoint, readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(raw))
		if len(message) > 600 {
			message = message[:600] + "…"
		}
		return "", response.StatusCode, fmt.Errorf("%s: HTTP %d: %s", endpoint, response.StatusCode, message)
	}
	text, parseErr := parseChatResponse(kind, raw)
	if parseErr != nil {
		return "", response.StatusCode, fmt.Errorf("%s: %w", endpoint, parseErr)
	}
	return text, response.StatusCode, nil
}

func parseChatResponse(kind string, raw []byte) (string, error) {
	switch kind {
	case "chat":
		var payload struct {
			Choices []struct {
				Message struct {
					Content any `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return "", fmt.Errorf("decode chat response: %w", err)
		}
		if len(payload.Choices) == 0 {
			return "", fmt.Errorf("chat response has no choices")
		}
		text := textFromContent(payload.Choices[0].Message.Content)
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("chat response has no text")
		}
		return strings.TrimSpace(text), nil
	case "anthropic":
		var payload struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return "", fmt.Errorf("decode anthropic response: %w", err)
		}
		var output strings.Builder
		for _, block := range payload.Content {
			if block.Type == "" || block.Type == "text" {
				output.WriteString(block.Text)
			}
		}
		if strings.TrimSpace(output.String()) == "" {
			return "", fmt.Errorf("anthropic response has no text")
		}
		return strings.TrimSpace(output.String()), nil
	case "responses":
		var payload struct {
			OutputText string `json:"output_text"`
			Output     []struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return "", fmt.Errorf("decode responses response: %w", err)
		}
		if text := strings.TrimSpace(payload.OutputText); text != "" {
			return text, nil
		}
		var output strings.Builder
		for _, item := range payload.Output {
			for _, block := range item.Content {
				if block.Type == "" || block.Type == "output_text" || block.Type == "text" {
					output.WriteString(block.Text)
				}
			}
		}
		if strings.TrimSpace(output.String()) == "" {
			return "", fmt.Errorf("responses response has no text")
		}
		return strings.TrimSpace(output.String()), nil
	default:
		return "", fmt.Errorf("unsupported wire api %q", kind)
	}
}

func textFromContent(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var output strings.Builder
		for _, raw := range typed {
			block, _ := raw.(map[string]any)
			text, _ := block["text"].(string)
			output.WriteString(text)
		}
		return output.String()
	default:
		return ""
	}
}
