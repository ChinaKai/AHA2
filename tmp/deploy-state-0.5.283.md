# 0.5.283 部署状态

- 刷新回项目页：
  - 0.5.282 起若保存的 view 是 `pet-settings`，刷新映射到 `advanced`。
  - 不再回项目页，也不会白屏。
- 自主时间一直为空：
  - 真正原因：`OwnerAssistant` 从未把 `petAutonomyStatus` 写入 `OwnerAssistantView`，新增字段一直是零值。
  - 0.5.282 已在返回前赋值 `PetLastBrainAt/PetNextBrainAt/PetBrainRuns`。
  - 0.5.283 再加兜底：即使调度器还没跑，next 也给出 `now + idle`，不再显示等待。
- 部署：`dist/user-deploy-result-20261029-102535.json`，Status=succeeded、Health=ok。
- 服务 18:25:52 启动；`/healthz` 200。
