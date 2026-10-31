# 0.5.222 部署状态

- MCP 页面点不进去：
  - 根因：`resourcesForView` 没有 `mcp` 分支，返回 undefined，nav 点击后渲染阶段抛异常。
  - 已补 `case "mcp": return []`。
  - 同时侧边栏改成可滚动、便签区不再强占剩余高度，避免 MCP 项在小屏被挤出可视区。
- 提交：`58eff88 fix: make the MCP page reachable`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-023418.json`。
- 进程 2026-10-29 10:34 起；`/healthz` 200。
