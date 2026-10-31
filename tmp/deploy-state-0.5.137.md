# 0.5.137 部署状态

- 提交：`6c3ecc8 fix: give the Codex rollout reader its runner and resumed thread`
- 关键修复：0.5.136 的 codexRollout 从未设置 runner 字段，导致 drain 永远走 os.Open 失败路径；同时用 ProviderSessionID 预置 thread（resume 轮没有 thread.started）
- 安装：`D:\Program Files\AHA2`；数据：`D:\ProgramData\AHA2`
- 候选：Server `aha2 0.5.137` SHA256 `FA237FEB2523CC06D01B96C3AF4FA1FDC5DBA2D01C02E67010A2DD734449A990`；Tray `B53CBF49AE2B2DADBB6325B4415EB17762ABFC4EAD9B17771C932688B37ED27E`
- 结果 JSON：`dist/user-deploy-result-05137-codex-rollout-runner2.json`
- 日志：`tmp/build-05137.log`、`tmp/deploy-05137.log`

## 若被打断

1. 读结果 JSON 确认 Status=succeeded。
2. 健康 200；安装目录哈希与候选一致；`AHA2 User` 任务不变。
3. 验证：Codex 后端跑一轮，应出现「思考」卡片。
