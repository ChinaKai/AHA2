# 0.5.156 部署状态

- 变更：宠物动作系统。
  - 10 个动作：走、跑、跳、蹲、起、转身、喜、怒、哀、乐（另有 idle）。
  - Web Animations API 在 `.pet-motion` 上播放；walk/run 与 rAF 位移联动，跳/蹲/起/转身/情绪为一次性姿态，结束后回 idle。
  - 面板右上角新增“动”按钮，2 列动作菜单可直接点测。
  - 模型控制：系统 Prompt 允许回复末尾输出 `[motion:动作]`；Web 解析并播放，同时从显示文本中移除标记。另暴露 `window.AHA2Assistant.play(name)` / `.list()` / `.moveTo(x,y)` 给后续工具。
- 基础提交：`9945b28`；本次改动仍在工作区未提交，WebVersion 带 `.dirty`。
- 候选 WebVersion：`v0.5.156.20261028.9945b281b5af.dirty`
- 候选 SHA256：
  - Server `983DB46514B67A8E5B50372F25207A0FF98DF2299DF712475D4F47D27878ACF8`
  - Tray `0B7ED1DCC223935F5A4BCED06FD58A201DE3779D9854F43066BF5E907BFA7D67`
  - Plugin `CDB811FD17395A322A762E5804E77D73DDF0AF28A68F6532C619CFA4130BF749`
- 部署结果：`Status=succeeded`、`Health=ok`，结果文件 `dist/user-deploy-result-20261028-111419.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-111427`。
- 安装：`D:\Program Files\AHA2`；数据 `D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`。
- 进程：tray 19:14:32、server 19:14:34、feishu plugin 19:14:37（2026-10-28）；`/healthz` 200；安装 server/tray 哈希与候选一致。
- 静态资源：`/assistant.js` 含 `MOTION_LABELS`、`data-assistant-motion`、`AHA2Assistant`、`[motion:`、`requestAnimationFrame`；两张形象 PNG 200。
- 浏览器验证：Playwright 390×844 下动作菜单 10 项；各动作均有 running 动画（walk/jump/crouch/turn/happy/sad 的 computed transform 随时间变化）；路径移动仍是逐帧推进。
- 待验收：Owner 手机硬刷新后点“动”逐个试，并在聊天里让模型输出动作标记；若要真正的骨骼/逐帧走路，需要额外动画素材。
