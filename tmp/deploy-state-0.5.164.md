# 0.5.164 部署状态

- 第二片：每设备 5 分钟空闲心跳 + 模型动作协议。
  - 客户端每 30 秒检查：页面可见、无面板、无输入、5 分钟无互动、距上次思考 5 分钟，才调用 `POST /api/v1/pet/brain`。
  - 服务端按 device_id 做 30 秒防抖，读取只读快照，让当前 Provider 模型输出严格 JSON：`say` + 最多两个 `play/move_to/open_task`。
  - 解析时只允许 walk/jump/happy/angry、left/right/top/bottom/dock、task-*；无效动作丢弃。
  - 客户端执行：说 9 秒气泡、播放动作、语义区域移动、打开 Task；`window.AHA2Assistant.brain(true)` 可强制测试。
- 提交：`3dc42e2 feat: let the owner pet think on an idle heartbeat`；未 push/tag。
- 部署：第一次因 `aha2.exe` 被占用失败并回滚；重试 `Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-143031.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-143038`。
- Server `87D3A660E9E12A9CD1461B5D8F7CD7AD57609FEC2A2CE91BBD8A76EB6B25D30F`；Tray `39CF68E81C2E9B63FAFD826173CFFD1795E007C0010FDCDF06808FC676CB12FD`。
- 进程 22:30:55 起；`/healthz` 200；`/api/v1/pet/brain` 未登录 401（路由存在）。
- 线上 `/assistant.js`：`maybeRunPetBrain`、`petBrain(`、`showPetSpeech`、`brain: (force` 均存在。
- 测试：`parsePetBrain` 合法/恶意动作测试通过；App/HTTP Pet/Assistant 测试通过；Web 133 pass；mock 验证 say 气泡 + jump + move_to。
- 尚未做：每小时最多 6 次、23:00–08:00 免打扰、事件驱动（任务失败/渠道异常）、`pet:control` scoped token、tool calling。
