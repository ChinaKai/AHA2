# 0.5.178 部署状态

- 变更：打字减速 20%、说话改最后 3 行滚动。
  - 宠物气泡 28ms/字 → 34ms/字；面板 24ms/字 → 29ms/字。
  - 气泡高度 44px（2 行）→ 60px（3 行），每行仍 23 字。
  - 内容超过 3 行时，`scrollTop=scrollHeight` 始终显示最新 3 行。
- 修正：0.5.177 只改了面板间隔，宠物气泡仍是 28ms；0.5.178 已把气泡改为 34ms。
- 提交：`f5e0edc fix: slow pet typing and keep three scrolling lines`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-161414.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-161422`。
- Server `E7D0471E3B793017FCF4B9CAD2A32BAEFEE06D0C64B04BF773B1857110D4E2FA`；Tray `9BF8F271EB437B719B62080581A0CD506E8E2270C7407CEA7653E78CA4DD13BB`。
- 进程 2026-10-29 00:14:28 起；`/healthz` 200；线上 `assistant.js` 含 `}, 34)`、`SPEECH_BUBBLE_HEIGHT = 60`。
