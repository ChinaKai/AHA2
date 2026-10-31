# 0.5.131 部署状态（重启中断后续接用）

- 提交：`dab4cae fix: keep intraword underscores literal in markdown`（Web 资源内嵌，必须重启才生效）
- 目标版本：0.5.131
- 安装目录：`D:\Program Files\AHA2`（Mode=per-user-custom，此前已授权，脚本写入探测）
- 数据目录：`D:\ProgramData\AHA2`
- 监听：`0.0.0.0:8766`；健康检查：`http://127.0.0.1:8766/healthz`
- 计划任务：`AHA2 User` → 动作 `D:\Program Files\AHA2\aha2-tray.exe --server ... --listen 0.0.0.0:8766 --data-dir D:\ProgramData\AHA2`，Principal=toope，RunLevel=Limited
- 部署方式：`-UpdateExistingInstallInPlace`（宿主无 Inno Setup），`-AllowCustomUserWritableInstallDir`
- 已跑：`-ValidateOnly` 通过（Mode=build-and-deploy，Distro=Ubuntu-24.04）
- 已跑：`-BuildOnly` 成功（第二次；第一次 `go test ./...` 撞到既有的 flaky 用例 TestClaudeNativeExecutionBorrowsProfileCredential）
- 候选：ServerVersion `aha2 0.5.131`，ServerSHA256 `6D75F83997B682F8B594966AA45375D3DFB506F8687462F20B174E0B455F6155`，TraySHA256 `1AB525BC3030826D5A5D1E50128E8B13E5C94DEC675B26F1615978C274DC98D7`，PluginSHA256 `66EE81E0628C69CEB364681DD8120C6D4C2445ABB8ADA291F674075948207967`
- 部署结果 JSON：`dist/user-deploy-result-05131-underscore.json`；日志：`tmp/deploy-05131.log`

## 若本 Turn 被重启打断

1. 先读 `dist/user-deploy-result-*.json` 与部署日志，确认结果 Status。
2. 核对线上版本与健康：`curl http://127.0.0.1:8766/healthz`；`Get-ScheduledTask 'AHA2 User'` 的 action/principal。
3. 比对安装目录 server/tray 的 SHA256 与 `dist\aha2-windows-amd64.exe` / `dist\aha2-tray-windows-amd64.exe`。
4. 验证下划线修复：Web 里发 `测_试_消息`，应原样显示。
5. 不要重复部署；只有确认失败才回滚（脚本自带备份恢复）。
