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
		if id == "mcp_pet" && project.ID != petProjectID {
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
	return items, nil
}

func (s *Service) SaveMCPServer(ctx context.Context, item domain.MCPServer) (domain.MCPServer, error) {
	item.ID = strings.TrimSpace(item.ID)
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
	if strings.TrimSpace(id) == "mcp_pet" {
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
