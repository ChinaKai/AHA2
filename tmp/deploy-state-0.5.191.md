# 0.5.191 部署状态

- 对话框展示完整模型返回：
  - 每条 assistant 消息下新增“模型返回详情”折叠块。
  - 展示原始回复（包括被 UI 隐藏的 `[motion]` / `[pet]` 标记）。
  - 展示当前模型名、工具数量、每个工具的名称/状态/原始参数 JSON。
  - Agent Update 汇总行和工具执行行继续保留。
- 面板遮宠物：
  - 打开面板立即 `positionAtDock(false)` 瞬移，不再走 3 秒被面板盖住。
  - 上方空间不足时放到面板内部顶端，`.assistant-pet.panel-open` z-index=5，浮在面板上。
  - `resize` + `visualViewport.resize` 重新贴边，键盘弹起后也会重算。
- 提交：`5c42379 feat: expand raw model replies and keep the pet above chat`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-172746.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-172754`。
- Server `0FB4B06D38845BFB0A67BCE80D47B708CADB8FF62CF2BD8E772202D0BC545B3E`；Tray `F5D3F3D6C17BFABF4BF0F0F4DEED1048E962D9BDE60B150E1AE954EAD4085E51`。
- 进程 2026-10-29 01:27 起；`/healthz` 200；线上 `assistant.js` 含 `assistant-raw`/`assistant-raw-tools`/`visualViewport`。
- 390×500 测试：面板 y=90，宠物 y=148（面板内顶部），z-index 高于面板；原始详情含 `[motion:angry]` 和工具参数。
