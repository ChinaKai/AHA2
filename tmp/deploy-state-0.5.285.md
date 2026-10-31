# 0.5.285 部署状态

- 模型检测增加整体 25 秒超时：
  - 以前每次 HTTP 15 秒，2 个 endpoint × 3 种认证最多 90 秒。
  - 现在 job context 25 秒截止，失败会回到 failed，不会一直“读取目录中”。
- 部署：`dist/user-deploy-result-20261029-103227.json` succeeded/Health ok。
- 服务 18:33:02 启动；`/healthz` 200。
- 另一个 103234 结果为文件锁失败，可忽略（已有成功结果）。
