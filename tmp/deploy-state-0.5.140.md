# 0.5.140 部署状态

- 提交：`eb28614 feat: copy hardware terminal selection automatically`
- 变更：硬件调试终端选中即复制（selectionChange 防抖 180ms + pointerup 兜底 + 同文本去重），工具栏新增「选中即复制」开关（localStorage 记忆，默认开）
- 候选：Server `aha2 0.5.140` SHA256 `30CCEDACE8155F43117E97BE785A6557B943B7BDF16D854916BB0013481BDF17`；Tray `967F34521C80BA2E5490FD0C42C3089F49E762BF91844D37EAADFEDB70F18B19`
- 结果 JSON：`dist/user-deploy-result-05140-terminal-autocopy.json`
- 日志：`tmp/build-05140.log`、`tmp/deploy-05140.log`

## 若被打断

1. 读结果 JSON 确认 Status=succeeded；健康 200；安装哈希与候选一致。
2. 验证：硬件 → 调试终端，选中几行，剪贴板应直接拿到内容并出现「已复制选中内容」提示。
