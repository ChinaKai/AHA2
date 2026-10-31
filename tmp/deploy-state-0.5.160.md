# 0.5.160 部署状态

- 变更：turn 动作从 420ms/ease-in 改为 220ms/ease-out，并加快速 rotateY 过渡。
- 部署：`Status=succeeded`、`Health=ok`，结果 `dist/user-deploy-result-20261028-123113.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-123122`。
- 安装 server hash `7D560CD090BBE2173C2B8BF1E1CA1E1B2F5EB93C7EBA41B58232F865E954256E`，tray `25F752BAB535E172E096779C3F733B6AC1852F000B531237100C2FCBDBCF51D4`；进程 20:31:28 起；`/healthz` 200。
- 线上 `/assistant.js` 含 `options.duration || 220`、`HINTS_ENABLED = false`。
- Owner 反馈：转身仍能看出是 2D 图。结论：220ms 只解决“慢”，不解决“平”；2.5D 分层无法做真正的侧/背转，必须有 3D 网格。
- 图生 3D 尝试：StableFast3D HF Space `/run_button` 报 AppError，`/lambda` 只回 update；本机无 GPU、无 torch；TripoSR 仓库 tarball 下载被截断，暂未拿到可用 GLB。
