# 0.5.294 部署状态

- 同步卡住根因：
  - `sync_state.last_error = hardware task_1aa.../hardware_resource_0864... has non-portable secret reference`
  - 该硬件资源的 credential_ref 仍是旧的任务级 `hardware/task_03f9.../hardware-1/credential`
  - 同步导出基础设施 secret 时整批失败，outbox 从 10-25 起堆积 21,954 条，cursor 停在 283535。
- 修复：允许旧任务级 hardware credential ref 作为可同步引用（值仍加密进入基础设施 bundle）。
- 部署：`dist/user-deploy-result-20261029-135004.json` succeeded。
- 验证：
  - `last_error` 已清空
  - `last_push_at` 更新到 2026-10-29T13:50:38Z
  - pending 从 21,954 降到 15,939，持续下降
- 提交：`fix: accept legacy hardware secret refs during sync`。
