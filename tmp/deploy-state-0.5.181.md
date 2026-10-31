# 0.5.181 部署状态

- 自主预算进设置：
  - `owner_assistants` 新增 `pet_idle_minutes`（默认 5）、`pet_quiet_start`（23）、`pet_quiet_end`（8）、`pet_hourly_limit`（6），migration v88。
  - 设置页新增“自主行为”四项：空闲触发分钟、每小时上限、免打扰开始/结束。
  - 客户端 `maybeRunPetBrain` 使用设置值：空闲阈值、免打扰区间、每小时上限；localStorage 记录最近一小时运行时间。
  - `PUT /api/v1/pet/settings` 接收并持久化这些字段；HTTP 测试断言 idle=10/hourly=4 保存成功。
- 提交：`5aa1285 feat: expose pet autonomy budget in settings`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-163135.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-163143`。
- Server `D5400B3D84DA9C6A1CAF4D314B1E3F7DBE3813C0A2E09FA3C900B6F5F9318DAF`；Tray `9955BF1D3488154C21C07C84FFC567DA87FF9E8E7C7814D379029FE1DCB988DA`。
- 进程 2026-10-29 00:31:49 起；`/healthz` 200；线上 `assistant.js` 含预算输入与 `petIdleMinutes`。
- 下一步：模型控制宠物（Pet API/动作协议），再补日记。
