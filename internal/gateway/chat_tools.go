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

type ChatTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ChatToolResponse struct {
	Text      string
	ToolCalls []ChatToolCall
}

// ChatWithTools sends one non-streaming completion with function tools and
// returns either text, tool calls, or both. Providers that don't support tools
// simply return text, so the assistant can keep its [pet] fallback.
func ChatWithTools(ctx context.Context, opts ChatOptions, tools []ChatTool) (ChatToolResponse, error) {
	kind := chatWireKind(opts.WireAPI)
	if kind == "" {
		return ChatToolResponse{}, fmt.Errorf("unsupported wire api %q", opts.WireAPI)
	}
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if kind == "anthropic" {
		if anthropic := strings.TrimRight(strings.TrimSpace(opts.AnthropicBaseURL), "/"); anthropic != "" {
			base = anthropic
		}
	}
	if base == "" {
		return ChatToolResponse{}, fmt.Errorf("provider base url is empty")
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		return ChatToolResponse{}, fmt.Errorf("model is empty")
	}
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 || maxTokens > 4096 {
		maxTokens = 700
	}
	timeout := opts.Timeout
	if timeout <= 0 || timeout > 3*time.Minute {
		timeout = 60 * time.Second
	}
	body, err := chatToolBody(kind, model, opts.System, opts.Messages, maxTokens, tools, opts.ForceTool)
	if err != nil {
		return ChatToolResponse{}, err
	}
	client := &http.Client{Timeout: timeout}
	endpoints := chatEndpoints(kind, base)
	auths := chatAuths(kind, opts.AuthStyle, opts.APIKey)
	var lastError string
	for _, endpoint := range endpoints {
		for _, auth := range auths {
			if err := ctx.Err(); err != nil {
				return ChatToolResponse{}, err
			}
			result, status, requestErr := postChatTools(ctx, client, endpoint, kind, auth, body)
			if requestErr == nil {
				return result, nil
			}
			lastError = requestErr.Error()
			switch {
			case status == http.StatusUnauthorized || status == http.StatusForbidden:
				continue
			case status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented:
				break
			default:
				return ChatToolResponse{}, requestErr
			}
		}
	}
	if lastError == "" {
		lastError = "no endpoint accepted the request"
	}
	return ChatToolResponse{}, fmt.Errorf("chat tool request failed: %s", lastError)
}

func chatToolBody(kind, model, system string, messages []ChatMessage, maxTokens int, tools []ChatTool, forceTool ...bool) ([]byte, error) {
	force := len(forceTool) > 0 && forceTool[0]
	switch kind {
	case "chat":
		items := make([]map[string]any, 0, len(messages)+1)
		if strings.TrimSpace(system) != "" {
			items = append(items, map[string]any{"role": "system", "content": system})
		}
		for _, message := range messages {
			role := strings.TrimSpace(message.Role)
			if role != "assistant" {
				role = "user"
			}
			items = append(items, map[string]any{"role": role, "content": message.Content})
		}
		payload := map[string]any{
			"model": model, "messages": items, "max_tokens": maxTokens,
			"temperature": 0.2, "stream": false,
		}
		if len(tools) > 0 {
			items := make([]map[string]any, 0, len(tools))
			for _, tool := range tools {
				items = append(items, map[string]any{
					"type": "function",
					"function": map[string]any{
						"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters,
					},
				})
			}
			payload["tools"] = items
			if force {
				payload["tool_choice"] = "required"
			} else {
				payload["tool_choice"] = "auto"
			}
		}
		return json.Marshal(payload)
	case "anthropic":
		items := make([]map[string]any, 0, len(messages))
		for _, message := range messages {
			role := strings.TrimSpace(message.Role)
			if role != "assistant" {
				role = "user"
			}
			items = append(items, map[string]any{"role": role, "content": message.Content})
		}
		payload := map[string]any{"model": model, "messages": items, "max_tokens": maxTokens, "temperature": 0.2}
		if strings.TrimSpace(system) != "" {
			payload["system"] = system
		}
		if len(tools) > 0 {
			items := make([]map[string]any, 0, len(tools))
			for _, tool := range tools {
				items = append(items, map[string]any{
					"name": tool.Name, "description": tool.Description, "input_schema": tool.Parameters,
				})
			}
			payload["tools"] = items
			if force {
				payload["tool_choice"] = map[string]any{"type": "any"}
			}
		}
		return json.Marshal(payload)
	case "responses":
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
		payload := map[string]any{"model": model, "input": strings.TrimSpace(input.String()), "max_output_tokens": maxTokens}
		if len(tools) > 0 {
			items := make([]map[string]any, 0, len(tools))
			for _, tool := range tools {
				items = append(items, map[string]any{
					"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.Parameters,
				})
			}
			payload["tools"] = items
			if force {
				payload["tool_choice"] = "required"
			}
		}
		return json.Marshal(payload)
	default:
		return nil, fmt.Errorf("unsupported wire api %q", kind)
	}
}

func postChatTools(ctx context.Context, client *http.Client, endpoint, kind string, auth chatAuth, body []byte) (ChatToolResponse, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ChatToolResponse{}, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "AHA2-Assistant/1.0")
	for key, value := range auth.headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return ChatToolResponse{}, 0, fmt.Errorf("%s: %w", endpoint, err)
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if readErr != nil {
		return ChatToolResponse{}, response.StatusCode, fmt.Errorf("%s: read response: %w", endpoint, readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(raw))
		if len(message) > 600 {
			message = message[:600] + "…"
		}
		return ChatToolResponse{}, response.StatusCode, fmt.Errorf("%s: HTTP %d: %s", endpoint, response.StatusCode, message)
	}
	result, parseErr := parseChatToolResponse(kind, raw)
	if parseErr == nil && (result.Text != "" || len(result.ToolCalls) > 0) {
		return result, response.StatusCode, nil
	}
	// Some Responses/Anthropic-compatible gateways wrap the result one level
	// deeper or use a slightly different function-call envelope. Fall back to
	// a loose recursive extraction before surfacing "no text".
	if calls := extractToolCallsLoose(raw); len(calls) > 0 {
		result.ToolCalls = calls
		result.Text = strings.TrimSpace(extractTextLoose(raw))
		return result, response.StatusCode, nil
	}
	if text := extractTextLoose(raw); text != "" {
		result.Text = text
		return result, response.StatusCode, nil
	}
	if parseErr != nil {
		message := strings.TrimSpace(string(raw))
		if len(message) > 500 {
			message = message[:500] + "…"
		}
		return ChatToolResponse{}, response.StatusCode, fmt.Errorf("%s: %w (raw %s)", endpoint, parseErr, message)
	}
	return ChatToolResponse{}, response.StatusCode, fmt.Errorf("%s: response has no text or tool calls", endpoint)
}

func parseChatToolResponse(kind string, raw []byte) (ChatToolResponse, error) {
	switch kind {
	case "chat":
		var payload struct {
			Choices []struct {
				Message struct {
					Content   any `json:"content"`
					ToolCalls []struct {
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return ChatToolResponse{}, fmt.Errorf("decode chat response: %w", err)
		}
		if len(payload.Choices) == 0 {
			return ChatToolResponse{}, fmt.Errorf("chat response has no choices")
		}
		result := ChatToolResponse{Text: strings.TrimSpace(textFromContent(payload.Choices[0].Message.Content))}
		for _, call := range payload.Choices[0].Message.ToolCalls {
			if strings.TrimSpace(call.Function.Name) == "" {
				continue
			}
			result.ToolCalls = append(result.ToolCalls, ChatToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
		}
		if result.Text == "" && len(result.ToolCalls) == 0 {
			return ChatToolResponse{}, fmt.Errorf("chat response has no text or tool calls")
		}
		return result, nil
	case "anthropic":
		var payload struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return ChatToolResponse{}, fmt.Errorf("decode anthropic response: %w", err)
		}
		var text strings.Builder
		result := ChatToolResponse{}
		for _, block := range payload.Content {
			switch block.Type {
			case "", "text":
				text.WriteString(block.Text)
			case "tool_use":
				result.ToolCalls = append(result.ToolCalls, ChatToolCall{ID: block.ID, Name: block.Name, Arguments: string(block.Input)})
			}
		}
		result.Text = strings.TrimSpace(text.String())
		if result.Text == "" && len(result.ToolCalls) == 0 {
			return ChatToolResponse{}, fmt.Errorf("anthropic response has no text or tool calls")
		}
		return result, nil
	case "responses":
		var payload struct {
			OutputText string `json:"output_text"`
			Output     []struct {
				Type      string `json:"type"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				CallID    string `json:"call_id"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return ChatToolResponse{}, fmt.Errorf("decode responses response: %w", err)
		}
		result := ChatToolResponse{Text: strings.TrimSpace(payload.OutputText)}
		var text strings.Builder
		for _, item := range payload.Output {
			if item.Type == "function_call" {
				result.ToolCalls = append(result.ToolCalls, ChatToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments})
			}
			for _, block := range item.Content {
				if block.Type == "" || block.Type == "output_text" || block.Type == "text" {
					text.WriteString(block.Text)
				}
			}
		}
		if result.Text == "" {
			result.Text = strings.TrimSpace(text.String())
		}
		if result.Text == "" && len(result.ToolCalls) == 0 {
			return ChatToolResponse{}, fmt.Errorf("responses response has no text or tool calls")
		}
		return result, nil
	default:
		return ChatToolResponse{}, fmt.Errorf("unsupported wire api %q", kind)
	}
}

// extractToolCallsLoose covers provider envelopes that wrap function calls in
// an extra object (for example a Responses "response" item) instead of the
// top-level shape the strict parser expects.
func extractToolCallsLoose(raw []byte) []ChatToolCall {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	seen := map[string]bool{}
	var calls []ChatToolCall
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			kind, _ := typed["type"].(string)
			if kind == "function_call" || kind == "tool_call" || kind == "tool_use" {
				name, _ := typed["name"].(string)
				arguments := ""
				switch args := typed["arguments"].(type) {
				case string:
					arguments = args
				default:
					if encoded, err := json.Marshal(args); err == nil {
						arguments = string(encoded)
					}
				}
				if name == "" {
					if fn, ok := typed["function"].(map[string]any); ok {
						name, _ = fn["name"].(string)
						if args, ok := fn["arguments"].(string); ok {
							arguments = args
						}
					}
				}
				if name != "" && json.Valid([]byte(arguments)) && !seen[name+arguments] {
					seen[name+arguments] = true
					calls = append(calls, ChatToolCall{Name: name, Arguments: arguments})
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return calls
}

func extractTextLoose(raw []byte) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var text string
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			for _, key := range []string{"output_text", "text"} {
				if current, ok := typed[key].(string); ok && strings.TrimSpace(current) != "" {
					text = current
					return
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return strings.TrimSpace(text)
}
