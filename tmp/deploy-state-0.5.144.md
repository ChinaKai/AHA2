# 0.5.144 部署状态

- 提交：`3fb9a28 feat: wait for the ADB authorization dialog instead of failing at once`
- 变更：connect 后最多 30s 轮询 get-state，等用户在电视上点「允许」后自动打开 shell；state 探测不可用时立即回退旧行为
- 候选：Server `aha2 0.5.144` SHA256 `C2B3DA6D75A6A306743234834B17387AF9A3598303F1BC7F7BEC72D3849826BD`；Tray `8BA1D0C8C656FACF2BC999C9F964A6C375500F4AA8C407BE24E47F04BBF657C5`
- 结果 JSON：`dist/user-deploy-result-05144-adb-authorize-wait.json`

## 若被打断

1. 读结果 JSON；健康 200；安装哈希与候选一致。
2. 验证：xiaomitv 点连接 → 电视出现授权弹窗 → 点允许 → 终端应自动连上（不必再点一次连接）。
