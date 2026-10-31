# 0.5.172 部署状态

- 变更：宠物思考/输出时冻结移动。
  - `petFrozen` 期间取消当前 `moveRAF`，`scheduleRoam` 不漫游，`positionAt` 直接忽略（脑子的 `move_to` 也不会把它带走）。
  - 聊天发送、自主思考、宠物说话气泡都进入冻结；气泡淡出/面板回复结束后 1.2 秒再恢复漫游。
  - 拖拽仍由用户直接控制，不受冻结影响。
- 提交：`6954265 fix: keep the pet still while it is thinking or speaking`；未 push/tag。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-154608.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-154617`。
- Server `AD57BF72C4DE19181C15A6A2CEFDBAD9A6246793A5A3CF292C0CB3039F5762D6`；Tray `318D087C03A294B2F4363589B288E122B509183690AD12B738B044C1D15EA8C6`。
- 进程 23:46:22 起；`/healthz` 200。
- 线上 `/assistant.js` 含 `petFrozen`/`setPetFrozen`/`if (petFrozen)`。
