# 0.5.174 部署状态

- 变更：说话气泡固定字数分页，打字中不再换位置。
  - 每页 42 个字符，气泡固定 86px 高。
  - 一页打完后清空，从头继续打剩余页；位置固定不变。
  - 最后一页打完后停留 3 秒再淡出。
  - 长文实测：第一页 42 字、第二页 24 字，气泡 y 坐标完全一致（662→662）。
- 提交：`edf3729 fix: paginate speech without moving the bubble`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-155442.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-155450`。
- Server `D90C979F174ED4928AE936EC0132030FE0F88737C2D9508FEF4CA885B05B3D43`；Tray `3E15053AAC99051C18DF17C76DA404E7D358610043F719ED2A568AAD9B957CD6`。
- 进程 23:54:56 起；`/healthz` 200；线上 `/assistant.js` 含 `SPEECH_PAGE_CHARS`/`SPEECH_BUBBLE_HEIGHT`/`speechPageTimer`。
