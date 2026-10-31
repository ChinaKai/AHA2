# 0.5.133 部署状态（重启中断后续接用）

- 提交：`e680e49 fix: dedupe Claude final text and surface Codex reasoning`
- 目标版本：0.5.133
- 安装目录：`D:\Program Files\AHA2`（per-user-custom，既往已授权）
- 数据目录：`D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`；健康 `http://127.0.0.1:8766/healthz`
- 方式：`-UpdateExistingInstallInPlace` + `-AllowCustomUserWritableInstallDir`
- 已跑：`-ValidateOnly` 通过
- 日志：`tmp/build-05133.log`、`tmp/deploy-05133.log`
- 结果 JSON：`dist/user-deploy-result-05133-backend-updates.json`

## 若被打断

1. 读结果 JSON 与部署日志，确认 Status。
2. `curl http://127.0.0.1:8766/healthz`，核对 `AHA2 User` 任务。
3. 比对安装目录 server/tray SHA256 与 `dist\aha2-windows-amd64.exe` / `dist\aha2-tray-windows-amd64.exe`。
4. 验证：Claude 任务应只出现一个最终消息（不再有重复 Update）；Codex 任务应出现「思考」卡片。

## 候选

- ServerVersion `aha2 0.5.133`
- ServerSHA256 `FF88B09D119A8AAB3E9681CC9DF0605FAD2A1896639462F204059443C3168464`
- TraySHA256 `C65B5F27D1891FE0C33C55ED9A49F89264FFB985ED7CB0DF7D685FB81B564867`
- PluginSHA256 `DEDECD977F6514CB62E50B64647ABFF6B94D8EF979F3C464901BCE88D76DDB6B`
