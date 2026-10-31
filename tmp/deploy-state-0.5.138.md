# 0.5.138 部署状态

- 提交：`3fb98b2 fix: stop replaying a resumed session's earlier thinking`
- 修复：resumed session 复用同一 rollout 文件，读取器原来从 offset 0 且无时间界，导致每轮把整段历史的 thinking 重放一遍。现以 turn 起点（-5s 容差）过滤更早的记录。
- 安装：`D:\Program Files\AHA2`；数据：`D:\ProgramData\AHA2`
- 候选：Server `aha2 0.5.138` SHA256 `E9B18A9DB179CEDF6C596AF98433CF1A812FA80399ED0B602C18480F7087D82D`；Tray `DA6C0B46C4C7BBFED32222F63AD62F34C3F3B3BF62825ABBC069808DCA4B4BB4`
- 结果 JSON：`dist/user-deploy-result-05138-codex-thinking-replay.json`

## 若被打断

1. 读结果 JSON 确认 Status=succeeded；健康 200；安装哈希与候选一致。
2. 验证：同一 Codex 任务连续发两条消息，第二条不应重复出现第一条的「思考」。
