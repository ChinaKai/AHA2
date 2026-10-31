# 0.5.158 部署状态

- 变更：关闭提示气泡。`HINTS_ENABLED=false`，不再轮询 hints、不再显示气泡；提示 API 与事件仍保留，后续可一键恢复。
- 继承 0.5.157：助手层 z-index 25、移动端面板抬到底栏上方、宠物移到面板上方/空间不足隐藏。
- 基础提交：`9945b28`；本次改动仍在工作区未提交，WebVersion 带 `.dirty`。
- 候选 WebVersion：`v0.5.158.20261028.9945b281b5af.dirty`
- 候选 SHA256：
  - Server `9C1EE36628391E7B001C20DE72A1FFFA36DCDDF938CB42FD54296A284738848F`
  - Tray `E9595873355FA141E30DA69F9BDCC4A50F95C6599A4A90F6EA7B4D4EE3456E08`
  - Plugin `CDB811FD17395A322A762E5804E77D73DDF0AF28A68F6532C619CFA4130BF749`
- 部署结果：第一次尝试因 `aha2-tray.exe` 被占用失败并回滚；重试 `Status=succeeded`、`Health=ok`，结果文件 `dist/user-deploy-result-20261028-113958.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-114006`。
- 安装：`D:\Program Files\AHA2`；数据 `D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`。
- 进程：tray 19:40:11、server 19:40:13、feishu plugin 19:40:16（2026-10-28）；`/healthz` 200；安装 server/tray 哈希与候选一致。
- 线上校验：`/assistant.js` 含 `HINTS_ENABLED = false`；健康 200。
- 免费 3D 模型调研：
  - 原版候选（Sketchfab，CC 免费但下载需登录）：`gugugaga penguin`、`DeepSeek Big Fat Whale Yukkuri`。
  - 可直接下载的免费模型：GitHub `TTZW1001/GuguGaga` 的 `penguin.glb`（CC-BY，rigged，无 clip）；Poly Pizza `Whale`（CC-BY，静态，177KB）。
  - 已下载到 `tmp/gugu3d/`，尚未接入 Three.js。
