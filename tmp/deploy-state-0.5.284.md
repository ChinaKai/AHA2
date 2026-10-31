# 0.5.284 部署状态

- 宠物设置现在是可恢复视图：
  - `pet-settings` 重新加入保存视图白名单。
  - 刷新后 render 会调用 `showPetSettings()`，把设置节点重新挂到 `#pet-settings-host`。
  - 不再跳项目页/高级页，也不白屏。
- 与日记/MCP 的差异：
  - 日记/MCP 是普通 View，每次 render 直接生成完整 HTML。
  - 宠物设置是 assistant 层的组件，之前只是把节点临时移进空 host；刷新后 host 为空。
  - 现在 host 渲染后自动重新挂载组件。
- 提交：`8fb2167 fix: restore pet settings on refresh`。
- 部署：`dist/user-deploy-result-20261029-103109.json`，Health=ok；服务 18:31:24 启动。
