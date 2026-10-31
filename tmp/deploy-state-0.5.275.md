# 0.5.275 部署状态

- 修复“下次预计”显示过去时间：
  - 服务重启后服务端 last 为空，前端回退到本地旧 last。
  - 现在 next <= 当前时间时显示“等待下一次检查”，不再显示过去时刻。
- 手动构建/部署：
  - `internal/webassets/dist` 同步最新 web。
  - `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build`。
  - 停止 aha2/tray，复制 exe，启动 tray。
- 服务 2026-10-29 17:42 启动；`/healthz` 200。
- 提交：`56f1f1a fix: avoid stale next autonomous run time`。
