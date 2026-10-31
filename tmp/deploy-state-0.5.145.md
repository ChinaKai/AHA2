# 0.5.145 部署状态

- 提交：`16ef5f2 fix: restart the adb server once when a device stays unauthorized`
- 变更：unauthorized 时先等 20s，仍未授权则 kill-server + 重连 + 再等 20s，最后才报错（错误里说明已自动重启 ADB 服务）
- 背景：Android 只在新的 server session 才重新弹授权框；被拒绝过/密钥不匹配时永不弹窗，表现为「电视根本不弹」
- 候选：Server `aha2 0.5.145` SHA256 `82AEE3EC812B43BDD23F7F4EA55A7A7971138597EF08C9170E388D22918B052E`；Tray `4960088A583FC5B276C5BEC5952FF54DCC749175B45893F8DE8D9C4571B2ECC0`
- 结果 JSON：`dist/user-deploy-result-05145-adb-restart.json`

## 若被打断

1. 读结果 JSON；健康 200；安装哈希与候选一致。
2. 验证：xiaomitv 点连接 → AHA 自动 kill-server 重连 → 电视出现授权框 → 允许后自动进入终端。
