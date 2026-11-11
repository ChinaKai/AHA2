package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	// mcpToolCacheTTL bounds how often a Turn re-reads tools/list from a bound
	// server. The persisted tool list doubles as the runtime allowlist, so
	// without a refresh a server-side tool change would never reach later Turns.
	mcpToolCacheTTL = time.Minute

	// mcpToolRefreshTimeout bounds the Turn-start refresh. A healthy local
	// server answers in milliseconds; an unavailable one falls back to the
	// cached list after this budget instead of stalling the Turn for the full
	// stdio fetch timeout.
	mcpToolRefreshTimeout = 10 * time.Second

	// channelMCPName is the model-facing namespace of the built-in group-chat
	// MCP. Keeping it stable preserves tool names across backends and sessions.
	channelMCPName = "aha2_channel"
	// channelMCPPath is mounted with the same turn-scoped Agent capability that
	// protects every other agent endpoint.
	channelMCPPath = "/api/v1/agent/channel/mcp"

	// mailMCPName is the model-facing namespace of the built-in mail MCP. It is
	// injected when the Task has at least one enabled mail binding.
	mailMCPName = "aha2_mail"
	mailMCPPath = "/api/v1/agent/mail/mcp"
)

var mailMCPToolNames = []string{
	"mail_list_accounts", "mail_list_templates", "mail_template_detail",
	"mail_preview", "mail_send", "mail_get_status",
}

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
				server = s.refreshMCPToolsIfStale(ctx, server)
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
		if routed, _ := s.store.HasActiveChannelTaskRoute(ctx, task.ID); routed {
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
				"mcp__"+channelMCPName+"__channel_request_permission",
			)
		}
		if accounts, _, _ := s.MailTaskResources(ctx, task); len(accounts) > 0 {
			servers = append(servers, backend.MCPServer{
				Name: mailMCPName, Transport: "http",
				URL:            strings.TrimRight(agentAPIURL, "/") + mailMCPPath,
				URLFromEnv:     "AHA2_AGENT_API_URL",
				URLPath:        mailMCPPath,
				BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
			})
			for _, tool := range mailMCPToolNames {
				allowed = append(allowed, "mcp__"+mailMCPName+"__"+tool)
			}
		}
	}
	return servers, allowed
}

// refreshMCPToolsIfStale re-probes a bound server whose persisted tool list is
// older than mcpToolCacheTTL. ClaimMCPServerProbe takes the refresh slot
// atomically before any process talks to the server: when concurrent Turns —
// or a second Service instance sharing the same database — load the same
// stale row, exactly one of them probes and the others keep the cached list
// for this Turn, so a server is probed at most once per cache window. The
// refresh is best effort: a failed probe keeps the last known tools, and the
// claim already advanced last_checked_at, so an unavailable server is also
// not retried until the next window.
func (s *Service) refreshMCPToolsIfStale(ctx context.Context, server domain.MCPServer) domain.MCPServer {
	if server.Builtin || !server.Enabled {
		return server
	}
	transport := strings.ToLower(strings.TrimSpace(server.Transport))
	switch transport {
	case "stdio":
		if strings.TrimSpace(server.Command) == "" {
			return server
		}
	case "http":
		if strings.TrimSpace(server.URL) == "" {
			return server
		}
	default:
		return server
	}
	now := s.now().UTC()
	if !server.LastCheckedAt.IsZero() && now.Sub(server.LastCheckedAt) < mcpToolCacheTTL {
		return server
	}
	claimed, err := s.store.ClaimMCPServerProbe(ctx, server.ID, now, now.Add(-mcpToolCacheTTL))
	if err != nil {
		// A failed claim must not degrade into a probe stampede: keep the
		// cached list and let the next Turn retry the claim. The error is a
		// store failure, never server-controlled text.
		log.Printf("mcp tools refresh claim failed: server=%s err=%v", server.ID, err)
		return server
	}
	if !claimed {
		return server
	}
	budget := s.mcpToolRefreshBudget
	if budget <= 0 {
		budget = mcpToolRefreshTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	tools, probeErr := s.fetchServerTools(probeCtx, server)
	// Persist with the Turn context, not probeCtx: a timed-out probe still has
	// to record LastCheckedAt and keep the previous tool list.
	refreshed := s.persistMCPProbe(ctx, server, tools, probeErr)
	elapsed := s.now().UTC().Sub(now).Round(time.Millisecond)
	if probeErr != nil {
		log.Printf("mcp tools refresh failed: server=%s elapsed=%s cached_tools=%d", server.ID, elapsed, len(refreshed.Tools))
	} else {
		log.Printf("mcp tools refreshed: server=%s elapsed=%s tools=%d", server.ID, elapsed, len(refreshed.Tools))
	}
	return refreshed
}

// fetchServerTools runs one tools/list probe over the configured transport.
// The mcpToolFetch seam lets tests substitute the transport without spawning
// processes or HTTP endpoints.
func (s *Service) fetchServerTools(ctx context.Context, server domain.MCPServer) ([]domain.MCPTool, error) {
	if s.mcpToolFetch != nil {
		return s.mcpToolFetch(ctx, server)
	}
	switch strings.ToLower(strings.TrimSpace(server.Transport)) {
	case "stdio":
		return fetchStdioTools(ctx, server)
	case "http":
		return fetchMCPTools(ctx, server)
	}
	return nil, fmt.Errorf("MCP transport 不支持: %q", server.Transport)
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
