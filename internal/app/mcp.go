package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Service) normalizeTaskMCPServers(ctx context.Context, project domain.Project, requested []string) ([]string, error) {
	servers, err := s.store.ListMCPServers(ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[string]domain.MCPServer{}
	for _, server := range servers {
		allowed[server.ID] = server
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(requested)+1)
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		if _, ok := allowed[id]; !ok {
			return
		}
		seen[id] = true
		result = append(result, id)
	}
	if project.ID == petProjectID {
		add("mcp_pet")
	}
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "mcp_channel" || (id == "mcp_pet" && project.ID != petProjectID) {
			continue
		}
		server, ok := allowed[id]
		if !ok || !server.Enabled {
			continue
		}
		add(id)
	}
	return result, nil
}

func (s *Service) MCPServers(ctx context.Context) ([]domain.MCPServer, error) {
	items, err := s.store.ListMCPServers(ctx)
	if err != nil {
		return nil, err
	}
	for index := range items {
		if items[index].ID == "mcp_pet" {
			items[index].Health = "ok"
			items[index].LastError = ""
			items[index].Tools = petMCPToolDetails()
		}
	}
	// The channel MCP is turn-scoped: its URL and token come from the current
	// Agent capability and it is attached only to channel-routed Turns. Keep it
	// virtual instead of persisting a server whose address would be misleading,
	// but still list it so the MCP page shows every built-in capability.
	channel := s.channelMCPDetail()
	insertAt := 0
	for index, item := range items {
		if item.Builtin {
			insertAt = index + 1
		}
	}
	combined := make([]domain.MCPServer, 0, len(items)+1)
	combined = append(combined, items[:insertAt]...)
	combined = append(combined, channel)
	combined = append(combined, items[insertAt:]...)
	return combined, nil
}

// channelMCPDetail describes the built-in channel MCP without claiming a
// static URL. The actual endpoint is injected per Turn by mcpRuntimeForTask.
func (s *Service) channelMCPDetail() domain.MCPServer {
	now := s.now().UTC()
	return domain.MCPServer{
		ID: "mcp_channel", Name: "渠道（内置）",
		Description: "群聊路由的 Turn 自动注入；提供渠道回复、结束回复和 Owner 权限申请工具。",
		Transport:   "builtin", Enabled: true, Builtin: true, AutoInject: true, Health: "ok",
		Tools: []domain.MCPTool{
			{Name: "channel_reply", Description: "回复触发本 Turn 的渠道消息；立即送达，只在最终答复时调用一次。"},
			{Name: "channel_reply_decision", Description: "本 Turn 无需回复时结束；理由只显示在 AHA Web 状态卡，不发到群里。"},
			{Name: "channel_request_permission", Description: "为非 Owner 群成员的敏感操作创建 Owner 权限申请；批准前不要执行。"},
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func (s *Service) SaveMCPServer(ctx context.Context, item domain.MCPServer) (domain.MCPServer, error) {
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "mcp_pet" || item.ID == "mcp_channel" {
		return domain.MCPServer{}, fmt.Errorf("builtin MCP cannot be modified")
	}
	item.Name = strings.TrimSpace(item.Name)
	item.URL = strings.TrimSpace(item.URL)
	item.Transport = strings.TrimSpace(strings.ToLower(item.Transport))
	if item.Transport == "" {
		item.Transport = "http"
	}
	if item.Transport != "http" && item.Transport != "stdio" {
		return domain.MCPServer{}, fmt.Errorf("unsupported MCP transport")
	}
	if item.Name == "" {
		return domain.MCPServer{}, fmt.Errorf("MCP name is required")
	}
	if item.Transport == "http" && !strings.HasPrefix(item.URL, "http://") && !strings.HasPrefix(item.URL, "https://") {
		return domain.MCPServer{}, fmt.Errorf("HTTP MCP URL is required")
	}
	if item.Transport == "stdio" && strings.TrimSpace(item.Command) == "" {
		return domain.MCPServer{}, fmt.Errorf("stdio MCP command is required")
	}
	existing, err := s.store.ListMCPServers(ctx)
	if err == nil {
		for _, server := range existing {
			if server.Builtin && server.ID == item.ID {
				return domain.MCPServer{}, fmt.Errorf("builtin MCP cannot be modified")
			}
		}
	}
	if item.ID == "" {
		item.ID = domain.NewID("mcp")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = s.now().UTC()
	}
	item.UpdatedAt = s.now().UTC()
	item.Builtin = false
	item.Health = "unknown"
	item.LastError = ""
	item.LastCheckedAt = time.Time{}
	if err := s.store.UpsertMCPServer(ctx, item); err != nil {
		return domain.MCPServer{}, err
	}
	return item, nil
}

func (s *Service) MCPServerByID(ctx context.Context, id string) (domain.MCPServer, error) {
	if strings.TrimSpace(id) == "mcp_channel" {
		return s.channelMCPDetail(), nil
	}
	items, err := s.store.ListMCPServers(ctx)
	if err != nil {
		return domain.MCPServer{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.MCPServer{}, fmt.Errorf("MCP not found")
}

func (s *Service) DeleteMCPServer(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "mcp_pet" || strings.TrimSpace(id) == "mcp_channel" {
		return fmt.Errorf("builtin MCP cannot be deleted")
	}
	return s.store.DeleteMCPServer(ctx, id)
}

func (s *Service) SetTaskMCPServers(ctx context.Context, taskID string, ids []string) (domain.Task, error) {
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		return domain.Task{}, err
	}
	normalized, err := s.normalizeTaskMCPServers(ctx, project, ids)
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.store.UpdateTaskMCPServers(ctx, taskID, normalized); err != nil {
		return domain.Task{}, err
	}
	return s.store.Task(ctx, taskID)
}
