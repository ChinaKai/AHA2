# 0.5.276 部署状态

- 找到服务崩溃根因：
  - `runPetAutonomyOnce` 第一次 tick 写 `s.petBrainAt["server_autonomy_schedule"]`。
  - 该 map 只在 `AskOwnerPetBrain` 里懒初始化，调度器先跑时是 nil。
  - 对 nil map 赋值直接 panic，进程退出；tray 再拉起，所以表现为服务频繁断连。
- 修复：调度器拿锁后先初始化 map。
- 0.5.276 手动构建并部署；服务 17:54:43 启动，已跨过首个 30 秒 tick，17:55 仍健康。
- 提交：`fix: initialize the autonomy schedule map`。
