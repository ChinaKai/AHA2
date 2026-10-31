# 0.5.169 部署状态

- 身份/准则 + 活动记录 + 气泡 UX：
  - `owner_assistants` 新增 `pet_identity`、`pet_principles`（migration v87），设置页可编辑；聊天/自主思考提示词都会注入。
  - 新增 `pet_events`：模型每次 brain 记录 device_id、page、原始回复、解析动作、状态（acted/said/empty/model_error）。
  - `GET /api/v1/pet/activity?limit=20`；设置页“活动记录”显示时间/状态/说话或动作。
  - 设置页新增“显示说话气泡”开关（localStorage `aha2.pet.bubble.v1`，每设备本地）。
  - 说话气泡改为透明无背景，打字机逐字输出；思考时宠物头旁显示三个冒泡点（chat 发送 + brain 思考都触发）。
  - 聊天提示词继续使用动态昵称和四个动作；身份/准则追加到 system prompt。
- 提交：`8e50cf6 feat: add pet identity, activity log, and speech bubbles`；未 push/tag。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-152241.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-152237`。
- Server `266F4BFE10724E99C3DC229FFE74721E1EA932121020E414CB9E571DAD66C6BE`；Tray `40BFF54A9DDE73E66DA8DB964BD15F4F237E5E1DFF38D6037D944B30A19931DE`。
- 进程 23:22:55 起；`/healthz` 200；`/api/v1/pet/activity` 未登录 401。
- 线上 `/assistant.js` 含 `pet-bubble-enabled`、`assistant-thinking`、`setThinking`、`speech`。
- 已知未做：活动流只记录模型 brain，不记录手动拖拽/菜单动作；日记、免打扰、每小时上限、pet:control token 未做。
