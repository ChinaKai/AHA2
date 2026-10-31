# 0.5.235 部署状态

- 内置宠物 MCP 健康状态：
  - 列表层对 `mcp_pet` 直接填 `health=ok` 和 8 个工具，不再显示 unknown。
  - 新安装迁移时也把内置行 health 设为 ok。
- 页面标题被宠物遮挡：
  - `.page-head` 提升到 `z-index:12`，高于宠物 z10，MCP 描述可读。
- 提交：`5bdc382 fix: show builtin MCP health and keep page headings above the pet`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-041320.json`。
- 进程 2026-10-29 12:13 起；`/healthz` 200。
