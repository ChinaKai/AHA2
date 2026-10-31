# 0.5.293 部署状态

- 模型检测回滚到 v0.5.149 行为：
  - `internal/httpapi/model_detection_jobs.go` 直接 checkout v0.5.149。
  - 前端检测块（interface/start/SSE/完成逻辑）替换为 v0.5.149 原文。
  - 移除 GET job 路由、轮询、直达接口、done 通道、25s 超时等所有后加补丁。
- 分析：
  - 本地 main 比 origin/main 多 126 个提交。
  - 检测相关提交全部出现在 0.5.285 之后，且都是本次排查所加。
  - v0.5.149 到首次故障之间，检测服务端/网关代码没有提交改动。
  - 因此首次故障不是“模型检测代码被后续提交改坏”；后续补丁反而引入了 done 重复 close 等真实回归。
- 部署：`dist/user-deploy-result-20261029-122857.json` succeeded；服务 20:29:12 启动。
- 提交：`39bcca0 revert: restore the 0.5.149 model detection flow`。
