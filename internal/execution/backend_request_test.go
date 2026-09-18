package execution

import (
	"testing"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

// TestBackendRequestCarriesTheAgentAPIForwardForEveryBackend pins that the fields
// shared by both backends are actually shared.
//
// This is the regression test for a real defect: the Claude path built its own
// backend.Request and omitted the Agent API forward, so for a remote workspace —
// where the address is only reachable through a per-Turn tunnel — every Agent API
// call failed to connect. Codex kept working, which made it look like a network
// fault rather than a missing field. Both backends must carry the same values.
func TestBackendRequestCarriesTheAgentAPIForwardForEveryBackend(t *testing.T) {
	t.Parallel()
	forward := &workspace.ReverseForward{Target: "127.0.0.1:8766", EnvName: "AHA2_AGENT_API_URL"}
	request := app.ExecutionRequest{
		Task:      domain.Task{ID: "task-1", WorkspaceID: "ws-1"},
		Workspace: domain.Workspace{ID: "ws-1", Transport: "native"},
		Snapshot:  domain.RuntimeConfigSnapshot{ReasoningEffort: "low"},
		Model:     domain.Model{WireModel: "claude-x", ContextWindow: 200000},
		Prompt:    "hello", ProviderSessionID: "session-1",
		AgentAPIForward: forward,
	}

	// The Claude path derives its own environment before calling, so exercise the
	// constructor the same way both callers do.
	built := newBackendRequest(request, "claude-x", map[string]string{"AHA2_AGENT_API_URL": "http://127.0.0.1:9"})

	if built.ReverseForward == nil {
		t.Fatal("the Agent API forward was dropped; a remote workspace would get an unreachable address")
	}
	if built.ReverseForward.Target != forward.Target || built.ReverseForward.EnvName != forward.EnvName {
		t.Fatalf("forward = %#v, want %#v", built.ReverseForward, forward)
	}
	// The rest of the shared fields must survive the same trip.
	if built.Prompt != "hello" || built.ProviderSessionID != "session-1" {
		t.Fatalf("shared fields were lost: prompt=%q session=%q", built.Prompt, built.ProviderSessionID)
	}
	if built.Model != "claude-x" || built.ContextWindow != 200000 {
		t.Fatalf("model fields were lost: model=%q window=%d", built.Model, built.ContextWindow)
	}
	if built.Environment["AHA2_AGENT_API_URL"] != "http://127.0.0.1:9" {
		t.Fatalf("environment was lost: %#v", built.Environment)
	}
}
