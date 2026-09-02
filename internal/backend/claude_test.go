package backend

import "testing"

func TestParseClaudeLineResult(t *testing.T) {
	t.Parallel()
	event, reply, session := parseClaudeLine(`{"type":"result","subtype":"success","result":"done","session_id":"sess-1"}`)
	if event.Type != "agent_message" || reply != "done" || session != "sess-1" {
		t.Fatalf("unexpected parse: event=%q reply=%q session=%q", event.Type, reply, session)
	}
}

func TestParseClaudeLineInitSession(t *testing.T) {
	t.Parallel()
	event, reply, session := parseClaudeLine(`{"type":"system","subtype":"init","session_id":"sess-2"}`)
	if event.Type != "agent_session" || reply != "" || session != "sess-2" {
		t.Fatalf("unexpected parse: event=%q reply=%q session=%q", event.Type, reply, session)
	}
}

func TestParseClaudeLineError(t *testing.T) {
	t.Parallel()
	event, _, _ := parseClaudeLine(`{"type":"result","subtype":"error_max_turns","error":"too many turns"}`)
	if event.Type != "agent_error" {
		t.Fatalf("expected agent_error, got %q", event.Type)
	}
}

func TestFilterClaudeEnvironment(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"ANTHROPIC_BASE_URL": "https://api.example.com",
		"ANTHROPIC_API_KEY":  "sk-123",
		"OPENAI_API_KEY":     "should-drop",
	}
	filtered := filterClaudeEnvironment(values)
	if filtered["ANTHROPIC_BASE_URL"] != "https://api.example.com" || filtered["ANTHROPIC_API_KEY"] != "sk-123" {
		t.Fatalf("claude env not filtered correctly: %#v", filtered)
	}
	if _, present := filtered["OPENAI_API_KEY"]; present {
		t.Fatalf("OPENAI_API_KEY should not leak into claude env: %#v", filtered)
	}
}
