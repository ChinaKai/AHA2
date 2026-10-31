# 0.5.264 部署状态

- 长期记忆不再注入日记全文。
- Pet Task Prompt 只附最多 5 条日记文件路径，标注“按需读取，不要一次全读”。
- 模型需要时自行读取 `pet-workspace/diary/*.md`。
- 提交：`0fe289f fix: pass diary paths instead of full memory text`。
- 部署：`Status=succeeded`、`Health=ok`，`dist/user-deploy-result-20261029-075948.json`。
- 进程 2026-10-29 15:59 起；`/healthz` 200。
