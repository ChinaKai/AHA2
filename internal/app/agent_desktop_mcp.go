package app

import (
	"context"
	"github.com/ChinaKai/AHA2/internal/domain"
)

const desktopMCPName = "aha2_desktop"
const desktopMCPPath = "/api/v1/agent/desktop/mcp"

var desktopMCPReadNames = []string{"desktop_status", "desktop_observe"}
var desktopMCPWriteNames = []string{"desktop_control", "desktop_action", "desktop_stop"}

func DesktopMCPToolNames(write bool) []string {
	names := append([]string{}, desktopMCPReadNames...)
	if write {
		names = append(names, desktopMCPWriteNames...)
	}
	return names
}

func (s *Service) SetDesktopMCPAvailability(available func(string) bool) {
	s.desktopMCPAvailable = available
}

// Channel input does not change the existing owner share. The desktop
// handlers still require the Task session, target, controller and observation.
func (s *Service) desktopMCPInjected(ctx context.Context, task domain.Task, agentID, batchID string) bool {
	return agentID == "main" && s.petDomainReadable(ctx, task) && s.desktopMCPAvailable != nil && s.desktopMCPAvailable(task.ID)
}

func (s *Service) AgentDesktopMCPAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.desktopMCPInjected(ctx, call.Task, call.Turn.AgentID, call.Turn.InboxBatchID)
}

func (s *Service) AgentDesktopMCPWriteAllowed(ctx context.Context, call AgentCallContext) bool {
	return s.AgentDesktopMCPAllowed(ctx, call) && s.hardwareMCPSnapshotWritable(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
}

func (s *Service) desktopMCPDetail() domain.MCPServer {
	now := s.now().UTC()
	tools := []domain.MCPTool{}
	for _, name := range DesktopMCPToolNames(true) {
		tools = append(tools, domain.MCPTool{Name: name, Description: "在当前Task的已有桌面授权范围内执行 " + name + "；不能选择或扩大共享目标，动作必须引用现有观察和版本。"})
	}
	return domain.MCPServer{ID: desktopMCPName, Name: "共享桌面（内置）", Description: "支持共享桌面时向Main注入状态与观察工具；可写Main可登记控制、执行已有目标动作或停止共享，目标选择仍走owner授权。", Transport: "builtin", Builtin: true, Enabled: true, AutoInject: true, Health: "ok", Tools: tools, CreatedAt: now, UpdatedAt: now}
}
