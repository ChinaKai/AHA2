# 0.5.130 live-inbox 部署状态（用于重启中断后续接）

- 提交：5125799 `feat: inject live owner messages into running DSH turns`
- 版本：0.5.130
- 安装目录：D:\Program Files\AHA2
- 数据目录：D:\ProgramData\AHA2
- 部署命令日志：/tmp/aha2-deploy-05130.log
- 部署结果 JSON：dist/user-deploy-result-live-inbox-05130.json
- 若本 Turn 被重启打断：先读结果 JSON 与日志，再核验线上版本、healthz、server/tray SHA256，最后通知 Owner 实测。
- Owner 实测要点：在 DSH backend 的 Task 正在执行长工具时，从 Web 或渠道发一条消息；应不等 Turn 结束就作为 `[AHA live inbox]` 注入，模型随之调整；检查 `agent_inbox.steered_at` 早于 turn.finished_at。
