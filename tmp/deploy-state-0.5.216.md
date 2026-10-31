# 0.5.216 部署状态

- 旧宠物聊天面板已停用：
  - `#assistant-panel` CSS `display:none !important`。
  - 点击宠物仍走 `aha:assistant-open-task` 到 `task_pet`。
  - 宠物设置仍从高级设置“打开宠物设置”进入。
- 宠物 Task：
  - `task_pet`（draft）、`project_pet`、`workspace_pet` 已在线上。
  - Claude backend 注入 `--mcp-config` 指向 `/api/v1/pet/mcp`。
- 提交：`cab09c2 fix: retire the old pet chat panel`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-012252.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261029-012301`。
- Server `CCA5DCF7141B17477A9ADE4F2172B2A83626D1363D717679DCFA5568A4B437A7`；Tray `ACE379986D11EFC234F54EB1CD3D79C04FBAEA5C61ED7F111C54B535479F2620`。
- 进程 2026-10-29 09:23 起；`/healthz` 200。
- 真实对话尚未在 Owner 登录态下执行：没有 Owner 密码/会话，不能代替登录点 Task。需 Owner 点击宠物、启动 `task_pet` 并发一条消息验证 Claude+MCP。
