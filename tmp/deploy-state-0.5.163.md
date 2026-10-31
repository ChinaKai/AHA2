# 0.5.163 部署状态

- 变更：宠物改为“共享身份 + 本地表现”。
  - 共享：形象（owner_assistants.character）、mood（pet_states.mood，随形象 SSE 推送）。
  - 本地：位置（localStorage `aha2.pet.position.v1`）、当前动作、open_task、心跳计时。
  - 客户端不再把拖拽/漫游位置写服务端，也不再应用远端 x/y/action；只应用 character 和 mood。
  - 点喜/怒/跳只同步 mood，不同步 action。
- 提交：`a7cd824 fix: keep pet presentation device-local`（前一片 456f0e6）。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-142107.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-142114`。
- Server `ED41FC56181801A8C785AB439F3EC4CAEE22C3802F7B420792698F4107EF1BBA`；Tray `88D1982E324D9233822F15D7811CB79BEA4A258C188231E00A58B441586AF8DC`。
- 进程 22:21:20 起；`/healthz` 200。
- 线上 `/assistant.js` 含 `PET_POSITION_KEY`、`saveLocalPetPosition`；不再含 `setPetPosition(state.x`。
- 浏览器验证：mock 远端 state(x=.2,y=.3,action=jump) 不改变本地 dock；拖拽后 localStorage 写入、无 PUT；点 happy 只 PUT mood。
- 下一片：每设备 5 分钟空闲心跳 + 模型动作协议（say/play/move_to/open_task），不做 leader/presence 全量同步。
