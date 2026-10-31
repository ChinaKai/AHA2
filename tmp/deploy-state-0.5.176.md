# 0.5.176 部署状态

- 变更：宠物说话放宠物左侧或右侧，按位置自动选择。
  - 宠物在左半屏 → 文字放右边；在右半屏 → 放左边；越界自动翻转。
  - 每行 23 字，最多显示两行（44px 高）。
  - 超过两行时文本在框内向上滚动，始终看最新两行；位置固定。
  - 最后一页/最后一行打完后停 3 秒淡出。
- 提交：`ca49eda fix: place two-line scrolling speech beside the pet`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-160659.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-160707`。
- Server `716E8572CE4B83DA41B038CE179AB83ACD88C28EF929EE8C1E949377288BA085`；Tray `76DEDA9B07F39EC8315E632A340913887BE1EDFA8C68038F2ED589B431DD92F1`。
- 进程 2026-10-29 00:07:12 起；`/healthz` 200。
- 390×844 测试：宠物 x=304，气泡 x=36 宽 258，左右不重叠；文本 23 字换行、3 行时滚动显示后两行。
