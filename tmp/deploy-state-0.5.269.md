# 0.5.269 部署状态

- 自主行为“上次/下次”回填：
  - 旧版本只存 hourly runs，不存 last。
  - `storedBrainLast` 在 last 缺失时回退到本小时 runs 的最大时间。
- 提交：`a492662 fix: backfill the last autonomous run time`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-083902.json`。
- 进程 2026-10-29 16:39 起；`/healthz` 200。
