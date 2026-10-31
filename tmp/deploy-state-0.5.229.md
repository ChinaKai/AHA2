# 0.5.229 部署状态

- 三点思考动效改成单个动态问号：
  - 白色圆底 + 黑色 `?`，上下浮动/轻微旋转。
  - 不再显示三个点。
- 宠物移动时隐藏：`pet-moving` 类由 positionAt/walkPath 维护。
- 宠物打字时隐藏：气泡打字期间加 `pet-typing`。
- 保留 reduce-motion 下的问号动画。
- 提交：`7833f55 feat: use an animated question mark while the pet thinks`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-032224.json`。
- 进程 2026-10-29 11:22 起；`/healthz` 200。
