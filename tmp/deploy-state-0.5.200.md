# 0.5.200 部署状态

- 动作持续时间改为 3 秒：
  - jump / walk：`playMotion(..., {once:true,duration:3000})`，总长 3s。
  - happy：2 次 × 1.5s = 3s。
  - angry：3 次 × 1s = 3s。
  - move_to：`positionAt(..., fixedDuration=3000)`，短距离移动也固定 3s。
- 提交：`21f4d55 fix: hold each pet action for three seconds`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-181315.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-181323`。
- Server `262143D463B955AEE4A689D0581705E5937ABBDA71FFE095E63369534346B804`；Tray `DF825E20637D1C0D8526F9096D71DE609E3699F66E8C6B379CD47BBDAD88579B`。
- 进程 2026-10-29 02:13 起；`/healthz` 200；线上 `assistant.js` 含 `fixedDuration`/`3000`。
