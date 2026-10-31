# 0.5.263 部署状态

- Pet Task 身份覆盖：
  - `prompt.BuildInput.Identity` 支持指定 identity。
  - `task_pet` 使用 `identity.pet-task`，替代通用 `identity.task-agent`。
  - 不再在 packedPrompt 前置额外段落，避免重复。
- 自主行为：
  - 提交前检查 `ActiveTurn`，宠物 Task 正在执行时跳过，不并发。
- 长期记忆：
  - Pet Task 每次构建 Prompt 时读取最近 3 篇日记，截断后附在 Prompt 末尾。
- 提交：`011e512 feat: give the pet task its own identity and diary memory`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-075507.json`。
- 进程 2026-10-29 15:55 起；`/healthz` 200。
- 待验收：真实空闲触发一次 `[自主行为]`，确认 Task 起 Turn 且 MCP 动作正常。
