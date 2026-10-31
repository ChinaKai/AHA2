# 0.5.188 部署状态

- 筛选改成任务对话框形式，放到发送旁边：
  - 顶部 chips 移除；composer 里发送按钮右侧加 `details` 筛选弹层，样式复刻任务的 conversation filter。
  - 4 个可多选分类：对话、Agent Update、工具调用、错误；勾选状态和 `x/4` 角标。
  - 工具行：`pet_move 已执行 right`；失败/回退归到“错误”，工具成功归到“工具调用”。
  - 关闭“对话”只看工具行；关闭“错误”隐藏失败行。
- 工具调用是否执行：
  - 服务端 `pet_events` 在 17:07:54 记录 `move_to right` + `play jump`，说明模型确实调用了工具。
  - 工具行只在当前会话内存；刷新后历史不显示。客户端旧缓存也会看不到行，需要硬刷新。
- 提交：`8cb506f feat: mirror task filters beside the pet composer`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-171203.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-171210`。
- Server `4B3CCD00D14C928C4A1A9FC74372017B896482444767A7F15506E84E4E08586B`；Tray `20F72AFD973B88869BB8EF2050D2BD3DAEC6AAED38B1B03F6D04F514DF2F7FB4`。
- 进程 2026-10-29 01:12:16 起；`/healthz` 200；线上 `assistant.js` 含 `assistant-filter-popover`、`data-chat-filter="update"`、`tool_events`。
- 测试：mock 3 tool_events；筛选弹层 4 项；关闭对话后气泡隐藏、工具行 3 条；关闭错误后工具行剩 2 条。
