# 0.5.278 部署状态

- 宠物设置“自主行为”增加按钮“立即触发一次自主行为”。
- 点击复用 `maybeRunPetBrain(true)`，和定时触发走同一个 `/api/v1/pet/brain` → `task_pet` 路径。
- 触发期间按钮禁用，状态显示“已请求一次自主行为，等待 Task 执行…”。
- 手动构建并部署；服务 2026-10-29 18:02:52 启动，`/healthz` 200。
- 提交：`feat: add a manual pet autonomy trigger`。
