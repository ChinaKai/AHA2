# 0.5.288 部署状态

- 模型检测 job 创建改为最多等待 5 秒：
  - 真实 provider 216ms，创建请求返回时 job 已 completed、带 57 个 models。
  - 慢/挂起时最多等 5 秒，然后返回 running，由 SSE/轮询继续。
- 前端仍有 1.2s job 轮询兜底，30s 无目录会显示失败。
- 部署：`dist/user-deploy-result-20261029-104054.json` succeeded；服务 18:41:09 启动。
- 提交：`1835aa3 fix: return model detection results synchronously when fast`。
