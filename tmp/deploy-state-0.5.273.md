# 0.5.273 部署状态

- 自主行为状态改由服务端返回：
  - `OwnerAssistantView` 增加 `pet_last_brain_at` / `pet_next_brain_at`。
  - 服务端调度成功后记录 `petAutonomyLast`。
  - 客户端优先显示服务端时间，回退本地记录。
- 提交：`8597f12 feat: expose the server-side autonomous schedule`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-091408.json`。
- 进程 2026-10-29 17:14 起；`/healthz` 200。
