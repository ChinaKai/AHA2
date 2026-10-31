# 0.5.289 部署状态

- 模型检测：
  - provider 真实请求：57 models / 216ms / bearer。
  - job 创建最多等 5 秒，通常返回时已 completed 并带 models。
  - 前端 0.5.289 直接使用 `created.job.models` 渲染，不再依赖 SSE。
  - SSE 失败时仍保留 job GET 轮询。
- 之前改动仅限检测 job 的超时/状态接口，未改 Provider 请求逻辑；宠物开发不涉及该模块。
- 部署：`dist/user-deploy-result-20261029-104523.json` succeeded；服务 18:45:38 启动。
- 提交：`c7480c5 fix: render detected models directly from the job response`。
