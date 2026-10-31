# 0.5.291 部署状态

- 前端检测增加同步兜底：
  - 首次轮询 1.2 秒时，如果 job 还没有 models，直接调用已有的 `POST /api/v1/providers/detect-models`。
  - 该接口同步返回 57 个模型，不依赖 SSE/job。
  - 拿到后立即渲染并结束 job。
- 0.5.290 已修复 done channel 第二次 close 的 panic。
- 部署：`dist/user-deploy-result-20261029-110719.json` succeeded；服务 19:07:34 启动。
- 提交：`3d3a4fb fix: fall back to direct model detection in the dialog`。
