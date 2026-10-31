# 0.5.258 部署状态

- 宠物设置强制内联为高级子页面：
  - 进入 `pet-settings` view，把设置节点 append 到 `#pet-settings-host`。
  - 加 `assistant-settings-inline` 和 inline style，覆盖原本的 fixed 全屏弹窗布局。
  - 卡片宽度、边框、阴影重置为页面内容。
- 返回高级设置仍保留。
- 提交：`aa6ae4f fix: force pet settings into the inline subpage layout`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-070616.json`。
- 进程 2026-10-29 15:06 起；`/healthz` 200。
