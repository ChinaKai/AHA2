package httpapi

import (
	"context"

	"github.com/ChinaKai/AHA2/internal/app"
)

// MCPToolCatalog lists every tool each built-in MCP server can advertise,
// before any per-Turn gate. Coverage tooling checks ledger evidence against
// it; it carries names only, which tools/list already publishes.
func MCPToolCatalog() map[string][]string {
	s := &Server{}
	names := func(tools []mcpTool) []string {
		result := make([]string, 0, len(tools))
		for _, tool := range tools {
			result = append(result, tool.Name)
		}
		return result
	}
	operations := func(items []agentMCPOperation) []string {
		result := make([]string, 0, len(items))
		for _, item := range items {
			result = append(result, item.Tool.Name)
		}
		return result
	}
	pet := []string{}
	for _, tool := range (&app.Service{}).PetMCPTools(context.Background()) {
		pet = append(pet, tool.Name)
	}
	return map[string][]string{
		"aha2_core":      names(coreMCPTools()),
		"aha2_knowledge": names(knowledgeMCPTools()),
		"aha2_workspace": names(s.workspaceMCPToolsFor(true)),
		"aha2_browser":   names(s.browserMCPToolsFor(true)),
		"aha2_desktop":   operations(s.desktopMCPOperations()),
		"aha2_hardware":  names(s.hardwareMCPToolsFor(true)),
		"aha2_mail":      names(mailMCPTools()),
		"aha2_channel":   names(channelMCPTools()),
		"aha2_collab":    names(agentCollabMCPTools()),
		"aha2_pet":       pet,
	}
}
