# 0.5.184 部署状态

- 直接 tool calling：
  - `gateway.ChatWithTools` 支持 OpenAI chat、Anthropic messages、Responses 三种 wire API 的 function tools，解析 text/tool_calls。
  - Owner 助手注册 `pet_play`、`pet_move`、`pet_mood`、`pet_open_task`、`pet_sleep`、`pet_wake` 六个工具。
  - 模型返回 tool_calls 后，服务端映射为 PetBrainAction，经 `ControlOwnerPet` 校验、广播 `pet_action` SSE、写活动记录；客户端执行。
  - 工具不支持/400 时自动回退普通 `gateway.Chat`；提示词仍保留 `[pet]` 文本回退。
  - 模型只调工具没文本时，聊天回复填“好。”。
- 提交：`fc12519 feat: call pet tools from the owner assistant`；未 push/tag。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261028-165315.json`，备份 `D:\ProgramData\AHA2\backups\user-installed-20261028-165322`。
- Server `08D51F4197D45EC4320A0ADA86EE7B4266AB8995B80B021CE70F594CA2B85731`；Tray `5C918B0EAAB18716AB8C809BD04D977592EBE08D9B1FDE4CE764AD974C1190ED`。
- 进程 2026-10-29 00:53:29 起；`/healthz` 200。
- 测试：`parseChatToolResponse` OpenAI/Anthropic/Responses 单测；`chatToolBody` 含 tools；app/http Pet/Assistant 测试通过。
- 已知：目前是单轮工具调用，工具执行结果没有回传给模型再生成最终回复；流式/多轮工具循环下一步。
