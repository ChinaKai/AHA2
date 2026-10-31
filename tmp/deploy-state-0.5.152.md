# 0.5.152 部署状态

- 变更：宠物形象与动画。新增三套内置形象 `xiaoa` / `gugugaga` / `deepseek_fish`，助手面板右上角可切换；迁移 v84 给 `owner_assistants` 加 `character`。
- 基础提交：`9945b28`；本次形象代码仍在工作区未提交，WebVersion 带 `.dirty`。
- 候选 WebVersion：`v0.5.152.20261028.9945b281b5af.dirty`
- 候选 SHA256：
  - Server `649E4054B9E4ADF7AA375471057D7B909E2FB2081D2FC6CCB46CF38393C27801`
  - Tray `A34F76DED7CF42DCB2185D7B5295AC26EB971ECE036F29430B47D847CE9CBB09`
  - Plugin `CDB811FD17395A322A762E5804E77D73DDF0AF28A68F6532C619CFA4130BF749`
- 部署结果：`Status=succeeded`、`Health=ok`，结果文件 `dist/user-deploy-result-20261028-094637.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-094644`。
- 安装：`D:\Program Files\AHA2`；数据 `D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`。
- 进程：tray 17:46:53、server 17:46:57、feishu plugin 17:47:02（2026-10-28）；`/healthz` 200；安装 server/tray 哈希与候选一致。
- 计划任务：`AHA2 User`，`toope` / `Limited` / `Running`。
- 静态资源核对：`/app.js` 与 `/assistant.js` 共享 `./api.js?v=93632fafef9d`；`/assistant.js` 含 `gugugaga`、`deepseek_fish`、`pet-duck-body`、`pet-whale-body`；`/assistant.js` 200，`/api/v1/assistant` 401。
- 动画：鸭子摇翅膀/张合嘴/摆尾，鲸鱼摆尾/摆鳍/喷水，走路跳动，思考/提示/打开面板有独立动画；`prefers-reduced-motion` 关闭。
- 待验收：Owner 硬刷新后切换三套形象，确认动画和菜单位置。
