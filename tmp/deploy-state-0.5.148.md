# 0.5.148 部署状态

- 提交：`11789c9 fix: restore Feishu channel health after reconnect`
- 变更：飞书插件注册 SDK `OnReconnected`，WebSocket 自动重连后恢复渠道 `ready`，不再一直 `degraded` 到进程重启；新增回归测试；插件 `1.1.8 -> 1.1.9`。
- 候选：Web `v0.5.148.20261027.11789c97c905.dirty`；Server `aha2 0.5.148` SHA256 `89A16B11FC79A157F1C98CB77C9E7A6D89EAE74B7D80D76C327F524CFCC2E62E`；Tray `B34A90EA822CEFA807A283C59FA28D706A1F831949BC23349391F67AEBF34EDF`；Plugin `58183C5904F3889811D9DD38BCF1A0BE7C24E6674A0E5F61BAE53AC35CF63987`。
- 结果 JSON：`dist/user-deploy-result-local-build.json`（Status=succeeded, Health=ok）
- 备份：`D:\ProgramData\AHA2\backups\user-installed-20261027-052729`
- 安装：`D:\Program Files\AHA2`（per-user-custom, existing-install-in-place）；数据：`D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`。
- 计划任务：`AHA2 User` -> `D:\Program Files\AHA2\aha2-tray.exe`，`toope` / `Limited` / `Interactive`；`AHA2 User Update` 已自动清理。
- 验证：`go test ./...`、`go vet ./...`、Web build/test、飞书插件 test/vet/build 全过；进程 13:27:35/13:27:37 重启；healthz 200；三个安装文件 SHA256 与候选一致；插件 manifest `1.1.9`；channel instance `HOME-AHA` `status=ready`（05:27:44Z）。
- 版本说明：本轮只部署，未创建 `v0.5.148` tag、未发布 Release。

## 若被打断

1. 读 `dist/user-deploy-result-local-build.json`；健康 200；安装哈希与候选一致。
2. 核对 `AHA2 User` 任务与插件 manifest 1.1.9；渠道实例应为 ready。
3. 如需对外发布，创建并推送 `v0.5.148` tag 触发 Release 工作流。
