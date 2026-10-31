# 0.5.271 部署状态

- 自主触发不再检查：
  - 页面是否隐藏
  - 对话面板是否打开
  - 主人最近是否操作
- 保留：宠物可见、未在发送、无并发。
- 安静时段：
  - 新增 `pet_quiet_enabled`（migration v95）。
  - 设置页可关闭，关闭后 `inQuietHours` 和 server `petQuietNow` 都直接返回 false。
- 每小时上限改为 0–60；0 表示不限。
- 提交：`dcfb060 feat: make pet autonomy independent of UI focus and quiet optional`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-085723.json`。
- 进程 2026-10-29 16:57 起；`/healthz` 200。
