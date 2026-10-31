# 0.5.214 部署状态

- 宠物可见 Task：
  - 系统项目 `project_pet`（名称“宠物”，project_type=system）。
  - Workspace `workspace_pet`，根目录 `DataDir/pet-workspace`，自动创建空文件夹，workspace-write。
  - Task `task_pet`（标题“宠物”），固定 ID，手动启动，single/max 1。
  - `owner_assistants` 新增 `pet_task_id`（migration v89）。
  - `OwnerAssistant` 首次加载时幂等创建项目/Workspace/Task，并返回 `pet_task_id`。
- 点击宠物：
  - 有 `pet_task_id` 时直接 `aha:assistant-open-task` 跳到宠物 Task，不再打开旧聊天面板。
  - 旧面板作为 fallback 保留，尚未删除代码。
- 提交：`57442d4 feat: make the pet a visible task with its own workspace`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-011108.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261029-011115`。
- Server `0124126CB5D505CE8624422B694DE7CE80CF69EBE6D0F7E10622098AD1274A3C`；Tray `183B46C492791F3B925553E56F3079677F9EFB0B2629701E324C071781169C59`。
- 进程 2026-10-29 09:11 起；`/healthz` 200。
- 尚未完成：宠物 Task 的 backend 还没挂 MCP；旧聊天面板 UI 待删除。
