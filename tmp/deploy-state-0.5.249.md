# 0.5.249 部署状态

- 宠物提示词移入 Prompt 注册表：
  - `identity.pet-assistant`：人格、职责、边界、工具、布局、快照。
  - `identity.pet-brain`：空闲自主行为。
  - 两块均可在 Prompt 页面编辑/重置，与 Task 提示词同一套 override。
- 移除 `assistantSystemPrompt` / `petBrainSystemPrompt` 的硬编码正文。
- 模板编辑校验支持宠物字段，不会因 missingkey 报错。
- 提交：`176f1a0 feat: move pet prompts into the prompt registry`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-062901.json`。
- 进程 2026-10-29 14:29 起；`/healthz` 200。
