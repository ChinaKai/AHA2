# 0.5.134 部署状态（重启中断后续接用）

- 提交：`d762087 fix: read Codex reasoning text from content[]`
- 目标版本：0.5.134
- 安装目录：`D:\Program Files\AHA2`（per-user-custom）；数据目录 `D:\ProgramData\AHA2`
- 方式：`-UpdateExistingInstallInPlace` + `-AllowCustomUserWritableInstallDir`
- 已跑：`-ValidateOnly` 通过、`-BuildOnly` 成功
- 候选：Server `aha2 0.5.134` SHA256 `55B2E710195F7DC96FE68F6F3A0F045394BED42754ADA140E91F1D8DB7617100`；Tray `9A68F5DE080715BD7DF9DF3EBE04745FF8C0A2A78C87B3B19F0F09DF3316DCB7`
- 日志：`tmp/build-05134.log`、`tmp/deploy-05134.log`
- 结果 JSON：`dist/user-deploy-result-05134-codex-content.json`

## 若被打断

1. 读结果 JSON 与部署日志，确认 Status=succeeded。
2. `curl http://127.0.0.1:8766/healthz`；核对 `AHA2 User` 任务。
3. 比对安装目录 server/tray SHA256 与候选。
4. 验证：用 Codex 后端跑 task-29，应出现「思考」卡片（rollout 里 summary 为空、内容在 content[] 的那种）。
