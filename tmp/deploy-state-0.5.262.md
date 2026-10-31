# 0.5.262 部署状态

- 自主行为合并进 Pet Task：
  - 删除 `identity.pet-brain` 模板和注册项。
  - `AskOwnerPetBrain` 不再直连 Provider，改为向 `task_pet` 提交 `[自主行为] ...` 消息。
  - 自动和主动现在都走同一条 Task/backend/MCP 路径。
  - 安静时段、小时上限仍在提交前检查。
- `identity.pet-task` 继续作为唯一宠物提示词。
- 提交：`5c6688e refactor: route autonomous pet behavior through the pet task`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-074106.json`。
- 进程 2026-10-29 15:41 起；`/healthz` 200。
