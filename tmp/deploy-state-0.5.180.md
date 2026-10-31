# 0.5.180 部署状态

- 变更：服务端锁定身份/准则写入 + 自主行为预算。
  - `PUT /api/v1/pet/settings` 仍接受 identity/principles 字段，但服务端忽略，不会修改；HTTP 测试断言写入后 `pet_identity` 仍为空。
  - 客户端自主思考增加：23:00–08:00 免打扰，每小时最多 6 次；记录在 localStorage `aha2.pet.brain.runs.v1`。
  - 设置页新增说明：空闲 5 分钟触发、23–08 免打扰、每小时最多 6 次。
- 提交：`2411cf2 fix: lock pet identity writes and cap autonomous runs`；未 push/tag。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-162401.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-162410`。
- Server `C9E030C9D5DE7A11DBBD1D723C5B5900384316749DC4C7FEEF49B0EEE6BADB15`；Tray `FF56C0BD4A2CD98514BEDB3ADC3D972B439EB66CF9E37BF38358D297E8F0DA81`。
- 进程 2026-10-29 00:24:15 起；`/healthz` 200；线上 `assistant.js` 含 `PET_QUIET_START`、`PET_HOURLY_LIMIT`、`brainRunsThisHour`。
