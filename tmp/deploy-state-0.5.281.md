# 0.5.281 部署状态

- 刷新白屏：
  - `pet-settings` 从可恢复导航白名单移除。
  - 刷新后回到高级设置，不再停在只有空 host 的宠物设置页。
- 自主时间：
  - 服务端首次调度 tick 写入 `petAutonomyNext`（当前 + idle）。
  - `petAutonomyLast` 仍只在实际触发后写入，所以“上次尚未运行”是预期。
  - 下次时间不再依赖上次时间。
- 本小时次数改为服务端 `pet_brain_runs`，不再只显示浏览器本地计数。
- 部署：`dist/user-deploy-result-20261029-101857.json`，Status=succeeded、Health=ok。
- 服务 18:19:14 启动，`/healthz` 200。
