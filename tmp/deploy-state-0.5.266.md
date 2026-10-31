# 0.5.266 部署状态

- Available context 只列日记根目录：
  - `PetDiaryPaths` 返回 `DataDir/pet-workspace/diary` 一个目录。
  - 模板文案改为“日记目录（按需读取其中的文件）”。
  - 不再列具体文件。
- 提交：`31a3c98 fix: list only the pet diary root in available context`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-081822.json`。
- 进程 2026-10-29 16:18 起；`/healthz` 200。
