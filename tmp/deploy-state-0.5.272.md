# 0.5.272 部署状态

- 自主调度移到服务端：
  - `Service.StartPetAutonomy` 每 30 秒检查一次。
  - 首次只记录基线，之后按 `pet_idle_minutes` 提交 `[自主行为]` 到 `task_pet`。
  - 安静时段、小时上限、ActiveTurn 仍由 `AskOwnerPetBrain` 检查。
  - 浏览器关闭不再停止自主行为；页面只负责接收 SSE 动作。
- 移除客户端 30 秒自主触发定时器。
- 修复宠物设置保存 `input is not defined`：删除 `renderModel` 中残留的旧输入框引用。
- 提交：`feat: schedule pet autonomy on the server`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-090934.json`。
- 进程 2026-10-29 17:09 起；`/healthz` 200。
