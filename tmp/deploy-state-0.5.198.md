# 0.5.198 部署状态

- 文字协议彻底移除：
  - 聊天提示词不再出现 `[motion]`、`[pet]`、JSON 或动作示例文本；只说明六个 pet_* 工具。
  - 自主思考提示词也从 JSON 改为 pet_* 工具调用。
  - 客户端删除 `assistantReplyControl` / `assistantReplyMotion` 和 `[pet]`/`[motion]` 解析；不再执行任何文本动作。
  - 动作只通过 tool_calls -> ControlOwnerPet -> SSE `pet_action` -> 客户端执行。
- 提交：`5913e70 refactor: use tool calls for all pet actions`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-180156.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-175954`。
- Server `9D33B2ED0171C5AE6E629061D287BDBE60CB14161E75D9DD0447B5141525F666`；Tray `5263846C63B25884A13C7CEC00665618C159BFD2EDFEA6028B290E0D463BB08D`。
- 进程 2026-10-29 02:00 起；`/healthz` 200；线上 `assistant.js` 已无 `[pet]` 文本解析。
- 测试：133 web tests；app/http/gateway Pet/Assistant/ChatTool 测试通过。
