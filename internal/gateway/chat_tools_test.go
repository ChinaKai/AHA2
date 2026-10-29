package gateway

import (
	"strings"
	"testing"
)

func TestParseChatToolResponseOpenAI(t *testing.T) {
	result, err := parseChatToolResponse("chat", []byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","function":{"name":"pet_play","arguments":"{\"name\":\"jump\"}"}}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "pet_play" || !strings.Contains(result.ToolCalls[0].Arguments, "jump") {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestParseChatToolResponseAnthropic(t *testing.T) {
	result, err := parseChatToolResponse("anthropic", []byte(`{"content":[{"type":"text","text":"好"},{"type":"tool_use","id":"toolu_1","name":"pet_move","input":{"zone":"right"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "好" || len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "pet_move" || result.ToolCalls[0].Arguments != `{"zone":"right"}` {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestParseChatToolResponseResponses(t *testing.T) {
	result, err := parseChatToolResponse("responses", []byte(`{"output":[{"type":"function_call","name":"pet_mood","arguments":"{\"mood\":\"happy\"}","call_id":"fc_1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "pet_mood" || result.ToolCalls[0].ID != "fc_1" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestChatToolBodyForcesToolChoice(t *testing.T) {
	tools := []ChatTool{{Name: "pet_play", Description: "play", Parameters: map[string]any{"type": "object"}}}
	chatBody, err := chatToolBody("chat", "m", "system", nil, 100, tools, true)
	if err != nil || !strings.Contains(string(chatBody), `"tool_choice":"required"`) {
		t.Fatalf("openai force: %s %v", chatBody, err)
	}
	anthropicBody, err := chatToolBody("anthropic", "m", "system", nil, 100, tools, true)
	if err != nil || !strings.Contains(string(anthropicBody), `"tool_choice":{"type":"any"}`) {
		t.Fatalf("anthropic force: %s %v", anthropicBody, err)
	}
}

func TestChatToolBodyIncludesTools(t *testing.T) {
	tools := []ChatTool{{Name: "pet_play", Description: "play", Parameters: map[string]any{"type": "object"}}}
	body, err := chatToolBody("chat", "m", "system", nil, 100, tools)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"tools"`) || !strings.Contains(string(body), `"pet_play"`) {
		t.Fatalf("tools missing from body: %s", body)
	}
}
