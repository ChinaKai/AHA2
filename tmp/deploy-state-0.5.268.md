# 0.5.268 部署状态

- 宠物设置新增“自主行为”状态：
  - 上次自主行为时间（本地存储）
  - 下次预计时间（上次 + pet_idle_minutes）
  - 本小时次数 / 上限
  - 是否处于安静时段
- 自主行为冷却改为读取 `pet_idle_minutes`，不再固定 5 分钟。
- 提交：`8488901 feat: show the last and next autonomous pet run`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-083633.json`。
- 进程 2026-10-29 16:36 起；`/healthz` 200。
