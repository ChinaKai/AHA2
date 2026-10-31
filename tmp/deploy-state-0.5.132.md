# 0.5.132 部署状态（重启中断后续接用）

- 提交：`d0d5385 feat: project Claude thinking blocks as reasoning events`
- 目标版本：0.5.132
- 安装目录：`D:\Program Files\AHA2`（Mode=per-user-custom，既往已授权，脚本写入探测）
- 数据目录：`D:\ProgramData\AHA2`；监听 `0.0.0.0:8766`；健康 `http://127.0.0.1:8766/healthz`
- 部署方式：`-UpdateExistingInstallInPlace`（宿主无 Inno Setup）+ `-AllowCustomUserWritableInstallDir`
- 已跑：`-ValidateOnly` 通过（Mode=build-and-deploy，Distro=Ubuntu-24.04）
- 构建日志：`tmp/build-05132.log`；部署日志：`tmp/deploy-05132.log`
- 部署结果 JSON：`dist/user-deploy-result-05132-claude-thinking.json`

## 若本 Turn 被重启打断

1. 先读 `dist/user-deploy-result-05132-claude-thinking.json` 与 `tmp/deploy-05132.log`。
2. `curl http://127.0.0.1:8766/healthz`，核对 `AHA2 User` 任务 action/Principal。
3. 比对安装目录 server/tray SHA256 与 `dist\aha2-windows-amd64.exe` / `dist\aha2-tray-windows-amd64.exe`。
4. 验证：用 Claude 后端的 Task 跑一轮，界面应出现「思考」卡片（agent_reasoning），Update 也会在 text 块出现时显示。
5. 不要重复部署。

## 候选（BuildOnly 成功）

- ServerVersion `aha2 0.5.132`
- ServerSHA256 `95E1328633AC32630D5DAB77620745D1C742240C0F448EB4A852B4C5E58418D5`
- TraySHA256 `2E4BBCED3731801E2E687503EF29A4749BD80652A2F4EF16822C2381F58FE2DD`
- PluginSHA256 `BD6DF3BF7659998A753778703397EB83E831DDF560A467573465AE08CE882143`
