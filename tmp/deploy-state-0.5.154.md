# 0.5.154 部署状态

- 变更：修复宠物形象与交互。
  - 鲸鱼娘改用官方透明 PNG（front 视图抠图），不再使用 WebM；头像和宠物不再有黑底/裁剪。
  - 删除小A，默认形象改为咕咕嘎嘎；旧 `xiaoa` 行在读取时自动迁移为 `gugugaga`。
  - 漫游改为 1.8 秒后首次移动，之后 6-12 秒随机移动；不再依赖 24 秒轮询。
  - 新增 pointer 拖拽，鼠标/手指可拖走；拖动不触发面板，轻点才打开。
- 基础提交：`9945b28`；本次改动仍在工作区未提交，WebVersion 带 `.dirty`。
- 候选 WebVersion：`v0.5.154.20261028.9945b281b5af.dirty`
- 候选 SHA256：
  - Server `37726503CFC15C606DE5889BE0F3C33AED78B8E4257910F5919307D1C0ADD5C0`
  - Tray `1587DFF6B5C07D8B2BC57FAC3E9EFC533AAF499413A2ADC4A30388C1174A187D`
  - Plugin `CDB811FD17395A322A762E5804E77D73DDF0AF28A68F6532C619CFA4130BF749`
- 部署结果：`Status=succeeded`、`Health=ok`，结果文件 `dist/user-deploy-result-20261028-105351.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-105359`。
- 首次构建因既有 httpapi 附件测试的临时目录清理偶发失败中止，未部署；重跑通过后完成部署。
- 安装：`D:\Program Files\AHA2`；数据 `D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`。
- 进程：tray 18:54:07、server 18:54:08、feishu plugin 18:54:12（2026-10-28）；`/healthz` 200；安装 server/tray 哈希与候选一致。
- 静态资源：`/assets/assistant/gugu-3d.png`、`/assets/assistant/fish-3d.png` 200；`/assistant.js` 无 `xiaoa`，含 `scheduleRoam`、`pointerdown`；`/api/v1/assistant` 401。
- 浏览器验证：Playwright 390x844 下宠物 2.5 秒内已移动；鼠标拖到 (110,220) 成功；拖拽后点击能打开面板。
- 待验收：Owner 手机硬刷新后确认鲸鱼头像透明、不裁剪、宠物会自己走且可拖。
