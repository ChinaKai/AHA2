# 0.5.226 部署状态

- 宠物 Task 没有思考/打字：
  - 旧面板隐藏后，原来 `sendMessage` 里的 thinking/typewriter 不再触发。
  - 现在改为监听宠物 Task 对话事件：
    - 新 `user_message` -> `aha:pet-task-thinking`
    - 新 `agent_message` -> `aha:pet-task-reply`，宠物头顶气泡按字符打字，停留 3 秒淡出
    - turn 结束 -> `aha:pet-task-idle`
  - 与旧面板的 `showPetSpeech` 共用同一套打字/淡出逻辑。
- 提交：`4fef1bb fix: show pet thinking and speech on task replies`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-030509.json`。
- 进程 2026-10-29 11:05 起；`/healthz` 200。
