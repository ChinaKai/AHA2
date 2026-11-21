# 复审 M1 / S6：目录契约与单次 Agent 读取

统一固定源码：`9bca3785b3ef1d071faed06041aeb2e6de0139a7`，tree `5d2a491a18fe4df4c9c348c0e491aba87a8537ba`。问题 diff 边界为 `9d01cb63cca51bff23c40dad2c61d55497f2cd3a..9bca3785b3ef1d071faed06041aeb2e6de0139a7` 中下列完整文件；门禁 diff 保留 3 行上下文。所有源码摘录也从该固定提交读取，不来自正在编辑的 worktree。

原断言保护内置域独立、不可手动绑定/修改及已公布工具集合。保留这些断言，并独立按名称固定当前 7 个硬件工具和父提交已有的 16 个浏览器工具；另固定读子集为 2/8，不能把写工具混入只读可见集合。没有删掉目录保护。mcpRuntimeForTurn trim AgentID 后只查一次 TaskAgent，其他域复用同一快照和写权限事实。对应修复提交 bcfd39c；父提交 9d01cb6 是已独立记录、尚未评审的 P2 能力，本文不把 P2 视为已批准。

固定提交的 20 个具名定向回归全部执行且退出 0（136.834s，显式核对每个顶层 Test 的 pass，missing=0）；`go vet ./internal/app ./internal/httpapi ./internal/hardware` 退出 0（1.176s）。完整命令和逐项覆盖在 `recheck-9bca378-validation.md`。固定提交的 app/httpapi 全包尚未完成，当前排队；旧 worktree 全包运行期间源码有改动，只作诊断，不是这个提交的通过证据。不能据此合并或声称完整 task-722 已完成。

完整 diff 文件边界：

- `internal/app/agent_browser_hardware_mcp_test.go`
- `internal/app/mcp_runtime.go`
- `internal/app/agent_pet_domains.go`

## 完整调用链依赖源码

### `internal/app/mcp_runtime.go` · `mcpRuntimeForTurn`

```go
func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agentID, agentAPIURL, inboxBatchID string) ([]backend.MCPServer, []string) {
	servers, allowed := s.mcpRuntimeForTask(ctx, task, agentAPIURL)
	agentAPIURL = strings.TrimSpace(agentAPIURL)
	if agentAPIURL == "" {
		return servers, allowed
	}
	agentID = strings.TrimSpace(agentID)
	agent, agentErr := s.store.TaskAgent(ctx, task.ID, agentID)
	write := agentErr == nil && s.agentSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agentID)
	ioWrite := agentErr == nil && s.hardwareMCPSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agentID)

	servers = append(servers, backend.MCPServer{
		Name: coreMCPName, Transport: "http",
		URL:            strings.TrimRight(agentAPIURL, "/") + coreMCPPath,
		URLFromEnv:     "AHA2_AGENT_API_URL",
		URLPath:        coreMCPPath,
		BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
	})
	coreNames := coreMCPNamesForTask(task, write)
	for _, tool := range coreNames {
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
		for _, tool := range s.knowledgeMCPNamesForTask(ctx, task, write) {
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
		bound, bindErr := s.store.Workspace(ctx, task.WorkspaceID)
		workspaceWrite := bindErr == nil && !bound.ReadOnly && ioWrite

		for _, tool := range WorkspaceMCPToolNamesFor(workspaceWrite) {
			allowed = append(allowed, "mcp__"+workspaceMCPName+"__"+tool)
		}
	}
	if s.browserMCPInjected(ctx, task, inboxBatchID) {
		servers = append(servers, backend.MCPServer{
			Name: browserMCPName, Transport: "http",
			URL:            strings.TrimRight(agentAPIURL, "/") + browserMCPPath,
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        browserMCPPath,
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
		})

		for _, tool := range BrowserMCPToolNamesFor(ioWrite) {
			allowed = append(allowed, "mcp__"+browserMCPName+"__"+tool)
		}
	}
	if s.hardwareMCPInjected(ctx, task, inboxBatchID) {
		servers = append(servers, backend.MCPServer{
			Name: hardwareMCPName, Transport: "http",
			URL:            strings.TrimRight(agentAPIURL, "/") + hardwareMCPPath,
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        hardwareMCPPath,
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
		})

		for _, tool := range HardwareMCPToolNamesFor(ioWrite) {
			allowed = append(allowed, "mcp__"+hardwareMCPName+"__"+tool)
		}
	}
	if s.desktopMCPInjected(ctx, task, agentID, inboxBatchID) {
		servers = append(servers, backend.MCPServer{Name: desktopMCPName, Transport: "http", URL: strings.TrimRight(agentAPIURL, "/") + desktopMCPPath, URLFromEnv: "AHA2_AGENT_API_URL", URLPath: desktopMCPPath, BearerTokenEnv: "AHA2_AGENT_API_TOKEN"})

		for _, tool := range DesktopMCPToolNames(ioWrite) {
			allowed = append(allowed, "mcp__"+desktopMCPName+"__"+tool)
		}
	}

	if s.collabMCPInjected(ctx, task, agentID, inboxBatchID) {
		servers = append(servers, backend.MCPServer{
			Name: collabMCPName, Transport: "http",
			URL:            strings.TrimRight(agentAPIURL, "/") + collabMCPPath,
			URLFromEnv:     "AHA2_AGENT_API_URL",
			URLPath:        collabMCPPath,
			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
		})
		for _, tool := range collabMCPToolNames {
			allowed = append(allowed, "mcp__"+collabMCPName+"__"+tool)
		}
	}
	return servers, allowed
}
```

### `internal/app/mcp_runtime.go` · `mcpRuntimeForTask`

```go
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
				"mcp__"+channelMCPName+"__channel_contacts",
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
```

### `internal/app/mcp_runtime.go` · `workspaceMCPInjected`

```go
func (s *Service) workspaceMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if !s.petDomainReadable(ctx, task) {
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
```

### `internal/app/mcp_runtime.go` · `browserMCPInjected`

```go
func (s *Service) browserMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if !s.petDomainReadable(ctx, task) {
		return false
	}
	if s.agentMCPChannelBound(ctx, task.ID, inboxBatchID) {
		return false
	}
	grants, err := s.store.BrowserTaskGrants(ctx, task.ID)
	if err != nil {
		return false
	}
	now := s.now().UTC()
	for _, grant := range grants {
		if grant.Status(now) != domain.BrowserGrantStatusActive {
			continue
		}
		session, sessionErr := s.store.BrowserSession(ctx, grant.SessionID)
		if sessionErr == nil && session.State != domain.BrowserSessionStateClosed {
			return true
		}
	}
	return false
}
```

### `internal/app/mcp_runtime.go` · `hardwareMCPInjected`

```go
func (s *Service) hardwareMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if !s.petDomainReadable(ctx, task) {
		return false
	}
	if s.agentMCPChannelBound(ctx, task.ID, inboxBatchID) {
		return false
	}
	groups, err := s.store.HardwareGroups(ctx, task.ID)
	return err == nil && len(groups) > 0
}
```

### `internal/app/mcp_runtime.go` · `knowledgeMCPInjected`

```go
func (s *Service) knowledgeMCPInjected(ctx context.Context, task domain.Task, inboxBatchID string) bool {
	if task.ID == petTaskID {
		_, err := s.petKnowledgeReadable(ctx)
		return task.ProjectID == petProjectID && err == nil && !s.agentMCPChannelBound(ctx, task.ID, inboxBatchID)
	}
	if task.ID == petTaskID || task.ProjectID == petProjectID || !knowledgeEnabled(domain.Project{}, task) {
		return false
	}
	return !s.agentMCPChannelBound(ctx, task.ID, inboxBatchID)
}
```

### `internal/app/mcp.go` · `MCPServers`

```go
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
	desktop := s.desktopMCPDetail()
	insertAt := 0
	for index, item := range items {
		if item.Builtin {
			insertAt = index + 1
		}
	}
	combined := make([]domain.MCPServer, 0, len(items)+9)
	combined = append(combined, items[:insertAt]...)
	combined = append(combined, channel, mail, core, knowledge, workspace, collab, browser, hardware, desktop)
	combined = append(combined, items[insertAt:]...)
	return combined, nil
}
```

### `internal/app/mcp.go` · `browserMCPDetail`

```go
func (s *Service) browserMCPDetail() domain.MCPServer {
	now := s.now().UTC()
	descriptions := map[string]string{
		"browser_status":    "读取当前 Task 浏览器租约状态、授权 ID、能力列表与标签页数量；不含 Profile 路径、Cookie 或凭据。",
		"browser_tabs_list": "列出当前授权下的标签页 ID、标题、站点与去掉 query/fragment 的网址；不含页面内容。",
		"browser_snapshot":  "读取当前标签页的可读文本与最多 200 个元素（ref/role/name/bounds）；不含输入值、HTML、Cookie 或可执行动作。",
	}
	tools := make([]domain.MCPTool, 0, len(BrowserMCPToolNames()))
	for _, name := range BrowserMCPToolNames() {
		description := descriptions[name]
		if description == "" {
			description = "在当前 Task 已有授权和能力范围内执行 " + name + "；每次调用重查授权，敏感 Cookie 值不返回模型。"
		}
		tools = append(tools, domain.MCPTool{Name: name, Description: description})
	}
	return domain.MCPServer{
		ID: browserMCPName, Name: "共享浏览器（内置）",
		Description: "Task 持有现有浏览器授权时自动注入；提供状态、页面、帧、下载、书签和 Cookie 元数据，Main 在当前权限允许时可管理标签、导航、动作、上传和释放授权。凭据和授权目标仍由 owner 管理。",
		Transport:   "builtin", Enabled: true, Builtin: true, AutoInject: true, Health: "ok",
		Tools: tools, CreatedAt: now, UpdatedAt: now,
	}
}
```

### `internal/app/mcp.go` · `hardwareMCPDetail`

```go
func (s *Service) hardwareMCPDetail() domain.MCPServer {
	now := s.now().UTC()
	descriptions := map[string]string{
		"hardware_connect":       "连接当前 Task 已绑定的设备；保留资源权限与 host key 检查，不接收或返回凭据。",
		"hardware_disconnect":    "断开已绑定设备连接。",
		"hardware_send":          "向已绑定、可读写的连接发送数据。",
		"hardware_login":         "用服务端已保存的凭据登录设备；不接收密码。",
		"hardware_adb_exec":      "执行设备范围的 ADB 命令；不允许 host 管理或配对。",
		"hardware_list":          "列出当前 Task 绑定的硬件资源 ID、模式、权限、连接方式与健康状态；不含密码、凭据引用、串口设备或网络配置。",
		"hardware_terminal_read": "按硬件 ID、连接方式与游标读取终端输出，单次响应上限 64 KiB；只读，不含连接、发送、登录或配置操作。",
	}
	tools := make([]domain.MCPTool, 0, len(HardwareMCPToolNames()))
	for _, name := range HardwareMCPToolNames() {
		tools = append(tools, domain.MCPTool{Name: name, Description: descriptions[name]})
	}
	return domain.MCPServer{
		ID: hardwareMCPName, Name: "Task 硬件（内置）",
		Description: "Task 绑定硬件资源后自动注入；提供元数据与有界终端读取，Main 在当前权限允许时可连接、断开、发送、登录和执行 ADB；凭据与信任仍由 owner 管理。",
		Transport:   "builtin", Enabled: true, Builtin: true, AutoInject: true, Health: "ok",
		Tools: tools, CreatedAt: now, UpdatedAt: now,
	}
}
```

### `internal/app/agent_core.go` · `agentSnapshotWritable`

```go
func (s *Service) agentSnapshotWritable(ctx context.Context, task domain.Task, snapshotID, agentID string) bool {
	if agentID != "main" || task.ReadOnly || task.Status.Terminal() {
		return false
	}
	snapshot, err := s.store.RuntimeSnapshot(ctx, snapshotID)
	if err != nil {
		return false
	}
	return strictSnapshotWritable(snapshot.PermissionsJSON)
}
```

### `internal/app/agent_core.go` · `strictSnapshotWritable`

```go
func strictSnapshotWritable(permissionsJSON string) bool {
	var payload struct {
		Filesystem *string `json:"filesystem"`
		Approval   *string `json:"approval"`
	}
	if err := json.Unmarshal([]byte(permissionsJSON), &payload); err != nil {
		return false
	}
	if payload.Filesystem == nil || payload.Approval == nil {
		return false
	}
	switch *payload.Approval {
	case "never", "auto":
	default:
		return false
	}
	switch *payload.Filesystem {
	case "workspace-write", "danger-full-access":
		return true
	default:
		return false
	}
}
```

### `internal/app/agent_hardware_mcp.go` · `hardwareMCPSnapshotWritable`

```go
func (s *Service) hardwareMCPSnapshotWritable(ctx context.Context, task domain.Task, snapshotID, agentID string) bool {
	if task.Status.Terminal() || !s.agentSnapshotWritable(ctx, task, snapshotID, agentID) {
		return false
	}
	if task.ID == petTaskID {
		assistant, err := s.petAssistant(ctx)
		return err == nil && assistant.PetAllowCreateTasks
	}
	return true
}
```

### `internal/app/agent_pet_domains.go` · `coreMCPNamesForTask`

```go
func coreMCPNamesForTask(task domain.Task, write bool) []string {
	names := coreMCPToolNames(write)
	if task.ID != petTaskID {
		return names
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if name != "task_message" {
			result = append(result, name)
		}
	}
	return result
}
```

### `internal/app/agent_browser_hardware_mcp_test.go` · `TestBrowserAndHardwareMCPAreIndependentBuiltins`

```go
func TestBrowserAndHardwareMCPAreIndependentBuiltins(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewService(database, nil, nil)

	servers, err := service.MCPServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]domain.MCPServer{}
	for _, item := range servers {
		byID[item.ID] = item
	}
	for id, wantTools := range map[string]int{
		browserMCPName:  len(BrowserMCPToolNames()),
		hardwareMCPName: len(HardwareMCPToolNames()),
	} {
		item, ok := byID[id]
		if !ok {
			t.Fatalf("%s is missing from the catalog", id)
		}
		if !item.Builtin || !item.AutoInject || !item.Enabled || item.Health != "ok" {
			t.Fatalf("unexpected %s catalog entry: %#v", id, item)
		}
		if len(item.Tools) != wantTools {
			t.Fatalf("%s tools=%#v", id, item.Tools)
		}
		detail, err := service.MCPServerByID(ctx, id)
		if err != nil || len(detail.Tools) != wantTools {
			t.Fatalf("%s detail=%#v err=%v", id, detail, err)
		}
		if _, err := service.SaveMCPServer(ctx, domain.MCPServer{
			ID: id, Name: "x", Transport: "http", URL: "https://example.test",
		}); err == nil {
			t.Fatalf("reserved %s save was allowed", id)
		}
		if err := service.DeleteMCPServer(ctx, id); err == nil {
			t.Fatalf("%s delete was allowed", id)
		}
		normalized, err := service.normalizeTaskMCPServers(ctx, domain.Project{ID: "project-normal"}, []string{id})
		if err != nil || len(normalized) != 0 {
			t.Fatalf("%s was assignable: %#v err=%v", id, normalized, err)
		}
	}
	// Pin the published surface independently from the catalog implementation.
	// Read subsets must stay separate so adding write tools never advertises
	// device I/O to read-only or non-Main callers.
	wantHardware := []string{"hardware_list", "hardware_terminal_read", "hardware_connect", "hardware_disconnect", "hardware_send", "hardware_login", "hardware_adb_exec"}
	wantBrowser := []string{"browser_status", "browser_tabs_list", "browser_snapshot", "browser_frame", "browser_downloads_list", "browser_download_get", "browser_bookmarks_list", "browser_cookie_metadata", "browser_tab_open", "browser_tab_bind_current", "browser_tab_activate", "browser_tab_close", "browser_navigate", "browser_action", "browser_upload", "browser_release"}
	if strings.Join(HardwareMCPToolNames(), ",") != strings.Join(wantHardware, ",") || strings.Join(BrowserMCPToolNames(), ",") != strings.Join(wantBrowser, ",") {
		t.Fatalf("published tools changed: browser=%v hardware=%v", BrowserMCPToolNames(), HardwareMCPToolNames())
	}
	if len(HardwareMCPToolNamesFor(false)) != 2 || len(BrowserMCPToolNamesFor(false)) != 8 {
		t.Fatal("write tools escaped the read subset")
	}

}
```

## 本问题边界的完整 diff

```diff
diff --git a/internal/app/agent_browser_hardware_mcp_test.go b/internal/app/agent_browser_hardware_mcp_test.go
index 4393c4d..c122e37 100644
--- a/internal/app/agent_browser_hardware_mcp_test.go
+++ b/internal/app/agent_browser_hardware_mcp_test.go
@@ -135,9 +135,18 @@ func TestBrowserAndHardwareMCPAreIndependentBuiltins(t *testing.T) {
 			t.Fatalf("%s was assignable: %#v err=%v", id, normalized, err)
 		}
 	}
-	if len(BrowserMCPToolNames()) != 3 || len(HardwareMCPToolNames()) != 2 {
-		t.Fatalf("browser=%v hardware=%v", BrowserMCPToolNames(), HardwareMCPToolNames())
+	// Pin the published surface independently from the catalog implementation.
+	// Read subsets must stay separate so adding write tools never advertises
+	// device I/O to read-only or non-Main callers.
+	wantHardware := []string{"hardware_list", "hardware_terminal_read", "hardware_connect", "hardware_disconnect", "hardware_send", "hardware_login", "hardware_adb_exec"}
+	wantBrowser := []string{"browser_status", "browser_tabs_list", "browser_snapshot", "browser_frame", "browser_downloads_list", "browser_download_get", "browser_bookmarks_list", "browser_cookie_metadata", "browser_tab_open", "browser_tab_bind_current", "browser_tab_activate", "browser_tab_close", "browser_navigate", "browser_action", "browser_upload", "browser_release"}
+	if strings.Join(HardwareMCPToolNames(), ",") != strings.Join(wantHardware, ",") || strings.Join(BrowserMCPToolNames(), ",") != strings.Join(wantBrowser, ",") {
+		t.Fatalf("published tools changed: browser=%v hardware=%v", BrowserMCPToolNames(), HardwareMCPToolNames())
 	}
+	if len(HardwareMCPToolNamesFor(false)) != 2 || len(BrowserMCPToolNamesFor(false)) != 8 {
+		t.Fatal("write tools escaped the read subset")
+	}
+
 }
 
 // TestBrowserHardwareMCPInjectionUsesIndependentTriggers proves the two
diff --git a/internal/app/agent_pet_domains.go b/internal/app/agent_pet_domains.go
index 2921546..885dc42 100644
--- a/internal/app/agent_pet_domains.go
+++ b/internal/app/agent_pet_domains.go
@@ -10,11 +10,11 @@ import (
 // has a permission-checked cross-project messaging tool; task_message retains
 // its ordinary project-scoped contract and is never widened for the pet.
 func (s *Service) AgentCoreMCPToolNames(ctx context.Context, call AgentCallContext) []string {
-	return s.coreMCPNamesForSnapshot(ctx, call.Task, call.Turn.RuntimeConfigSnapshotID, call.Turn.AgentID)
+	return coreMCPNamesForTask(call.Task, s.AgentWriteAllowed(ctx, call))
 }
 
-func (s *Service) coreMCPNamesForSnapshot(ctx context.Context, task domain.Task, snapshotID, agentID string) []string {
-	names := coreMCPToolNames(s.agentSnapshotWritable(ctx, task, snapshotID, agentID))
+func coreMCPNamesForTask(task domain.Task, write bool) []string {
+	names := coreMCPToolNames(write)
 	if task.ID != petTaskID {
 		return names
 	}
diff --git a/internal/app/mcp_runtime.go b/internal/app/mcp_runtime.go
index 7f14007..5ebb1a7 100644
--- a/internal/app/mcp_runtime.go
+++ b/internal/app/mcp_runtime.go
@@ -537,10 +537,11 @@ func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agent
 	if agentAPIURL == "" {
 		return servers, allowed
 	}
-	write := false
-	if agent, err := s.store.TaskAgent(ctx, task.ID, strings.TrimSpace(agentID)); err == nil {
-		write = s.agentSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agent.AgentID)
-	}
+	agentID = strings.TrimSpace(agentID)
+	agent, agentErr := s.store.TaskAgent(ctx, task.ID, agentID)
+	write := agentErr == nil && s.agentSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agentID)
+	ioWrite := agentErr == nil && s.hardwareMCPSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agentID)
+
 	servers = append(servers, backend.MCPServer{
 		Name: coreMCPName, Transport: "http",
 		URL:            strings.TrimRight(agentAPIURL, "/") + coreMCPPath,
@@ -548,10 +549,7 @@ func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agent
 		URLPath:        coreMCPPath,
 		BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
 	})
-	coreNames := coreMCPToolNames(false)
-	if agent, err := s.store.TaskAgent(ctx, task.ID, strings.TrimSpace(agentID)); err == nil {
-		coreNames = s.coreMCPNamesForSnapshot(ctx, task, agent.RuntimeConfigSnapshotID, agent.AgentID)
-	}
+	coreNames := coreMCPNamesForTask(task, write)
 	for _, tool := range coreNames {
 		allowed = append(allowed, "mcp__"+coreMCPName+"__"+tool)
 	}
@@ -575,11 +573,9 @@ func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agent
 			URLPath:        workspaceMCPPath,
 			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
 		})
-		workspaceWrite := false
-		if agent, err := s.store.TaskAgent(ctx, task.ID, agentID); err == nil {
-			bound, bindErr := s.store.Workspace(ctx, task.WorkspaceID)
-			workspaceWrite = bindErr == nil && !bound.ReadOnly && s.hardwareMCPSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agentID)
-		}
+		bound, bindErr := s.store.Workspace(ctx, task.WorkspaceID)
+		workspaceWrite := bindErr == nil && !bound.ReadOnly && ioWrite
+
 		for _, tool := range WorkspaceMCPToolNamesFor(workspaceWrite) {
 			allowed = append(allowed, "mcp__"+workspaceMCPName+"__"+tool)
 		}
@@ -592,11 +588,8 @@ func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agent
 			URLPath:        browserMCPPath,
 			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
 		})
-		browserWrite := false
-		if agent, err := s.store.TaskAgent(ctx, task.ID, agentID); err == nil {
-			browserWrite = s.hardwareMCPSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agentID)
-		}
-		for _, tool := range BrowserMCPToolNamesFor(browserWrite) {
+
+		for _, tool := range BrowserMCPToolNamesFor(ioWrite) {
 			allowed = append(allowed, "mcp__"+browserMCPName+"__"+tool)
 		}
 	}
@@ -608,21 +601,15 @@ func (s *Service) mcpRuntimeForTurn(ctx context.Context, task domain.Task, agent
 			URLPath:        hardwareMCPPath,
 			BearerTokenEnv: "AHA2_AGENT_API_TOKEN",
 		})
-		hardwareWrite := false
-		if agent, err := s.store.TaskAgent(ctx, task.ID, strings.TrimSpace(agentID)); err == nil {
-			hardwareWrite = s.hardwareMCPSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agentID)
-		}
-		for _, tool := range HardwareMCPToolNamesFor(hardwareWrite) {
+
+		for _, tool := range HardwareMCPToolNamesFor(ioWrite) {
 			allowed = append(allowed, "mcp__"+hardwareMCPName+"__"+tool)
 		}
 	}
 	if s.desktopMCPInjected(ctx, task, agentID, inboxBatchID) {
 		servers = append(servers, backend.MCPServer{Name: desktopMCPName, Transport: "http", URL: strings.TrimRight(agentAPIURL, "/") + desktopMCPPath, URLFromEnv: "AHA2_AGENT_API_URL", URLPath: desktopMCPPath, BearerTokenEnv: "AHA2_AGENT_API_TOKEN"})
-		desktopWrite := false
-		if agent, err := s.store.TaskAgent(ctx, task.ID, agentID); err == nil {
-			desktopWrite = s.hardwareMCPSnapshotWritable(ctx, task, agent.RuntimeConfigSnapshotID, agentID)
-		}
-		for _, tool := range DesktopMCPToolNames(desktopWrite) {
+
+		for _, tool := range DesktopMCPToolNames(ioWrite) {
 			allowed = append(allowed, "mcp__"+desktopMCPName+"__"+tool)
 		}
 	}
```
