# 0.5.234 部署状态

- MCP 健康状态：
  - `mcp_servers` 增加 health / last_error / last_checked_at（migration v92）。
  - 列表显示 启用状态 + 健康状态。
  - 打开详情会尝试 initialize + tools/list 并写回 health/tools。
- MCP 编辑：
  - 详情弹窗内可修改名称、URL、描述并保存。
  - 内置宠物 MCP 只读，不显示编辑。
- 提交：`fd20e82 feat: expose MCP health and edit connection settings`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-040508.json`。
- 进程 2026-10-29 12:05 起；`/healthz` 200。
