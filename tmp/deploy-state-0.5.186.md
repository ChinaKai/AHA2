# 0.5.186 部署状态

- 排查“模型控制不管用”：
  - `pet_events` 显示 16:54:35 模型确实执行了 `move_to right` + `play jump`（actor=model, status=acted），服务端工具调用是成功的。
  - 但 `ownerPetEvents` SSE handler 只转发 `pet_state`，把 `pet_action` 直接 `continue` 丢掉了，浏览器根本没收到动作。
  - 另外聊天请求期间 `petFrozen=true`，`positionAt` 会忽略移动，即使收到 `move_to` 也走不动。
- 修复：
  - SSE 现在转发 `event: pet_action`，data 为 `{say, actions}`。
  - `positionAt(point, force)` / `applyPetBrainAction(action, forceMove)`；模型工具调用与 `[pet]` 指令强制移动，不再被聊天冻结挡住。
  - 0.5.185 先修 SSE，0.5.186 补强制移动。
- 提交：`d799f23 fix: deliver pet tool actions over SSE`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-165930.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-165937`。
- Server `97027C9C29E78C39B463AAFA970C48035D57686ECB36B3884F640CF188EC4DF3`；Tray `4E12412BBA549E65F94A3704FAAE10CE50D5E3370FAB0C3B96B6CCAA826E90C0`。
- 进程 2026-10-29 00:59:44 起；`/healthz` 200；线上 `assistant.js` 含 `force = false`。
