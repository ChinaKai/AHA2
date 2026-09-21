package httpapi

import (
	"context"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// The window shown has to match the endpoint the run actually uses, so endpoint
// detection is what decides it. The env group is where an Env-provider run gets
// its endpoint, so that is what these read.
func TestClaudeEndpointFrom(t *testing.T) {
	// A custom endpoint is a gateway: the CLI budgets such a run at the fallback
	// window, so it must be reported as one.
	if got := claudeEndpointFrom(map[string]string{"ANTHROPIC_BASE_URL": "https://gateway.example"}); got != "https://gateway.example" {
		t.Fatalf("custom endpoint = %q, want the configured gateway", got)
	}
	// A group's environment is free-form, so the name may not be uppercase.
	if got := claudeEndpointFrom(map[string]string{"anthropic_base_url": "https://gateway.example"}); got != "https://gateway.example" {
		t.Fatalf("lowercase endpoint not detected: %q", got)
	}
	// Pointing at Anthropic itself is the direct path, not a gateway.
	for _, endpoint := range []string{
		"https://api.anthropic.com",
		"https://api.anthropic.com/v1",
		"https://API.Anthropic.com",
	} {
		if got := claudeEndpointFrom(map[string]string{"ANTHROPIC_BASE_URL": endpoint}); got != "" {
			t.Fatalf("endpoint %q treated as a gateway", endpoint)
		}
	}
	if got := claudeEndpointFrom(map[string]string{}); got != "" {
		t.Fatalf("empty environment treated as a gateway: %q", got)
	}
}

// A remote run does not inherit AHA's environment, so an endpoint set on the AHA
// process must not be attributed to it; a local run does inherit it.
func TestClaudeRunInheritsProcessEnv(t *testing.T) {
	for _, remote := range []domain.Workspace{
		{Locality: "remote", Transport: "ssh"},
		{Transport: "ssh"},
		{Transport: "wsl"},
	} {
		if claudeRunInheritsProcessEnv(remote) {
			t.Fatalf("%#v must not inherit AHA's own environment", remote)
		}
	}
	if !claudeRunInheritsProcessEnv(domain.Workspace{Locality: "local", Transport: "native"}) {
		t.Fatal("a local run does inherit AHA's own environment")
	}
}

// The group's own model name is where the 1M marker goes for a gateway run, so a
// marker the operator set has to be visible to the window resolution.
func TestEnvValueReadsModelNameCaseInsensitively(t *testing.T) {
	if got := envValue(map[string]string{"anthropic_model": " claude-sonnet-5[1m] "}, "ANTHROPIC_MODEL"); got != "claude-sonnet-5[1m]" {
		t.Fatalf("model name = %q, want the trimmed marker name", got)
	}
	if got := envValue(map[string]string{}, "ANTHROPIC_MODEL"); got != "" {
		t.Fatalf("absent name = %q, want empty", got)
	}
}

// The native source blanks ANTHROPIC_* so the run reaches Anthropic on the
// operator's own login. Reporting the endpoint the AHA process happens to carry
// instead would mark an Official run as gateway-routed and budget its window at
// the 200K fallback -- a wrong answer only on a host whose AHA is launched with
// a gateway endpoint, which is exactly where it would be believed.
func TestClaudeRunEnvironmentBlanksNativeSource(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://tokens-api.example")
	server := &Server{}
	snapshot := domain.RuntimeConfigSnapshot{EnvGroupID: domain.ClaudeNativeEnvGroupID}
	for _, item := range []domain.Workspace{
		{Transport: "native", Locality: "local"},
		{Transport: "wsl", Locality: "local"},
	} {
		environment := server.claudeRunEnvironment(context.Background(), snapshot, item)
		if got := envValue(environment, "ANTHROPIC_BASE_URL"); got != "" {
			t.Fatalf("native source reported endpoint %q, want it blanked", got)
		}
		if claudeEndpointFrom(environment) != "" {
			t.Fatalf("native source resolved as gateway-routed: %v", environment)
		}
		if got := envValue(environment, "ANTHROPIC_MODEL"); got != "" {
			t.Fatalf("native source reported model %q, want it blanked", got)
		}
	}
}
