package prompt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/store"
)

// TestAgentAPIReferenceTellsTheAgentToUseTheVariable pins the contract that keeps
// the Agent API address correct across Turns.
//
// The reachable address is chosen per Turn and, for a remote workspace, is often a
// tunnel port assigned when the Turn starts. The backend session, however, is
// resumed across Turns and carries its own transcript, so an address the Agent
// wrote in an earlier turn is still in its context. If this reference presents a
// single literal address as authoritative, the Agent reuses that stale address and
// every call fails to connect — with the failure appearing as an unreachable
// control plane rather than as a stale value in the Agent's own history.
func TestAgentAPIReferenceTellsTheAgentToUseTheVariable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	input := sharedSnapshotTestInput()
	input.AgentAPIURL = "http://127.0.0.1:8766"

	result, err := engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}

	var reference string
	for _, item := range result.SharedManifest {
		if item.ID == "agent-api" {
			reference = item.Content
		}
	}
	if reference == "" {
		t.Fatal("the agent-api reference is missing from the shared manifest")
	}

	if !strings.Contains(reference, "AHA2_AGENT_API_URL") {
		t.Fatal("reference does not name the environment variable the agent must use")
	}
	// The stale-address failure this prevents: a lone literal presented as the API.
	if strings.Contains(reference, "exposes a Task-scoped API at http") {
		t.Fatal("reference still presents one fixed address as authoritative")
	}
	// The literal still renders, but only as a hint alongside the variable.
	if !strings.Contains(reference, "http://127.0.0.1:8766") {
		t.Fatal("the literal hint address stopped rendering")
	}
	if !strings.Contains(reference, "stale") {
		t.Fatal("reference does not warn that an earlier address may be stale")
	}
}
