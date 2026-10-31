# 0.5.148 发布状态

- 提交：`11789c9 fix: restore Feishu channel health after reconnect`（与已部署 0.5.148 相同）
- Tag：`v0.5.148` annotated tag object `f1d9b4e` -> commit `11789c9`，已推送 origin。
- Release：https://github.com/ChinaKai/AHA2/releases/tag/v0.5.148
  - release id `425073359`，draft=false，prerelease=false，published 2026-10-27T05:41:48Z。
- 工作流：第一次 run `40455737421` 仅 `Test Go` 失败（既有偶发用例 `TestClaudeNativeExecutionBorrowsProfileCredential`），打包全部 skipped；删/重推 tag 后 run `40456236405` 全绿（test、Windows、Linux amd64/arm64、macOS amd64/arm64、publish-release 全部 success）。
- 产物：13 项（12 个安装包 + SHA256SUMS），数量与 SHA256SUMS 的 12 条完全对应。
- 校验：已下载 `AHA2-Setup-x64.exe`、`AHA2-Setup-User-x64.exe`，SHA256 分别为 `6b6a92d0...4433`、`f37959c1...cb0e`，与 SHA256SUMS 一致。
- 本机宿主机仍为已部署的 0.5.148：`/healthz` 200，安装 `aha2.exe` SHA256 `89A16B11...E62E`（与部署候选一致）。
