package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Service) mcpRuntimeForTask(ctx context.Context, task domain.Task) (string, []string) {
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
	if len(ids) == 0 {
		return "", nil
	}
	servers, err := s.store.ListMCPServers(ctx)
	if err != nil {
		return "", nil
	}
	byID := map[string]domain.MCPServer{}
	for _, server := range servers {
		byID[server.ID] = server
	}
	mcpServers := map[string]any{}
	allowed := []string{}
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
			mcpServers["aha2-pet"] = map[string]any{"type": "http", "url": url}
			allowed = append(allowed, petMCPAllowedTools(task)...)
			continue
		}
		if server.Transport == "stdio" && server.Command != "" {
			entry := map[string]any{"type": "stdio", "command": server.Command}
			if len(server.Args) > 0 {
				entry["args"] = server.Args
			}
			if len(server.Env) > 0 {
				entry["env"] = server.Env
			}
			mcpServers[server.ID] = entry
			if len(server.Tools) > 0 {
				disabled := map[string]bool{}
				for _, name := range server.DisabledTools {
					disabled[name] = true
				}
				for _, tool := range server.Tools {
					if tool.Name != "" && !disabled[tool.Name] {
						allowed = append(allowed, fmt.Sprintf("mcp__%s__%s", server.ID, tool.Name))
					}
				}
			} else {
				allowed = append(allowed, fmt.Sprintf("mcp__%s__*", server.ID))
			}
			continue
		}
		if server.Transport == "http" && server.URL != "" {
			entry := map[string]any{"type": "http", "url": server.URL}
			if len(server.Headers) > 0 {
				entry["headers"] = server.Headers
			}
			mcpServers[server.ID] = entry
			if len(server.Tools) > 0 {
				disabled := map[string]bool{}
				for _, name := range server.DisabledTools {
					disabled[name] = true
				}
				for _, tool := range server.Tools {
					if tool.Name != "" && !disabled[tool.Name] {
						allowed = append(allowed, fmt.Sprintf("mcp__%s__%s", server.ID, tool.Name))
					}
				}
			} else {
				allowed = append(allowed, fmt.Sprintf("mcp__%s__*", server.ID))
			}
		}
	}
	if len(mcpServers) == 0 {
		return "", allowed
	}
	encoded, _ := json.Marshal(map[string]any{"mcpServers": mcpServers})
	return string(encoded), allowed
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
