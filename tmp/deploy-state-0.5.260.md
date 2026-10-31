# 0.5.260 部署状态

- 宠物设置外观改为内容区页面：
  - 固定从侧栏右侧开始，保留左侧导航可见。
  - 页面背景，不再暗色全屏蒙层。
  - 卡片去边框/阴影/圆角/最大高度。
  - 隐藏头部关闭和底部取消，只保留“返回高级设置”。
- 提交：`1a86ace style: render pet settings over the content page instead of a modal`。
- 部署：第一次 tray 占用失败，重试成功；`dist/user-deploy-result-20261029-071237.json`，`Health=ok`。
- Server `172316FAAA1FDA999465883C3120E4BE7F0029BFAF2EF5FE35A79E95AEBE59C3`；Tray `1C1A9D74690FCCA3C18A4CF0F722A631FDAFBEB168B9EDF66B508E788532C64C`。
- 进程 2026-10-29 15:13 起；`/healthz` 200。
