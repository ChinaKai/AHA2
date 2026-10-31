# 0.5.228 部署状态

- 宠物无打字/思考动效：
  - 0.5.227 已把旧面板事件改成 main 直接调用 assistant 导出函数，提交时立即 thinking，agent_message 时气泡打字。
  - 0.5.228 处理 `prefers-reduced-motion`：此前系统减少动态效果会让气泡直接全文显示、思考动画被 `animation:none` 关闭。
  - 现在宠物打字与思考动画不受 reduce-motion 影响；其它 UI 仍尊重系统设置。
- 提交：`4ca5569 fix: keep pet typing and thinking under reduced motion`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-031134.json`。
- 进程 2026-10-29 11:11 起；`/healthz` 200。
