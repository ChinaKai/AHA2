# 0.5.211 部署状态

- Anthropic `response has no text`：
  - forced tool_choice 失败后原来直接回退纯 Chat；纯 Chat 收到 tool-only/空 text 就报 no text。
  - 现在改为：先重试 ChatWithTools（保留 tools，但 ForceTool=false），成功就用；再退纯 Chat。
  - 最终错误保留第一次 tool 请求的错误，包含 raw 前 500 字，不再被 fallback 的 no text 覆盖。
- 提交：`b3c23eb fix: retry tool calls without forcing Anthropic choice`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-003555.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261029-003604`。
- Server `531EDED9FBC23D04B979E1C7466A6DDD56F4418578ED4DBBB753879134C60720`；Tray `FE34B91020EABF776D735A3EDE814E0AE7149ABF4DA195F9F26D4F9614F7800D`。
- 进程 2026-10-29 08:36 起；`/healthz` 200。
