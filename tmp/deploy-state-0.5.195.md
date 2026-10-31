# 0.5.195 部署状态

- 打开对话面板后宠物默认在面板内部：
  - `dockPoint` 面板打开时返回面板内右上位置：x=right-68，y=top+58，避开 header 和底部 composer。
  - 小面板会 clamp 到 `rect.bottom - PET_SIZE - 76`，不被输入框盖住。
  - 宠物 inline z-index=10，面板=4，确保不被面板遮。
  - 面板关闭后恢复全屏随机漫游；打开期间不漫游。
- 提交：`afa64ff fix: open the pet inside the chat panel`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-175102.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-175109`。
- Server `FE61B7515ED2C564D7D821DDFC4B74043C2C7A8F451BFB91084218EE71FEA6E2`；Tray `BB1D4FB9580B0054400AF183665F940753764FEBA5B34278202B96D5DBEFB9E7`。
- 进程 2026-10-29 01:51 起；`/healthz` 200。
- 390×844 测试：面板 (8,214,374,560)，宠物 (308,272,58,58)，z-index=10，位于面板内部右上。
