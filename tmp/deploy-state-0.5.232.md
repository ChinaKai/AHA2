# 0.5.232 部署状态

- 宠物日记：
  - `GET /api/v1/pet/diary` 读取 `DataDir/pet-workspace/diary/*.md`，按日期倒序最多 30 条。
  - 宠物设置新增“日记”折叠列表，默认显示最近 7 天。
- 安静时段/每小时上限：客户端已有 `inQuietHours` / `brainRunsThisHour`，本轮未改。
- 提交：`254dbfe feat: show pet diary in settings`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-034949.json`。
- 进程 2026-10-29 11:49 起；`/healthz` 200。
- 旧面板死代码仍在，只是入口和 CSS 已停用。
