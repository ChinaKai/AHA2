# 0.5.290 部署状态

- 根因：0.5.288 引入 `done` 通道等待时，把 `close(j.done)` 误放进了 `recordResult`。
  - 每收到一个模型就 close 一次。
  - 第一个模型后 `start` 提前返回。
  - 第二个模型再次 close 同一 channel，直接 panic，进程退出。
  - 因此 0.5.149 正常，0.5.288/289 检测会卡住/崩溃。
- 修复：`close(j.done)` 只保留在 `finish`，`recordResult` 不再关闭。
- 部署：`dist/user-deploy-result-20261029-110405.json` succeeded；服务 19:04:20 启动。
- 提交：`1319410 fix: close the detection job signal only when finished`。
