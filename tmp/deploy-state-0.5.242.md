# 0.5.242 部署状态

- MCP 详情卡“正在读取”：
  - 根因：详情接口自动执行 stdio 探测，npx 卡住时 HTTP 请求一起卡住，前端无法编辑。
  - 已拆分：`MCPServerDetail` 只返回已保存配置；探测只在“测试并读取工具”时执行。
  - 详情现在立即打开，可改命令/参数/URL。
- 提交：`4c1678a fix: return MCP details before probing the server`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-055313.json`。
- 进程 2026-10-29 13:53 起；`/healthz` 200。
