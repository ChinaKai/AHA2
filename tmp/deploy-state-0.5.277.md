# 0.5.277 部署状态

- 根因（0.5.276 修复）：
  - 调度器第一次 tick 写 `s.petBrainAt[...]`
  - 这个 map 只在 `AskOwnerPetBrain` 懒初始化
  - 调度先跑时是 nil，赋值直接 panic，进程退出
  - 因此不是“每次自主都崩”，而是服务启动后第一次调度检查就崩
- 0.5.277 追加：
  - `go s.runTurn` 增加 recover
  - 调度器 recover 改为记录日志
  - 单次 Task/backend panic 不再杀进程
- 0.5.276 已跨过首个 30 秒 tick；0.5.277 服务 17:57:32 启动，`/healthz` 200。
- 提交：`e1bd11d` + `fix: recover turn panics instead of crashing the service`。
