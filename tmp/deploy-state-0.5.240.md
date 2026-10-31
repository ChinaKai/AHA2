# 0.5.240 部署状态

- Windows stdio MCP：
  - Go `exec` 不能直接 CreateProcess `.cmd`，会报 path not found。
  - 运行时和工具发现统一把 `.cmd/.bat` 包成 `cmd.exe /c <command> ...`。
  - Claude MCP 配置也同步包 `cmd.exe`。
- 已安装便携 Node：
  - `D:\ProgramData\AHA2\node`
  - Node v22.14.0 / npm 10.9.2 / npx.cmd
- 提交：`33d09ed fix: launch Windows cmd MCP servers through cmd.exe`。
- 部署：第一次 WSL 服务超时，重试成功；`dist/user-deploy-result-20261029-053841.json`。
- 进程 2026-10-29 13:38 起；`/healthz` 200。
