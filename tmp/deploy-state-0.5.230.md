# 0.5.230 部署状态

- 思考问号去背景：白底/阴影移除，只留黑色 `?` + 轻微 text-shadow。
- 思考动效交给 MCP：
  - 新增 `pet_thinking` 工具，参数 `active:true|false`。
  - MCP `tools/call` 实测返回 `{"type":"thinking","active":true}` / false。
  - Claude `--allowedTools` 增加到 8 个（含 `mcp__aha2-pet__pet_thinking`）。
  - 客户端 `applyPetBrainAction` 支持 `thinking` 开/关。
- 保留 Task 对话自动 thinking 作为兜底，MCP 可显式覆盖。
- 提交：`65739be feat: add a pet thinking MCP control`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-033039.json`。
- 进程 2026-10-29 11:30 起；`/healthz` 200。
