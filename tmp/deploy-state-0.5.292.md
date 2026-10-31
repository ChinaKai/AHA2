# 0.5.292 部署状态

- 跟因：模型协议探测的单次超时是 8 秒。
  - `gpt-6.1-terra` 实测完整探测 8.9 秒。
  - `claude-gpt-6.1-terra` 6.9 秒。
  - 8 秒超时后三个协议全部记为 unavailable，UI 显示“不支持/无支持协议”。
- 修复：探测超时从 8 秒提高到 25 秒。
- 真实探测结果（0.5.292 前用同一函数）：
  - 三个模型均 responses/chat_completions/anthropic_messages supported。
- 部署：`dist/user-deploy-result-20261029-111230.json` succeeded；服务 19:12:45 启动。
- 提交：`435c41f fix: allow slow model capability probes`。
