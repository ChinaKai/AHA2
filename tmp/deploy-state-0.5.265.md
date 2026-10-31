# 0.5.265 部署状态

- 日记路径进入 Available context：
  - `prompt.BuildInput` 增加 `PetDiaryPaths`。
  - `section.available-context` 增加“宠物日记”子块，最多 5 个路径，提示按需读取。
  - Service 在 Pet Task 构建 Prompt 时只列文件名/路径，不读取全文。
- 删除 Prompt 末尾的日记路径块。
- 提交：`d1c401a feat: list pet diary paths in the available context`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-081317.json`。
- 进程 2026-10-29 16:13 起；`/healthz` 200。
