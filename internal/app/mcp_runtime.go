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

	// coreMCPName is the model-facing namespace of the core Task API MCP. It is
	// injected into every ordinary Turn with the same Turn capability as the
	// HTTP Agent API. Write tools are only advertised to a writable Main Turn.
	coreMCPName = "aha2_core"
	coreMCPPath = "/api/v1/agent/core/mcp"

	// knowledgeMCPName is the model-facing namespace of the knowledge domain
	// MCP. It is injected only when the current Task's knowledge switch is on
	// and the Turn is not resolving another Project through a channel route.
	knowledgeMCPName = "aha2_knowledge"
	knowledgeMCPPath = "/api/v1/agent/knowledge/mcp"

	// workspaceMCPName is the model-facing namespace of the workspace domain
	// MCP. It is injected only when the Task has a bound Workspace in its own
	// Project and the Turn is not hosted by a channel. The first slice is
	// read-only: metadata, managed process status and runtime option names.
	workspaceMCPName = "aha2_workspace"
	workspaceMCPPath = "/api/v1/agent/workspace/mcp"
)

var mailMCPToolNames = []string{
	"mail_list_accounts", "mail_list_templates", "mail_template_detail",
	"mail_preview", "mail_send", "mail_get_status",
}

// coreMCPReadToolNames are safe for every Agent in the Task, including a
// read-only review Task or a non-Main collaborator.
var coreMCPReadToolNames = []string{
	"inbox_wait", "task_get", "task_list", "capabilities",
}

// coreMCPWriteToolNames change Task state and are advertised to Main only.
var coreMCPWriteToolNames = []string{
	"memory_patch", "progress", "task_message", "attachment_bind",
}

func coreMCPToolNames(write bool) []string {
	names := append([]string{}, coreMCPReadToolNames...)
	if write {
		names = append(names, coreMCPWriteToolNames...)
	}
	return names
}

// CoreMCPToolNames is the exact allowlist for a core MCP client. It is exported
// so the HTTP facade advertises the same subset the runtime injects.
func CoreMCPToolNames(write bool) []string {
	return coreMCPToolNames(write)
}

// CoreMCPToolIsWrite reports whether a core tool mutates Task state.
func CoreMCPToolIsWrite(name string) bool {
	for _, tool := range coreMCPWriteToolNames {
		if tool == name {
			return true
		}
	}
	return false
}

// knowledgeMCPReadToolNames are safe for every Agent whose Task receives the
// knowledge domain, including a non-Main collaborator or read-only reviewer.
var knowledgeMCPReadToolNames = []string{
	"knowledge_list", "knowledge_get", "knowledge_guide", "skill_list", "skill_get",
}

// knowledgeMCPWriteToolNames change the knowledge library and are advertised
// to a writable Main Turn only.
var knowledgeMCPWriteToolNames = []string{
	"knowledge_submit", "knowledge_feedback",
}

func knowledgeMCPToolNames(write bool) []string {
	names := append([]string{}, knowledgeMCPReadToolNames...)
	if write {
		names = append(names, knowledgeMCPWriteToolNames...)
	}
	return names
}

// KnowledgeMCPToolNames is the exact allowlist for a knowledge MCP client. It
// is exported so the HTTP facade advertises the same subset the runtime injects.
func KnowledgeMCPToolNames(write bool) []string {
	return knowledgeMCPToolNames(write)
}

// KnowledgeMCPToolIsWrite reports whether a knowledge tool mutates the library.
func KnowledgeMCPToolIsWrite(name string) bool {
	for _, tool := range knowledgeMCPWriteToolNames {
		if tool == name {
			return true
		}
	}
	return false
}

// workspaceMCPReadToolNames are the complete first-slice tool set. Every tool
// is read-only and safe for any Agent whose Task receives the domain,
// including a read-only reviewer or a non-Main collaborator.
var workspaceMCPReadToolNames = []string{
	"workspace_list", "workspace_process_list", "workspace_process_get", "workspace_runtime_list",
}

// WorkspaceMCPToolNames is the exact allowlist for a workspace MCP client. It
// is exported so the HTTP facade advertises the same subset the runtime
// injects, and so a call to an unadvertised tool is rejected before dispatch.
func WorkspaceMCPToolNames() []string {
	return workspaceMCPToolNames()
}

func workspaceMCPToolNames() []string {
	return append([]string{}, workspaceMCPReadToolNames...)
}

// WorkspaceMCPToolIsRead reports whether a workspace tool is in the read-only
// first slice. There is intentionally no write tool in this domain.
func WorkspaceMCPToolIsRead(name string) bool {
	for _, tool := range workspaceMCPReadToolNames {
		if tool == name {
			return true
		}
	}
	return false
}

// workspaceMCPInjected mirrors AgentWorkspaceMCPAllowed for the runtime path,
// which has no AgentCallContext yet. A Task needs a bound Workspace that
// belongs to the same Project; the pet Task and channel-hosted Turns are
// excluded by the shared rules. inboxBatchID is the Turn's actual batch.
func (s *Service) workspaceMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if task.ID == petTaskID || task.ProjectID == petProjectID {
		return false
	}
	workspaceID := strings.TrimSpace(task.WorkspaceID)
	if workspaceID == "" {
		return false
	}
	item, err := s.store.Workspace(ctx, workspaceID)
	if err != nil || item.ProjectID != task.ProjectID {
		return false
	}
	return !s.agentMCPChannelBound(ctx, task.ID, inboxBatchID)
}

// knowledgeMCPInjected mirrors AgentKnowledgeMCPAllowed for the runtime path,
// which has no AgentCallContext yet. The pet Task is excluded because its root
// libraries are outside a Project, and channel-bound Turns (task_route or
// conversation_host) are excluded because their knowledge resolves from
// another source. inboxBatchID is the Turn's actual batch, not just the Task.
func (s *Service) knowledgeMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if task.ID == petTaskID || task.ProjectID == petProjectID || !knowledgeEnabled(domain.Project{}, task) {
		return false
	}
	return !s.agentMCPChannelBound(ctx, task.ID, inboxBatchID)
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
	// Namespaces that a built-in server will occupy in this Turn. A legacy
	// user server that normalizes onto one of them must be skipped: Codex
	// renders the namespace as a TOML key, and two tables with the same key
	// make the backend configuration invalid.
	builtinNamespaces := map[string]string{
		normalizedMCPIdentifier(coreMCPName):    "core",
		normalizedMCPIdentifier(channelMCPName): "channel",
		normalizedMCPIdentifier(mailMCPName):    "mail",
		normalizedMCPIdentifier("aha2-pet"):     "pet",
		normalizedMCPIdentifier("mcp_core"):     "core",
		normalizedMCPIdentifier("mcp_channel"):  "channel",
		normalizedMCPIdentifier("mcp_mail"):     "mail",
		normalizedMCPIdentifier("mcp_pet"):      "pet",
	}
	seenNamespaces := map[string]string{}
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
					petServer := backend.MCPServer{Name: "aha2-pet", Transport: "http", URL: url}
					if s.agentAPI != nil && strings.TrimSpace(agentAPIURL) != "" {
						// The endpoint uses the same Turn capability as the Agent
						// API to prove a mutating call comes from the pet Task.
						petServer.BearerTokenEnv = "AHA2_AGENT_API_TOKEN"
					}
					servers = append(servers, petServer)
					allowed = append(allowed, petMCPAllowedTools(task)...)
					seenNamespaces[normalizedMCPIdentifier("aha2-pet")] = server.ID
					continue
				}
				// The whole normalized aha2_* range is reserved, not just the
				// names of the built-in servers. Legacy rows can predate the
				// save-time check, so stop before refresh, server append and
				// allowlist construction; otherwise an unknown suffix such as
				// AHA2-extra would be diagnosed by the catalog but still injected.
				if reservedMCPIdentifier(server.ID) {
					log.Printf("mcp server %s skipped: normalized namespace uses the reserved aha2_* range", server.ID)
					continue
				}
				namespace := normalizedMCPIdentifier(server.ID)
				if builtin, reserved := builtinNamespaces[namespace]; reserved {
					log.Printf("mcp server %s skipped: normalized namespace collides with built-in %s", server.ID, builtin)
					continue
				}
				if previous, duplicate := seenNamespaces[namespace]; duplicate {
					log.Printf("mcp server %s skipped: normalized namespace duplicates %s", server.ID, previous)
					continue
				}
				seenNamespaces[namespace] = server.ID
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

// mcpRuntimeForTurn adds the core Task API MCP to the servers a Turn receives.
// The core server is only injected when an Agent API URL exists and the Task is
// not the pet Task. A writable Main Turn receives all tools; everyone else —
// non-Main collaborators and read-only review Tasks — receives read tools only.
// This keeps one Turn from being offered the entire mutating surface at once.
func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agentID, agentAPIURL, inboxBatchID string) ([]backend.MCPServer, []string) {
	servers, allowed := s.mcpRuntimeForTask(ctx, task, agentAPIURL)
	agentAPIURL = strings.TrimSpace(agentAPIURL)
	if agentAPIURL == "" || task.ID == petTaskID {
		return servers, allowed
	}
	write := false
	if agent, err := s.store.TaskAgent(ctx, task.ID, strings.TrimSpace(agentID)); err == nil {
		write = s.agentSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agent.AgentID)
	}
	servers = append(servers, backend.MCPServer{
		Name: coreMCPName, Transport: "http",
		URL:            strings.TrimRight(agentAPIURL, "/") + coreMCPPath,
		URLFromEnv:     "AHA2_AGENT_API_URL",
		URLPath:        coreMCPPath,
		BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
	})
	for _, tool := range coreMCPToolNames(write) {
		allowed = append(allowed, "mcp__"+coreMCPName+"__"+tool)
	}
	if s.knowledgeMCPInjected(ctx, task, inboxBatchID) {
		servers = append(servers, backend.MCPServer{
			Name: knowledgeMCPName, Transport: "http",
			URL:            strings.TrimRight(agentAPIURL, "/") + knowledgeMCPPath,
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        knowledgeMCPPath,
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
		})
		for _, tool := range knowledgeMCPToolNames(write) {
			allowed = append(allowed, "mcp__"+knowledgeMCPName+"__"+tool)
		}
	}
	if s.workspaceMCPInjected(ctx, task, inboxBatchID) {
		servers = append(servers, backend.MCPServer{
			Name: workspaceMCPName, Transport: "http",
			URL:            strings.TrimRight(agentAPIURL, "/") + workspaceMCPPath,
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        workspaceMCPPath,
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
		})
		for _, tool := range workspaceMCPToolNames() {
			allowed = append(allowed, "mcp__"+workspaceMCPName+"__"+tool)
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
