# 0.5.238 部署状态

- stdio MCP 工具发现：
  - 启动命令，`initialize` -> `notifications/initialized` -> `tools/list`。
  - 工具写入 mcp_servers.tools_json 与 health。
  - 测试用 Go helper 进程验证 `stdio_echo` 能被读取。
- 宠物小时限额：
  - 服务端读取 `assistant.PetHourlyLimit`，不再硬编码 6。
  - 安静时段仍服务端强制。
- 提交：`2359482 feat: discover stdio MCP tools and honor pet hourly limits`。
- 部署：第一次 tray 占用失败，重试成功；`dist/user-deploy-result-20261029-050142.json`，`Health=ok`。
- Server `295B28ED48A5880480F2F9816BF33CEAE692E46043D1484FDE40AC2C41DC36EC`；Tray `66B312721C46A85B1E9B4D6BBB03C04B47566B06B06D668FFBA4E4230929869A`。
- 进程 2026-10-29 13:02 起；`/healthz` 200。
