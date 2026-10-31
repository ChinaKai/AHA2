# 0.5.215 部署状态

- Pet Task 后端挂载 MCP：
  - `backend.Request` / `app.ExecutionRequest` 增加 `MCPConfig`。
  - `task_pet` 注入 Claude `--mcp-config`，内容：
    `{"mcpServers":{"aha2-pet":{"type":"http","url":"http://127.0.0.1:8766/api/v1/pet/mcp"}}}`
  - 其他 Task 的 MCPConfig 为空。
  - 可用 `AHA2_PET_MCP_URL` 覆盖 URL。
- 线上数据已确认：
  - `task_pet` 存在，status=draft，title=宠物，project_pet/workspace_pet。
  - `workspace_pet` root `D:\ProgramData\AHA2\pet-workspace`，read_only=0。
- 提交：`8716a1c feat: mount pet MCP on the pet task backend`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-011801.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261029-011809`。
- Server `388394FE1D3E5F96A399824AAFDAB4D548037A377CEF266A512985B354C34411`；Tray `3A5DCD874CD66830B985AA94590C8C57869447BF353F2BB2BA2C9C94A1864C49`。
- 进程 2026-10-29 09:18 起；`/healthz` 200。
- 尚未完成：旧聊天面板代码删除；Codex/DSH 的 MCP 挂载；宠物 Task 实际对话验证。
