package app

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	// channelMCPName is the model-facing namespace of the built-in group-chat
	// MCP. Keeping it stable preserves tool names across backends and sessions.
	channelMCPName = "aha2_channel"
	// channelMCPPath is mounted with the same turn-scoped Agent capability that
	// protects every other agent endpoint.
	channelMCPPath = "/api/v1/agent/channel/mcp"
)

// mcpRuntimeForTask returns the MCP servers a Turn should receive and the
// Claude-style allowed-tool names for them. The returned list is neutral: the
// Claude, Codex and DSH adapters each render their own CLI configuration from
// it, so a Task bound to an MCP server gets the same tools on every backend.
//
// A group task_route Turn also receives the built-in narrow channel MCP. That
// route mode has no automatic reply mirror, and a read-only Agent otherwise has
// no way to answer the group.
func (s *Service) mcpRuntimeForTask(ctx context.Context, task domain.Task, agentAPIURL string) ([]backend.MCPServer, []string) {
	ids := append([]string{}, task.MCPServerIDs...)
	if task.ID == petTaskID {
		found := false
		for _, id := range ids {
			if id == "mcp_pet" {
				found = true
			}
		}
		if !found {
			ids = append(ids, "mcp_pet")
		}
	}
	servers := make([]backend.MCPServer, 0, len(ids)+1)
	allowed := make([]string, 0)
	if len(ids) > 0 {
		stored, err := s.store.ListMCPServers(ctx)
		if err == nil {
			byID := map[string]domain.MCPServer{}
			for _, server := range stored {
				byID[server.ID] = server
			}
			for _, id := range ids {
				server, ok := byID[id]
				if !ok || !server.Enabled {
					continue
				}
				if server.Builtin && server.ID == "mcp_pet" {
					url := strings.TrimSpace(os.Getenv("AHA2_PET_MCP_URL"))
					if url == "" {
						url = "http://127.0.0.1:8766/api/v1/pet/mcp"
					}
					servers = append(servers, backend.MCPServer{
						Name: "aha2-pet", Transport: "http", URL: url,
					})
					allowed = append(allowed, petMCPAllowedTools(task)...)
					continue
				}
				if server.Transport == "stdio" && server.Command != "" {
					command, args := mcpStdioCommand(server)
					servers = append(servers, backend.MCPServer{
						Name: server.ID, Transport: "stdio",
						Command: command, Args: args,
						Env: cloneStringMap(server.Env),
					})
					allowed = append(allowed, mcpAllowedToolNames(server)...)
					continue
				}
				if server.Transport == "http" && server.URL != "" {
					servers = append(servers, backend.MCPServer{
						Name: server.ID, Transport: "http",
						URL: server.URL, Headers: cloneStringMap(server.Headers),
					})
					allowed = append(allowed, mcpAllowedToolNames(server)...)
				}
			}
		}
	}
	if agentAPIURL = strings.TrimSpace(agentAPIURL); agentAPIURL != "" {
		if group, _ := s.store.HasActiveTaskGroupRoute(ctx, task.ID); group {
			servers = append(servers, backend.MCPServer{
				Name: channelMCPName, Transport: "http",
				URL:            strings.TrimRight(agentAPIURL, "/") + channelMCPPath,
				URLFromEnv:     "AHA2_AGENT_API_URL",
				URLPath:        channelMCPPath,
				BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
			})
			allowed = append(allowed,
				"mcp__"+channelMCPName+"__channel_reply",
				"mcp__"+channelMCPName+"__channel_reply_decision",
			)
		}
	}
	return servers, allowed
}

// mcpAllowedToolNames mirrors the exact tool allowlist a stored MCP server
// declares. Servers without a discovered tool list keep the wildcard form the
// settings UI already promises.
func mcpAllowedToolNames(server domain.MCPServer) []string {
	if len(server.Tools) == 0 {
		return []string{fmt.Sprintf("mcp__%s__*", server.ID)}
	}
	disabled := map[string]bool{}
	for _, name := range server.DisabledTools {
		disabled[name] = true
	}
	names := make([]string, 0, len(server.Tools))
	for _, tool := range server.Tools {
		if tool.Name != "" && !disabled[tool.Name] {
			names = append(names, fmt.Sprintf("mcp__%s__%s", server.ID, tool.Name))
		}
	}
	return names
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func mcpStdioCommand(item domain.MCPServer) (string, []string) {
	command := strings.TrimSpace(item.Command)
	args := append([]string{}, item.Args...)
	if runtime.GOOS == "windows" {
		lower := strings.ToLower(command)
		if strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat") {
			return "cmd.exe", append([]string{"/c", command}, args...)
		}
	}
	return command, args
}
