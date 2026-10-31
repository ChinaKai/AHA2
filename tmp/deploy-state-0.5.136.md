# 0.5.136 部署状态（重启中断后续接用）

- 提交：`4aa1a8b fix: read Codex rollout through the workspace runner`
- 目标版本：0.5.136；安装 `D:\Program Files\AHA2`；数据 `D:\ProgramData\AHA2`
- 方式：`-UpdateExistingInstallInPlace` + `-AllowCustomUserWritableInstallDir`
- 结果 JSON：`dist/user-deploy-result-05136-codex-rollout-runner.json`
- 日志：`tmp/build-05136.log`、`tmp/deploy-05136.log`

## 若被打断

1. 读结果 JSON / 部署日志确认 Status。
2. `curl http://127.0.0.1:8766/healthz`；核对 `AHA2 User` 任务与安装目录哈希（与 05136 候选一致）。
3. 验证：task-29 或新任务切 Codex 跑一轮，应出现「思考」卡片。
