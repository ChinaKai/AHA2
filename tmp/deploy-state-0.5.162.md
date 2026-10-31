# 0.5.162 部署状态

- 第一片：Owner 跨设备单宠物状态 + API + SSE + 客户端同步。
  - migration v85 `pet_states` 单例：mood/action/zone/x/y/facing/sleeping/quiet/version。
  - `GET /api/v1/pet`、`PUT /api/v1/pet`（版本冲突 409）、`GET /api/v1/pet/events`（SSE）。
  - 换形象、动作、拖拽、漫游结束都会写服务端；其他设备经 SSE 更新形象/动作/位置。
  - 位置存归一化坐标 + 语义 zone；同 AHA 服务的手机/电脑各自映射。
- 未做：presence/leader、5 分钟心跳、模型动作协议、open_task、pet:control scoped token、免打扰。
- 提交：`456f0e6 feat: sync owner pet state across devices`；未 push/tag。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-141550.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-141558`。
- Server `3CB63C2234B6AF550B5A39C4ECD95329C18D09179A628E4DB2EC820599392168`；Tray `3C72D6D6C608D53B1A93EFAFCBB00D1A8BABF3B7E2CD1EB50BF07138013A40A5`。
- 进程 22:16:04 起；`/healthz` 200；`/api/v1/pet`、`/api/v1/pet/events` 未登录 401（路由存在）。
- 测试：Pet store/HTTP 测试通过；Assistant/Pet 包测试通过；Web 133 pass。
- 线上 `/assistant.js` 含 `petState()`、`petEventsURL`、`applyPetState`、`schedulePetSync`。
