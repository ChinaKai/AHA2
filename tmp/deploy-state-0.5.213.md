# 0.5.213 部署状态

- 宠物架构第 1 步：本地 MCP server
  - `POST /api/v1/pet/mcp`（loopback-only）
  - JSON-RPC：initialize / tools/list / tools/call / ping
  - 工具 7 个：pet_play、pet_move_to、pet_walk_path、pet_mood、pet_open_task、pet_sleep、pet_wake
  - tools/call -> `ControlOwnerPetTool` -> 校验 -> `/api/v1/pet/control` 同一执行链路
  - 非 loopback 返回 403；不暴露通用 AHA 工具
- 提交：`0354c29 feat: add a loopback pet MCP server`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-005925.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261029-005933`。
- Server `DE415F93DCC11126777C08B8E9D30F8A650A1801C2482090D17B6AA802A18793`；Tray `0584CD56FF2603B1BC2EAF055B1C7947777C3BE316F502A54A982A08412A0873`。
- 进程 2026-10-29 08:59 起；`/healthz` 200。
- 实测：initialize 返回 aha2-pet/1.0.0；tools/list 返回上述 7 个工具。
- 尚未切换：宠物聊天仍走 gateway 直连；下一步隐藏 Pet Task + backend 挂 MCP，再切换对话。
