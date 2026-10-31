# 0.5.192 部署状态

- 宠物层与活动范围：
  - 宠物 z-index 1，面板 z-index 4，气泡 3：宠物是界面最底层。
  - 打开/关闭面板不再移动、隐藏或把宠物抬到面板上；键盘 resize 也不再强制贴边。
  - `safePoint` 改为全屏随机（8px 边距），取消 blockedAt 的 UI 避让，活动范围覆盖整块屏幕。
- 对话信息精简：
  - 删除输入框右侧筛选弹层、筛选状态、工具行、Agent Update 汇总行。
  - 每条 assistant 消息只保留正文 + “模型返回详情”折叠块。
  - 详情里包含原始回复（含 `[motion]`/`[pet]`）、模型名、工具名/状态/原始参数 JSON。
- 提交：`a3f471e feat: keep raw model details and let the pet roam beneath chat`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-173634.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-173641`。
- Server `802F870D007DCD4C06CD5C5CD80397E0717569EB3206C78BD92ACA6D6705F2FF`；Tray `3BF6660FE12FCFE275DB624A7ED6311A6CBA6F393D4C41E00FA2EE452831F22D`。
- 进程 2026-10-29 01:36 起；`/healthz` 200；线上 `assistant.js` 含 `assistant-raw`/`safePoint`，无 `assistant-chat-filter`/`assistant-tool-row`。
- 390×844 测试：面板打开后宠物仍在 y=758 的底层，z-index=1；筛选和工具行数量 0，模型返回详情 1 个。
