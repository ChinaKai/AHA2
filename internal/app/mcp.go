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
		if id == "mcp_channel" || id == "mcp_mail" || id == "mcp_core" || id == knowledgeMCPName || (id == "mcp_pet" && project.ID != petProjectID) {
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
	// Legacy rows could predate the reserved-namespace validation. Surface a
	// conflict instead of silently rendering two backend tables with the same
	// normalized key; runtime injection skips these entries as well.
	seenMCPNamespaces := map[string]string{}
	for index := range items {
		namespace := normalizedMCPIdentifier(items[index].ID)
		if namespace == "" {
			continue
		}
		if reservedMCPIdentifier(items[index].ID) {
			items[index].Health = "error"
			items[index].LastError = "MCP ID uses the reserved aha2_* namespace after normalization; this server is not injected"
			continue
		}
		if !items[index].Builtin && isBuiltinMCPIdentifier(items[index].ID) {
			items[index].Health = "error"
			items[index].LastError = "MCP ID conflicts with a built-in MCP namespace after normalization; this server is not injected"
			continue
		}
		if previous, ok := seenMCPNamespaces[namespace]; ok && previous != items[index].ID {
			items[index].Health = "error"
			items[index].LastError = fmt.Sprintf("MCP ID conflicts with %s after normalization; this server is not injected", previous)
			continue
		}
		seenMCPNamespaces[namespace] = items[index].ID
	}
	// The channel MCP is turn-scoped: its URL and token come from the current
	// Agent capability and it is attached only to channel-routed Turns. Keep it
	// virtual instead of persisting a server whose address would be misleading,
	// but still list it so the MCP page shows every built-in capability.
	channel := s.channelMCPDetail()
	mail := s.mailMCPDetail()
	core := s.coreMCPDetail()
	knowledge := s.knowledgeMCPDetail()
	insertAt := 0
	for index, item := range items {
		if item.Builtin {
			insertAt = index + 1
		}
	}
	combined := make([]domain.MCPServer, 0, len(items)+4)
	combined = append(combined, items[:insertAt]...)
	combined = append(combined, channel, mail, core, knowledge)
	combined = append(combined, items[insertAt:]...)
	return combined, nil
}

// coreMCPDetail describes the built-in core Task API MCP without claiming a
// static URL. The endpoint is injected per Turn by mcpRuntimeForTurn and uses
// the same short-lived Turn capability as every other Agent endpoint.
func (s *Service) coreMCPDetail() domain.MCPServer {
	now := s.now().UTC()
	descriptions := map[string]string{
		"inbox_wait":      "读取并认领当前 Turn 的 live inbox 消息（只读工具）。",
		"task_get":        "读取当前 Task 或同 Project 的另一个 Task 摘要；不跨 Project。",
		"task_list":       "列出当前 Project 内最近的 Task 摘要。",
		"capabilities":    "读取当前 Turn 的 Agent 能力清单。",
		"memory_patch":    "向当前 Task 记忆追加 decisions/facts/progress 等数组；Main 专用，不能改 Task 目标。",
		"progress":        "上报一条进度消息；Main 专用。",
		"task_message":    "向同 Project 的另一个 Task 发送消息；Main 专用，来源标记为 peer task。",
		"attachment_bind": "把当前 Task 已上传且未绑定的附件 ID 绑定到一条附件消息；Main 专用。",
	}
	tools := make([]domain.MCPTool, 0, len(coreMCPReadToolNames)+len(coreMCPWriteToolNames))
	for _, name := range coreMCPToolNames(true) {
		tools = append(tools, domain.MCPTool{Name: name, Description: descriptions[name]})
	}
	return domain.MCPServer{
		ID: "mcp_core", Name: "核心 Agent API（内置）",
		Description: "每个普通 Turn 自动注入；Main 获得读写工具，其他 Agent 只获得只读工具。",
		Transport:   "builtin", Enabled: true, Builtin: true, AutoInject: true, Health: "ok",
		Tools: tools, CreatedAt: now, UpdatedAt: now,
	}
}

// mailMCPDetail describes the built-in mail MCP without claiming a static URL.
// The endpoint is injected per Turn when the Task has at least one enabled mail
// binding.
func (s *Service) mailMCPDetail() domain.MCPServer {
	now := s.now().UTC()
	tools := make([]domain.MCPTool, 0, len(mailMCPToolNames))
	descriptions := map[string]string{
		"mail_list_accounts":   "列出当前 Task 可用的邮件账号（不含凭据）。",
		"mail_list_templates":  "列出当前 Task/Project 可见的活动模板及 mode/description/version 元数据。",
		"mail_template_detail": "返回模板完整示例主题、正文、填写说明与模式；guide 模式由调用方自行撰写最终内容。",
		"mail_preview":         "按账号、模板（或直接主题/正文）、收件人与受控附件渲染预览并校验。",
		"mail_send":            "按模板或直接主题/正文创建待 Owner 审批的邮件；固定返回 pending_approval。",
		"mail_get_status":      "查询当前 Task 邮件的审批与投递状态。",
	}
	for _, name := range mailMCPToolNames {
		tools = append(tools, domain.MCPTool{Name: name, Description: descriptions[name]})
	}
	return domain.MCPServer{
		ID: "mcp_mail", Name: "邮件（内置）",
		Description: "Task 绑定邮件账号后自动注入；提供账号、模板、预览、发送与状态工具。",
		Transport:   "builtin", Enabled: true, Builtin: true, AutoInject: true, Health: "ok",
		Tools: tools, CreatedAt: now, UpdatedAt: now,
	}
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

// knowledgeMCPDetail describes the built-in knowledge domain MCP. The endpoint
// is injected per Turn with the Turn capability; it claims no static URL.
func (s *Service) knowledgeMCPDetail() domain.MCPServer {
	now := s.now().UTC()
	descriptions := map[string]string{
		"knowledge_list":     "列出当前 Project 与全局知识的元数据；支持查询、类型和分页，不返回正文或其他 Project。",
		"knowledge_get":      "读取当前可读范围内一条知识的正文并标明 vault/legacy 来源；跨 Project ID 按未找到处理。",
		"knowledge_guide":    "读取知识库静态指南；不暴露内部路径或凭据。",
		"skill_list":         "列出当前 Task 已选 Skill 的元数据；不包含打包文件、vault key 或内部路径。",
		"skill_get":          "只读读取当前 Task 已选 Skill 的说明；不包含打包文件、vault key 或内部路径。",
		"knowledge_submit":   "向当前 Project 提交 create/revise 候选；Main 专用，仅 project scope，不支持发布、删除或审核。",
		"knowledge_feedback": "为当前可读范围内的一条知识记录 helped/stale/wrong；Main 专用。",
	}
	tools := make([]domain.MCPTool, 0, len(knowledgeMCPReadToolNames)+len(knowledgeMCPWriteToolNames))
	for _, name := range knowledgeMCPToolNames(true) {
		tools = append(tools, domain.MCPTool{Name: name, Description: descriptions[name]})
	}
	return domain.MCPServer{
		ID: knowledgeMCPName, Name: "知识库（内置）",
		Description: "当前 Task 开启知识时自动注入；读取工具对所有 Agent 开放，写入工具仅限可写 Main。",
		Transport:   "builtin", Enabled: true, Builtin: true, AutoInject: true, Health: "ok",
		Tools: tools, CreatedAt: now, UpdatedAt: now,
	}
}

// normalizedMCPIdentifier mirrors the namespace normalization applied by an
// MCP backend that has to express a server name as a TOML key: case is folded
// and hyphen/dot separators become underscores. It is used only for collision
// and reserved-namespace checks; stored and rendered names are never rewritten.
func normalizedMCPIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, ".", "_")
	return value
}

func isBuiltinMCPIdentifier(value string) bool {
	switch normalizedMCPIdentifier(value) {
	case "mcp_pet", "mcp_channel", "mcp_mail", "mcp_core":
		return true
	}
	return false
}

// reservedMCPIdentifier reports whether a server ID or display name falls in
// the aha2_* namespace reserved for built-in, Turn-scoped MCP servers. A user
// server must never shadow a built-in tool namespace, including a hyphen, dot
// or case variant that a backend would normalize onto the same key. Catalog
// diagnostics and runtime injection both use this helper so a legacy row
// cannot be diagnosed yet still injected.
func reservedMCPIdentifier(value string) bool {
	return strings.HasPrefix(normalizedMCPIdentifier(value), "aha2_")
}

func (s *Service) SaveMCPServer(ctx context.Context, item domain.MCPServer) (domain.MCPServer, error) {
	item.ID = strings.TrimSpace(item.ID)
	if isBuiltinMCPIdentifier(item.ID) {
		return domain.MCPServer{}, fmt.Errorf("builtin MCP cannot be modified")
	}
	item.Name = strings.TrimSpace(item.Name)
	if reservedMCPIdentifier(item.ID) || reservedMCPIdentifier(item.Name) {
		return domain.MCPServer{}, fmt.Errorf("MCP ID or name uses the reserved aha2_* namespace after normalization")
	}
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
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("list MCP servers: %w", err)
	}
	normalizedID := normalizedMCPIdentifier(item.ID)
	for _, server := range existing {
		if server.Builtin && normalizedMCPIdentifier(server.ID) == normalizedID {
			return domain.MCPServer{}, fmt.Errorf("builtin MCP cannot be modified")
		}
		if item.ID != "" && server.ID != item.ID && normalizedMCPIdentifier(server.ID) == normalizedID {
			return domain.MCPServer{}, fmt.Errorf("MCP ID conflicts with existing server %q after normalization", server.ID)
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
	if strings.TrimSpace(id) == "mcp_mail" {
		return s.mailMCPDetail(), nil
	}
	if strings.TrimSpace(id) == "mcp_core" {
		return s.coreMCPDetail(), nil
	}
	if strings.TrimSpace(id) == knowledgeMCPName {
		return s.knowledgeMCPDetail(), nil
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
	if strings.TrimSpace(id) == "mcp_pet" || strings.TrimSpace(id) == "mcp_channel" || strings.TrimSpace(id) == "mcp_mail" || strings.TrimSpace(id) == "mcp_core" || strings.TrimSpace(id) == knowledgeMCPName {
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
