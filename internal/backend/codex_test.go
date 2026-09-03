package backend

import (
	"strings"
	"testing"
)

func TestCodexArgumentsResumeAndProvider(t *testing.T) {
	t.Parallel()
	args := codexArguments(Request{
		WorkDir: "/workspace", Model: "gpt-test", ContextWindow: 200000,
		ReasoningEffort: "high", ProviderSessionID: "session-1",
		Environment: map[string]string{
			"AHA_PROVIDER_ID": "internal", "OPENAI_BASE_URL": "https://example.test/v1",
			"CODEX_WIRE_API": "responses", "CODEX_ENV_KEY": "OPENAI_API_KEY",
		},
	})
	joined := strings.Join(args, " ")
	for _, expected := range []string{"model_provider", "--disable multi_agent", "exec", "resume", "session-1", "--json"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("arguments missing %q: %s", expected, joined)
		}
	}
}

func TestParseCodexLine(t *testing.T) {
	t.Parallel()
	event, reply, session := parseCodexLine(`{"type":"thread.started","thread_id":"abc"}`)
	if event.Type != "agent_session" || session != "abc" {
		t.Fatalf("unexpected session event: %#v %q", event, session)
	}
	event, reply, _ = parseCodexLine(`{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`)
	if event.Type != "agent_message" || reply != "done" {
		t.Fatalf("unexpected reply event: %#v %q", event, reply)
	}
}

func TestFilterEnvironmentDropsUnknownSecrets(t *testing.T) {
	t.Parallel()
	filtered := filterEnvironment(map[string]string{"OPENAI_API_KEY": "ok", "UNRELATED_SECRET": "drop"})
	if filtered["OPENAI_API_KEY"] != "ok" {
		t.Fatal("allowed key dropped")
	}
	if _, ok := filtered["UNRELATED_SECRET"]; ok {
		t.Fatal("unknown secret was forwarded")
	}
}

func TestFilterEnvironmentAllowsDeclaredCredentialKey(t *testing.T) {
	t.Parallel()
	filtered := filterEnvironment(map[string]string{
		"CODEX_ENV_KEY":       "INTERNAL_AUTH_TOKEN",
		"INTERNAL_AUTH_TOKEN": "ok",
		"PATH":                "drop",
	})
	if filtered["INTERNAL_AUTH_TOKEN"] != "ok" {
		t.Fatal("declared credential key was dropped")
	}
	if _, ok := filtered["PATH"]; ok {
		t.Fatal("dangerous environment variable was forwarded")
	}
}
