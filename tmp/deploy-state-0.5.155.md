# 0.5.155 部署状态

- 变更：宠物移动从 CSS transition 改成逐帧路径移动，不再瞬移。
  - `positionAt` 使用 requestAnimationFrame，按距离计算 0.9–3.2 秒路程，ease-in-out 逐帧推进。
  - 移动时叠加 0.38 秒步行摆动；拖拽会取消当前移动。
  - 保留 1.8 秒后首次漫游、6–12 秒随机移动；透明 PNG 形象、删除小A、拖拽均沿用 0.5.154。
- 基础提交：`9945b28`；本次改动仍在工作区未提交，WebVersion 带 `.dirty`。
- 候选 WebVersion：`v0.5.155.20261028.9945b281b5af.dirty`
- 候选 SHA256：
  - Server `1EF936D885B708A9C3F0151916F7420C68031A3AC4EFDCA686BC2F78E0EFD3B3`
  - Tray `BBF4C055218BCC839369D1B27ECA41550EA2CE9C7BF2586DB5263CE2FA174C21`
  - Plugin `CDB811FD17395A322A762E5804E77D73DDF0AF28A68F6532C619CFA4130BF749`
- 部署结果：`Status=succeeded`、`Health=ok`，结果文件 `dist/user-deploy-result-20261028-110312.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-110320`。
- 安装：`D:\Program Files\AHA2`；数据 `D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`。
- 进程：tray 19:03:25、server 19:03:27、feishu plugin 19:03:30（2026-10-28）；`/healthz` 200；安装 server/tray 哈希与候选一致。
- 静态资源：`/assistant.js` 含 `requestAnimationFrame`、`walking`、`fish-3d.png`；`/assets/assistant/gugu-3d.png`、`fish-3d.png` 200。
- Playwright 390×844 实测：移动过程有 24 个不同位置、逐步推进；拖拽到 (100,200) 成功；拖拽后点击能打开面板。
- 待验收：Owner 手机硬刷新后观察是否仍有瞬移；若仍有，重点查缓存或 Safari/浏览器对 rAF 的后台节流。
