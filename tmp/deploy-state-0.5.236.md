# 0.5.236 部署状态

- stdio MCP：
  - mcp_servers 增加 command / args / env（migration v93）。
  - UI 添加时可选 HTTP 或 stdio，详情可编辑命令/参数。
  - 运行时生成 Claude `type:stdio` 配置；工具白名单按已缓存 tools 精确生成。
- 工具级开关：
  - mcp_servers 增加 disabled_tools（migration v94）。
  - 详情工具列表可勾选/取消，运行时跳过已禁用工具。
- 宠物服务端限额：
  - 自主思考服务端检查安静时段，命中直接拒绝。
  - 每设备每小时最多 6 次自主思考（当前为固定默认值）。
- 提交：`48273e9 feat: support stdio MCP, tool switches and server pet limits`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-042504.json`。
- 进程 2026-10-29 12:25 起；`/healthz` 200。
