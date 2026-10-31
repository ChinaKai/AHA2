# 0.5.150 部署状态

- 变更：Owner 宠物助手 P0（只读问答 + 宠物提示层），迁移 v83。
- 基础提交：`f753cc5`；助手 P0 代码仍在工作区未提交，WebVersion 带 `.dirty`。
- 候选 WebVersion：`v0.5.150.20261028.f753cc57c9f6.dirty`
- 候选 SHA256：
  - Server `31AA38E91B73947ACAC32600B9FCD8EBBC7B4ED184B99CEB83CCABF0B347BC78`
  - Tray `79D67C6CF84DB384F3A8B74BF77A39ABDB377351B5CB54A918983E3E296CBD18`
  - Plugin `DAFF4A711DCD0347870905C6739649E02D4E37274276D27AD1E2033AE0DA7DED`（manifest 1.1.10）
- 部署结果：`Status=succeeded`、`Health=ok`，结果文件 `dist/user-deploy-result-20261028-092253.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-092302`。
- 安装：`D:\Program Files\AHA2`（per-user-custom / existing-install-in-place）；数据 `D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`。
- 进程：tray 17:23:09、server 17:23:11、feishu plugin 17:23:20（2026-10-28）；`/healthz` 200；安装 server/tray/plugin 哈希与候选完全一致。
- 计划任务：`AHA2 User` -> `D:\Program Files\AHA2\aha2-tray.exe`，`toope` / `Limited` / `Running`。
- 静态资源：`/app.js` 200 且引用 `assistant.js?v=53114ee70598`；`/assistant.js` 200 且含 `mountAssistant`；`/styles.css` 含 assistant 样式；`GET /api/v1/assistant` 与 `/hints` 均返回 401（路由已注册，等待登录）。
- 待验收：Owner 在浏览器中确认宠物出现、能问答、提示不遮挡操作；助手只支持 Provider 模型（chat/anthropic/responses），若设备只有 Codex Official/Claude Native 会提示无可用模型。
