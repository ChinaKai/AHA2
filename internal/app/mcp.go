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
		if id == "mcp_channel" || id == "mcp_mail" || id == "mcp_core" || id == knowledgeMCPName || id == workspaceMCPName || id == collabMCPName || id == browserMCPName || id == hardwareMCPName || (id == "mcp_pet" && project.ID != petProjectID) {
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
	workspace := s.workspaceMCPDetail()
	collab := s.collabMCPDetail()
	browser := s.browserMCPDetail()
	hardware := s.hardwareMCPDetail()
	insertAt := 0
	for index, item := range items {
		if item.Builtin {
			insertAt = index + 1
		}
	}
	combined := make([]domain.MCPServer, 0, len(items)+8)
	combined = append(combined, items[:insertAt]...)
	combined = append(combined, channel, mail, core, knowledge, workspace, collab, browser, hardware)
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
		"memory_update":   "更新当前 Task 的状态卡（现在到哪了、下一步、约束、待决定事项），可同时变更目标；Main 专用。",
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
		Description: "群聊路由的 Turn 自动注入；提供渠道回复（可 @ 联系人）、结束回复、Owner 权限申请和联系人查询工具。",
		Transport:   "builtin", Enabled: true, Builtin: true, AutoInject: true, Health: "ok",
		Tools: []domain.MCPTool{
			{Name: "channel_reply", Description: "回复触发本 Turn 的渠道消息；立即送达，只在最终答复时调用一次。群聊里自动 @ 发送者，可另外 @ 联系人。"},
			{Name: "channel_reply_decision", Description: "本 Turn 无需回复时结束；理由只显示在 AHA Web 状态卡，不发到群里。"},
			{Name: "channel_request_permission", Description: "为非 Owner 群成员的敏感操作创建 Owner 权限申请；批准前不要执行。"},
			{Name: "channel_contacts", Description: "列出本 Task 群里可以 @ 的联系人及其 ID，供 channel_reply 的 mention_identity_link_ids 使用。"},
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

// workspaceMCPDetail describes the built-in workspace domain MCP. The endpoint
// is injected per Turn and claims no static URL. It is read-only: process
// start/stop, shell, file access and port forwarding stay on their dedicated
// Agent APIs.
func (s *Service) workspaceMCPDetail() domain.MCPServer {
	now := s.now().UTC()
	descriptions := map[string]string{
		"workspace_list":         "读取当前 Task 绑定 Workspace 的非敏感元数据；不含根路径、SSH 凭据或内部地址。",
		"workspace_process_list": "列出当前 Task 管理的进程状态摘要；不含命令行、环境变量、工作目录或输出。",
		"workspace_process_get":  "按名称读取当前 Task 的一个进程状态摘要；跨 Task 名称按未找到处理。",
		"workspace_runtime_list": "列出当前 Project 可用的运行配置字段与非密钥值；不含 token、密钥、环境变量或内网地址。",
	}
	tools := make([]domain.MCPTool, 0, len(workspaceMCPReadToolNames))
	for _, name := range workspaceMCPToolNames() {
		tools = append(tools, domain.MCPTool{Name: name, Description: descriptions[name]})
	}
	return domain.MCPServer{
		ID: workspaceMCPName, Name: "工作区（内置）",
		Description: "Task 绑定 Workspace 后自动注入；只读提供工作区元数据、进程状态与运行配置，不含写操作。",
		Transport:   "builtin", Enabled: true, Builtin: true, AutoInject: true, Health: "ok",
		Tools: tools, CreatedAt: now, UpdatedAt: now,
	}
}

// collabMCPDetail describes the built-in collaboration domain MCP. The endpoint
// is injected per Turn for a Main agent in auto collaboration mode; it claims
// no static URL and offers no agent-lifecycle operation beyond the existing
// batch submit and wait endpoints.
func (s *Service) collabMCPDetail() domain.MCPServer {
	now := s.now().UTC()
	descriptions := map[string]string{
		"collab_batch_submit": "向当前 Task 的子 Agent 提交一个协作批次；仅自动协作模式的 Main 可用，沿用现有数量上限与去重校验。",
		"collab_batch_wait":   "轮询当前 Task 本轮的一个协作批次；仅 Main 可用，游标单调，超时返回未完成而不是错误。",
	}
	tools := make([]domain.MCPTool, 0, len(collabMCPToolNames))
	for _, name := range collabMCPToolNames {
		tools = append(tools, domain.MCPTool{Name: name, Description: descriptions[name]})
	}
	return domain.MCPServer{
		ID: collabMCPName, Name: "协作（内置）",
		Description: "Task 为自动协作模式且当前由 Main 执行时注入；提供批次提交与等待，只读 Agent 与单 Agent 模式均不注入。",
		Transport:   "builtin", Enabled: true, Builtin: true, AutoInject: true, Health: "ok",
		Tools: tools, CreatedAt: now, UpdatedAt: now,
	}
}

// browserMCPDetail describes the built-in shared-browser domain MCP. The
// endpoint is injected per Turn only while the Task holds an active browser
// grant; it claims no static URL and exposes no page interaction, tab lifecycle,
// cookie, frame or grant management operation.
func (s *Service) browserMCPDetail() domain.MCPServer {
	now := s.now().UTC()
	descriptions := map[string]string{
		"browser_status":    "读取当前 Task 浏览器租约状态、授权 ID、能力列表与标签页数量；不含 Profile 路径、Cookie 或凭据。",
		"browser_tabs_list": "列出当前授权下的标签页 ID、标题、站点与去掉 query/fragment 的网址；不含页面内容。",
		"browser_snapshot":  "读取当前标签页的可读文本与最多 200 个元素（ref/role/name/bounds）；不含输入值、HTML、Cookie 或可执行动作。",
	}
	tools := make([]domain.MCPTool, 0, len(browserMCPToolNames))
	for _, name := range browserMCPToolNames {
		tools = append(tools, domain.MCPTool{Name: name, Description: descriptions[name]})
	}
	return domain.MCPServer{
		ID: browserMCPName, Name: "共享浏览器（内置）",
		Description: "Task 持有活跃浏览器授权时自动注入；只读提供租约、标签页与页面快照，不含点击、导航、上传、Cookie、帧或授权管理。",
		Transport:   "builtin", Enabled: true, Builtin: true, AutoInject: true, Health: "ok",
		Tools: tools, CreatedAt: now, UpdatedAt: now,
	}
}

// hardwareMCPDetail describes the built-in Task hardware domain MCP. Its
// injection trigger (a bound hardware group) is independent from the browser
// domain. It is read-only: connect, disconnect, send, login, ADB exec and
// configuration stay on the existing Agent hardware API.
func (s *Service) hardwareMCPDetail() domain.MCPServer {
	now := s.now().UTC()
	descriptions := map[string]string{
		"hardware_list":          "列出当前 Task 绑定的硬件资源 ID、模式、权限、连接方式与健康状态；不含密码、凭据引用、串口设备或网络配置。",
		"hardware_terminal_read": "按硬件 ID、连接方式与游标读取终端输出，单次响应上限 64 KiB；只读，不含连接、发送、登录或配置操作。",
	}
	tools := make([]domain.MCPTool, 0, len(hardwareMCPToolNames))
	for _, name := range hardwareMCPToolNames {
		tools = append(tools, domain.MCPTool{Name: name, Description: descriptions[name]})
	}
	return domain.MCPServer{
		ID: hardwareMCPName, Name: "Task 硬件（内置）",
		Description: "Task 绑定硬件资源后自动注入；只读提供资源元数据与有界终端输出，不含任何连接、发送、登录、ADB 执行或配置能力。",
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
	if strings.TrimSpace(id) == workspaceMCPName {
		return s.workspaceMCPDetail(), nil
	}
	if strings.TrimSpace(id) == collabMCPName {
		return s.collabMCPDetail(), nil
	}
	if strings.TrimSpace(id) == browserMCPName {
		return s.browserMCPDetail(), nil
	}
	if strings.TrimSpace(id) == hardwareMCPName {
		return s.hardwareMCPDetail(), nil
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
	if strings.TrimSpace(id) == "mcp_pet" || strings.TrimSpace(id) == "mcp_channel" || strings.TrimSpace(id) == "mcp_mail" || strings.TrimSpace(id) == "mcp_core" || strings.TrimSpace(id) == knowledgeMCPName || strings.TrimSpace(id) == workspaceMCPName || strings.TrimSpace(id) == collabMCPName || strings.TrimSpace(id) == browserMCPName || strings.TrimSpace(id) == hardwareMCPName {
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
