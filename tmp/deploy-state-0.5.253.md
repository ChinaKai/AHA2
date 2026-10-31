# 0.5.253 部署状态

- 宠物设置恢复：
  - 补回误删的 `openPetSettings`；`closePetSettings`/`applyPetEnabled` 也已恢复。
- 高级设置重组：
  - 顶层只保留：提示词、同步、代理、硬件资源、宠物、其他设置。
  - Backend 超时、修改密码、访问范围、Agent API 地址安全、监听地址、Origin 校验、密码恢复统一进入“其他设置”子页。
- 宠物 Task Prompt：
  - 0.5.252 起 `identity.pet-task` 会前置到宠物 Task 的 Prompt Snapshot。
  - 已有历史 Turn 不会回填，需要新发一条消息后查看新 Turn。
- 提交：`17b5142 fix: restore pet settings and split other advanced settings`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-064722.json`。
- 进程 2026-10-29 14:47 起；`/healthz` 200。
