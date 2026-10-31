# 0.5.182 部署状态

- 模型控制宠物：
  - 新增 `POST /api/v1/pet/control`（owner auth）：接收 say + actions，允许 play/move_to/mood/sleep/wake/open_task；校验动作/区域/任务，最多 3 个动作；广播 `pet_action` SSE 并写活动记录。
  - 客户端订阅 `pet_action`，执行说话、动作、移动、心情、睡眠/唤醒、打开任务。
  - 聊天提示词新增 `[pet]{"actions":[{"type":"play","name":"jump"}]}[/pet]` 指令；客户端解析后执行，并把标记从显示文本中移除。
  - 兼容现有 `[motion:...]`；模型可直接控制，不需要额外工具调用。
- 提交：`9a59166 feat: let models control the owner pet`；未 push/tag。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-163637.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-163645`。
- Server `9DF219A7177DF5A2FFBDAD672850C913D3B81DD13F1F3BCFAA0316C5D49CC3D4`；Tray `6D55C1FDA7ED78927538E0433EA157FBB23EBCB6B861D409A976A79AB2DAFAE6`。
- 进程 2026-10-29 00:36:51 起；`/healthz` 200；`POST /api/v1/pet/control` 未登录 401；线上 `assistant.js` 含 `pet_action`。
- 测试：Pet control HTTP 测试（合法 2 动作、非法动作 400）、聊天 `[pet]` mock 验证 mood=happy 且标记不出现在气泡。
- 下一步：Pet API 独立 scoped token + 工具调用；日记/客户端动作记录。
