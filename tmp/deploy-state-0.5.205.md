# 0.5.205 部署状态

- 从预设动作改为能力：
  - 删除 `pet_move`（左/右/上/下区域预设）。
  - 删除 `pet_walk_circle`（预设圆圈）。
  - 保留 7 个工具：`pet_play`、`pet_move_to`、`pet_walk_path`、`pet_mood`、`pet_open_task`、`pet_sleep`、`pet_wake`。
  - `pet_move_to` 是任意点能力；`pet_walk_path` 是任意路径能力，points 上限 128，模型自己生成圆圈/曲线点。
  - 提示词明确“路径由你生成 points，没有预设圆圈/区域动作”。
- 提交：`c4f38df refactor: let the pet generate arbitrary paths`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-234320.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-234328`。
- Server `18B4E42A711AE8A5751A4EC3DBBCFD6A80F8189382231943D92FA9ACCDD55FFA`；Tray `212B33181EE29423101C45E5AAACFD19644322563B2835703F05992BFC731FC4`。
- 进程 2026-10-29 07:43 起；`/healthz` 200；线上 `assistant.js` 含 `walk_path`，无 `walk_circle`。
- 测试：web 133、app/http Assistant/Pet 通过；`walkPath` 已用 36 段圆点验证连续移动。
