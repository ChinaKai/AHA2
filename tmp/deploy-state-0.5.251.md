# 0.5.251 部署状态

- 修复 `closePetSettings is not defined`：
  - 0.5.250 删除设置区块时误删 `closePetSettings` 和 `applyPetEnabled`。
  - 已从父提交恢复两个函数。
- 提交：`eb9d136 fix: restore the pet settings close and enable handlers`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-063502.json`。
- 进程 2026-10-29 14:35 起；`/healthz` 200。
