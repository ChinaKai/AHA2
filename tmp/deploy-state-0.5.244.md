# 0.5.244 部署状态

- 主 Agent 配置弹窗增加“MCP 工具”折叠多选，与 New Task 一致。
- 保存通过 `PATCH /api/v1/tasks/{id}` 的 `mcp_server_ids` 落库。
- 宠物 Task 继续强制保留 `mcp_pet`。
- 提交：`19975c8 feat: edit MCP tools in the main agent config`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-060317.json`。
- 进程 2026-10-29 14:03 起；`/healthz` 200。
