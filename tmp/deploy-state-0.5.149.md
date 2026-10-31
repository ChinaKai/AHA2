# 0.5.149 部署状态

- 变更：飞书渠道可靠性修复（SDK 3.12.2、心跳/看门狗、capability 滑动续期、stderr 可观测性），插件 1.1.10。
- 候选 WebVersion：`v0.5.149.20261028.11789c97c905.dirty`
- 候选 SHA256：
  - Server `ADF19BF0E020F41F576C04657E98850EF3A5DD256CF55FE213BA5817E3AFF893`
  - Tray `B166CCB181B73BDF99D57C32ACA2AEC904BB375518C09830D715EF6230F2BD29`
  - Plugin `F5630739476E438EFD4799ADA578947E966257CC0C06A7196B47B2B4536A6F35`
- 部署结果：`Status=succeeded`、`Health=ok`，结果文件 `dist/user-deploy-result-0.5.149.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-064340`。
- 安装：`D:\Program Files\AHA2`（per-user-custom / existing-install-in-place）；数据 `D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`。
- 进程：aha2 14:43:48、tray 14:43:46、feishu plugin 14:43:53（2026-10-28）；`/healthz` 200；安装版本 `aha2 0.5.149`。
- 校验：安装 server/tray/plugin SHA256 与候选完全一致；插件 manifest `1.1.10`。
- 计划任务：`AHA2 User` -> `D:\Program Files\AHA2\aha2-tray.exe`，`toope` / `Limited` / `Running`；`AHA2 User Update` 已自动删除。
- 数据库：迁移 v82 已执行；active capability `ttl_seconds=86400`，`expires_at = last_used_at + 24h`，滑动续期生效。
- 渠道：实例 `HOME-AHA` `status=ready`，`runtime_heartbeat_supported=1`；`last_seen_at` 每 30s 前进（06:44:25Z -> 06:45:25Z）。
- 待验收：真实飞书入站/出站消息、断网重连、跨 24h 不再假 ready。
