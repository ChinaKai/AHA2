# 0.5.255 部署状态

- 高级设置“其他设置”回到统一网格，不再单独一列。
- 高级设置进入的页面统一有“返回高级设置”：提示词、同步、代理、硬件资源、MCP、其他设置。
- 左侧边栏移除代理、MCP；MCP 改从高级设置卡片进入。
- 宠物设置：
  - `assistant-settings[hidden]` 明确 display:none。
  - 高级设置按钮增加 onclick 兜底。
  - `openPetSettings` 渲染异常不再阻止面板显示。
- 提交：`a99e5a5 fix: finish the advanced settings navigation and pet settings opener`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-065739.json`。
- 进程 2026-10-29 14:58 起；`/healthz` 200。
