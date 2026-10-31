# 0.5.261 部署状态

- 删除旧 `identity.pet-assistant` 模板与注册项。
- 只保留：
  - `identity.pet-task`：宠物 Task 对话
  - `identity.pet-brain`：空闲自主行为
- 旧助手路径临时复用 `pet-task`，并保留 snapshot/layout 可选注入。
- 提交：`72f25bd refactor: remove the retired pet assistant prompt`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-072737.json`。
- 进程 2026-10-29 15:27 起；`/healthz` 200。
