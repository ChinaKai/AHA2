# 0.5.231 部署状态

- Task 详情 MCP 编辑：
  - 任务头新增 MCP 按钮，弹窗勾选/取消 MCP。
  - 保存走 `PUT /api/v1/tasks/{id}/mcp-servers`，下一轮生效。
  - 宠物项目强制保留 `mcp_pet`，普通项目不显示。
- MCP 工具有限白名单：
  - 运行时读取 MCP 的 `tools`，生成精确 `mcp__<id>__<tool>`。
  - 工具列表为空时才退回 `mcp__<id>__*`。
- 提交：`b938e19 feat: edit task MCP servers and scope tool permissions`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-034456.json`。
- 进程 2026-10-29 11:44 起；`/healthz` 200。
- 剩余：旧面板死代码清理、宠物日记/安静时段/小时上限。
