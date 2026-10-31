# 0.5.139 部署状态

- 提交：`89055b4 refactor: start the Codex rollout reader at the current end of file`
- 变更：按 Owner 思路，turn 开始时把读取器定位到 rollout 现有末尾（prime），历史根本不读；删除 timestamp 过滤
- 安装：`D:\Program Files\AHA2`；数据 `D:\ProgramData\AHA2`
- 候选：Server `aha2 0.5.139` SHA256 `1B172A7922B9E314C9FB875F4315A07897BD3049781BB772200D8BFBB667A832`；Tray `35599175765BA2E9789F93515C82D2DC8FD9A278558339736C5E7DEF08DCCEDD`
- 结果 JSON：`dist/user-deploy-result-05139-codex-prime.json`

## 若被打断

1. 读结果 JSON；健康 200；安装哈希与候选一致。
2. 验证：同一 Codex 任务连发两条，第二条只出现本轮思考。
