# 0.5.267 部署状态

- 自主行为消息补充 `petLayoutPrompt(input.Layout)`，模型知道视口/宠物/面板布局。
- 宠物设置删除“活动记录”区块及 `renderPetActivity` 调用。
- 宠物设置删除“日记”区块，改为高级设置独立“宠物日记”子页：
  - 路由 `pet-diary`
  - 按日期倒序展示，可展开全文
  - 有返回高级设置
- 提交：`63ddb94 feat: add pet diary page and remove activity log`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-082703.json`。
- 进程 2026-10-29 16:27 起；`/healthz` 200。
