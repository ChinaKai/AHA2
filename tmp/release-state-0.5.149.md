# 0.5.149 发布状态

- 提交：`f753cc5 fix: keep Feishu channel alive across reconnects and capability expiry`（与已部署 0.5.149 相同）
- Tag：`v0.5.149` annotated tag object `131b1c4` -> commit `f753cc5`，已推送 origin。
- Release：https://github.com/ChinaKai/AHA2/releases/tag/v0.5.149
  - release id `426392906`，draft=false，prerelease=false，published 2026-10-28T08:01:05Z。
- 工作流：run `40653201413`，一次通过；`test`、Windows、Linux amd64/arm64、macOS amd64/arm64、`publish-release` 全部 success。
- 产物：13 项（12 个安装包 + SHA256SUMS），SHA256SUMS 12 条与产物名一一对应。
- 校验：下载 `aha2-sync_0.5.149_arm64.deb`，SHA256 `9c735c16...26de5`，与 SHA256SUMS 完全一致。Windows 安装包因当前链路下载过慢未在本轮完整下载，其预期哈希为 `c6d82caf...6a53`（见 SHA256SUMS）。
- 本机宿主机仍为已部署的 0.5.149：`/healthz` 200，安装哈希与部署候选一致。
