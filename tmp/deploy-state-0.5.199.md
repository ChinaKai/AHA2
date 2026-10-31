# 0.5.199 部署状态

- 未触发工具的修复：
  - 动作类请求（走/跑/跳/左/右/开心/生气/难过/睡/醒/打开/task- 等关键词）强制 tool_choice。
  - OpenAI chat / Responses：`tool_choice="required"`；Anthropic messages：`tool_choice={"type":"any"}`。
  - 普通问答仍是 auto，不会每条都强行调用工具。
- 提交：`8b44179 fix: force pet tool calls for action requests`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-180831.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-180839`。
- Server `7FEDCC5F6A5DA75E5727EF7B9DDCA71AFB15904B5A983E65D9D4FCFFBDC2803E`；Tray `1616BF2842DA28FBF17A87F20B27C07E0A8A453D73DEAF45103B1B5B74F2F5CD`。
- 进程 2026-10-29 02:08:57 起；`/healthz` 200。
- 测试：gateway force tool_choice 单测；web 133 通过。
