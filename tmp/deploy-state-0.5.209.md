# 0.5.209 部署状态

- 残影改为“边走边淡出”：
  - 每只残影生成时，按“本次动作剩余时间”设置 inline `transitionDuration`。
  - 残影立即进入 opacity 0，因此越早的残影越淡，最后生成的也会在动作结束时归零。
  - 动作结束 `clearTrailGhosts()` 直接清空，不会走完才另起淡出。
  - 上限 80，长路径不丢早段。
- 提交：`cd693bc fix: fade path ghosts during the walk`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-000404.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261029-000412`。
- Server `06CCC51DB49B76194EE9000F24EF203C81498C2413B57008DD4E4F6DD1C40D00`；Tray `8D94159C469E0E8BCC4F54B202787AA1E9F57A489E001B36EF9A441635DA7A9B`。
- 进程 2026-10-29 08:04 起；`/healthz` 200。
- 测试：3s 路径，1s 时前 5 个残影 opacity 0.15~0.23 正在下降；3.3s 时残影数为 0。
