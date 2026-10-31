# 0.5.241 部署状态

- stdio 详情卡在“正在读取”：
  - `fetchStdioTools` 增加 45s 超时，避免无限等待。
- 已验证 filesystem MCP 的直启方式：
  - 用 `D:\ProgramData\AHA2\node\node.exe` 直接跑 server index.js 时，initialize/tools/list 正常。
  - 通过 npx/cmd 启动在该环境会挂起，建议 MCP 配置改用 node.exe 直启。
- 提交：`dbc8da5 fix: time out stalled stdio MCP discovery`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-054607.json`。
- 进程 2026-10-29 13:46 起；`/healthz` 200。
