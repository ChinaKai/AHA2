# 0.5.135 部署状态（重启中断后续接用）

- 提交：`33f70d5 feat: project Codex rollout reasoning as thinking cards`
- 目标版本：0.5.135
- 安装：`D:\Program Files\AHA2`（per-user-custom）；数据：`D:\ProgramData\AHA2`
- 方式：`-UpdateExistingInstallInPlace` + `-AllowCustomUserWritableInstallDir`
- 已跑：`-ValidateOnly` 通过、`-BuildOnly` 成功
- 候选：Server `aha2 0.5.135` SHA256 `BA7410D7E67515AE2603E543C066CA39F99438FAB73C698223B3581B916B048B`；Tray `8B498480A629957A26D03BB39640CC32D5614E61DBD58B537027C73D9D42F89F`；Plugin `72146B30D157ADFBDB040F50695A8D9C59926CFCD39BBD23F8FDCE36718E8C4C`
- 日志：`tmp/build-05135.log`、`tmp/deploy-05135.log`
- 结果 JSON：`dist/user-deploy-result-05135-codex-rollout.json`

## 若被打断

1. 读结果 JSON / 部署日志确认 Status。
2. `curl http://127.0.0.1:8766/healthz`；核对 `AHA2 User` 任务。
3. 比对安装目录 server/tray SHA256 与候选。
4. 验证：task-29 切 Codex 跑一轮，应出现「思考」卡片（内容来自 rollout 的 reasoning_text）。
