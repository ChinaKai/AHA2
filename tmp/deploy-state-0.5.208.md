# 0.5.208 部署状态

- 路径残影：
  - 动作执行期间残影保持 opacity .35，不提前淡出。
  - `walkPath` 完成时才统一加 `.trail-fade`，CSS opacity 过渡 2.6s，2.8s 后移除。
  - 残影上限从 24 提到 80，长路径不会过早丢早段。
- 提交：`533b739 fix: keep path ghosts until the walk finishes`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-000041.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261029-000048`。
- Server `472E6C82AF2423913DC00D63B9E4559923913B2CF0FAB1761AA0023B2EB524FF`；Tray `5EE40BD07D0D97D7A6E1F17179FA2C7934F44A81226B0CEFDCE5262067F25EC6`。
- 进程 2026-10-29 08:01 起；`/healthz` 200；`styles.css` 含 2.6s 过渡。
- 测试：3s 路径执行中 14 个残影无 fade；结束后 31 个统一 fade；3s 后清空。
