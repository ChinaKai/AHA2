# 0.5.187 部署状态

- 宠物对话增加筛选与工具执行状态：
  - 助手 API 返回 `tool_events`：每个工具 name/status/message；成功 ok、参数/执行失败 error、工具被 Provider 拒绝回退时 warning。
  - 聊天消息下方显示工具行：`pet_move 已执行 right` / `pet_open_task 失败 ...`。
  - 顶部筛选：全部 / 对话 / 工具 / 错误；工具视图只看工具行，错误视图只看 error/warning。
- 提交：`5ae75ae feat: show pet tool execution status in chat`；未 push/tag。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-170612.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-170621`。
- Server `8E883056A0AEC199E648763F576EE269FECB8D6991F571D700F0D5DA16F77801`；Tray `664D3521DF4FA4E1A6128F4BF03A5F3B7BF6F175827ABFD01238F9C7247596D0`。
- 进程 2026-10-29 01:06:27 起；`/healthz` 200；线上 `assistant.js` 含 `assistant-chat-filter`/`assistant-tool-row`。
- 测试：mock 3 个 tool_events，工具筛选显示 3 行、错误筛选只显示 1 行 `pet_open_task`。
- 限制：tool_events 目前只在当前会话内存中，刷新后历史消息不显示工具行；下一步可存 message metadata。
