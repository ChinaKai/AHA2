# 0.5.219 部署状态

- 宠物 MCP 工具没权限：
  - Claude 非交互模式需要 `--allowedTools` 放行 MCP 工具，`acceptEdits` 只覆盖文件编辑。
  - `backend.Request` / `ExecutionRequest` 增加 `AllowedTools`。
  - `task_pet` 注入 7 个：
    `mcp__aha2-pet__pet_play`、`pet_move_to`、`pet_walk_path`、`pet_mood`、`pet_open_task`、`pet_sleep`、`pet_wake`。
- 提交：`7e9898f fix: allow pet MCP tools in the Claude task`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-015404.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261029-015412`。
- Server `4A1C33CBC8DF64F89CAD44BE99AF1870F52F56F2BD0B69006E597D28D1738207`；Tray `1C9DD6A941E485B8B824F09E7E9007ECC01FB0650222A0288D71812D522265A2`。
- 进程 2026-10-29 09:54 起；`/healthz` 200。
- 待验证：Owner 重进 task_pet，让它画“3”，确认 Claude 直接调用 MCP 不再弹权限。
