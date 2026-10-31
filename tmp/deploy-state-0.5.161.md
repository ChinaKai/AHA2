# 0.5.161 部署状态

- 变更：
  - 动作菜单只保留 `走/跳/喜/怒`；`MOTION_NAMES` 同步限制为 idle/walk/jump/happy/angry，模型输出其他动作会被忽略。
  - 点“走”现在会先找一个空位再走过去，不是原地踏步。
  - 昵称随形象：咕咕嘎嘎 / 大肥鱼；头部标题、空状态、输入框 placeholder、面板/宠物 aria-label 都跟着变。
  - 形象列表改为固定 42px 图标列 + 文本列 grid，两行文案垂直居中、左边缘对齐。
- 基础提交：`9945b28`；改动未提交。
- WebVersion：`v0.5.161.20261028.9945b281b5af.dirty`
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-130917.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-130926`。
- Server `D02A2FA5C6EBBA686507017154AC167D04148639463C3B06BA50ACE08118EECF`；Tray `7E02B8C17979BA57B7E1313AE55A6AADA2C4F1A6F54A71F2D2946B6109A7C464`。
- 进程 21:09:31 起；`/healthz` 200。
- 线上 `/assistant.js`：`MOTION_LABELS` 4 项、`MOTION_NAMES` 只含 5 个（含 idle）、`nickname: "大肥鱼"`、`HINTS_ENABLED=false`。
- Playwright 校验：gugu 标题“咕咕嘎嘎”、fish 标题“大肥鱼”；动作菜单 4 项；角色菜单 2 项、图标/描述对齐。
- 说明：`转身` 已从菜单移除；2.5D 转身会露图，真转身仍待 3D 网格。
