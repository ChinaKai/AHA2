# 0.5.151 部署状态

- 变更：修复小A发送消息返回 `csrf_rejected`。
- 根因：`assistant.js` 由构建产物直接引用未加版本号的 `./api.js`，而 `app.js` 引用 `./api.js?v=...`；浏览器把两者当成两个模块实例，`api.setCSRF()` 只设置在主实例上，助手实例的 CSRF 为空。
- 修复：`scripts/build-web.mjs` 对 `assistant.js` 的 `./api.js` 同样加版本号；新增 `web/tests/assistant.test.mjs` 断言助手模块与主应用共享同一个 `api.js?v=...` 实例。
- 基础提交：`f753cc5`；助手 P0 与本次修复仍在工作区未提交，WebVersion 带 `.dirty`。
- 候选 WebVersion：`v0.5.151.20261028.f753cc57c9f6.dirty`
- 候选 SHA256：
  - Server `F7F5E1C4F1F2D63FD8AE246B01D98B5475668AE574AC2809917ABEC5A5480D2A`
  - Tray `9E10001272A987AAF4907115874CC3FBAF1CA701C180E76C7F91BAFF1AEE5C83`
  - Plugin `DAFF4A711DCD0347870905C6739649E02D4E37274276D27AD1E2033AE0DA7DED`
- 部署结果：`Status=succeeded`、`Health=ok`，结果文件 `dist/user-deploy-result-20261028-092842.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-092850`。
- 安装：`D:\Program Files\AHA2`；数据 `D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`。
- 进程：tray 17:28:57、server 17:28:59、feishu plugin 17:29:02（2026-10-28）；`/healthz` 200；安装 server/tray 哈希与候选一致。
- 计划任务：`AHA2 User`，`toope` / `Limited` / `Running`。
- 模块链核对：`/app.js` 与 `/assistant.js` 都引用 `./api.js?v=53114ee70598`，`/app.js` 引用 `assistant.js?v=53114ee70598`；`/assistant.js` 200，`/api/v1/assistant` 401（路由正常）。
- 待验收：Owner 硬刷新页面后重试“现在哪些任务在等我？”，确认不再出现 csrf_rejected。
