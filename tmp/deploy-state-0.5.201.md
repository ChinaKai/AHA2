# 0.5.201 部署状态

- 宠物页面感知：
  - 每次聊天/自主思考都发送 layout：viewport、scroll_y、宠物矩形、面板矩形，以及 mobile_header/sidebar/bottom_nav/chat_panel/chat_composer/chat_messages/settings_panel 的矩形。
  - 后端把 layout 以自然语言注入 system prompt：“视口…宠物矩形…面板…关键区域…”。
- 任意移动：
  - 新增 `pet_move_to` 工具，x/y 为 0–1 视口比例（宠物中心），duration_ms 可选。
  - 聊天和自主思考都注册该工具；`ControlOwnerPet` 校验 0–1 并广播。
  - 客户端 `move_to` 支持 x/y 任意坐标，默认 3 秒；保留区域移动兼容。
- 动作时长（0.5.200）：jump/walk 3s，happy 2×1.5s，angry 3×1s，move 固定 3s。
- 提示词：无 JSON、无 `[motion]`/`[pet]`；只列 7 个 pet_* 工具；动作关键词强制 tool_choice。
- 提交：`e7ba8e6 feat: give the pet page layout and free movement`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-182326.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-182335`。
- Server `BD1B5E65E0950DF482CB8B1B1E03FDC5DA0B9429D72812C98A5EFB80C67B61BA`；Tray `800835F194B5B35DBEE1EDCA013C55696D1F40326F7DFC92FA776B80E44334F4`。
- 进程 2026-10-29 02:23 起；`/healthz` 200。
- 测试：聊天请求实际带 layout（pet 308,272，viewport 390）；SSE `pet_move_to(0.2,0.5)` 3s 后宠物中心到约 (75,419)，目标 (78,422)。
