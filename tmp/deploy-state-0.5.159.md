# 0.5.159 部署状态

- 变更：现有 2D 形象做 2.5D 分层，保留原画并增加 3D 透视。
  - 生成 `gugu-head.png`、`fish-head.png`（只保留头部，透明下半），主宠物改为 body + head 两层。
  - `.assistant-pet-art` 加 `perspective: 280px`；头层 `translateZ(16px)`，`transform-origin: 50% 78%`。
  - idle/walk/run/jump/crouch/happy/angry/sad/joy 动作增加 `rotateX/rotateY`，头部有独立视差关键帧。
  - 仍保留 0.5.158 的提示气泡关闭、0.5.157 的对话框防遮挡修复。
- 基础提交：`9945b28`；本次改动仍在工作区未提交，WebVersion 带 `.dirty`。
- 候选 WebVersion：`v0.5.159.20261028.9945b281b5af.dirty`
- 候选 SHA256：
  - Server `FA4FFE40B05089DFD612C9A2A7F34DE39FAFB32FCBD7A11A03054EF0C13644F6`
  - Tray `6EF27EC95136AAB669D7E6089FD540EB9CED7DA6647570257A1E9C838262CF67`
  - Plugin `CDB811FD17395A322A762E5804E77D73DDF0AF28A68F6532C619CFA4130BF749`
- 部署结果：`Status=succeeded`、`Health=ok`，结果文件 `dist/user-deploy-result-20261028-122736.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-122745`。
- 安装：`D:\Program Files\AHA2`；数据 `D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`。
- 进程：tray 20:27:53、server 20:27:55、feishu plugin 20:28:00（2026-10-28）；`/healthz` 200；安装 server/tray 哈希与候选一致。
- 静态资源：`/assistant.js` 含 `pet-2_5d`、`pet-head-layer`、`HINTS_ENABLED = false`；无 `assistant3d`；两张 head PNG 200；`/assistant3d.js` 404。
- 浏览器验证：两张原画在 160px 下头部/身体对齐无缝；动作 computed transform 出现 matrix3d/rotateY；提示气泡关闭。
- 边界：这是 2.5D 分层透视，不是多边形 3D 网格；真 3D 需要建模或图生 3D。
