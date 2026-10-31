# 0.5.212 部署状态

- 路径工具参数 `{}`：
  - 截图 pet_walk_path status=error、arguments={}；模型先输出大段规划，原 MaxTokens=700 可能把 tool args 截断。
  - 聊天 token 预算 700→3000；自主脑 260→1200。
- 架构说明：
  - Owner 助手不是走 `internal/backend`（DSH/Claude Code 适配器），而是 `gateway.ChatWithTools` 直接 HTTPS 调 Provider。
  - 直接请求 Anthropic `/v1/messages`、OpenAI `/v1/chat/completions` 或 `/responses`，没有 backend 的会话/工作区/工具运行时。
  - 因此 Provider 的 tool_use 格式差异、包裹结构、token 截断都直接暴露在助手层。
- 提交：`1f67f14 fix: raise the token budget for pet tool arguments`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-004259.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261029-004307`。
- Server `5EB7A2EF049049D2EE7387C3F3E5C53F92CD6795FC19CA1973C180451B499B72`；Tray `B5BFDBBC98B6B63EE7681C4F702D75DC4CBF57D8EEAF91B967909FB0EE5871FB`。
- 进程 2026-10-29 08:43 起；`/healthz` 200。
