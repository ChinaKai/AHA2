# 0.5.286 部署状态

- 用真实 provider 凭据直接请求：
  - base：`https://tokens-api.hualaikeji.com`
  - `DetectModelsContext` 返回 57 个模型，216ms，auth=bearer。
  - 说明 provider、Key、网络都正常。
- 检测卡住不是 provider 问题，而是移动端/浏览器 SSE 没有把 job 事件送到 UI。
- 修复：
  - 新增 `GET /api/v1/providers/{id}/model-detection-jobs/{job}`。
  - 前端启动 job 后并行每 1.2 秒轮询；SSE 失效也能拿到 models/status。
  - 保留 25 秒整体超时。
- 部署：`dist/user-deploy-result-20261029-103551.json` succeeded；服务 18:36:06 启动。
- 提交：`f506fd0 fix: poll model detection jobs as an SSE fallback`。
