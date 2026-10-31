# 0.5.153 部署状态

- 变更：宠物形象改为原版素材，不再使用手绘 Q 版 SVG。
  - 咕咕嘎嘎：`gugugagapenguin.com/logo.png` 官方透明 3D 立绘（256×256）。
  - DeepSeek 大肥鱼：`aigengtu.com/public/pet/webm/` 官方桌宠视频 `idle.webm` / `run.webm`（VP9 透明）。
- 自由活动：沿用安全区漫游；鲸鱼移动时切 run 视频，停下回 idle；咕咕嘎嘎用 3D 图做走路/呼吸姿态。
- 基础提交：`9945b28`；本次素材与代码仍在工作区未提交，WebVersion 带 `.dirty`。
- 候选 WebVersion：`v0.5.153.20261028.9945b281b5af.dirty`
- 候选 SHA256：
  - Server `74E05634D8AF1690FBB2EBFDA33D195475493313FCA9705E5EA0503F19921C01`
  - Tray `3F96C6F4ED343C9291EDCF10D15D00A24CB4B6035E6CEF446B7A283041A8994B`
  - Plugin `CDB811FD17395A322A762E5804E77D73DDF0AF28A68F6532C619CFA4130BF749`
- 部署结果：`Status=succeeded`、`Health=ok`，结果文件 `dist/user-deploy-result-20261028-103508.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-103515`。
- 安装：`D:\Program Files\AHA2`；数据 `D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`。
- 进程：tray 18:35:21、server 18:35:22、feishu plugin 18:35:26（2026-10-28）；`/healthz` 200；安装 server/tray 哈希与候选一致。
- 计划任务：`AHA2 User`，`toope` / `Limited` / `Running`。
- 静态资源：`/assets/assistant/gugu-3d.png` 200 `image/png`；`/assets/assistant/whale-idle.webm`、`whale-run.webm` 200 `video/webm`；`/assistant.js` 已引用三份资源；`/api/v1/assistant` 401。
- 备注：部署后又删除了未使用的旧 SVG 常量并重建 Web，行为不变，未再次部署。
- 待验收：Owner 硬刷新后切换两套形象，确认 3D/视频原版观感与自由走动。
