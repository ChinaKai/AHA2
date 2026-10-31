# 0.5.224 部署状态

- MCP 详情：
  - `mcp_servers` 增加 `tools_json`（migration v91）。
  - `GET /api/v1/mcp-servers/{id}` 返回详情与工具。
  - 内置 `mcp_pet` 直接返回 7 个宠物工具。
  - HTTP MCP 通过 `initialize` + `tools/list` 读取工具，结果写回。
  - `POST /api/v1/mcp-servers/test` 改为按 id 测试并刷新工具。
- UI：
  - MCP 列表每行增加“详情”。
  - 详情弹窗显示描述、传输、地址、状态、支持工具（名称+描述）。
  - 提供“测试并读取工具”。
- 提交：`0e93c9b feat: show MCP capabilities in a detail dialog`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-024700.json`。
- 进程 2026-10-29 10:47 起；`/healthz` 200。
