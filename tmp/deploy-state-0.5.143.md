# 0.5.143 部署状态

- 提交：`f8d5a7b fix: explain an unauthorized ADB device instead of a bare shell failure`
- 变更：adb connect 后追加 get-state 检查，unauthorized/offline 返回可操作提示；ready 时行为不变
- 背景：xiaomitv 192.168.0.49:5555 端口可连、adb connect 成功，但设备状态是 unauthorized —— 电视上没确认过 RSA 授权弹窗
- 候选：Server `aha2 0.5.143` SHA256 `CF6AFBA2969CB9F526B52C0562F9A1349E7518F8B7645D062CFE31B3E01D89E7`；Tray `1668606530262A488A4408C93C1C2E950CCE83AFE34BAE8DC3972B38808581DE`
- 结果 JSON：`dist/user-deploy-result-05143-adb-state.json`

## 若被打断

1. 读结果 JSON；健康 200；安装哈希与候选一致。
2. 验证：xiaomitv 直接连接应提示「设备未授权…请在电视上确认允许 USB 调试」。
