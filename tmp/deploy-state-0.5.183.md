# 0.5.183 部署状态

- 排查宠物对话：
  - 查库 recent assistant_messages：00:39–00:45 模型回复“我控制不了自己往哪个方向走”“只能跳一下”，并且同时输出 `[motion:jump]` 和 `[pet]...play jump...`，导致重复动作。
  - 根因：聊天提示词仍写“自主移动由宠物设置/心跳控制”，且 `[pet]` 只给 play 示例；模型不知道可 move_to/mood/open_task。
- 修复：
  - 提示词明确：可以控制宠物移动/动作/心情/打开任务，不能说“控制不了”；列出 move_to left/right/top/bottom/dock、mood、open_task、sleep/wake；给出“去右边跳一下”的 move+play 示例。
  - 如果输出 `[pet]` 就不要再输出 `[motion]`；客户端在有 `[pet]` 时忽略 `[motion]`，避免重复执行。
- 提交：`7cbc1a0 fix: teach the pet model to control movement`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-164808.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-164816`。
- Server `16BD13F676AD40F3D33F8AD67A434CC30C873457D2FFD6BD5BBC7A607C6C2158`；Tray `9E8B4C4862D260E73EA9DDACC34669A786B5A5894D193FA54796537B087BC716`。
- 进程 2026-10-29 00:48:22 起；`/healthz` 200。
- 仍存在：聊天非流式（等完整回复）、模型延迟；tool calling/scoped Pet token 未做。
