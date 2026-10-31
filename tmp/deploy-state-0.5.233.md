# 0.5.233 部署状态

- 旧宠物聊天面板死代码拆除：
  - 删除 `#assistant-messages`、`#assistant-form`、输入框、发送按钮。
  - 删除 `sendMessage`、`renderMessages`、`showComposerTyping`、`hideComposerTyping`、`typewriterMessage` 及对应事件绑定。
  - 保留宠物 overlay、气泡、动作、设置、模型选择。
- 删除未使用的 `petMCPConfig` helper。
- 提交：`c070b0e refactor: remove the retired pet chat panel`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-035739.json`。
- 进程 2026-10-29 11:58 起；`/healthz` 200。
