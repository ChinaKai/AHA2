# 0.5.207 部署状态

- 为什么模型“不调用工具”：
  - 工具确实通过 `tools` 字段提供给模型（7 个）。
  - 但这个 Provider/模型返回的是文本形式的 `<tool_call>pet_walk_path{...}</tool_call_end>`，不是结构化 tool_use。
  - 0.5.206 的正则闭合标签写成了 `<tool_call_end>`，少了 `/`，所以从未匹配；截图闭合实际是 `</tool_call_end>`。
  - 0.5.207 修正为 `</tool_call_end>`，文本工具调用现在能解析、从正文删除并执行。
- 提交：`9c7ea55 fix: match the closing text tool call tag`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-235614.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-235622`。
- Server `0A43A7E9F40B4F56EAAB18059F7BA4D159DEB068E1F1559169A7EA8DE59D2E3D`；Tray `D2F6A665BADC8E9DD836EF2E5B6214E9E482CA63EE58A80475CEE92116D2F7A5`。
- 进程 2026-10-29 07:56 起；`/healthz` 200。
- 测试：文本 `<tool_call>...</tool_call_end>` 解析、数组 points、路径残影。
