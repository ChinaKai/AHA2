package app

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/domain"
)

// TestPetMCPRuntimeCarriesAgentCapability pins the injection contract the pet
// Task depends on: its MCP registration points at the loopback endpoint and
// reads the Turn capability from AHA2_AGENT_API_TOKEN, the same variable the
// Agent API uses. The endpoint now rejects anonymous calls, so a missing token
// environment would break the pet's own read-only tools too.
func TestPetMCPRuntimeCarriesAgentCapability(t *testing.T) {
	fixture := newPetTaskFixture(t)
	capabilities := agentapi.NewCapabilities()
	fixture.service.SetAgentAPI(capabilities, "http://127.0.0.1:8766")

	servers, allowed := fixture.service.mcpRuntimeForTask(fixture.ctx,
		domain.Task{ID: petTaskID}, "http://127.0.0.1:8766")
	var pet backend.MCPServer
	for _, server := range servers {
		if server.Name == "aha2-pet" {
			pet = server
		}
	}
	if pet.Name == "" {
		t.Fatalf("pet MCP missing from runtime: %#v", servers)
	}
	if pet.Transport != "http" || pet.URL != "http://127.0.0.1:8766/api/v1/pet/mcp" {
		t.Fatalf("pet MCP endpoint=%#v", pet)
	}
	if pet.BearerTokenEnv != "AHA2_AGENT_API_TOKEN" {
		t.Fatalf("pet MCP bearer env=%q, want AHA2_AGENT_API_TOKEN", pet.BearerTokenEnv)
	}
	foundAllowed := false
	for _, name := range allowed {
		if strings.HasPrefix(name, "mcp__aha2-pet__") {
			foundAllowed = true
		}
	}
	if !foundAllowed {
		t.Fatalf("pet MCP tools missing from allowlist: %#v", allowed)
	}
}
