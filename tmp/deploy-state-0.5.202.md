# 0.5.202 部署状态

- “工具 ok 但宠物没动”修复：
  - `OwnerAssistantReply` 新增 `actions`，聊天 HTTP 响应直接返回本次执行的 PetBrainAction。
  - 客户端 `sendMessage` 收到响应后立即 `applyPetBrainAction(action,true)`，不再只依赖 SSE `pet_action`。
  - 增加 1.5 秒动作去重：SSE 和响应同时到达时只执行一次。
- 截图证据：22:48:32 pet_events 已有 `move_to x=0.85 y=0.1 duration_ms=800` 和 `play angry`，说明服务端执行成功；问题在传输/客户端接收。
- 提交：`43a8ec6 fix: apply pet actions from the chat response`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-225511.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-225520`。
- Server `D3259F5A480D4D9A4D033A98757780BF3BB1BD86323B6E146713D50D8D5B10D8`；Tray `1F5EF1E6E845FB8A3E33A1BADBBD31F9D876D38415D043BCC052ECECA4BD1F4F`。
- 进程 2026-10-29 06:55 起；`/healthz` 200。
- 测试：mock 聊天响应 actions 移动，1.2s 后宠物到 (299.5,52.4)，目标 (331.5,84.4) 附近；无 SSE 也生效。
