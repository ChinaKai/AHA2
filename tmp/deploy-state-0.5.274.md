# 0.5.274 部署状态

- 白屏原因：宠物设置保存后关闭设置，但页面仍停留在只有 `#pet-settings-host` 的 `pet-settings` 视图，host 为空，所以白屏。保存成功后改为跳回高级设置。
- 服务崩溃防护：服务端自主调度加 `recover`，单次自主行为 panic 不再杀进程；修正调度 key 防止每 30 秒重复提交。
- 安装脚本在版本检查/安装阶段多次卡住，最终手动复制构建产物：
  - `dist/aha2-windows-amd64.exe` -> `D:\Program Files\AHA2\aha2.exe`
  - `dist/aha2-tray-windows-amd64.exe` -> `D:\Program Files\AHA2\aha2-tray.exe`
  - 两边 SHA256 一致：`786b9758eedaad7c9acb2308c1938afc76a1a3c7ff8c2e5be2381677e9961f5b`
- 服务 2026-10-29 17:34 启动；`/healthz` 200。
- 提交：`b554285 fix: return to advanced settings after saving pet settings`。
- 注意：当前 `pet_idle_minutes=1`、`hourly_limit=50`、安静时段关闭，自主行为会非常频繁。建议先调到 10 分钟/6 次观察。
